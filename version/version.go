package version

const (
	// TMCoreSemVer is the used as the fallback version of CometBFT
	// when not using git describe. It is formatted with semantic versioning.
	TMCoreSemVer = "0.39.0"
	// ABCISemVer is the semantic version of the ABCI protocol
	ABCISemVer  = "2.0.0"
	ABCIVersion = ABCISemVer
	// P2PProtocol versions all p2p behavior and msgs.
	// This includes proposer selection.
	P2PProtocol uint64 = 8

	// BlockProtocol versions all block data structures and processing.
	// This includes validity of blocks and state updates.
	BlockProtocol uint64 = 11

	// ConsensusRules versions the rules validators apply to consensus data
	// without it changing on the wire, such as how MedianTime computes a
	// block's time. Two nodes on different rules can each consider the
	// other's proposals invalid, so a network whose validators straddle a
	// rules change can split with neither side reaching +2/3, and halt.
	//
	// Bump it with every such change. libp2p protocol IDs include it (see
	// lp2p.ProtocolID), so a partial rollout shows up as disconnected peers
	// instead of a silent halt. Roll a bump out to all validators at once,
	// at an agreed halt height.
	//
	// The split covers every reactor channel, not just consensus: protocol
	// IDs gate mempool, block sync, state sync, evidence and PEX traffic
	// alike. Two consequences to plan the rollout around:
	//
	//   - A node left on the old version cannot block sync or state sync to
	//     catch up, because it cannot open a stream to a peer on the new
	//     version. Upgrading its binary is always the first step.
	//   - A seed node on the old version serves no peer discovery to nodes
	//     on the new one, so seeds must be upgraded too.
	//
	// Version 1 is the rules of every node built before this constant
	// existed, including upstream #5901's MedianTime (Nil precommits
	// excluded); its protocol IDs carry no rules segment, so it stays
	// compatible with them.
	ConsensusRules uint64 = 1
)

// TMGitCommitHash uses git rev-parse HEAD to find commit hash which is helpful
// for the engineering team when working with the cometbft binary. See Makefile
var TMGitCommitHash = ""
