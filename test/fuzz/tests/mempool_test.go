//go:build gofuzz || go1.21

package tests

import (
	"testing"

	abciclient "github.com/tachibtc/cometbft/abci/client"
	"github.com/tachibtc/cometbft/abci/example/kvstore"
	"github.com/tachibtc/cometbft/config"
	cmtsync "github.com/tachibtc/cometbft/libs/sync"
	mempool "github.com/tachibtc/cometbft/mempool"
	"github.com/tachibtc/cometbft/proxy"
)

func FuzzMempool(f *testing.F) {
	app := kvstore.NewInMemoryApplication()
	mtx := new(cmtsync.Mutex)
	conn := abciclient.NewLocalClient(mtx, app)
	err := conn.Start()
	if err != nil {
		panic(err)
	}

	cfg := config.DefaultMempoolConfig()
	cfg.Broadcast = false

	appConnMem := proxy.NewAppConnMempool(conn, proxy.NopMetrics())
	mp := mempool.NewCListMempool(cfg, appConnMem, 0)

	f.Fuzz(func(t *testing.T, data []byte) {
		_ = mp.CheckTx(data, nil, mempool.TxInfo{})
	})
}
