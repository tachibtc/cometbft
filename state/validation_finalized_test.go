package state_test

import (
	"testing"
	"time"

	"github.com/go-kit/kit/metrics"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	dbm "github.com/cometbft/cometbft-db"

	"github.com/tachibtc/cometbft/libs/log"
	mpmocks "github.com/tachibtc/cometbft/mempool/mocks"
	sm "github.com/tachibtc/cometbft/state"
	"github.com/tachibtc/cometbft/store"
	"github.com/tachibtc/cometbft/types"
)

// TestValidateFinalizedBlockTime checks that a block committed by +2/3 of the
// validator set is accepted even if its time differs from the median time of
// its LastCommit, while every other check, and strict validation of proposals,
// is unchanged.
func TestValidateFinalizedBlockTime(t *testing.T) {
	proxyApp := newTestApp()
	require.NoError(t, proxyApp.Start())
	defer proxyApp.Stop() //nolint:errcheck // ignore for tests

	state, stateDB, privVals := makeState(3, 1)
	stateStore := sm.NewStore(stateDB, sm.StoreOptions{
		DiscardABCIResponses: false,
	})
	mp := &mpmocks.Mempool{}
	mp.On("Lock").Return()
	mp.On("Unlock").Return()
	mp.On("FlushAppConn", mock.Anything).Return(nil)
	mp.On("Update",
		mock.Anything,
		mock.Anything,
		mock.Anything,
		mock.Anything,
		mock.Anything,
		mock.Anything).Return(nil)

	blockStore := store.NewBlockStore(dbm.NewMemDB())

	metrics := sm.NopMetrics()
	mismatches := &testCounter{}
	metrics.FinalizedBlockTimeMismatches = mismatches

	newBlockExec := func() *sm.BlockExecutor {
		return sm.NewBlockExecutor(
			stateStore,
			log.TestingLogger(),
			proxyApp.Consensus(),
			mp,
			sm.EmptyEvidencePool{},
			blockStore,
			sm.BlockExecutorWithMetrics(metrics),
		)
	}
	blockExec := newBlockExec()
	lastCommit := &types.Commit{}
	var lastExtCommit *types.ExtendedCommit

	// Build up state for test
	for height := int64(1); height < 3; height++ {
		var err error
		state, _, lastExtCommit, err = makeAndCommitGoodBlock(
			state, height, lastCommit, state.Validators.GetProposer().Address, blockExec, privVals, nil)
		require.NoError(t, err, "height %d", height)
		lastCommit = lastExtCommit.ToCommit()
	}

	const height = int64(3)

	// offMedianBlock returns a block at height whose time is shifted by d from
	// the median time of its LastCommit, and its ID. Each subtest uses its own
	// d, so no block hash is shared with the executor's validated-block cache.
	offMedianBlock := func(t *testing.T, d time.Duration) (*types.Block, types.BlockID) {
		t.Helper()
		block, err := makeBlock(state, height, lastCommit)
		require.NoError(t, err)
		block.Time = block.Time.Add(d)
		parts, err := block.MakePartSet(types.BlockPartSizeBytes)
		require.NoError(t, err)
		return block, types.BlockID{Hash: block.Hash(), PartSetHeader: parts.Header()}
	}
	commitFor := func(t *testing.T, blockID types.BlockID) *types.Commit {
		t.Helper()
		extCommit, _, err := makeValidCommit(height, blockID, state.Validators, privVals)
		require.NoError(t, err)
		return extCommit.ToCommit()
	}

	t.Run("committed block off the median time is accepted", func(t *testing.T) {
		block, blockID := offMedianBlock(t, time.Second)
		commit := commitFor(t, blockID)

		before := mismatches.Value()
		require.ErrorContains(t, newBlockExec().ValidateBlock(state, block), "invalid block time")
		require.NoError(t, newBlockExec().ValidateFinalizedBlock(state, blockID, block, commit))
		require.NoError(t, newBlockExec().ValidateFinalizedBlockSkipLastCommit(state, blockID, block, commit))
		require.Equal(t, before+2, mismatches.Value())
	})

	t.Run("block on the median time does not count a mismatch", func(t *testing.T) {
		block, blockID := offMedianBlock(t, 0)
		commit := commitFor(t, blockID)

		before := mismatches.Value()
		require.NoError(t, newBlockExec().ValidateFinalizedBlock(state, blockID, block, commit))
		require.Equal(t, before, mismatches.Value())
	})

	t.Run("no commit", func(t *testing.T) {
		block, blockID := offMedianBlock(t, 2*time.Second)
		err := newBlockExec().ValidateFinalizedBlock(state, blockID, block, nil)
		require.ErrorContains(t, err, "invalid block time")
	})

	t.Run("commit for another block", func(t *testing.T) {
		block, blockID := offMedianBlock(t, 3*time.Second)
		_, otherID := offMedianBlock(t, 4*time.Second)
		err := newBlockExec().ValidateFinalizedBlock(state, blockID, block, commitFor(t, otherID))
		require.ErrorContains(t, err, "invalid block time")

		// Nor may the commit and block ID both name another block.
		err = newBlockExec().ValidateFinalizedBlock(state, otherID, block, commitFor(t, otherID))
		require.ErrorContains(t, err, "invalid block time")
	})

	t.Run("commit without +2/3 of the voting power", func(t *testing.T) {
		block, blockID := offMedianBlock(t, 5*time.Second)
		commit := commitFor(t, blockID)
		// Only one of the three validators signed.
		for i := 1; i < len(commit.Signatures); i++ {
			commit.Signatures[i] = types.NewCommitSigAbsent()
		}
		err := newBlockExec().ValidateFinalizedBlock(state, blockID, block, commit)
		require.ErrorContains(t, err, "invalid block time")
	})

	t.Run("commit with a forged signature", func(t *testing.T) {
		block, blockID := offMedianBlock(t, 6*time.Second)
		commit := commitFor(t, blockID)
		commit.Signatures[0].Signature[0] ^= 0xff
		err := newBlockExec().ValidateFinalizedBlock(state, blockID, block, commit)
		require.ErrorContains(t, err, "invalid block time")
	})

	t.Run("committed block before last block time is still rejected", func(t *testing.T) {
		block, blockID := offMedianBlock(t, -time.Hour)
		err := newBlockExec().ValidateFinalizedBlock(state, blockID, block, commitFor(t, blockID))
		require.ErrorContains(t, err, "not greater than last block time")
	})

	t.Run("committed block too far in the future is still rejected", func(t *testing.T) {
		blockExecWithTol := sm.NewBlockExecutor(
			stateStore,
			log.TestingLogger(),
			proxyApp.Consensus(),
			mp,
			sm.EmptyEvidencePool{},
			blockStore,
			sm.BlockExecutorWithBlockTimeTolerance(30*time.Second),
		)
		block, blockID := offMedianBlock(t, 1000*time.Hour)
		err := blockExecWithTol.ValidateFinalizedBlock(state, blockID, block, commitFor(t, blockID))
		require.ErrorContains(t, err, "too far in the future")
	})

	t.Run("committed block with another invalid field is still rejected", func(t *testing.T) {
		block, _ := offMedianBlock(t, 7*time.Second)
		block.AppHash = make([]byte, 32)
		parts, err := block.MakePartSet(types.BlockPartSizeBytes)
		require.NoError(t, err)
		blockID := types.BlockID{Hash: block.Hash(), PartSetHeader: parts.Header()}
		err = newBlockExec().ValidateFinalizedBlock(state, blockID, block, commitFor(t, blockID))
		require.ErrorContains(t, err, "wrong Block.Header.AppHash")
	})

	t.Run("ApplyFinalizedBlock applies a committed block off the median time", func(t *testing.T) {
		block, blockID := offMedianBlock(t, 8*time.Second)
		commit := commitFor(t, blockID)

		_, err := newBlockExec().ApplyBlock(state, blockID, block)
		require.ErrorContains(t, err, "invalid block time")

		newState, err := newBlockExec().ApplyFinalizedBlock(state, blockID, block, commit)
		require.NoError(t, err)
		require.Equal(t, height, newState.LastBlockHeight)
		require.Equal(t, block.Time, newState.LastBlockTime)
	})
}

// testCounter is a metrics.Counter whose value tests can read.
type testCounter struct{ value float64 }

func (c *testCounter) With(...string) metrics.Counter { return c }
func (c *testCounter) Add(delta float64)              { c.value += delta }
func (c *testCounter) Value() float64                 { return c.value }
