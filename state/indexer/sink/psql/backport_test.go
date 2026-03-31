package psql

import (
	"github.com/tachibtc/cometbft/state/indexer"
	"github.com/tachibtc/cometbft/state/txindex"
)

var (
	_ indexer.BlockIndexer = BackportBlockIndexer{}
	_ txindex.TxIndexer    = BackportTxIndexer{}
)
