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
	// Tracer visibility:
	//   - At evm.depth == 0 (the outer tx call hasn't entered interpreter yet),
	//     a nested evm.Call here would hit the tracer's CaptureStart branch
	//     and replace the transaction's top frame with this synthetic 0x7005
	//     call, dropping all subsequent traces/events. Suppress tracer here.
	//   - At evm.depth >= 1 (invoked from an in-flight user contract via CALL
	//     opcode), the nested call takes the CaptureEnter branch and is
	//     recorded as a sibling of the user sub-call, which matches parity
	//     trace_transaction output.
	if evm.depth == 0 {
		savedTracer := evm.Config.Tracer
		evm.Config.Tracer = nil
		_, _, err = evm.Call(AccountRef(evm.Context.Coinbase), systemcontract.DeployerProxyContractAddress, input, 1_000_000, uint256.MustFromBig(big.NewInt(0)))
		evm.Config.Tracer = savedTracer
	} else {
		_, _, err = evm.Call(AccountRef(evm.Context.Coinbase), systemcontract.DeployerProxyContractAddress, input, 1_000_000, uint256.MustFromBig(big.NewInt(0)))
	}
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
	// See applyChilizInvocationEvmHook for the depth==0 rationale.
	if evm.depth == 0 {
		savedTracer := evm.Config.Tracer
		evm.Config.Tracer = nil
		_, gas, err = evm.Call(AccountRef(evm.Context.Coinbase), systemcontract.DeployerProxyContractAddress, input, gas, uint256.MustFromBig(big.NewInt(0)))
		evm.Config.Tracer = savedTracer
	} else {
		_, gas, err = evm.Call(AccountRef(evm.Context.Coinbase), systemcontract.DeployerProxyContractAddress, input, gas, uint256.MustFromBig(big.NewInt(0)))
	}
	if err != nil {
		return gas, ErrNotAllowed
	}
	return gas, nil
}
