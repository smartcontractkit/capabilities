// Code generated - DO NOT EDIT.
// This file is a generated binding and any manual changes will be lost.

package contract

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

// ContractMetaData contains all meta data concerning the Contract contract.
var ContractMetaData = &bind.MetaData{
	ABI: "[{\"anonymous\":false,\"inputs\":[{\"indexed\":true,\"internalType\":\"address\",\"name\":\"addr\",\"type\":\"address\"}],\"name\":\"AddressLogged\",\"type\":\"event\"},{\"anonymous\":false,\"inputs\":[{\"indexed\":false,\"internalType\":\"bool\",\"name\":\"value\",\"type\":\"bool\"}],\"name\":\"BoolLogged\",\"type\":\"event\"},{\"anonymous\":false,\"inputs\":[{\"indexed\":false,\"internalType\":\"bytes32\",\"name\":\"value\",\"type\":\"bytes32\"}],\"name\":\"Bytes32Logged\",\"type\":\"event\"},{\"anonymous\":false,\"inputs\":[{\"indexed\":false,\"internalType\":\"bytes\",\"name\":\"value\",\"type\":\"bytes\"}],\"name\":\"BytesLogged\",\"type\":\"event\"},{\"anonymous\":false,\"inputs\":[{\"indexed\":true,\"internalType\":\"int256\",\"name\":\"value\",\"type\":\"int256\"}],\"name\":\"IntLogged\",\"type\":\"event\"},{\"anonymous\":false,\"inputs\":[{\"indexed\":false,\"internalType\":\"string\",\"name\":\"value\",\"type\":\"string\"}],\"name\":\"StringLogged\",\"type\":\"event\"},{\"anonymous\":false,\"inputs\":[{\"indexed\":true,\"internalType\":\"uint256\",\"name\":\"value\",\"type\":\"uint256\"}],\"name\":\"UintLogged\",\"type\":\"event\"},{\"inputs\":[{\"internalType\":\"address[]\",\"name\":\"addresses\",\"type\":\"address[]\"},{\"internalType\":\"uint256[]\",\"name\":\"uints\",\"type\":\"uint256[]\"},{\"internalType\":\"int256[]\",\"name\":\"ints\",\"type\":\"int256[]\"},{\"internalType\":\"bool[]\",\"name\":\"bools\",\"type\":\"bool[]\"},{\"internalType\":\"string[]\",\"name\":\"strings\",\"type\":\"string[]\"},{\"internalType\":\"bytes[]\",\"name\":\"bytesArray\",\"type\":\"bytes[]\"},{\"internalType\":\"bytes32[]\",\"name\":\"bytes32Array\",\"type\":\"bytes32[]\"}],\"name\":\"GetValue\",\"outputs\":[{\"internalType\":\"int256[]\",\"name\":\"\",\"type\":\"int256[]\"}],\"stateMutability\":\"nonpayable\",\"type\":\"function\"}]",
	Bin: "0x608060405234801561000f575f80fd5b506110888061001d5f395ff3fe608060405234801561000f575f80fd5b5060043610610029575f3560e01c80639286aa3f1461002d575b5f80fd5b61004760048036038101906100429190610c91565b61005d565b6040516100549190610ea9565b60405180910390f35b60605f5b88518110156100d45788818151811061007d5761007c610ec9565b5b602002602001015173ffffffffffffffffffffffffffffffffffffffff167f36341629533027a87dbcdf4df3be2c81bf2b9e23f8d33eb2b6903f9b873a9fe860405160405180910390a28080600101915050610061565b505f5b8751811015610134578781815181106100f3576100f2610ec9565b5b60200260200101517f7f6e805926ceb536bd3e8ae88fd58959cf1508c52212c163fc3ae2d5e02c25aa60405160405180910390a280806001019150506100d7565b505f5b86518110156101945786818151811061015357610152610ec9565b5b60200260200101517f8c208a695bc4e5cf03539c05e9e2aac5f3c8beb4f49b7ea6d390a7332d5e185260405160405180910390a28080600101915050610137565b505f5b85518110156101fe577f16b1e5b436e512b113b4e554fb2406f5f3ff3994282919502b4a5840fd25ff698682815181106101d4576101d3610ec9565b5b60200260200101516040516101e99190610f05565b60405180910390a18080600101915050610197565b505f5b8451811015610268577f04920d60568e8fdd457e29d24329808c937b3df3e6053b7882b7c6c2a03e2e8485828151811061023e5761023d610ec9565b5b60200260200101516040516102539190610f98565b60405180910390a18080600101915050610201565b505f5b83518110156102d2577fa360bbbfd835630a61528a4bd2a988199e5fac752a9151c0f83e822b4f8336af8482815181106102a8576102a7610ec9565b5b60200260200101516040516102bd919061100a565b60405180910390a1808060010191505061026b565b505f5b825181101561033c577f251ee811081772787cde9508ba9974ac2466eed194b0e39555655870e2e7b03983828151811061031257610311610ec9565b5b60200260200101516040516103279190611039565b60405180910390a180806001019150506102d5565b505f600367ffffffffffffffff81111561035957610358610423565b5b6040519080825280602002602001820160405280156103875781602001602082028036833780820191505090505b5090506015815f8151811061039f5761039e610ec9565b5b602002602001018181525050602a816001815181106103c1576103c0610ec9565b5b602002602001018181525050603f816002815181106103e3576103e2610ec9565b5b60200260200101818152505080915050979650505050505050565b5f604051905090565b5f80fd5b5f80fd5b5f80fd5b5f601f19601f8301169050919050565b7f4e487b71000000000000000000000000000000000000000000000000000000005f52604160045260245ffd5b61045982610413565b810181811067ffffffffffffffff8211171561047857610477610423565b5b80604052505050565b5f61048a6103fe565b90506104968282610450565b919050565b5f67ffffffffffffffff8211156104b5576104b4610423565b5b602082029050602081019050919050565b5f80fd5b5f73ffffffffffffffffffffffffffffffffffffffff82169050919050565b5f6104f3826104ca565b9050919050565b610503816104e9565b811461050d575f80fd5b50565b5f8135905061051e816104fa565b92915050565b5f6105366105318461049b565b610481565b90508083825260208201905060208402830185811115610559576105586104c6565b5b835b81811015610582578061056e8882610510565b84526020840193505060208101905061055b565b5050509392505050565b5f82601f8301126105a05761059f61040f565b5b81356105b0848260208601610524565b91505092915050565b5f67ffffffffffffffff8211156105d3576105d2610423565b5b602082029050602081019050919050565b5f819050919050565b6105f6816105e4565b8114610600575f80fd5b50565b5f81359050610611816105ed565b92915050565b5f610629610624846105b9565b610481565b9050808382526020820190506020840283018581111561064c5761064b6104c6565b5b835b8181101561067557806106618882610603565b84526020840193505060208101905061064e565b5050509392505050565b5f82601f8301126106935761069261040f565b5b81356106a3848260208601610617565b91505092915050565b5f67ffffffffffffffff8211156106c6576106c5610423565b5b602082029050602081019050919050565b5f819050919050565b6106e9816106d7565b81146106f3575f80fd5b50565b5f81359050610704816106e0565b92915050565b5f61071c610717846106ac565b610481565b9050808382526020820190506020840283018581111561073f5761073e6104c6565b5b835b81811015610768578061075488826106f6565b845260208401935050602081019050610741565b5050509392505050565b5f82601f8301126107865761078561040f565b5b813561079684826020860161070a565b91505092915050565b5f67ffffffffffffffff8211156107b9576107b8610423565b5b602082029050602081019050919050565b5f8115159050919050565b6107de816107ca565b81146107e8575f80fd5b50565b5f813590506107f9816107d5565b92915050565b5f61081161080c8461079f565b610481565b90508083825260208201905060208402830185811115610834576108336104c6565b5b835b8181101561085d578061084988826107eb565b845260208401935050602081019050610836565b5050509392505050565b5f82601f83011261087b5761087a61040f565b5b813561088b8482602086016107ff565b91505092915050565b5f67ffffffffffffffff8211156108ae576108ad610423565b5b602082029050602081019050919050565b5f80fd5b5f67ffffffffffffffff8211156108dd576108dc610423565b5b6108e682610413565b9050602081019050919050565b828183375f83830152505050565b5f61091361090e846108c3565b610481565b90508281526020810184848401111561092f5761092e6108bf565b5b61093a8482856108f3565b509392505050565b5f82601f8301126109565761095561040f565b5b8135610966848260208601610901565b91505092915050565b5f61098161097c84610894565b610481565b905080838252602082019050602084028301858111156109a4576109a36104c6565b5b835b818110156109eb57803567ffffffffffffffff8111156109c9576109c861040f565b5b8086016109d68982610942565b855260208501945050506020810190506109a6565b5050509392505050565b5f82601f830112610a0957610a0861040f565b5b8135610a1984826020860161096f565b91505092915050565b5f67ffffffffffffffff821115610a3c57610a3b610423565b5b602082029050602081019050919050565b5f67ffffffffffffffff821115610a6757610a66610423565b5b610a7082610413565b9050602081019050919050565b5f610a8f610a8a84610a4d565b610481565b905082815260208101848484011115610aab57610aaa6108bf565b5b610ab68482856108f3565b509392505050565b5f82601f830112610ad257610ad161040f565b5b8135610ae2848260208601610a7d565b91505092915050565b5f610afd610af884610a22565b610481565b90508083825260208201905060208402830185811115610b2057610b1f6104c6565b5b835b81811015610b6757803567ffffffffffffffff811115610b4557610b4461040f565b5b808601610b528982610abe565b85526020850194505050602081019050610b22565b5050509392505050565b5f82601f830112610b8557610b8461040f565b5b8135610b95848260208601610aeb565b91505092915050565b5f67ffffffffffffffff821115610bb857610bb7610423565b5b602082029050602081019050919050565b5f819050919050565b610bdb81610bc9565b8114610be5575f80fd5b50565b5f81359050610bf681610bd2565b92915050565b5f610c0e610c0984610b9e565b610481565b90508083825260208201905060208402830185811115610c3157610c306104c6565b5b835b81811015610c5a5780610c468882610be8565b845260208401935050602081019050610c33565b5050509392505050565b5f82601f830112610c7857610c7761040f565b5b8135610c88848260208601610bfc565b91505092915050565b5f805f805f805f60e0888a031215610cac57610cab610407565b5b5f88013567ffffffffffffffff811115610cc957610cc861040b565b5b610cd58a828b0161058c565b975050602088013567ffffffffffffffff811115610cf657610cf561040b565b5b610d028a828b0161067f565b965050604088013567ffffffffffffffff811115610d2357610d2261040b565b5b610d2f8a828b01610772565b955050606088013567ffffffffffffffff811115610d5057610d4f61040b565b5b610d5c8a828b01610867565b945050608088013567ffffffffffffffff811115610d7d57610d7c61040b565b5b610d898a828b016109f5565b93505060a088013567ffffffffffffffff811115610daa57610da961040b565b5b610db68a828b01610b71565b92505060c088013567ffffffffffffffff811115610dd757610dd661040b565b5b610de38a828b01610c64565b91505092959891949750929550565b5f81519050919050565b5f82825260208201905092915050565b5f819050602082019050919050565b610e24816106d7565b82525050565b5f610e358383610e1b565b60208301905092915050565b5f602082019050919050565b5f610e5782610df2565b610e618185610dfc565b9350610e6c83610e0c565b805f5b83811015610e9c578151610e838882610e2a565b9750610e8e83610e41565b925050600181019050610e6f565b5085935050505092915050565b5f6020820190508181035f830152610ec18184610e4d565b905092915050565b7f4e487b71000000000000000000000000000000000000000000000000000000005f52603260045260245ffd5b610eff816107ca565b82525050565b5f602082019050610f185f830184610ef6565b92915050565b5f81519050919050565b5f82825260208201905092915050565b5f5b83811015610f55578082015181840152602081019050610f3a565b5f8484015250505050565b5f610f6a82610f1e565b610f748185610f28565b9350610f84818560208601610f38565b610f8d81610413565b840191505092915050565b5f6020820190508181035f830152610fb08184610f60565b905092915050565b5f81519050919050565b5f82825260208201905092915050565b5f610fdc82610fb8565b610fe68185610fc2565b9350610ff6818560208601610f38565b610fff81610413565b840191505092915050565b5f6020820190508181035f8301526110228184610fd2565b905092915050565b61103381610bc9565b82525050565b5f60208201905061104c5f83018461102a565b9291505056fea26469706673582212202579b7b39d25ff72981c77193f11c630b48132a9ff1b969d3fe3d298f29b066e64736f6c63430008180033",
}

// ContractABI is the input ABI used to generate the binding from.
// Deprecated: Use ContractMetaData.ABI instead.
var ContractABI = ContractMetaData.ABI

// ContractBin is the compiled bytecode used for deploying new contracts.
// Deprecated: Use ContractMetaData.Bin instead.
var ContractBin = ContractMetaData.Bin

// DeployContract deploys a new Ethereum contract, binding an instance of Contract to it.
func DeployContract(auth *bind.TransactOpts, backend bind.ContractBackend) (common.Address, *types.Transaction, *Contract, error) {
	parsed, err := ContractMetaData.GetAbi()
	if err != nil {
		return common.Address{}, nil, nil, err
	}
	if parsed == nil {
		return common.Address{}, nil, nil, errors.New("GetABI returned nil")
	}

	address, tx, contract, err := bind.DeployContract(auth, *parsed, common.FromHex(ContractBin), backend)
	if err != nil {
		return common.Address{}, nil, nil, err
	}
	return address, tx, &Contract{ContractCaller: ContractCaller{contract: contract}, ContractTransactor: ContractTransactor{contract: contract}, ContractFilterer: ContractFilterer{contract: contract}}, nil
}

// Contract is an auto generated Go binding around an Ethereum contract.
type Contract struct {
	ContractCaller     // Read-only binding to the contract
	ContractTransactor // Write-only binding to the contract
	ContractFilterer   // Log filterer for contract events
}

// ContractCaller is an auto generated read-only Go binding around an Ethereum contract.
type ContractCaller struct {
	contract *bind.BoundContract // Generic contract wrapper for the low level calls
}

// ContractTransactor is an auto generated write-only Go binding around an Ethereum contract.
type ContractTransactor struct {
	contract *bind.BoundContract // Generic contract wrapper for the low level calls
}

// ContractFilterer is an auto generated log filtering Go binding around an Ethereum contract events.
type ContractFilterer struct {
	contract *bind.BoundContract // Generic contract wrapper for the low level calls
}

// ContractSession is an auto generated Go binding around an Ethereum contract,
// with pre-set call and transact options.
type ContractSession struct {
	Contract     *Contract         // Generic contract binding to set the session for
	CallOpts     bind.CallOpts     // Call options to use throughout this session
	TransactOpts bind.TransactOpts // Transaction auth options to use throughout this session
}

// ContractCallerSession is an auto generated read-only Go binding around an Ethereum contract,
// with pre-set call options.
type ContractCallerSession struct {
	Contract *ContractCaller // Generic contract caller binding to set the session for
	CallOpts bind.CallOpts   // Call options to use throughout this session
}

// ContractTransactorSession is an auto generated write-only Go binding around an Ethereum contract,
// with pre-set transact options.
type ContractTransactorSession struct {
	Contract     *ContractTransactor // Generic contract transactor binding to set the session for
	TransactOpts bind.TransactOpts   // Transaction auth options to use throughout this session
}

// ContractRaw is an auto generated low-level Go binding around an Ethereum contract.
type ContractRaw struct {
	Contract *Contract // Generic contract binding to access the raw methods on
}

// ContractCallerRaw is an auto generated low-level read-only Go binding around an Ethereum contract.
type ContractCallerRaw struct {
	Contract *ContractCaller // Generic read-only contract binding to access the raw methods on
}

// ContractTransactorRaw is an auto generated low-level write-only Go binding around an Ethereum contract.
type ContractTransactorRaw struct {
	Contract *ContractTransactor // Generic write-only contract binding to access the raw methods on
}

// NewContract creates a new instance of Contract, bound to a specific deployed contract.
func NewContract(address common.Address, backend bind.ContractBackend) (*Contract, error) {
	contract, err := bindContract(address, backend, backend, backend)
	if err != nil {
		return nil, err
	}
	return &Contract{ContractCaller: ContractCaller{contract: contract}, ContractTransactor: ContractTransactor{contract: contract}, ContractFilterer: ContractFilterer{contract: contract}}, nil
}

// NewContractCaller creates a new read-only instance of Contract, bound to a specific deployed contract.
func NewContractCaller(address common.Address, caller bind.ContractCaller) (*ContractCaller, error) {
	contract, err := bindContract(address, caller, nil, nil)
	if err != nil {
		return nil, err
	}
	return &ContractCaller{contract: contract}, nil
}

// NewContractTransactor creates a new write-only instance of Contract, bound to a specific deployed contract.
func NewContractTransactor(address common.Address, transactor bind.ContractTransactor) (*ContractTransactor, error) {
	contract, err := bindContract(address, nil, transactor, nil)
	if err != nil {
		return nil, err
	}
	return &ContractTransactor{contract: contract}, nil
}

// NewContractFilterer creates a new log filterer instance of Contract, bound to a specific deployed contract.
func NewContractFilterer(address common.Address, filterer bind.ContractFilterer) (*ContractFilterer, error) {
	contract, err := bindContract(address, nil, nil, filterer)
	if err != nil {
		return nil, err
	}
	return &ContractFilterer{contract: contract}, nil
}

// bindContract binds a generic wrapper to an already deployed contract.
func bindContract(address common.Address, caller bind.ContractCaller, transactor bind.ContractTransactor, filterer bind.ContractFilterer) (*bind.BoundContract, error) {
	parsed, err := ContractMetaData.GetAbi()
	if err != nil {
		return nil, err
	}
	return bind.NewBoundContract(address, *parsed, caller, transactor, filterer), nil
}

// Call invokes the (constant) contract method with params as input values and
// sets the output to result. The result type might be a single field for simple
// returns, a slice of interfaces for anonymous returns and a struct for named
// returns.
func (_Contract *ContractRaw) Call(opts *bind.CallOpts, result *[]interface{}, method string, params ...interface{}) error {
	return _Contract.Contract.ContractCaller.contract.Call(opts, result, method, params...)
}

// Transfer initiates a plain transaction to move funds to the contract, calling
// its default method if one is available.
func (_Contract *ContractRaw) Transfer(opts *bind.TransactOpts) (*types.Transaction, error) {
	return _Contract.Contract.ContractTransactor.contract.Transfer(opts)
}

// Transact invokes the (paid) contract method with params as input values.
func (_Contract *ContractRaw) Transact(opts *bind.TransactOpts, method string, params ...interface{}) (*types.Transaction, error) {
	return _Contract.Contract.ContractTransactor.contract.Transact(opts, method, params...)
}

// Call invokes the (constant) contract method with params as input values and
// sets the output to result. The result type might be a single field for simple
// returns, a slice of interfaces for anonymous returns and a struct for named
// returns.
func (_Contract *ContractCallerRaw) Call(opts *bind.CallOpts, result *[]interface{}, method string, params ...interface{}) error {
	return _Contract.Contract.contract.Call(opts, result, method, params...)
}

// Transfer initiates a plain transaction to move funds to the contract, calling
// its default method if one is available.
func (_Contract *ContractTransactorRaw) Transfer(opts *bind.TransactOpts) (*types.Transaction, error) {
	return _Contract.Contract.contract.Transfer(opts)
}

// Transact invokes the (paid) contract method with params as input values.
func (_Contract *ContractTransactorRaw) Transact(opts *bind.TransactOpts, method string, params ...interface{}) (*types.Transaction, error) {
	return _Contract.Contract.contract.Transact(opts, method, params...)
}

// GetValue is a paid mutator transaction binding the contract method 0x9286aa3f.
//
// Solidity: function GetValue(address[] addresses, uint256[] uints, int256[] ints, bool[] bools, string[] strings, bytes[] bytesArray, bytes32[] bytes32Array) returns(int256[])
func (_Contract *ContractTransactor) GetValue(opts *bind.TransactOpts, addresses []common.Address, uints []*big.Int, ints []*big.Int, bools []bool, strings []string, bytesArray [][]byte, bytes32Array [][32]byte) (*types.Transaction, error) {
	return _Contract.contract.Transact(opts, "GetValue", addresses, uints, ints, bools, strings, bytesArray, bytes32Array)
}

// GetValue is a paid mutator transaction binding the contract method 0x9286aa3f.
//
// Solidity: function GetValue(address[] addresses, uint256[] uints, int256[] ints, bool[] bools, string[] strings, bytes[] bytesArray, bytes32[] bytes32Array) returns(int256[])
func (_Contract *ContractSession) GetValue(addresses []common.Address, uints []*big.Int, ints []*big.Int, bools []bool, strings []string, bytesArray [][]byte, bytes32Array [][32]byte) (*types.Transaction, error) {
	return _Contract.Contract.GetValue(&_Contract.TransactOpts, addresses, uints, ints, bools, strings, bytesArray, bytes32Array)
}

// GetValue is a paid mutator transaction binding the contract method 0x9286aa3f.
//
// Solidity: function GetValue(address[] addresses, uint256[] uints, int256[] ints, bool[] bools, string[] strings, bytes[] bytesArray, bytes32[] bytes32Array) returns(int256[])
func (_Contract *ContractTransactorSession) GetValue(addresses []common.Address, uints []*big.Int, ints []*big.Int, bools []bool, strings []string, bytesArray [][]byte, bytes32Array [][32]byte) (*types.Transaction, error) {
	return _Contract.Contract.GetValue(&_Contract.TransactOpts, addresses, uints, ints, bools, strings, bytesArray, bytes32Array)
}

// ContractAddressLoggedIterator is returned from FilterAddressLogged and is used to iterate over the raw logs and unpacked data for AddressLogged events raised by the Contract contract.
type ContractAddressLoggedIterator struct {
	Event *ContractAddressLogged // Event containing the contract specifics and raw log

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
func (it *ContractAddressLoggedIterator) Next() bool {
	// If the iterator failed, stop iterating
	if it.fail != nil {
		return false
	}
	// If the iterator completed, deliver directly whatever's available
	if it.done {
		select {
		case log := <-it.logs:
			it.Event = new(ContractAddressLogged)
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
		it.Event = new(ContractAddressLogged)
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
func (it *ContractAddressLoggedIterator) Error() error {
	return it.fail
}

// Close terminates the iteration process, releasing any pending underlying
// resources.
func (it *ContractAddressLoggedIterator) Close() error {
	it.sub.Unsubscribe()
	return nil
}

// ContractAddressLogged represents a AddressLogged event raised by the Contract contract.
type ContractAddressLogged struct {
	Addr common.Address
	Raw  types.Log // Blockchain specific contextual infos
}

// FilterAddressLogged is a free log retrieval operation binding the contract event 0x36341629533027a87dbcdf4df3be2c81bf2b9e23f8d33eb2b6903f9b873a9fe8.
//
// Solidity: event AddressLogged(address indexed addr)
func (_Contract *ContractFilterer) FilterAddressLogged(opts *bind.FilterOpts, addr []common.Address) (*ContractAddressLoggedIterator, error) {

	var addrRule []interface{}
	for _, addrItem := range addr {
		addrRule = append(addrRule, addrItem)
	}

	logs, sub, err := _Contract.contract.FilterLogs(opts, "AddressLogged", addrRule)
	if err != nil {
		return nil, err
	}
	return &ContractAddressLoggedIterator{contract: _Contract.contract, event: "AddressLogged", logs: logs, sub: sub}, nil
}

// WatchAddressLogged is a free log subscription operation binding the contract event 0x36341629533027a87dbcdf4df3be2c81bf2b9e23f8d33eb2b6903f9b873a9fe8.
//
// Solidity: event AddressLogged(address indexed addr)
func (_Contract *ContractFilterer) WatchAddressLogged(opts *bind.WatchOpts, sink chan<- *ContractAddressLogged, addr []common.Address) (event.Subscription, error) {

	var addrRule []interface{}
	for _, addrItem := range addr {
		addrRule = append(addrRule, addrItem)
	}

	logs, sub, err := _Contract.contract.WatchLogs(opts, "AddressLogged", addrRule)
	if err != nil {
		return nil, err
	}
	return event.NewSubscription(func(quit <-chan struct{}) error {
		defer sub.Unsubscribe()
		for {
			select {
			case log := <-logs:
				// New log arrived, parse the event and forward to the user
				event := new(ContractAddressLogged)
				if err := _Contract.contract.UnpackLog(event, "AddressLogged", log); err != nil {
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

// ParseAddressLogged is a log parse operation binding the contract event 0x36341629533027a87dbcdf4df3be2c81bf2b9e23f8d33eb2b6903f9b873a9fe8.
//
// Solidity: event AddressLogged(address indexed addr)
func (_Contract *ContractFilterer) ParseAddressLogged(log types.Log) (*ContractAddressLogged, error) {
	event := new(ContractAddressLogged)
	if err := _Contract.contract.UnpackLog(event, "AddressLogged", log); err != nil {
		return nil, err
	}
	event.Raw = log
	return event, nil
}

// ContractBoolLoggedIterator is returned from FilterBoolLogged and is used to iterate over the raw logs and unpacked data for BoolLogged events raised by the Contract contract.
type ContractBoolLoggedIterator struct {
	Event *ContractBoolLogged // Event containing the contract specifics and raw log

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
func (it *ContractBoolLoggedIterator) Next() bool {
	// If the iterator failed, stop iterating
	if it.fail != nil {
		return false
	}
	// If the iterator completed, deliver directly whatever's available
	if it.done {
		select {
		case log := <-it.logs:
			it.Event = new(ContractBoolLogged)
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
		it.Event = new(ContractBoolLogged)
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
func (it *ContractBoolLoggedIterator) Error() error {
	return it.fail
}

// Close terminates the iteration process, releasing any pending underlying
// resources.
func (it *ContractBoolLoggedIterator) Close() error {
	it.sub.Unsubscribe()
	return nil
}

// ContractBoolLogged represents a BoolLogged event raised by the Contract contract.
type ContractBoolLogged struct {
	Value bool
	Raw   types.Log // Blockchain specific contextual infos
}

// FilterBoolLogged is a free log retrieval operation binding the contract event 0x16b1e5b436e512b113b4e554fb2406f5f3ff3994282919502b4a5840fd25ff69.
//
// Solidity: event BoolLogged(bool value)
func (_Contract *ContractFilterer) FilterBoolLogged(opts *bind.FilterOpts) (*ContractBoolLoggedIterator, error) {

	logs, sub, err := _Contract.contract.FilterLogs(opts, "BoolLogged")
	if err != nil {
		return nil, err
	}
	return &ContractBoolLoggedIterator{contract: _Contract.contract, event: "BoolLogged", logs: logs, sub: sub}, nil
}

// WatchBoolLogged is a free log subscription operation binding the contract event 0x16b1e5b436e512b113b4e554fb2406f5f3ff3994282919502b4a5840fd25ff69.
//
// Solidity: event BoolLogged(bool value)
func (_Contract *ContractFilterer) WatchBoolLogged(opts *bind.WatchOpts, sink chan<- *ContractBoolLogged) (event.Subscription, error) {

	logs, sub, err := _Contract.contract.WatchLogs(opts, "BoolLogged")
	if err != nil {
		return nil, err
	}
	return event.NewSubscription(func(quit <-chan struct{}) error {
		defer sub.Unsubscribe()
		for {
			select {
			case log := <-logs:
				// New log arrived, parse the event and forward to the user
				event := new(ContractBoolLogged)
				if err := _Contract.contract.UnpackLog(event, "BoolLogged", log); err != nil {
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

// ParseBoolLogged is a log parse operation binding the contract event 0x16b1e5b436e512b113b4e554fb2406f5f3ff3994282919502b4a5840fd25ff69.
//
// Solidity: event BoolLogged(bool value)
func (_Contract *ContractFilterer) ParseBoolLogged(log types.Log) (*ContractBoolLogged, error) {
	event := new(ContractBoolLogged)
	if err := _Contract.contract.UnpackLog(event, "BoolLogged", log); err != nil {
		return nil, err
	}
	event.Raw = log
	return event, nil
}

// ContractBytes32LoggedIterator is returned from FilterBytes32Logged and is used to iterate over the raw logs and unpacked data for Bytes32Logged events raised by the Contract contract.
type ContractBytes32LoggedIterator struct {
	Event *ContractBytes32Logged // Event containing the contract specifics and raw log

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
func (it *ContractBytes32LoggedIterator) Next() bool {
	// If the iterator failed, stop iterating
	if it.fail != nil {
		return false
	}
	// If the iterator completed, deliver directly whatever's available
	if it.done {
		select {
		case log := <-it.logs:
			it.Event = new(ContractBytes32Logged)
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
		it.Event = new(ContractBytes32Logged)
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
func (it *ContractBytes32LoggedIterator) Error() error {
	return it.fail
}

// Close terminates the iteration process, releasing any pending underlying
// resources.
func (it *ContractBytes32LoggedIterator) Close() error {
	it.sub.Unsubscribe()
	return nil
}

// ContractBytes32Logged represents a Bytes32Logged event raised by the Contract contract.
type ContractBytes32Logged struct {
	Value [32]byte
	Raw   types.Log // Blockchain specific contextual infos
}

// FilterBytes32Logged is a free log retrieval operation binding the contract event 0x251ee811081772787cde9508ba9974ac2466eed194b0e39555655870e2e7b039.
//
// Solidity: event Bytes32Logged(bytes32 value)
func (_Contract *ContractFilterer) FilterBytes32Logged(opts *bind.FilterOpts) (*ContractBytes32LoggedIterator, error) {

	logs, sub, err := _Contract.contract.FilterLogs(opts, "Bytes32Logged")
	if err != nil {
		return nil, err
	}
	return &ContractBytes32LoggedIterator{contract: _Contract.contract, event: "Bytes32Logged", logs: logs, sub: sub}, nil
}

// WatchBytes32Logged is a free log subscription operation binding the contract event 0x251ee811081772787cde9508ba9974ac2466eed194b0e39555655870e2e7b039.
//
// Solidity: event Bytes32Logged(bytes32 value)
func (_Contract *ContractFilterer) WatchBytes32Logged(opts *bind.WatchOpts, sink chan<- *ContractBytes32Logged) (event.Subscription, error) {

	logs, sub, err := _Contract.contract.WatchLogs(opts, "Bytes32Logged")
	if err != nil {
		return nil, err
	}
	return event.NewSubscription(func(quit <-chan struct{}) error {
		defer sub.Unsubscribe()
		for {
			select {
			case log := <-logs:
				// New log arrived, parse the event and forward to the user
				event := new(ContractBytes32Logged)
				if err := _Contract.contract.UnpackLog(event, "Bytes32Logged", log); err != nil {
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

// ParseBytes32Logged is a log parse operation binding the contract event 0x251ee811081772787cde9508ba9974ac2466eed194b0e39555655870e2e7b039.
//
// Solidity: event Bytes32Logged(bytes32 value)
func (_Contract *ContractFilterer) ParseBytes32Logged(log types.Log) (*ContractBytes32Logged, error) {
	event := new(ContractBytes32Logged)
	if err := _Contract.contract.UnpackLog(event, "Bytes32Logged", log); err != nil {
		return nil, err
	}
	event.Raw = log
	return event, nil
}

// ContractBytesLoggedIterator is returned from FilterBytesLogged and is used to iterate over the raw logs and unpacked data for BytesLogged events raised by the Contract contract.
type ContractBytesLoggedIterator struct {
	Event *ContractBytesLogged // Event containing the contract specifics and raw log

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
func (it *ContractBytesLoggedIterator) Next() bool {
	// If the iterator failed, stop iterating
	if it.fail != nil {
		return false
	}
	// If the iterator completed, deliver directly whatever's available
	if it.done {
		select {
		case log := <-it.logs:
			it.Event = new(ContractBytesLogged)
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
		it.Event = new(ContractBytesLogged)
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
func (it *ContractBytesLoggedIterator) Error() error {
	return it.fail
}

// Close terminates the iteration process, releasing any pending underlying
// resources.
func (it *ContractBytesLoggedIterator) Close() error {
	it.sub.Unsubscribe()
	return nil
}

// ContractBytesLogged represents a BytesLogged event raised by the Contract contract.
type ContractBytesLogged struct {
	Value []byte
	Raw   types.Log // Blockchain specific contextual infos
}

// FilterBytesLogged is a free log retrieval operation binding the contract event 0xa360bbbfd835630a61528a4bd2a988199e5fac752a9151c0f83e822b4f8336af.
//
// Solidity: event BytesLogged(bytes value)
func (_Contract *ContractFilterer) FilterBytesLogged(opts *bind.FilterOpts) (*ContractBytesLoggedIterator, error) {

	logs, sub, err := _Contract.contract.FilterLogs(opts, "BytesLogged")
	if err != nil {
		return nil, err
	}
	return &ContractBytesLoggedIterator{contract: _Contract.contract, event: "BytesLogged", logs: logs, sub: sub}, nil
}

// WatchBytesLogged is a free log subscription operation binding the contract event 0xa360bbbfd835630a61528a4bd2a988199e5fac752a9151c0f83e822b4f8336af.
//
// Solidity: event BytesLogged(bytes value)
func (_Contract *ContractFilterer) WatchBytesLogged(opts *bind.WatchOpts, sink chan<- *ContractBytesLogged) (event.Subscription, error) {

	logs, sub, err := _Contract.contract.WatchLogs(opts, "BytesLogged")
	if err != nil {
		return nil, err
	}
	return event.NewSubscription(func(quit <-chan struct{}) error {
		defer sub.Unsubscribe()
		for {
			select {
			case log := <-logs:
				// New log arrived, parse the event and forward to the user
				event := new(ContractBytesLogged)
				if err := _Contract.contract.UnpackLog(event, "BytesLogged", log); err != nil {
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

// ParseBytesLogged is a log parse operation binding the contract event 0xa360bbbfd835630a61528a4bd2a988199e5fac752a9151c0f83e822b4f8336af.
//
// Solidity: event BytesLogged(bytes value)
func (_Contract *ContractFilterer) ParseBytesLogged(log types.Log) (*ContractBytesLogged, error) {
	event := new(ContractBytesLogged)
	if err := _Contract.contract.UnpackLog(event, "BytesLogged", log); err != nil {
		return nil, err
	}
	event.Raw = log
	return event, nil
}

// ContractIntLoggedIterator is returned from FilterIntLogged and is used to iterate over the raw logs and unpacked data for IntLogged events raised by the Contract contract.
type ContractIntLoggedIterator struct {
	Event *ContractIntLogged // Event containing the contract specifics and raw log

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
func (it *ContractIntLoggedIterator) Next() bool {
	// If the iterator failed, stop iterating
	if it.fail != nil {
		return false
	}
	// If the iterator completed, deliver directly whatever's available
	if it.done {
		select {
		case log := <-it.logs:
			it.Event = new(ContractIntLogged)
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
		it.Event = new(ContractIntLogged)
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
func (it *ContractIntLoggedIterator) Error() error {
	return it.fail
}

// Close terminates the iteration process, releasing any pending underlying
// resources.
func (it *ContractIntLoggedIterator) Close() error {
	it.sub.Unsubscribe()
	return nil
}

// ContractIntLogged represents a IntLogged event raised by the Contract contract.
type ContractIntLogged struct {
	Value *big.Int
	Raw   types.Log // Blockchain specific contextual infos
}

// FilterIntLogged is a free log retrieval operation binding the contract event 0x8c208a695bc4e5cf03539c05e9e2aac5f3c8beb4f49b7ea6d390a7332d5e1852.
//
// Solidity: event IntLogged(int256 indexed value)
func (_Contract *ContractFilterer) FilterIntLogged(opts *bind.FilterOpts, value []*big.Int) (*ContractIntLoggedIterator, error) {

	var valueRule []interface{}
	for _, valueItem := range value {
		valueRule = append(valueRule, valueItem)
	}

	logs, sub, err := _Contract.contract.FilterLogs(opts, "IntLogged", valueRule)
	if err != nil {
		return nil, err
	}
	return &ContractIntLoggedIterator{contract: _Contract.contract, event: "IntLogged", logs: logs, sub: sub}, nil
}

// WatchIntLogged is a free log subscription operation binding the contract event 0x8c208a695bc4e5cf03539c05e9e2aac5f3c8beb4f49b7ea6d390a7332d5e1852.
//
// Solidity: event IntLogged(int256 indexed value)
func (_Contract *ContractFilterer) WatchIntLogged(opts *bind.WatchOpts, sink chan<- *ContractIntLogged, value []*big.Int) (event.Subscription, error) {

	var valueRule []interface{}
	for _, valueItem := range value {
		valueRule = append(valueRule, valueItem)
	}

	logs, sub, err := _Contract.contract.WatchLogs(opts, "IntLogged", valueRule)
	if err != nil {
		return nil, err
	}
	return event.NewSubscription(func(quit <-chan struct{}) error {
		defer sub.Unsubscribe()
		for {
			select {
			case log := <-logs:
				// New log arrived, parse the event and forward to the user
				event := new(ContractIntLogged)
				if err := _Contract.contract.UnpackLog(event, "IntLogged", log); err != nil {
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

// ParseIntLogged is a log parse operation binding the contract event 0x8c208a695bc4e5cf03539c05e9e2aac5f3c8beb4f49b7ea6d390a7332d5e1852.
//
// Solidity: event IntLogged(int256 indexed value)
func (_Contract *ContractFilterer) ParseIntLogged(log types.Log) (*ContractIntLogged, error) {
	event := new(ContractIntLogged)
	if err := _Contract.contract.UnpackLog(event, "IntLogged", log); err != nil {
		return nil, err
	}
	event.Raw = log
	return event, nil
}

// ContractStringLoggedIterator is returned from FilterStringLogged and is used to iterate over the raw logs and unpacked data for StringLogged events raised by the Contract contract.
type ContractStringLoggedIterator struct {
	Event *ContractStringLogged // Event containing the contract specifics and raw log

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
func (it *ContractStringLoggedIterator) Next() bool {
	// If the iterator failed, stop iterating
	if it.fail != nil {
		return false
	}
	// If the iterator completed, deliver directly whatever's available
	if it.done {
		select {
		case log := <-it.logs:
			it.Event = new(ContractStringLogged)
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
		it.Event = new(ContractStringLogged)
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
func (it *ContractStringLoggedIterator) Error() error {
	return it.fail
}

// Close terminates the iteration process, releasing any pending underlying
// resources.
func (it *ContractStringLoggedIterator) Close() error {
	it.sub.Unsubscribe()
	return nil
}

// ContractStringLogged represents a StringLogged event raised by the Contract contract.
type ContractStringLogged struct {
	Value string
	Raw   types.Log // Blockchain specific contextual infos
}

// FilterStringLogged is a free log retrieval operation binding the contract event 0x04920d60568e8fdd457e29d24329808c937b3df3e6053b7882b7c6c2a03e2e84.
//
// Solidity: event StringLogged(string value)
func (_Contract *ContractFilterer) FilterStringLogged(opts *bind.FilterOpts) (*ContractStringLoggedIterator, error) {

	logs, sub, err := _Contract.contract.FilterLogs(opts, "StringLogged")
	if err != nil {
		return nil, err
	}
	return &ContractStringLoggedIterator{contract: _Contract.contract, event: "StringLogged", logs: logs, sub: sub}, nil
}

// WatchStringLogged is a free log subscription operation binding the contract event 0x04920d60568e8fdd457e29d24329808c937b3df3e6053b7882b7c6c2a03e2e84.
//
// Solidity: event StringLogged(string value)
func (_Contract *ContractFilterer) WatchStringLogged(opts *bind.WatchOpts, sink chan<- *ContractStringLogged) (event.Subscription, error) {

	logs, sub, err := _Contract.contract.WatchLogs(opts, "StringLogged")
	if err != nil {
		return nil, err
	}
	return event.NewSubscription(func(quit <-chan struct{}) error {
		defer sub.Unsubscribe()
		for {
			select {
			case log := <-logs:
				// New log arrived, parse the event and forward to the user
				event := new(ContractStringLogged)
				if err := _Contract.contract.UnpackLog(event, "StringLogged", log); err != nil {
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

// ParseStringLogged is a log parse operation binding the contract event 0x04920d60568e8fdd457e29d24329808c937b3df3e6053b7882b7c6c2a03e2e84.
//
// Solidity: event StringLogged(string value)
func (_Contract *ContractFilterer) ParseStringLogged(log types.Log) (*ContractStringLogged, error) {
	event := new(ContractStringLogged)
	if err := _Contract.contract.UnpackLog(event, "StringLogged", log); err != nil {
		return nil, err
	}
	event.Raw = log
	return event, nil
}

// ContractUintLoggedIterator is returned from FilterUintLogged and is used to iterate over the raw logs and unpacked data for UintLogged events raised by the Contract contract.
type ContractUintLoggedIterator struct {
	Event *ContractUintLogged // Event containing the contract specifics and raw log

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
func (it *ContractUintLoggedIterator) Next() bool {
	// If the iterator failed, stop iterating
	if it.fail != nil {
		return false
	}
	// If the iterator completed, deliver directly whatever's available
	if it.done {
		select {
		case log := <-it.logs:
			it.Event = new(ContractUintLogged)
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
		it.Event = new(ContractUintLogged)
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
func (it *ContractUintLoggedIterator) Error() error {
	return it.fail
}

// Close terminates the iteration process, releasing any pending underlying
// resources.
func (it *ContractUintLoggedIterator) Close() error {
	it.sub.Unsubscribe()
	return nil
}

// ContractUintLogged represents a UintLogged event raised by the Contract contract.
type ContractUintLogged struct {
	Value *big.Int
	Raw   types.Log // Blockchain specific contextual infos
}

// FilterUintLogged is a free log retrieval operation binding the contract event 0x7f6e805926ceb536bd3e8ae88fd58959cf1508c52212c163fc3ae2d5e02c25aa.
//
// Solidity: event UintLogged(uint256 indexed value)
func (_Contract *ContractFilterer) FilterUintLogged(opts *bind.FilterOpts, value []*big.Int) (*ContractUintLoggedIterator, error) {

	var valueRule []interface{}
	for _, valueItem := range value {
		valueRule = append(valueRule, valueItem)
	}

	logs, sub, err := _Contract.contract.FilterLogs(opts, "UintLogged", valueRule)
	if err != nil {
		return nil, err
	}
	return &ContractUintLoggedIterator{contract: _Contract.contract, event: "UintLogged", logs: logs, sub: sub}, nil
}

// WatchUintLogged is a free log subscription operation binding the contract event 0x7f6e805926ceb536bd3e8ae88fd58959cf1508c52212c163fc3ae2d5e02c25aa.
//
// Solidity: event UintLogged(uint256 indexed value)
func (_Contract *ContractFilterer) WatchUintLogged(opts *bind.WatchOpts, sink chan<- *ContractUintLogged, value []*big.Int) (event.Subscription, error) {

	var valueRule []interface{}
	for _, valueItem := range value {
		valueRule = append(valueRule, valueItem)
	}

	logs, sub, err := _Contract.contract.WatchLogs(opts, "UintLogged", valueRule)
	if err != nil {
		return nil, err
	}
	return event.NewSubscription(func(quit <-chan struct{}) error {
		defer sub.Unsubscribe()
		for {
			select {
			case log := <-logs:
				// New log arrived, parse the event and forward to the user
				event := new(ContractUintLogged)
				if err := _Contract.contract.UnpackLog(event, "UintLogged", log); err != nil {
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

// ParseUintLogged is a log parse operation binding the contract event 0x7f6e805926ceb536bd3e8ae88fd58959cf1508c52212c163fc3ae2d5e02c25aa.
//
// Solidity: event UintLogged(uint256 indexed value)
func (_Contract *ContractFilterer) ParseUintLogged(log types.Log) (*ContractUintLogged, error) {
	event := new(ContractUintLogged)
	if err := _Contract.contract.UnpackLog(event, "UintLogged", log); err != nil {
		return nil, err
	}
	event.Raw = log
	return event, nil
}
