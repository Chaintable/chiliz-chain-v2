//go:build !pipeline

package eth

import (
	"context"
	"fmt"

	"github.com/ethereum/go-ethereum/rpc"
)

type DebankAPI struct {
	eth *Ethereum
}

func NewDebankAPI(eth *Ethereum) *DebankAPI {
	return &DebankAPI{eth: eth}
}

// DebankBlock is a placeholder for the pipeline/debank demo.
// Enable the `pipeline` build tag to use the full implementation.
func (api *DebankAPI) DebankBlock(ctx context.Context, blockNrOrHash rpc.BlockNumberOrHash) (interface{}, error) {
	_ = ctx
	_ = blockNrOrHash
	return nil, fmt.Errorf("DebankBlock is disabled; build with -tags=pipeline")
}
