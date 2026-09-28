package lp2p

import (
	"context"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/protocol"
	"github.com/stretchr/testify/require"

	"github.com/tachibtc/cometbft/abci/types"
	"github.com/tachibtc/cometbft/p2p"
)

func TestProtocolIDForRules(t *testing.T) {
	for _, tt := range []struct {
		rules    uint64
		channel  byte
		expected string
	}{
		{rules: 1, channel: 0x20, expected: "/p2p/cometbft/1.0.0/channel/0x20"},
		{rules: 2, channel: 0x20, expected: "/p2p/cometbft/1.0.0/rules/2/channel/0x20"},
		{rules: 17, channel: 0xff, expected: "/p2p/cometbft/1.0.0/rules/17/channel/0xff"},
	} {
		id := protocolIDForRules(tt.channel, tt.rules)
		require.Equal(t, protocol.ID(tt.expected), id)

		rules, ok := consensusRulesOf(id)
		require.True(t, ok, id)
		require.Equal(t, tt.rules, rules, id)
	}

	// This node's IDs use its own rules version.
	require.Equal(t, protocolIDForRules(0x20, consensusRules), ProtocolID(0x20))

	for _, id := range []protocol.ID{
		"/ipfs/id/1.0.0",
		"/p2p/cometbft/1.0.0/rules/x/channel/0x20",
		"/p2p/cometbft/1.0.0/rules/2/0x20",
	} {
		_, ok := consensusRulesOf(id)
		require.False(t, ok, id)
	}
}

// Nodes on different consensus rules cannot exchange messages, and the send
// error says why.
func TestConsensusRulesMismatch(t *testing.T) {
	var (
		ctx   = context.Background()
		hosts = makeTestHosts(t, 2)
		hostA = hosts[0]
		hostB = hosts[1]
	)

	const channelID = byte(0xaa)
	peerRules := consensusRules + 1

	// hostB serves the channel under different consensus rules.
	received := make(chan struct{}, 1)
	hostB.SetStreamHandler(protocolIDForRules(channelID, peerRules), func(stream network.Stream) {
		_, _ = StreamReadClose(stream)
		received <- struct{}{}
	})

	require.NoError(t, hostA.Connect(ctx, hostB.AddrInfo()))
	require.Eventually(t, func() bool {
		protocols, err := hostA.Peerstore().GetProtocols(hostB.ID())
		return err == nil && slices.Contains(protocols, protocolIDForRules(channelID, peerRules))
	}, 5*time.Second, 50*time.Millisecond, "identify did not report hostB's protocols")

	peerB, err := NewPeer(hostA, hostB.AddrInfo(), p2p.NopMetrics(), false, false, false)
	require.NoError(t, err)

	err = peerB.send(p2p.Envelope{ChannelID: channelID, Message: types.ToRequestEcho("hello")})
	require.ErrorIs(t, err, ErrConsensusRulesMismatch)
	require.ErrorContains(t, err, fmt.Sprintf("peer runs consensus rules v%d, this node v%d", peerRules, consensusRules))
	require.Empty(t, received)

	// Under the same rules, the message goes through.
	hostB.SetStreamHandler(ProtocolID(channelID), func(stream network.Stream) {
		_, _ = StreamReadClose(stream)
		received <- struct{}{}
	})
	require.Eventually(t, func() bool {
		return peerB.send(p2p.Envelope{ChannelID: channelID, Message: types.ToRequestEcho("hello")}) == nil
	}, 5*time.Second, 100*time.Millisecond)
	require.Eventually(t, func() bool { return len(received) == 1 }, 5*time.Second, 50*time.Millisecond)
}
