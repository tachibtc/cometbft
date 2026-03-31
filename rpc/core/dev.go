package core

import (
	ctypes "github.com/tachibtc/cometbft/rpc/core/types"
	rpctypes "github.com/tachibtc/cometbft/rpc/jsonrpc/types"
)

// UnsafeFlushMempool removes all transactions from the mempool.
func (env *Environment) UnsafeFlushMempool(*rpctypes.Context) (*ctypes.ResultUnsafeFlushMempool, error) {
	env.Mempool.Flush()
	return &ctypes.ResultUnsafeFlushMempool{}, nil
}
