package eth

import (
	"bytes"
	"encoding/json"
	"math/big"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/types"
)

func TestGenesisBlockFileChilizMainnet(t *testing.T) {
	raw, err := os.ReadFile("../config/embedded/chiliz.json")
	if err != nil {
		t.Fatal(err)
	}
	var genesis core.Genesis
	if err := json.Unmarshal(raw, &genesis); err != nil {
		t.Fatal(err)
	}
	block := genesis.ToBlock()
	if block.Hash().Hex() != "0xd79fa059ef8cdfcf72676df19e209ee014183a5fa1cf132b2ff9288dbbcf5042" {
		t.Fatalf("unexpected mainnet genesis hash %s", block.Hash())
	}
	file := buildGenesisBlockFile(block, genesis.Alloc)
	if len(file.Txs) != 34 || len(file.Traces) != 34 || len(file.StorageContracts) != 8 {
		t.Fatalf("unexpected genesis counts: %d txs, %d traces, %d storage contracts", len(file.Txs), len(file.Traces), len(file.StorageContracts))
	}
	seen := make(map[string]bool)
	counts := make(map[int]int)
	for i, tx := range file.Txs {
		if len(tx.ID) != 66 || seen[tx.ID] || tx.TransactionIndex != int64(i) {
			t.Fatalf("invalid or duplicate synthetic transaction %q", tx.ID)
		}
		if _, err := hexutil.Decode(tx.ID); err != nil {
			t.Fatalf("transaction ID must be bytes32: %v", err)
		}
		seen[tx.ID] = true
		kind, err := strconv.Atoi(tx.ID[2:4])
		if err != nil {
			t.Fatal(err)
		}
		counts[kind]++
		trace := file.Traces[i]
		if trace.TxID != tx.ID || trace.To != tx.To || trace.From != tx.From {
			t.Fatalf("trace does not match transaction %s", tx.ID)
		}
		account := genesis.Alloc[common.HexToAddress(tx.To)]
		switch kind {
		case 1:
			if account.Balance == nil || tx.Value.ToInt().Cmp(account.Balance) != 0 || trace.Value.ToInt().Cmp(account.Balance) != 0 || trace.CallCreateType != "call" {
				t.Fatalf("incorrect initial balance for %s", tx.To)
			}
		case 2:
			if len(account.Code) == 0 || !bytes.Equal(tx.Input, account.Code) || !bytes.Equal(trace.Input, account.Code) || !bytes.Equal(trace.Output, account.Code) || trace.CallCreateType != "create" {
				t.Fatalf("incorrect initial code for %s", tx.To)
			}
		case 3:
			if tx.To != "0xeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee" || len(tx.Input) != 0 || len(trace.Output) != 0 || tx.Value.ToInt().Sign() != 0 {
				t.Fatal("incorrect native-token marker")
			}
		default:
			t.Fatalf("unexpected genesis record kind %d", kind)
		}
	}
	if counts[1] != 25 || counts[2] != 8 || counts[3] != 1 {
		t.Fatalf("unexpected record kinds: %v", counts)
	}
	for _, address := range file.StorageContracts {
		if len(genesis.Alloc[common.HexToAddress(address)].Storage) == 0 {
			t.Fatalf("unexpected storage contract %s", address)
		}
	}
}

func TestGenesisBlockFileOmitsNonceOnlyAndZeroBalanceRecords(t *testing.T) {
	withCode := common.HexToAddress("0x10")
	nonceOnly := common.HexToAddress("0x20")
	storageOnly := common.HexToAddress("0x30")
	alloc := types.GenesisAlloc{
		withCode:    {Balance: big.NewInt(7), Code: []byte{0x60, 0x00}},
		nonceOnly:   {Balance: new(big.Int), Nonce: 9},
		storageOnly: {Storage: map[common.Hash]common.Hash{common.HexToHash("0x01"): common.HexToHash("0x02")}},
	}
	block := types.NewBlockWithHeader(&types.Header{Number: new(big.Int), Difficulty: new(big.Int)})
	file := buildGenesisBlockFile(block, alloc)
	if len(file.Txs) != 3 || len(file.Traces) != 3 || len(file.StorageContracts) != 1 || file.StorageContracts[0] != strings.ToLower(storageOnly.Hex()) {
		t.Fatal("nonce/storage-only accounts must not create balance/code records")
	}
	if !strings.HasPrefix(file.Txs[0].ID, "0x01") || !strings.HasPrefix(file.Txs[1].ID, "0x02") || file.Txs[0].To != file.Txs[1].To {
		t.Fatal("an account with balance and code needs both record types")
	}
}
