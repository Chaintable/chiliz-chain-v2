package eth

import (
	"fmt"
	"math/big"
	"sort"
	"strings"

	ptypes "github.com/Chaintable/pipeline/types"
	"github.com/Chaintable/pipeline/util"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
)

// buildGenesisBlockFile projects initial balances, code and the native-token
// marker into the same synthetic records as Pipeline's genesis hook.
func buildGenesisBlockFile(block *types.Block, alloc types.GenesisAlloc) *ptypes.BlockFile {
	file := &ptypes.BlockFile{
		Block:            util.BuildPipelineBlock(block),
		Txs:              []ptypes.Transaction{},
		Events:           []ptypes.Event{},
		Traces:           []ptypes.Trace{},
		ErrorEvents:      []ptypes.Event{},
		ErrorTraces:      []ptypes.Trace{},
		StorageContracts: []string{},
	}
	addresses := make([]common.Address, 0, len(alloc))
	for address := range alloc {
		addresses = append(addresses, address)
	}
	sort.Slice(addresses, func(i, j int) bool { return addresses[i].Hex() < addresses[j].Hex() })
	for _, address := range addresses {
		account := alloc[address]
		if len(account.Storage) > 0 {
			file.StorageContracts = append(file.StorageContracts, strings.ToLower(address.Hex()))
		}
		if account.Balance != nil && account.Balance.Sign() > 0 {
			appendGenesisRecord(file, 1, address, nil, account.Balance)
		}
		if len(account.Code) > 0 {
			appendGenesisRecord(file, 2, address, account.Code, new(big.Int))
		}
	}
	appendGenesisRecord(file, 3, common.HexToAddress("0xeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"), nil, new(big.Int))
	return file
}

func appendGenesisRecord(file *ptypes.BlockFile, kind int, address common.Address, input []byte, value *big.Int) {
	zero := "0x0000000000000000000000000000000000000000"
	to := strings.ToLower(address.Hex())
	id := fmt.Sprintf("0x%02d%022d%s", kind, 0, strings.TrimPrefix(to, "0x"))
	file.Txs = append(file.Txs, ptypes.Transaction{
		ID:               id,
		From:             zero,
		To:               to,
		Gas:              new(big.Int),
		GasPrice:         new(big.Int),
		GasUsed:          new(big.Int),
		Status:           true,
		GasFeeCap:        new(big.Int),
		GasTipCap:        new(big.Int),
		Input:            input,
		Nonce:            new(big.Int),
		TransactionIndex: int64(len(file.Txs)),
		Value:            (*hexutil.Big)(value),
	})
	trace := ptypes.Trace{
		ID:             util.ToHash([]string{id, "", "0"}),
		From:           zero,
		To:             to,
		Gas:            new(big.Int),
		GasUsed:        new(big.Int),
		Input:          input,
		Output:         input,
		Value:          (*hexutil.Big)(value),
		CallCreateType: "create",
		TxID:           id,
		TraceAddress:   []int64{},
	}
	if kind == 1 {
		trace.CallCreateType = "call"
		trace.CallType = "call"
	}
	file.Traces = append(file.Traces, trace)
}
