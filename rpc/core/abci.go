package core

import (
	"context"

	abci "github.com/tachibtc/cometbft/abci/types"
	"github.com/tachibtc/cometbft/libs/bytes"
	"github.com/tachibtc/cometbft/proxy"
	ctypes "github.com/tachibtc/cometbft/rpc/core/types"
	rpctypes "github.com/tachibtc/cometbft/rpc/jsonrpc/types"
)

// ABCIQuery queries the application for some information.
// More: https://docs.cometbft.com/v0.38/spec/rpc/#abciquery
func (env *Environment) ABCIQuery(
	_ *rpctypes.Context,
	path string,
	data bytes.HexBytes,
	height int64,
	prove bool,
) (*ctypes.ResultABCIQuery, error) {
	resQuery, err := env.ProxyAppQuery.Query(context.TODO(), &abci.RequestQuery{
		Path:   path,
		Data:   data,
		Height: height,
		Prove:  prove,
	})
	if err != nil {
		return nil, err
	}

	return &ctypes.ResultABCIQuery{Response: *resQuery}, nil
}

// ABCIInfo gets some info about the application.
// More: https://docs.cometbft.com/v0.38/spec/rpc/#abciinfo
func (env *Environment) ABCIInfo(_ *rpctypes.Context) (*ctypes.ResultABCIInfo, error) {
	resInfo, err := env.ProxyAppQuery.Info(context.TODO(), proxy.RequestInfo)
	if err != nil {
		return nil, err
	}

	return &ctypes.ResultABCIInfo{Response: *resInfo}, nil
}
