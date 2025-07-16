// Code generated - DO NOT EDIT.
// This file is a generated binding and any manual changes will be lost.

package gethwrappers

import (
	"errors"
	"math/big"
	"strings"

	ethereum "github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/event"
)

// Reference imports to suppress errors if they are not otherwise used.
var (
	_ = errors.New
	_ = big.NewInt
	_ = strings.NewReader
	_ = ethereum.NotFound
	_ = bind.Bind
	_ = common.Big1
	_ = types.BloomLookup
	_ = event.NewSubscription
	_ = abi.ConvertType
)

// KeystoneForwarderTestTransmissionInfo is an auto generated low-level Go binding around an user-defined struct.
type KeystoneForwarderTestTransmissionInfo struct {
	TransmissionId  [32]byte
	State           uint8
	Transmitter     common.Address
	InvalidReceiver bool
	Success         bool
	GasLimit        *big.Int
}

// KeystoneForwarderTestMetaData contains all meta data concerning the KeystoneForwarderTest contract.
var KeystoneForwarderTestMetaData = &bind.MetaData{
	ABI: "[{\"inputs\":[],\"stateMutability\":\"nonpayable\",\"type\":\"constructor\"},{\"inputs\":[{\"internalType\":\"bytes32\",\"name\":\"transmissionId\",\"type\":\"bytes32\"}],\"name\":\"AlreadyAttempted\",\"type\":\"error\"},{\"inputs\":[],\"name\":\"FaultToleranceMustBePositive\",\"type\":\"error\"},{\"inputs\":[{\"internalType\":\"bytes32\",\"name\":\"transmissionId\",\"type\":\"bytes32\"}],\"name\":\"InsufficientGasForRouting\",\"type\":\"error\"},{\"inputs\":[],\"name\":\"InvalidReport\",\"type\":\"error\"},{\"inputs\":[],\"name\":\"UnauthorizedForwarder\",\"type\":\"error\"},{\"anonymous\":false,\"inputs\":[{\"indexed\":true,\"internalType\":\"address\",\"name\":\"receiver\",\"type\":\"address\"},{\"indexed\":true,\"internalType\":\"bytes32\",\"name\":\"workflowExecutionId\",\"type\":\"bytes32\"},{\"indexed\":true,\"internalType\":\"bytes2\",\"name\":\"reportId\",\"type\":\"bytes2\"},{\"indexed\":false,\"internalType\":\"bool\",\"name\":\"result\",\"type\":\"bool\"}],\"name\":\"ReportProcessed\",\"type\":\"event\"},{\"inputs\":[{\"internalType\":\"address\",\"name\":\"forwarder\",\"type\":\"address\"}],\"name\":\"addForwarder\",\"outputs\":[],\"stateMutability\":\"nonpayable\",\"type\":\"function\"},{\"inputs\":[{\"internalType\":\"address\",\"name\":\"receiver\",\"type\":\"address\"},{\"internalType\":\"bytes32\",\"name\":\"workflowExecutionId\",\"type\":\"bytes32\"},{\"internalType\":\"bytes2\",\"name\":\"reportId\",\"type\":\"bytes2\"}],\"name\":\"getTransmissionId\",\"outputs\":[{\"internalType\":\"bytes32\",\"name\":\"\",\"type\":\"bytes32\"}],\"stateMutability\":\"pure\",\"type\":\"function\"},{\"inputs\":[{\"internalType\":\"address\",\"name\":\"receiver\",\"type\":\"address\"},{\"internalType\":\"bytes32\",\"name\":\"workflowExecutionId\",\"type\":\"bytes32\"},{\"internalType\":\"bytes2\",\"name\":\"reportId\",\"type\":\"bytes2\"}],\"name\":\"getTransmissionInfo\",\"outputs\":[{\"components\":[{\"internalType\":\"bytes32\",\"name\":\"transmissionId\",\"type\":\"bytes32\"},{\"internalType\":\"enumKeystoneForwarderTest.TransmissionState\",\"name\":\"state\",\"type\":\"uint8\"},{\"internalType\":\"address\",\"name\":\"transmitter\",\"type\":\"address\"},{\"internalType\":\"bool\",\"name\":\"invalidReceiver\",\"type\":\"bool\"},{\"internalType\":\"bool\",\"name\":\"success\",\"type\":\"bool\"},{\"internalType\":\"uint80\",\"name\":\"gasLimit\",\"type\":\"uint80\"}],\"internalType\":\"structKeystoneForwarderTest.TransmissionInfo\",\"name\":\"\",\"type\":\"tuple\"}],\"stateMutability\":\"view\",\"type\":\"function\"},{\"inputs\":[{\"internalType\":\"address\",\"name\":\"receiver\",\"type\":\"address\"},{\"internalType\":\"bytes32\",\"name\":\"workflowExecutionId\",\"type\":\"bytes32\"},{\"internalType\":\"bytes2\",\"name\":\"reportId\",\"type\":\"bytes2\"}],\"name\":\"getTransmitter\",\"outputs\":[{\"internalType\":\"address\",\"name\":\"\",\"type\":\"address\"}],\"stateMutability\":\"view\",\"type\":\"function\"},{\"inputs\":[{\"internalType\":\"address\",\"name\":\"forwarder\",\"type\":\"address\"}],\"name\":\"isForwarder\",\"outputs\":[{\"internalType\":\"bool\",\"name\":\"\",\"type\":\"bool\"}],\"stateMutability\":\"view\",\"type\":\"function\"},{\"inputs\":[{\"internalType\":\"address\",\"name\":\"forwarder\",\"type\":\"address\"}],\"name\":\"removeForwarder\",\"outputs\":[],\"stateMutability\":\"nonpayable\",\"type\":\"function\"},{\"inputs\":[{\"internalType\":\"address\",\"name\":\"receiver\",\"type\":\"address\"},{\"internalType\":\"bytes\",\"name\":\"rawReport\",\"type\":\"bytes\"},{\"internalType\":\"bytes\",\"name\":\"reportContext\",\"type\":\"bytes\"},{\"internalType\":\"bytes[]\",\"name\":\"signatures\",\"type\":\"bytes[]\"}],\"name\":\"report\",\"outputs\":[],\"stateMutability\":\"nonpayable\",\"type\":\"function\"},{\"inputs\":[{\"internalType\":\"bytes32\",\"name\":\"transmissionId\",\"type\":\"bytes32\"},{\"internalType\":\"address\",\"name\":\"transmitter\",\"type\":\"address\"}],\"name\":\"route\",\"outputs\":[{\"internalType\":\"bool\",\"name\":\"\",\"type\":\"bool\"}],\"stateMutability\":\"nonpayable\",\"type\":\"function\"}]",
	Bin: "0x608060405234801561000f575f80fd5b5060015f803073ffffffffffffffffffffffffffffffffffffffff1673ffffffffffffffffffffffffffffffffffffffff1681526020019081526020015f205f6101000a81548160ff021916908315150217905550611165806100715f395ff3fe608060405234801561000f575f80fd5b5060043610610086575f3560e01c80635c41d2fe116100595780635c41d2fe146101225780638864b8641461013e578063abcef5541461016e578063ed0802191461019e57610086565b8063112895651461008a578063272cbd93146100a6578063354bdd66146100d65780634d93172d14610106575b5f80fd5b6100a4600480360381019061009f9190610b72565b6101ce565b005b6100c060048036038101906100bb9190610cbe565b61036d565b6040516100cd9190610e56565b60405180910390f35b6100f060048036038101906100eb9190610cbe565b61054a565b6040516100fd9190610e7e565b60405180910390f35b610120600480360381019061011b9190610e97565b610582565b005b61013c60048036038101906101379190610e97565b6105d8565b005b61015860048036038101906101539190610cbe565b61062f565b6040516101659190610ed1565b60405180910390f35b61018860048036038101906101839190610e97565b610676565b6040516101959190610ef9565b60405180910390f35b6101b860048036038101906101b39190610f12565b6106c7565b6040516101c59190610ef9565b60405180910390f35b606d86869050101561020c576040517fb55ac75400000000000000000000000000000000000000000000000000000000815260040160405180910390fd5b5f805f61025b89898080601f0160208091040260200160405190810160405280939291908181526020018383808284375f81840152601f19601f820116905080830192505050505050506109ca565b8094508193508295505050505f3073ffffffffffffffffffffffffffffffffffffffff1663ed08021961028f8d878761054a565b336040518363ffffffff1660e01b81526004016102ad929190610f50565b6020604051808303815f875af11580156102c9573d5f803e3d5ffd5b505050506040513d601f19601f820116820180604052508101906102ed9190610fa1565b9050827dffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff1916848c73ffffffffffffffffffffffffffffffffffffffff167f3617b009e9785c42daebadb6d3fb553243a4bf586d07ea72d65d80013ce116b5846040516103589190610ef9565b60405180910390a45050505050505050505050565b6103756109ef565b5f61038185858561054a565b90505f60015f8381526020019081526020015f206040518060800160405290815f82015f9054906101000a900473ffffffffffffffffffffffffffffffffffffffff1673ffffffffffffffffffffffffffffffffffffffff1673ffffffffffffffffffffffffffffffffffffffff1681526020015f820160149054906101000a900460ff161515151581526020015f820160159054906101000a900460ff161515151581526020015f820160169054906101000a900469ffffffffffffffffffff1669ffffffffffffffffffff1669ffffffffffffffffffff168152505090505f8073ffffffffffffffffffffffffffffffffffffffff16825f015173ffffffffffffffffffffffffffffffffffffffff16036104a0575f90506104ca565b8160200151156104b357600290506104c9565b81604001516104c35760036104c6565b60015b90505b5b6040518060c001604052808481526020018260038111156104ee576104ed610d1d565b5b8152602001835f015173ffffffffffffffffffffffffffffffffffffffff168152602001836020015115158152602001836040015115158152602001836060015169ffffffffffffffffffff1681525093505050509392505050565b5f8360601b838360405160200161056393929190611057565b6040516020818303038152906040528051906020012090509392505050565b5f805f8373ffffffffffffffffffffffffffffffffffffffff1673ffffffffffffffffffffffffffffffffffffffff1681526020019081526020015f205f6101000a81548160ff02191690831515021790555050565b60015f808373ffffffffffffffffffffffffffffffffffffffff1673ffffffffffffffffffffffffffffffffffffffff1681526020019081526020015f205f6101000a81548160ff02191690831515021790555050565b5f60015f61063e86868661054a565b81526020019081526020015f205f015f9054906101000a900473ffffffffffffffffffffffffffffffffffffffff1690509392505050565b5f805f8373ffffffffffffffffffffffffffffffffffffffff1673ffffffffffffffffffffffffffffffffffffffff1681526020019081526020015f205f9054906101000a900460ff169050919050565b5f805f3373ffffffffffffffffffffffffffffffffffffffff1673ffffffffffffffffffffffffffffffffffffffff1681526020019081526020015f205f9054906101000a900460ff16610747576040517fd79e123d00000000000000000000000000000000000000000000000000000000815260040160405180910390fd5b5f6113886161a861075891906110c9565b5a61076391906110fc565b905061271062015f906113886161a861077c91906110c9565b61078691906110c9565b61079091906110c9565b8110156107d457836040517f0bfecd630000000000000000000000000000000000000000000000000000000081526004016107cb9190610e7e565b60405180910390fd5b5f60015f8681526020019081526020015f206040518060800160405290815f82015f9054906101000a900473ffffffffffffffffffffffffffffffffffffffff1673ffffffffffffffffffffffffffffffffffffffff1673ffffffffffffffffffffffffffffffffffffffff1681526020015f820160149054906101000a900460ff161515151581526020015f820160159054906101000a900460ff161515151581526020015f820160169054906101000a900469ffffffffffffffffffff1669ffffffffffffffffffff1669ffffffffffffffffffff168152505090508060400151806108c3575080602001515b1561090557846040517fa53dc8ca0000000000000000000000000000000000000000000000000000000081526004016108fc9190610e7e565b60405180910390fd5b8360015f8781526020019081526020015f205f015f6101000a81548173ffffffffffffffffffffffffffffffffffffffff021916908373ffffffffffffffffffffffffffffffffffffffff1602179055508160015f8781526020019081526020015f205f0160166101000a81548169ffffffffffffffffffff021916908369ffffffffffffffffffff1602179055506001805f8781526020019081526020015f205f0160146101000a81548160ff0219169083151502179055505f9250505092915050565b5f805f60218401519250604584015160086018021c9150608b84015190509193909250565b6040518060c001604052805f80191681526020015f6003811115610a1657610a15610d1d565b5b81526020015f73ffffffffffffffffffffffffffffffffffffffff1681526020015f151581526020015f151581526020015f69ffffffffffffffffffff1681525090565b5f80fd5b5f80fd5b5f73ffffffffffffffffffffffffffffffffffffffff82169050919050565b5f610a8b82610a62565b9050919050565b610a9b81610a81565b8114610aa5575f80fd5b50565b5f81359050610ab681610a92565b92915050565b5f80fd5b5f80fd5b5f80fd5b5f8083601f840112610add57610adc610abc565b5b8235905067ffffffffffffffff811115610afa57610af9610ac0565b5b602083019150836001820283011115610b1657610b15610ac4565b5b9250929050565b5f8083601f840112610b3257610b31610abc565b5b8235905067ffffffffffffffff811115610b4f57610b4e610ac0565b5b602083019150836020820283011115610b6b57610b6a610ac4565b5b9250929050565b5f805f805f805f6080888a031215610b8d57610b8c610a5a565b5b5f610b9a8a828b01610aa8565b975050602088013567ffffffffffffffff811115610bbb57610bba610a5e565b5b610bc78a828b01610ac8565b9650965050604088013567ffffffffffffffff811115610bea57610be9610a5e565b5b610bf68a828b01610ac8565b9450945050606088013567ffffffffffffffff811115610c1957610c18610a5e565b5b610c258a828b01610b1d565b925092505092959891949750929550565b5f819050919050565b610c4881610c36565b8114610c52575f80fd5b50565b5f81359050610c6381610c3f565b92915050565b5f7fffff00000000000000000000000000000000000000000000000000000000000082169050919050565b610c9d81610c69565b8114610ca7575f80fd5b50565b5f81359050610cb881610c94565b92915050565b5f805f60608486031215610cd557610cd4610a5a565b5b5f610ce286828701610aa8565b9350506020610cf386828701610c55565b9250506040610d0486828701610caa565b9150509250925092565b610d1781610c36565b82525050565b7f4e487b71000000000000000000000000000000000000000000000000000000005f52602160045260245ffd5b60048110610d5b57610d5a610d1d565b5b50565b5f819050610d6b82610d4a565b919050565b5f610d7a82610d5e565b9050919050565b610d8a81610d70565b82525050565b610d9981610a81565b82525050565b5f8115159050919050565b610db381610d9f565b82525050565b5f69ffffffffffffffffffff82169050919050565b610dd781610db9565b82525050565b60c082015f820151610df15f850182610d0e565b506020820151610e046020850182610d81565b506040820151610e176040850182610d90565b506060820151610e2a6060850182610daa565b506080820151610e3d6080850182610daa565b5060a0820151610e5060a0850182610dce565b50505050565b5f60c082019050610e695f830184610ddd565b92915050565b610e7881610c36565b82525050565b5f602082019050610e915f830184610e6f565b92915050565b5f60208284031215610eac57610eab610a5a565b5b5f610eb984828501610aa8565b91505092915050565b610ecb81610a81565b82525050565b5f602082019050610ee45f830184610ec2565b92915050565b610ef381610d9f565b82525050565b5f602082019050610f0c5f830184610eea565b92915050565b5f8060408385031215610f2857610f27610a5a565b5b5f610f3585828601610c55565b9250506020610f4685828601610aa8565b9150509250929050565b5f604082019050610f635f830185610e6f565b610f706020830184610ec2565b9392505050565b610f8081610d9f565b8114610f8a575f80fd5b50565b5f81519050610f9b81610f77565b92915050565b5f60208284031215610fb657610fb5610a5a565b5b5f610fc384828501610f8d565b91505092915050565b5f7fffffffffffffffffffffffffffffffffffffffff00000000000000000000000082169050919050565b5f819050919050565b61101161100c82610fcc565b610ff7565b82525050565b5f819050919050565b61103161102c82610c36565b611017565b82525050565b5f819050919050565b61105161104c82610c69565b611037565b82525050565b5f6110628286611000565b6014820191506110728285611020565b6020820191506110828284611040565b600282019150819050949350505050565b5f819050919050565b7f4e487b71000000000000000000000000000000000000000000000000000000005f52601160045260245ffd5b5f6110d382611093565b91506110de83611093565b92508282019050808211156110f6576110f561109c565b5b92915050565b5f61110682611093565b915061111183611093565b92508282039050818111156111295761112861109c565b5b9291505056fea26469706673582212205b70e94f9d6b489d21d8f99d8586ef7a95b765dbc41d3e7c256aa2715708224464736f6c63430008140033",
}

// KeystoneForwarderTestABI is the input ABI used to generate the binding from.
// Deprecated: Use KeystoneForwarderTestMetaData.ABI instead.
var KeystoneForwarderTestABI = KeystoneForwarderTestMetaData.ABI

// KeystoneForwarderTestBin is the compiled bytecode used for deploying new contracts.
// Deprecated: Use KeystoneForwarderTestMetaData.Bin instead.
var KeystoneForwarderTestBin = KeystoneForwarderTestMetaData.Bin

// DeployKeystoneForwarderTest deploys a new Ethereum contract, binding an instance of KeystoneForwarderTest to it.
func DeployKeystoneForwarderTest(auth *bind.TransactOpts, backend bind.ContractBackend) (common.Address, *types.Transaction, *KeystoneForwarderTest, error) {
	parsed, err := KeystoneForwarderTestMetaData.GetAbi()
	if err != nil {
		return common.Address{}, nil, nil, err
	}
	if parsed == nil {
		return common.Address{}, nil, nil, errors.New("GetABI returned nil")
	}

	address, tx, contract, err := bind.DeployContract(auth, *parsed, common.FromHex(KeystoneForwarderTestBin), backend)
	if err != nil {
		return common.Address{}, nil, nil, err
	}
	return address, tx, &KeystoneForwarderTest{KeystoneForwarderTestCaller: KeystoneForwarderTestCaller{contract: contract}, KeystoneForwarderTestTransactor: KeystoneForwarderTestTransactor{contract: contract}, KeystoneForwarderTestFilterer: KeystoneForwarderTestFilterer{contract: contract}}, nil
}

// KeystoneForwarderTest is an auto generated Go binding around an Ethereum contract.
type KeystoneForwarderTest struct {
	KeystoneForwarderTestCaller     // Read-only binding to the contract
	KeystoneForwarderTestTransactor // Write-only binding to the contract
	KeystoneForwarderTestFilterer   // Log filterer for contract events
}

// KeystoneForwarderTestCaller is an auto generated read-only Go binding around an Ethereum contract.
type KeystoneForwarderTestCaller struct {
	contract *bind.BoundContract // Generic contract wrapper for the low level calls
}

// KeystoneForwarderTestTransactor is an auto generated write-only Go binding around an Ethereum contract.
type KeystoneForwarderTestTransactor struct {
	contract *bind.BoundContract // Generic contract wrapper for the low level calls
}

// KeystoneForwarderTestFilterer is an auto generated log filtering Go binding around an Ethereum contract events.
type KeystoneForwarderTestFilterer struct {
	contract *bind.BoundContract // Generic contract wrapper for the low level calls
}

// KeystoneForwarderTestSession is an auto generated Go binding around an Ethereum contract,
// with pre-set call and transact options.
type KeystoneForwarderTestSession struct {
	Contract     *KeystoneForwarderTest // Generic contract binding to set the session for
	CallOpts     bind.CallOpts          // Call options to use throughout this session
	TransactOpts bind.TransactOpts      // Transaction auth options to use throughout this session
}

// KeystoneForwarderTestCallerSession is an auto generated read-only Go binding around an Ethereum contract,
// with pre-set call options.
type KeystoneForwarderTestCallerSession struct {
	Contract *KeystoneForwarderTestCaller // Generic contract caller binding to set the session for
	CallOpts bind.CallOpts                // Call options to use throughout this session
}

// KeystoneForwarderTestTransactorSession is an auto generated write-only Go binding around an Ethereum contract,
// with pre-set transact options.
type KeystoneForwarderTestTransactorSession struct {
	Contract     *KeystoneForwarderTestTransactor // Generic contract transactor binding to set the session for
	TransactOpts bind.TransactOpts                // Transaction auth options to use throughout this session
}

// KeystoneForwarderTestRaw is an auto generated low-level Go binding around an Ethereum contract.
type KeystoneForwarderTestRaw struct {
	Contract *KeystoneForwarderTest // Generic contract binding to access the raw methods on
}

// KeystoneForwarderTestCallerRaw is an auto generated low-level read-only Go binding around an Ethereum contract.
type KeystoneForwarderTestCallerRaw struct {
	Contract *KeystoneForwarderTestCaller // Generic read-only contract binding to access the raw methods on
}

// KeystoneForwarderTestTransactorRaw is an auto generated low-level write-only Go binding around an Ethereum contract.
type KeystoneForwarderTestTransactorRaw struct {
	Contract *KeystoneForwarderTestTransactor // Generic write-only contract binding to access the raw methods on
}

// NewKeystoneForwarderTest creates a new instance of KeystoneForwarderTest, bound to a specific deployed contract.
func NewKeystoneForwarderTest(address common.Address, backend bind.ContractBackend) (*KeystoneForwarderTest, error) {
	contract, err := bindKeystoneForwarderTest(address, backend, backend, backend)
	if err != nil {
		return nil, err
	}
	return &KeystoneForwarderTest{KeystoneForwarderTestCaller: KeystoneForwarderTestCaller{contract: contract}, KeystoneForwarderTestTransactor: KeystoneForwarderTestTransactor{contract: contract}, KeystoneForwarderTestFilterer: KeystoneForwarderTestFilterer{contract: contract}}, nil
}

// NewKeystoneForwarderTestCaller creates a new read-only instance of KeystoneForwarderTest, bound to a specific deployed contract.
func NewKeystoneForwarderTestCaller(address common.Address, caller bind.ContractCaller) (*KeystoneForwarderTestCaller, error) {
	contract, err := bindKeystoneForwarderTest(address, caller, nil, nil)
	if err != nil {
		return nil, err
	}
	return &KeystoneForwarderTestCaller{contract: contract}, nil
}

// NewKeystoneForwarderTestTransactor creates a new write-only instance of KeystoneForwarderTest, bound to a specific deployed contract.
func NewKeystoneForwarderTestTransactor(address common.Address, transactor bind.ContractTransactor) (*KeystoneForwarderTestTransactor, error) {
	contract, err := bindKeystoneForwarderTest(address, nil, transactor, nil)
	if err != nil {
		return nil, err
	}
	return &KeystoneForwarderTestTransactor{contract: contract}, nil
}

// NewKeystoneForwarderTestFilterer creates a new log filterer instance of KeystoneForwarderTest, bound to a specific deployed contract.
func NewKeystoneForwarderTestFilterer(address common.Address, filterer bind.ContractFilterer) (*KeystoneForwarderTestFilterer, error) {
	contract, err := bindKeystoneForwarderTest(address, nil, nil, filterer)
	if err != nil {
		return nil, err
	}
	return &KeystoneForwarderTestFilterer{contract: contract}, nil
}

// bindKeystoneForwarderTest binds a generic wrapper to an already deployed contract.
func bindKeystoneForwarderTest(address common.Address, caller bind.ContractCaller, transactor bind.ContractTransactor, filterer bind.ContractFilterer) (*bind.BoundContract, error) {
	parsed, err := KeystoneForwarderTestMetaData.GetAbi()
	if err != nil {
		return nil, err
	}
	return bind.NewBoundContract(address, *parsed, caller, transactor, filterer), nil
}

// Call invokes the (constant) contract method with params as input values and
// sets the output to result. The result type might be a single field for simple
// returns, a slice of interfaces for anonymous returns and a struct for named
// returns.
func (_KeystoneForwarderTest *KeystoneForwarderTestRaw) Call(opts *bind.CallOpts, result *[]interface{}, method string, params ...interface{}) error {
	return _KeystoneForwarderTest.Contract.KeystoneForwarderTestCaller.contract.Call(opts, result, method, params...)
}

// Transfer initiates a plain transaction to move funds to the contract, calling
// its default method if one is available.
func (_KeystoneForwarderTest *KeystoneForwarderTestRaw) Transfer(opts *bind.TransactOpts) (*types.Transaction, error) {
	return _KeystoneForwarderTest.Contract.KeystoneForwarderTestTransactor.contract.Transfer(opts)
}

// Transact invokes the (paid) contract method with params as input values.
func (_KeystoneForwarderTest *KeystoneForwarderTestRaw) Transact(opts *bind.TransactOpts, method string, params ...interface{}) (*types.Transaction, error) {
	return _KeystoneForwarderTest.Contract.KeystoneForwarderTestTransactor.contract.Transact(opts, method, params...)
}

// Call invokes the (constant) contract method with params as input values and
// sets the output to result. The result type might be a single field for simple
// returns, a slice of interfaces for anonymous returns and a struct for named
// returns.
func (_KeystoneForwarderTest *KeystoneForwarderTestCallerRaw) Call(opts *bind.CallOpts, result *[]interface{}, method string, params ...interface{}) error {
	return _KeystoneForwarderTest.Contract.contract.Call(opts, result, method, params...)
}

// Transfer initiates a plain transaction to move funds to the contract, calling
// its default method if one is available.
func (_KeystoneForwarderTest *KeystoneForwarderTestTransactorRaw) Transfer(opts *bind.TransactOpts) (*types.Transaction, error) {
	return _KeystoneForwarderTest.Contract.contract.Transfer(opts)
}

// Transact invokes the (paid) contract method with params as input values.
func (_KeystoneForwarderTest *KeystoneForwarderTestTransactorRaw) Transact(opts *bind.TransactOpts, method string, params ...interface{}) (*types.Transaction, error) {
	return _KeystoneForwarderTest.Contract.contract.Transact(opts, method, params...)
}

// GetTransmissionId is a free data retrieval call binding the contract method 0x354bdd66.
//
// Solidity: function getTransmissionId(address receiver, bytes32 workflowExecutionId, bytes2 reportId) pure returns(bytes32)
func (_KeystoneForwarderTest *KeystoneForwarderTestCaller) GetTransmissionId(opts *bind.CallOpts, receiver common.Address, workflowExecutionId [32]byte, reportId [2]byte) ([32]byte, error) {
	var out []interface{}
	err := _KeystoneForwarderTest.contract.Call(opts, &out, "getTransmissionId", receiver, workflowExecutionId, reportId)

	if err != nil {
		return *new([32]byte), err
	}

	out0 := *abi.ConvertType(out[0], new([32]byte)).(*[32]byte)

	return out0, err

}

// GetTransmissionId is a free data retrieval call binding the contract method 0x354bdd66.
//
// Solidity: function getTransmissionId(address receiver, bytes32 workflowExecutionId, bytes2 reportId) pure returns(bytes32)
func (_KeystoneForwarderTest *KeystoneForwarderTestSession) GetTransmissionId(receiver common.Address, workflowExecutionId [32]byte, reportId [2]byte) ([32]byte, error) {
	return _KeystoneForwarderTest.Contract.GetTransmissionId(&_KeystoneForwarderTest.CallOpts, receiver, workflowExecutionId, reportId)
}

// GetTransmissionId is a free data retrieval call binding the contract method 0x354bdd66.
//
// Solidity: function getTransmissionId(address receiver, bytes32 workflowExecutionId, bytes2 reportId) pure returns(bytes32)
func (_KeystoneForwarderTest *KeystoneForwarderTestCallerSession) GetTransmissionId(receiver common.Address, workflowExecutionId [32]byte, reportId [2]byte) ([32]byte, error) {
	return _KeystoneForwarderTest.Contract.GetTransmissionId(&_KeystoneForwarderTest.CallOpts, receiver, workflowExecutionId, reportId)
}

// GetTransmissionInfo is a free data retrieval call binding the contract method 0x272cbd93.
//
// Solidity: function getTransmissionInfo(address receiver, bytes32 workflowExecutionId, bytes2 reportId) view returns((bytes32,uint8,address,bool,bool,uint80))
func (_KeystoneForwarderTest *KeystoneForwarderTestCaller) GetTransmissionInfo(opts *bind.CallOpts, receiver common.Address, workflowExecutionId [32]byte, reportId [2]byte) (KeystoneForwarderTestTransmissionInfo, error) {
	var out []interface{}
	err := _KeystoneForwarderTest.contract.Call(opts, &out, "getTransmissionInfo", receiver, workflowExecutionId, reportId)

	if err != nil {
		return *new(KeystoneForwarderTestTransmissionInfo), err
	}

	out0 := *abi.ConvertType(out[0], new(KeystoneForwarderTestTransmissionInfo)).(*KeystoneForwarderTestTransmissionInfo)

	return out0, err

}

// GetTransmissionInfo is a free data retrieval call binding the contract method 0x272cbd93.
//
// Solidity: function getTransmissionInfo(address receiver, bytes32 workflowExecutionId, bytes2 reportId) view returns((bytes32,uint8,address,bool,bool,uint80))
func (_KeystoneForwarderTest *KeystoneForwarderTestSession) GetTransmissionInfo(receiver common.Address, workflowExecutionId [32]byte, reportId [2]byte) (KeystoneForwarderTestTransmissionInfo, error) {
	return _KeystoneForwarderTest.Contract.GetTransmissionInfo(&_KeystoneForwarderTest.CallOpts, receiver, workflowExecutionId, reportId)
}

// GetTransmissionInfo is a free data retrieval call binding the contract method 0x272cbd93.
//
// Solidity: function getTransmissionInfo(address receiver, bytes32 workflowExecutionId, bytes2 reportId) view returns((bytes32,uint8,address,bool,bool,uint80))
func (_KeystoneForwarderTest *KeystoneForwarderTestCallerSession) GetTransmissionInfo(receiver common.Address, workflowExecutionId [32]byte, reportId [2]byte) (KeystoneForwarderTestTransmissionInfo, error) {
	return _KeystoneForwarderTest.Contract.GetTransmissionInfo(&_KeystoneForwarderTest.CallOpts, receiver, workflowExecutionId, reportId)
}

// GetTransmitter is a free data retrieval call binding the contract method 0x8864b864.
//
// Solidity: function getTransmitter(address receiver, bytes32 workflowExecutionId, bytes2 reportId) view returns(address)
func (_KeystoneForwarderTest *KeystoneForwarderTestCaller) GetTransmitter(opts *bind.CallOpts, receiver common.Address, workflowExecutionId [32]byte, reportId [2]byte) (common.Address, error) {
	var out []interface{}
	err := _KeystoneForwarderTest.contract.Call(opts, &out, "getTransmitter", receiver, workflowExecutionId, reportId)

	if err != nil {
		return *new(common.Address), err
	}

	out0 := *abi.ConvertType(out[0], new(common.Address)).(*common.Address)

	return out0, err

}

// GetTransmitter is a free data retrieval call binding the contract method 0x8864b864.
//
// Solidity: function getTransmitter(address receiver, bytes32 workflowExecutionId, bytes2 reportId) view returns(address)
func (_KeystoneForwarderTest *KeystoneForwarderTestSession) GetTransmitter(receiver common.Address, workflowExecutionId [32]byte, reportId [2]byte) (common.Address, error) {
	return _KeystoneForwarderTest.Contract.GetTransmitter(&_KeystoneForwarderTest.CallOpts, receiver, workflowExecutionId, reportId)
}

// GetTransmitter is a free data retrieval call binding the contract method 0x8864b864.
//
// Solidity: function getTransmitter(address receiver, bytes32 workflowExecutionId, bytes2 reportId) view returns(address)
func (_KeystoneForwarderTest *KeystoneForwarderTestCallerSession) GetTransmitter(receiver common.Address, workflowExecutionId [32]byte, reportId [2]byte) (common.Address, error) {
	return _KeystoneForwarderTest.Contract.GetTransmitter(&_KeystoneForwarderTest.CallOpts, receiver, workflowExecutionId, reportId)
}

// IsForwarder is a free data retrieval call binding the contract method 0xabcef554.
//
// Solidity: function isForwarder(address forwarder) view returns(bool)
func (_KeystoneForwarderTest *KeystoneForwarderTestCaller) IsForwarder(opts *bind.CallOpts, forwarder common.Address) (bool, error) {
	var out []interface{}
	err := _KeystoneForwarderTest.contract.Call(opts, &out, "isForwarder", forwarder)

	if err != nil {
		return *new(bool), err
	}

	out0 := *abi.ConvertType(out[0], new(bool)).(*bool)

	return out0, err

}

// IsForwarder is a free data retrieval call binding the contract method 0xabcef554.
//
// Solidity: function isForwarder(address forwarder) view returns(bool)
func (_KeystoneForwarderTest *KeystoneForwarderTestSession) IsForwarder(forwarder common.Address) (bool, error) {
	return _KeystoneForwarderTest.Contract.IsForwarder(&_KeystoneForwarderTest.CallOpts, forwarder)
}

// IsForwarder is a free data retrieval call binding the contract method 0xabcef554.
//
// Solidity: function isForwarder(address forwarder) view returns(bool)
func (_KeystoneForwarderTest *KeystoneForwarderTestCallerSession) IsForwarder(forwarder common.Address) (bool, error) {
	return _KeystoneForwarderTest.Contract.IsForwarder(&_KeystoneForwarderTest.CallOpts, forwarder)
}

// AddForwarder is a paid mutator transaction binding the contract method 0x5c41d2fe.
//
// Solidity: function addForwarder(address forwarder) returns()
func (_KeystoneForwarderTest *KeystoneForwarderTestTransactor) AddForwarder(opts *bind.TransactOpts, forwarder common.Address) (*types.Transaction, error) {
	return _KeystoneForwarderTest.contract.Transact(opts, "addForwarder", forwarder)
}

// AddForwarder is a paid mutator transaction binding the contract method 0x5c41d2fe.
//
// Solidity: function addForwarder(address forwarder) returns()
func (_KeystoneForwarderTest *KeystoneForwarderTestSession) AddForwarder(forwarder common.Address) (*types.Transaction, error) {
	return _KeystoneForwarderTest.Contract.AddForwarder(&_KeystoneForwarderTest.TransactOpts, forwarder)
}

// AddForwarder is a paid mutator transaction binding the contract method 0x5c41d2fe.
//
// Solidity: function addForwarder(address forwarder) returns()
func (_KeystoneForwarderTest *KeystoneForwarderTestTransactorSession) AddForwarder(forwarder common.Address) (*types.Transaction, error) {
	return _KeystoneForwarderTest.Contract.AddForwarder(&_KeystoneForwarderTest.TransactOpts, forwarder)
}

// RemoveForwarder is a paid mutator transaction binding the contract method 0x4d93172d.
//
// Solidity: function removeForwarder(address forwarder) returns()
func (_KeystoneForwarderTest *KeystoneForwarderTestTransactor) RemoveForwarder(opts *bind.TransactOpts, forwarder common.Address) (*types.Transaction, error) {
	return _KeystoneForwarderTest.contract.Transact(opts, "removeForwarder", forwarder)
}

// RemoveForwarder is a paid mutator transaction binding the contract method 0x4d93172d.
//
// Solidity: function removeForwarder(address forwarder) returns()
func (_KeystoneForwarderTest *KeystoneForwarderTestSession) RemoveForwarder(forwarder common.Address) (*types.Transaction, error) {
	return _KeystoneForwarderTest.Contract.RemoveForwarder(&_KeystoneForwarderTest.TransactOpts, forwarder)
}

// RemoveForwarder is a paid mutator transaction binding the contract method 0x4d93172d.
//
// Solidity: function removeForwarder(address forwarder) returns()
func (_KeystoneForwarderTest *KeystoneForwarderTestTransactorSession) RemoveForwarder(forwarder common.Address) (*types.Transaction, error) {
	return _KeystoneForwarderTest.Contract.RemoveForwarder(&_KeystoneForwarderTest.TransactOpts, forwarder)
}

// Report is a paid mutator transaction binding the contract method 0x11289565.
//
// Solidity: function report(address receiver, bytes rawReport, bytes reportContext, bytes[] signatures) returns()
func (_KeystoneForwarderTest *KeystoneForwarderTestTransactor) Report(opts *bind.TransactOpts, receiver common.Address, rawReport []byte, reportContext []byte, signatures [][]byte) (*types.Transaction, error) {
	return _KeystoneForwarderTest.contract.Transact(opts, "report", receiver, rawReport, reportContext, signatures)
}

// Report is a paid mutator transaction binding the contract method 0x11289565.
//
// Solidity: function report(address receiver, bytes rawReport, bytes reportContext, bytes[] signatures) returns()
func (_KeystoneForwarderTest *KeystoneForwarderTestSession) Report(receiver common.Address, rawReport []byte, reportContext []byte, signatures [][]byte) (*types.Transaction, error) {
	return _KeystoneForwarderTest.Contract.Report(&_KeystoneForwarderTest.TransactOpts, receiver, rawReport, reportContext, signatures)
}

// Report is a paid mutator transaction binding the contract method 0x11289565.
//
// Solidity: function report(address receiver, bytes rawReport, bytes reportContext, bytes[] signatures) returns()
func (_KeystoneForwarderTest *KeystoneForwarderTestTransactorSession) Report(receiver common.Address, rawReport []byte, reportContext []byte, signatures [][]byte) (*types.Transaction, error) {
	return _KeystoneForwarderTest.Contract.Report(&_KeystoneForwarderTest.TransactOpts, receiver, rawReport, reportContext, signatures)
}

// Route is a paid mutator transaction binding the contract method 0xed080219.
//
// Solidity: function route(bytes32 transmissionId, address transmitter) returns(bool)
func (_KeystoneForwarderTest *KeystoneForwarderTestTransactor) Route(opts *bind.TransactOpts, transmissionId [32]byte, transmitter common.Address) (*types.Transaction, error) {
	return _KeystoneForwarderTest.contract.Transact(opts, "route", transmissionId, transmitter)
}

// Route is a paid mutator transaction binding the contract method 0xed080219.
//
// Solidity: function route(bytes32 transmissionId, address transmitter) returns(bool)
func (_KeystoneForwarderTest *KeystoneForwarderTestSession) Route(transmissionId [32]byte, transmitter common.Address) (*types.Transaction, error) {
	return _KeystoneForwarderTest.Contract.Route(&_KeystoneForwarderTest.TransactOpts, transmissionId, transmitter)
}

// Route is a paid mutator transaction binding the contract method 0xed080219.
//
// Solidity: function route(bytes32 transmissionId, address transmitter) returns(bool)
func (_KeystoneForwarderTest *KeystoneForwarderTestTransactorSession) Route(transmissionId [32]byte, transmitter common.Address) (*types.Transaction, error) {
	return _KeystoneForwarderTest.Contract.Route(&_KeystoneForwarderTest.TransactOpts, transmissionId, transmitter)
}

// KeystoneForwarderTestReportProcessedIterator is returned from FilterReportProcessed and is used to iterate over the raw logs and unpacked data for ReportProcessed events raised by the KeystoneForwarderTest contract.
type KeystoneForwarderTestReportProcessedIterator struct {
	Event *KeystoneForwarderTestReportProcessed // Event containing the contract specifics and raw log

	contract *bind.BoundContract // Generic contract to use for unpacking event data
	event    string              // Event name to use for unpacking event data

	logs chan types.Log        // Log channel receiving the found contract events
	sub  ethereum.Subscription // Subscription for errors, completion and termination
	done bool                  // Whether the subscription completed delivering logs
	fail error                 // Occurred error to stop iteration
}

// Next advances the iterator to the subsequent event, returning whether there
// are any more events found. In case of a retrieval or parsing error, false is
// returned and Error() can be queried for the exact failure.
func (it *KeystoneForwarderTestReportProcessedIterator) Next() bool {
	// If the iterator failed, stop iterating
	if it.fail != nil {
		return false
	}
	// If the iterator completed, deliver directly whatever's available
	if it.done {
		select {
		case log := <-it.logs:
			it.Event = new(KeystoneForwarderTestReportProcessed)
			if err := it.contract.UnpackLog(it.Event, it.event, log); err != nil {
				it.fail = err
				return false
			}
			it.Event.Raw = log
			return true

		default:
			return false
		}
	}
	// Iterator still in progress, wait for either a data or an error event
	select {
	case log := <-it.logs:
		it.Event = new(KeystoneForwarderTestReportProcessed)
		if err := it.contract.UnpackLog(it.Event, it.event, log); err != nil {
			it.fail = err
			return false
		}
		it.Event.Raw = log
		return true

	case err := <-it.sub.Err():
		it.done = true
		it.fail = err
		return it.Next()
	}
}

// Error returns any retrieval or parsing error occurred during filtering.
func (it *KeystoneForwarderTestReportProcessedIterator) Error() error {
	return it.fail
}

// Close terminates the iteration process, releasing any pending underlying
// resources.
func (it *KeystoneForwarderTestReportProcessedIterator) Close() error {
	it.sub.Unsubscribe()
	return nil
}

// KeystoneForwarderTestReportProcessed represents a ReportProcessed event raised by the KeystoneForwarderTest contract.
type KeystoneForwarderTestReportProcessed struct {
	Receiver            common.Address
	WorkflowExecutionId [32]byte
	ReportId            [2]byte
	Result              bool
	Raw                 types.Log // Blockchain specific contextual infos
}

// FilterReportProcessed is a free log retrieval operation binding the contract event 0x3617b009e9785c42daebadb6d3fb553243a4bf586d07ea72d65d80013ce116b5.
//
// Solidity: event ReportProcessed(address indexed receiver, bytes32 indexed workflowExecutionId, bytes2 indexed reportId, bool result)
func (_KeystoneForwarderTest *KeystoneForwarderTestFilterer) FilterReportProcessed(opts *bind.FilterOpts, receiver []common.Address, workflowExecutionId [][32]byte, reportId [][2]byte) (*KeystoneForwarderTestReportProcessedIterator, error) {

	var receiverRule []interface{}
	for _, receiverItem := range receiver {
		receiverRule = append(receiverRule, receiverItem)
	}
	var workflowExecutionIdRule []interface{}
	for _, workflowExecutionIdItem := range workflowExecutionId {
		workflowExecutionIdRule = append(workflowExecutionIdRule, workflowExecutionIdItem)
	}
	var reportIdRule []interface{}
	for _, reportIdItem := range reportId {
		reportIdRule = append(reportIdRule, reportIdItem)
	}

	logs, sub, err := _KeystoneForwarderTest.contract.FilterLogs(opts, "ReportProcessed", receiverRule, workflowExecutionIdRule, reportIdRule)
	if err != nil {
		return nil, err
	}
	return &KeystoneForwarderTestReportProcessedIterator{contract: _KeystoneForwarderTest.contract, event: "ReportProcessed", logs: logs, sub: sub}, nil
}

// WatchReportProcessed is a free log subscription operation binding the contract event 0x3617b009e9785c42daebadb6d3fb553243a4bf586d07ea72d65d80013ce116b5.
//
// Solidity: event ReportProcessed(address indexed receiver, bytes32 indexed workflowExecutionId, bytes2 indexed reportId, bool result)
func (_KeystoneForwarderTest *KeystoneForwarderTestFilterer) WatchReportProcessed(opts *bind.WatchOpts, sink chan<- *KeystoneForwarderTestReportProcessed, receiver []common.Address, workflowExecutionId [][32]byte, reportId [][2]byte) (event.Subscription, error) {

	var receiverRule []interface{}
	for _, receiverItem := range receiver {
		receiverRule = append(receiverRule, receiverItem)
	}
	var workflowExecutionIdRule []interface{}
	for _, workflowExecutionIdItem := range workflowExecutionId {
		workflowExecutionIdRule = append(workflowExecutionIdRule, workflowExecutionIdItem)
	}
	var reportIdRule []interface{}
	for _, reportIdItem := range reportId {
		reportIdRule = append(reportIdRule, reportIdItem)
	}

	logs, sub, err := _KeystoneForwarderTest.contract.WatchLogs(opts, "ReportProcessed", receiverRule, workflowExecutionIdRule, reportIdRule)
	if err != nil {
		return nil, err
	}
	return event.NewSubscription(func(quit <-chan struct{}) error {
		defer sub.Unsubscribe()
		for {
			select {
			case log := <-logs:
				// New log arrived, parse the event and forward to the user
				event := new(KeystoneForwarderTestReportProcessed)
				if err := _KeystoneForwarderTest.contract.UnpackLog(event, "ReportProcessed", log); err != nil {
					return err
				}
				event.Raw = log

				select {
				case sink <- event:
				case err := <-sub.Err():
					return err
				case <-quit:
					return nil
				}
			case err := <-sub.Err():
				return err
			case <-quit:
				return nil
			}
		}
	}), nil
}

// ParseReportProcessed is a log parse operation binding the contract event 0x3617b009e9785c42daebadb6d3fb553243a4bf586d07ea72d65d80013ce116b5.
//
// Solidity: event ReportProcessed(address indexed receiver, bytes32 indexed workflowExecutionId, bytes2 indexed reportId, bool result)
func (_KeystoneForwarderTest *KeystoneForwarderTestFilterer) ParseReportProcessed(log types.Log) (*KeystoneForwarderTestReportProcessed, error) {
	event := new(KeystoneForwarderTestReportProcessed)
	if err := _KeystoneForwarderTest.contract.UnpackLog(event, "ReportProcessed", log); err != nil {
		return nil, err
	}
	event.Raw = log
	return event, nil
}
