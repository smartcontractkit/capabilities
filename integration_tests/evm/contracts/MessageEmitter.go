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
	ABI: "[{\"anonymous\":false,\"inputs\":[{\"indexed\":false,\"internalType\":\"string\",\"name\":\"message\",\"type\":\"string\"}],\"name\":\"MessageEmitted\",\"type\":\"event\"},{\"inputs\":[{\"internalType\":\"string\",\"name\":\"message\",\"type\":\"string\"}],\"name\":\"emitMessage\",\"outputs\":[],\"stateMutability\":\"nonpayable\",\"type\":\"function\"}]",
	Bin: "0x6080604052348015600e575f5ffd5b506101ec8061001c5f395ff3fe608060405234801561000f575f5ffd5b5060043610610029575f3560e01c80632ac0df261461002d575b5f5ffd5b610047600480360381019061004291906100ef565b610049565b005b7f50ede1f15a65bab9edf83cef0d1ffb1f21234653b3e58170594c3d8685d30e7a828260405161007a929190610194565b60405180910390a15050565b5f5ffd5b5f5ffd5b5f5ffd5b5f5ffd5b5f5ffd5b5f5f83601f8401126100af576100ae61008e565b5b8235905067ffffffffffffffff8111156100cc576100cb610092565b5b6020830191508360018202830111156100e8576100e7610096565b5b9250929050565b5f5f6020838503121561010557610104610086565b5b5f83013567ffffffffffffffff8111156101225761012161008a565b5b61012e8582860161009a565b92509250509250929050565b5f82825260208201905092915050565b828183375f83830152505050565b5f601f19601f8301169050919050565b5f610173838561013a565b935061018083858461014a565b61018983610158565b840190509392505050565b5f6020820190508181035f8301526101ad818486610168565b9050939250505056fea2646970667358221220c199d99eace1c6468c729d11ee03d6fd24e3e5467422b4e882c34432c3ce929164736f6c634300081e0033",
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

// EmitMessage is a paid mutator transaction binding the contract method 0x2ac0df26.
//
// Solidity: function emitMessage(string message) returns()
func (_Contract *ContractTransactor) EmitMessage(opts *bind.TransactOpts, message string) (*types.Transaction, error) {
	return _Contract.contract.Transact(opts, "emitMessage", message)
}

// EmitMessage is a paid mutator transaction binding the contract method 0x2ac0df26.
//
// Solidity: function emitMessage(string message) returns()
func (_Contract *ContractSession) EmitMessage(message string) (*types.Transaction, error) {
	return _Contract.Contract.EmitMessage(&_Contract.TransactOpts, message)
}

// EmitMessage is a paid mutator transaction binding the contract method 0x2ac0df26.
//
// Solidity: function emitMessage(string message) returns()
func (_Contract *ContractTransactorSession) EmitMessage(message string) (*types.Transaction, error) {
	return _Contract.Contract.EmitMessage(&_Contract.TransactOpts, message)
}

// ContractMessageEmittedIterator is returned from FilterMessageEmitted and is used to iterate over the raw logs and unpacked data for MessageEmitted events raised by the Contract contract.
type ContractMessageEmittedIterator struct {
	Event *ContractMessageEmitted // Event containing the contract specifics and raw log

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
func (it *ContractMessageEmittedIterator) Next() bool {
	// If the iterator failed, stop iterating
	if it.fail != nil {
		return false
	}
	// If the iterator completed, deliver directly whatever's available
	if it.done {
		select {
		case log := <-it.logs:
			it.Event = new(ContractMessageEmitted)
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
		it.Event = new(ContractMessageEmitted)
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
func (it *ContractMessageEmittedIterator) Error() error {
	return it.fail
}

// Close terminates the iteration process, releasing any pending underlying
// resources.
func (it *ContractMessageEmittedIterator) Close() error {
	it.sub.Unsubscribe()
	return nil
}

// ContractMessageEmitted represents a MessageEmitted event raised by the Contract contract.
type ContractMessageEmitted struct {
	Message string
	Raw     types.Log // Blockchain specific contextual infos
}

// FilterMessageEmitted is a free log retrieval operation binding the contract event 0x50ede1f15a65bab9edf83cef0d1ffb1f21234653b3e58170594c3d8685d30e7a.
//
// Solidity: event MessageEmitted(string message)
func (_Contract *ContractFilterer) FilterMessageEmitted(opts *bind.FilterOpts) (*ContractMessageEmittedIterator, error) {

	logs, sub, err := _Contract.contract.FilterLogs(opts, "MessageEmitted")
	if err != nil {
		return nil, err
	}
	return &ContractMessageEmittedIterator{contract: _Contract.contract, event: "MessageEmitted", logs: logs, sub: sub}, nil
}

// WatchMessageEmitted is a free log subscription operation binding the contract event 0x50ede1f15a65bab9edf83cef0d1ffb1f21234653b3e58170594c3d8685d30e7a.
//
// Solidity: event MessageEmitted(string message)
func (_Contract *ContractFilterer) WatchMessageEmitted(opts *bind.WatchOpts, sink chan<- *ContractMessageEmitted) (event.Subscription, error) {

	logs, sub, err := _Contract.contract.WatchLogs(opts, "MessageEmitted")
	if err != nil {
		return nil, err
	}
	return event.NewSubscription(func(quit <-chan struct{}) error {
		defer sub.Unsubscribe()
		for {
			select {
			case log := <-logs:
				// New log arrived, parse the event and forward to the user
				event := new(ContractMessageEmitted)
				if err := _Contract.contract.UnpackLog(event, "MessageEmitted", log); err != nil {
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

// ParseMessageEmitted is a log parse operation binding the contract event 0x50ede1f15a65bab9edf83cef0d1ffb1f21234653b3e58170594c3d8685d30e7a.
//
// Solidity: event MessageEmitted(string message)
func (_Contract *ContractFilterer) ParseMessageEmitted(log types.Log) (*ContractMessageEmitted, error) {
	event := new(ContractMessageEmitted)
	if err := _Contract.contract.UnpackLog(event, "MessageEmitted", log); err != nil {
		return nil, err
	}
	event.Raw = log
	return event, nil
}
