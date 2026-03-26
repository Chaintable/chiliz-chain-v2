package vm

import (
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/systemcontract"
	"github.com/holiman/uint256"
)

func applyChilizInvocationEvmHook(evm *EVM, addr common.Address, gas uint64) (leftOverGas uint64, err error) {
	if systemcontract.IsSystemContract(addr) {
		return gas, nil
	}
	input, err := systemcontract.EvmHooksAbi.Pack("checkContractActive", addr)
	if err != nil {
		return gas, ErrNotAllowed
	}
	// don't charge gas for this interceptor to let simple send be 21000 gas
	//
	// Temporarily disable the tracer during the hook call. This internal
	// validation call happens at the same evm.depth as the outer Call/Create,
	// which would inject an extra depth-0 frame into the callTracer's stack
	// and cause all traces/events for the transaction to be silently dropped.
	savedTracer := evm.Config.Tracer
	evm.Config.Tracer = nil
	_, _, err = evm.Call(AccountRef(evm.Context.Coinbase), systemcontract.DeployerProxyContractAddress, input, 1_000_000, uint256.MustFromBig(big.NewInt(0)))
	evm.Config.Tracer = savedTracer
	if err != nil {
		return gas, ErrNotAllowed
	}
	return gas, nil
}

func applyChilizDeploymentEvmHook(evm *EVM, caller ContractRef, addr common.Address, gas uint64) (leftOverGas uint64, err error) {
	if systemcontract.IsSystemContract(addr) {
		return gas, nil
	}
	var input []byte
	if evm.chainRules.HasDeployOrigin && !evm.chainRules.DeployerFactory {
		input, err = systemcontract.EvmHooksAbi.Pack("registerDeployedContract", evm.TxContext.Origin, addr)
	} else {
		input, err = systemcontract.EvmHooksAbi.Pack("registerDeployedContract", caller.Address(), addr)
	}
	if err != nil {
		return gas, ErrNotAllowed
	}
	// Temporarily disable the tracer during the hook call (same reason as
	// applyChilizInvocationEvmHook: avoid extra depth-0 frame).
	savedTracer := evm.Config.Tracer
	evm.Config.Tracer = nil
	_, gas, err = evm.Call(AccountRef(evm.Context.Coinbase), systemcontract.DeployerProxyContractAddress, input, gas, uint256.MustFromBig(big.NewInt(0)))
	evm.Config.Tracer = savedTracer
	if err != nil {
		return gas, ErrNotAllowed
	}
	return gas, nil
}
