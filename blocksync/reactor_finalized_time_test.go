package blocksync

import (
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/tachibtc/cometbft/consensus"
	"github.com/tachibtc/cometbft/internal/test"
	"github.com/tachibtc/cometbft/libs/log"
	"github.com/tachibtc/cometbft/p2p"
	sm "github.com/tachibtc/cometbft/state"
)

// requireOffMedianTime asserts that the provider's block at height carries a
// time this binary's median-time rule rejects.
func requireOffMedianTime(t *testing.T, provider ReactorPair, height int64) {
	t.Helper()
	block := provider.reactor.store.LoadBlock(height)
	require.NotNil(t, block)
	vals, err := provider.reactor.blockExec.Store().LoadValidators(height - 1)
	require.NoError(t, err)
	median, err := sm.MedianTime(block.LastCommit, vals)
	require.NoError(t, err)
	require.NotEqual(t, median, block.Time, "test chain block %d should be off the median time", height)
}

// TestBlockSyncCommittedBlockOffMedianTime checks that block sync does not
// halt on a committed block whose time differs from this binary's median-time
// rule, such as a block committed before a change to that rule.
func TestBlockSyncCommittedBlockOffMedianTime(t *testing.T) {
	config = test.ResetTestRoot("blocksync_off_median_time_test")
	defer os.RemoveAll(config.RootDir)
	genDoc, privVals := genesisDocWithValsPowers([]int64{30})

	const (
		maxBlockHeight  = int64(10)
		offMedianHeight = int64(5)
	)

	reactorPairs := []ReactorPair{
		newReactor(t, log.TestingLogger(), genDoc, privVals, maxBlockHeight,
			withDeterministicVoteTimes(), withOffMedianTimeBlock(offMedianHeight)),
		newReactor(t, log.TestingLogger(), genDoc, privVals, 0, withDeterministicVoteTimes()),
	}
	requireOffMedianTime(t, reactorPairs[0], offMedianHeight)

	p2p.MakeConnectedSwitches(config.P2P, len(reactorPairs), func(i int, s *p2p.Switch) *p2p.Switch {
		s.AddReactor("BLOCKSYNC", reactorPairs[i].reactor)
		return s
	}, p2p.Connect2Switches)

	defer func() {
		for _, r := range reactorPairs {
			require.NoError(t, r.reactor.Stop())
			require.NoError(t, r.app.Stop())
		}
	}()

	// Block sync needs block N+1 to verify block N, so the follower can sync
	// up to maxBlockHeight-1.
	follower := reactorPairs[1].reactor
	require.Eventually(t, func() bool {
		return follower.store.Height() >= maxBlockHeight-1
	}, 20*time.Second, 100*time.Millisecond, "follower did not sync past the off-median block")
}

// TestReactorAdaptiveCommittedBlockOffMedianTime checks the same for adaptive
// sync, which validates blocks before handing them to consensus.
func TestReactorAdaptiveCommittedBlockOffMedianTime(t *testing.T) {
	const offMedianHeight = int64(3)

	ts := newAdaptiveSyncTestSuite(t, "blocksync_adaptive_off_median_time")

	var (
		provider = newReactor(t, ts.logger, ts.genDoc, ts.privVals, 4,
			withDeterministicVoteTimes(), withOffMedianTimeBlock(offMedianHeight))
		follower = newReactor(t, ts.logger, ts.genDoc, ts.privVals, 2, withDeterministicVoteTimes())
	)
	requireOffMedianTime(t, provider, offMedianHeight)

	follower.reactor.adaptiveSyncEnabled = true
	follower.reactor.intervalStatusUpdate = adaptiveSyncInternalStatusUpdate
	provider.reactor.intervalStatusUpdate = adaptiveSyncInternalStatusUpdate

	ts.blockIngestor.SetOnIngest(func(consensus.IngestCandidate) error { return nil })

	p2p.MakeConnectedSwitches(ts.config.P2P, 2, func(i int, s *p2p.Switch) *p2p.Switch {
		switch i {
		case 0:
			s.AddReactor("BLOCKSYNC", provider.reactor)
		case 1:
			s.AddReactor("BLOCKSYNC", follower.reactor)
			s.AddReactor("CONSENSUS", ts.blockIngestor)
		}
		return s
	}, p2p.Connect2Switches)

	// The block reaches consensus only after passing validation.
	require.Eventually(t, func() bool {
		return len(ts.blockIngestor.Requests()) == 1
	}, 5*time.Second, 100*time.Millisecond, "off-median block was not ingested")
	require.Equal(t, offMedianHeight, ts.blockIngestor.Requests()[0].Height())
}
