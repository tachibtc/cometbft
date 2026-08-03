package lp2p

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
	"github.com/libp2p/go-libp2p/p2p/net/swarm"
	"github.com/tachibtc/cometbft/libs/service"
	"github.com/tachibtc/cometbft/p2p"
	"github.com/tachibtc/cometbft/p2p/conn"
)

// Peer represents a remote node connected via libp2p.
// It implements p2p.Peer interface and wraps the libp2p connection
// with CometBFT-specific peer attributes and messaging capabilities.
type Peer struct {
	service.BaseService

	host *Host

	// addrInfo lp2p peer representation. Note that lp2p's addressbook CAN contain
	// different AddrInfo.Addrs for this peer: e.g. peer could announce different addresses in identity protocol.
	// Imagine peerA has p2p.ExternalAddress=<some_pub_ip>, but in our bootstrap_peers it exists under
	// <vpc_private_ip>. We want to use <vpc_private_ip> in this case regardless of what peerA tells us.
	// We might make this configurable and revisit if needed.
	addrInfo peer.AddrInfo

	netAddr *p2p.NetAddress

	// behavioral flags (are not mutually exclusive)
	isPrivate       bool
	isPersistent    bool
	isUnconditional bool

	metrics *p2p.Metrics
}

var _ p2p.Peer = (*Peer)(nil)

func NewPeer(
	host *Host,
	addrInfo peer.AddrInfo,
	metrics *p2p.Metrics,
	isPrivate, isPersistent, isUnconditional bool,
) (*Peer, error) {
	netAddr, err := netAddressFromPeer(addrInfo)
	if err != nil {
		return nil, fmt.Errorf("unable to parse net address: %w", err)
	}

	p := &Peer{
		host:     host,
		addrInfo: addrInfo,
		netAddr:  netAddr,

		isPrivate:       isPrivate,
		isPersistent:    isPersistent,
		isUnconditional: isUnconditional,

		metrics: metrics,
	}

	logger := host.Logger().With("peer_id", addrInfo.ID.String())

	p.BaseService = *service.NewBaseService(nil, "Peer", p)
	p.SetLogger(logger)

	return p, nil
}

func (p *Peer) String() string {
	return fmt.Sprintf("Peer{%s}", p.ID())
}

func (p *Peer) ID() p2p.ID {
	return peerIDToKey(p.addrInfo.ID)
}

func (p *Peer) SocketAddr() *p2p.NetAddress {
	return p.netAddr
}

// AddrInfo returns original addr info.
// Note it might differ from host's peerstore
func (p *Peer) AddrInfo() peer.AddrInfo {
	return p.addrInfo
}

func (p *Peer) Get(key string) any {
	v, err := p.host.Peerstore().Get(p.addrInfo.ID, key)
	if err != nil {
		return nil
	}

	return v
}

func (p *Peer) Set(key string, value any) {
	//nolint:errcheck // always returns err=nil
	p.host.Peerstore().Put(p.addrInfo.ID, key, value)
}

func (p *Peer) IsPersistent() bool {
	return p.isPersistent
}

func (p *Peer) IsPrivate() bool {
	// todo: STACK-2089
	return p.isPrivate
}

func (p *Peer) IsUnconditional() bool {
	return p.isUnconditional
}

// Send implements p2p.Peer.
func (p *Peer) Send(e p2p.Envelope) bool {
	if err := p.send(e); err != nil {
		p.Logger.Error("failed to send message", "channel", e.ChannelID, "method", "Send", "err", err)
		p.handleSendErr(err)
		return false
	}

	return true
}

// TrySend has no difference from Send in lib-p2p. Implements p2p.Peer.
func (p *Peer) TrySend(e p2p.Envelope) bool {
	if err := p.send(e); err != nil {
		p.Logger.Error("failed to send message", "channel", e.ChannelID, "method", "TrySend", "err", err)
		p.handleSendErr(err)
		return false
	}

	return true
}

func (p *Peer) CloseConn() error {
	return p.host.Network().ClosePeer(p.addrInfo.ID)
}

func (p *Peer) send(e p2p.Envelope) (err error) {
	var (
		peerID     = p.addrInfo.ID
		protocolID = ProtocolID(e.ChannelID)
	)

	payload, err := marshalProto(e.Message)
	if err != nil {
		return err
	}

	var (
		peerIDStr    = peerID.String()
		messageType  = protoTypeName(e.Message)
		payloadLen   = float64(len(payload))
		metricLabels = []string{
			"peer_id", peerIDStr,
			"chID", fmt.Sprintf("%#x", e.ChannelID),
		}

		peerSendQueueSize = p.metrics.PeerSendQueueSize.With("peer_id", peerIDStr)
	)

	peerSendQueueSize.Add(1)

	ctx, cancel := context.WithTimeout(context.Background(), TimeoutStream)
	defer cancel()

	start := time.Now()

	defer func() {
		peerSendQueueSize.Add(-1)

		if err != nil {
			return
		}

		p.metrics.PeerSendBytesTotal.With(metricLabels...).Add(payloadLen)
		p.metrics.MessageSendBytesTotal.With("message_type", messageType).Add(payloadLen)

		p.Logger.Debug(
			"Sent envelope",
			"protocol", protocolID,
			"peer_id", peerIDStr,
			"send_dur", time.Since(start).String(),
		)
	}()

	s, err := p.openStreamWithRetry(ctx, protocolID)
	if err != nil {
		return fmt.Errorf("failed to open stream %s: %w", protocolID, err)
	}

	return StreamWriteClose(s, payload)
}

// streamOpenGraceWindow caps the time openStreamWithRetry spends retrying
// "protocols not supported" errors. The error has two possible causes:
//
//  1. Transient startup race — the remote established a libp2p connection
//     but hasn't yet registered this channel's stream handler via
//     SetStreamHandler (called inside Switch.OnStart). Clears within a few
//     hundred milliseconds once OnStart runs and identify propagates.
//
//  2. Permanent mismatch — the remote was built without support for this
//     channel, or dropped the protocol entirely. Will never clear.
//
// The libp2p error text is identical for both cases. 500ms is long enough
// to paper over realistic case-1 races, short enough that case 2 doesn't
// stall every Send/TrySend call for the caller's full TimeoutStream budget.
const streamOpenGraceWindow = 500 * time.Millisecond

// openStreamWithRetry opens a libp2p stream for the given protocol, retrying
// briefly on "protocols not supported" errors. See streamOpenGraceWindow for
// the rationale behind the cap. Upstream CometBFT's MConnection transport
// avoids this race entirely because all channels share a single multiplexed
// TCP stream — the race is unique to the libp2p per-channel model used here.
// Any non-matching error returns immediately; retries end at the sooner of
// the grace window, the caller's context deadline, or a successful open.
func (p *Peer) openStreamWithRetry(ctx context.Context, protocolID protocol.ID) (network.Stream, error) {
	deadline := time.Now().Add(streamOpenGraceWindow)
	backoff := 50 * time.Millisecond
	for {
		s, err := p.host.NewStream(ctx, p.addrInfo.ID, protocolID)
		if err == nil {
			return s, nil
		}
		if !strings.Contains(err.Error(), "protocols not supported") {
			return nil, err
		}
		// Clamp the sleep so total time spent here never exceeds the grace
		// window — an unclamped sleep could overshoot the cap by up to one
		// backoff interval when only a thin slice of the window remains.
		remaining := time.Until(deadline)
		if remaining <= 0 {
			// Grace window elapsed — treat as permanent mismatch and fail fast.
			return nil, err
		}
		sleep := backoff
		if sleep > remaining {
			sleep = remaining
		}
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("%w (last: %v)", ctx.Err(), err)
		case <-time.After(sleep):
		}
		if backoff < streamOpenGraceWindow {
			backoff *= 2
		}
	}
}

func (p *Peer) handleSendErr(err error) {
	switch {
	case err == nil:
		return
	case errors.Is(err, swarm.ErrAllDialsFailed), errors.Is(err, swarm.ErrNoGoodAddresses):
		p.host.EmitPeerFailure(p.addrInfo.ID, err)
	}
}

// NodeInfo returns a DefaultNodeInfo populated with the peer's ID and address.
// Since libp2p does not perform a CometBFT-style handshake, only the fields
// derivable from the connection are filled in (ID, listen address).
func (p *Peer) NodeInfo() p2p.NodeInfo {
	return p2p.DefaultNodeInfo{
		DefaultNodeID: p.ID(),
		ListenAddr:    p.netAddr.DialString(),
	}
}

// RemoteIP returns the remote IP address of the peer derived from its address info.
func (p *Peer) RemoteIP() net.IP {
	return p.netAddr.IP
}

// RemoteAddr returns the remote address of the peer as a net.Addr.
func (p *Peer) RemoteAddr() net.Addr {
	return &net.TCPAddr{
		IP:   p.netAddr.IP,
		Port: int(p.netAddr.Port),
	}
}

// IsOutbound returns true because all lp2p peers are bi-directional.
func (*Peer) IsOutbound() bool { return true }

// Status returns an empty ConnectionStatus. Per-channel send queue
// statistics are not available with the libp2p transport.
func (*Peer) Status() conn.ConnectionStatus { return conn.ConnectionStatus{} }

func (*Peer) FlushStop()             {}
func (*Peer) SetRemovalFailed()      {}
func (*Peer) GetRemovalFailed() bool { return false }
