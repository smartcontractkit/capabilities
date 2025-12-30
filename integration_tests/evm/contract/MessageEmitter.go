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
	ABI: "[{\"anonymous\":false,\"inputs\":[{\"indexed\":false,\"internalType\":\"string\",\"name\":\"message\",\"type\":\"string\"}],\"name\":\"MessageEmitted\",\"type\":\"event\"},{\"anonymous\":false,\"inputs\":[{\"indexed\":true,\"internalType\":\"uint256\",\"name\":\"topic2\",\"type\":\"uint256\"},{\"indexed\":true,\"internalType\":\"uint256\",\"name\":\"topic3\",\"type\":\"uint256\"},{\"indexed\":true,\"internalType\":\"uint256\",\"name\":\"topic4\",\"type\":\"uint256\"},{\"indexed\":false,\"internalType\":\"string\",\"name\":\"message\",\"type\":\"string\"}],\"name\":\"MultiTopicEmitted\",\"type\":\"event\"},{\"inputs\":[{\"internalType\":\"string\",\"name\":\"message\",\"type\":\"string\"}],\"name\":\"emitMessage\",\"outputs\":[],\"stateMutability\":\"nonpayable\",\"type\":\"function\"},{\"inputs\":[{\"internalType\":\"uint256\",\"name\":\"topic2\",\"type\":\"uint256\"},{\"internalType\":\"uint256\",\"name\":\"topic3\",\"type\":\"uint256\"},{\"internalType\":\"uint256\",\"name\":\"topic4\",\"type\":\"uint256\"},{\"internalType\":\"string\",\"name\":\"message\",\"type\":\"string\"}],\"name\":\"emitMultiTopic\",\"outputs\":[],\"stateMutability\":\"nonpayable\",\"type\":\"function\"}]",
	Bin: "0x6080604052348015600e575f5ffd5b5061030d8061001c5f395ff3fe608060405234801561000f575f5ffd5b5060043610610034575f3560e01c80632ac0df2614610038578063c6aea90514610054575b5f5ffd5b610052600480360381019061004d9190610159565b610070565b005b61006e600480360381019061006991906101d7565b6100ad565b005b7f50ede1f15a65bab9edf83cef0d1ffb1f21234653b3e58170594c3d8685d30e7a82826040516100a19291906102b5565b60405180910390a15050565b8284867f815c4a45ed35542680545d6ab891cc82bbae48ed01cab76b527f3daf19b9f0ba85856040516100e19291906102b5565b60405180910390a45050505050565b5f5ffd5b5f5ffd5b5f5ffd5b5f5ffd5b5f5ffd5b5f5f83601f840112610119576101186100f8565b5b8235905067ffffffffffffffff811115610136576101356100fc565b5b60208301915083600182028301111561015257610151610100565b5b9250929050565b5f5f6020838503121561016f5761016e6100f0565b5b5f83013567ffffffffffffffff81111561018c5761018b6100f4565b5b61019885828601610104565b92509250509250929050565b5f819050919050565b6101b6816101a4565b81146101c0575f5ffd5b50565b5f813590506101d1816101ad565b92915050565b5f5f5f5f5f608086880312156101f0576101ef6100f0565b5b5f6101fd888289016101c3565b955050602061020e888289016101c3565b945050604061021f888289016101c3565b935050606086013567ffffffffffffffff8111156102405761023f6100f4565b5b61024c88828901610104565b92509250509295509295909350565b5f82825260208201905092915050565b828183375f83830152505050565b5f601f19601f8301169050919050565b5f610294838561025b565b93506102a183858461026b565b6102aa83610279565b840190509392505050565b5f6020820190508181035f8301526102ce818486610289565b9050939250505056fea2646970667358221220431798067102dc183e232a1929a7b81111860b1d542da3f9947f4b2185496fd264736f6c634300081e0033",
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

// EmitMultiTopic is a paid mutator transaction binding the contract method 0xc6aea905.
//
// Solidity: function emitMultiTopic(uint256 topic2, uint256 topic3, uint256 topic4, string message) returns()
func (_Contract *ContractTransactor) EmitMultiTopic(opts *bind.TransactOpts, topic2 *big.Int, topic3 *big.Int, topic4 *big.Int, message string) (*types.Transaction, error) {
	return _Contract.contract.Transact(opts, "emitMultiTopic", topic2, topic3, topic4, message)
}

// EmitMultiTopic is a paid mutator transaction binding the contract method 0xc6aea905.
//
// Solidity: function emitMultiTopic(uint256 topic2, uint256 topic3, uint256 topic4, string message) returns()
func (_Contract *ContractSession) EmitMultiTopic(topic2 *big.Int, topic3 *big.Int, topic4 *big.Int, message string) (*types.Transaction, error) {
	return _Contract.Contract.EmitMultiTopic(&_Contract.TransactOpts, topic2, topic3, topic4, message)
}

// EmitMultiTopic is a paid mutator transaction binding the contract method 0xc6aea905.
//
// Solidity: function emitMultiTopic(uint256 topic2, uint256 topic3, uint256 topic4, string message) returns()
func (_Contract *ContractTransactorSession) EmitMultiTopic(topic2 *big.Int, topic3 *big.Int, topic4 *big.Int, message string) (*types.Transaction, error) {
	return _Contract.Contract.EmitMultiTopic(&_Contract.TransactOpts, topic2, topic3, topic4, message)
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

// ContractMultiTopicEmittedIterator is returned from FilterMultiTopicEmitted and is used to iterate over the raw logs and unpacked data for MultiTopicEmitted events raised by the Contract contract.
type ContractMultiTopicEmittedIterator struct {
	Event *ContractMultiTopicEmitted // Event containing the contract specifics and raw log

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
func (it *ContractMultiTopicEmittedIterator) Next() bool {
	// If the iterator failed, stop iterating
	if it.fail != nil {
		return false
	}
	// If the iterator completed, deliver directly whatever's available
	if it.done {
		select {
		case log := <-it.logs:
			it.Event = new(ContractMultiTopicEmitted)
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
		it.Event = new(ContractMultiTopicEmitted)
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
func (it *ContractMultiTopicEmittedIterator) Error() error {
	return it.fail
}

// Close terminates the iteration process, releasing any pending underlying
// resources.
func (it *ContractMultiTopicEmittedIterator) Close() error {
	it.sub.Unsubscribe()
	return nil
}

// ContractMultiTopicEmitted represents a MultiTopicEmitted event raised by the Contract contract.
type ContractMultiTopicEmitted struct {
	Topic2  *big.Int
	Topic3  *big.Int
	Topic4  *big.Int
	Message string
	Raw     types.Log // Blockchain specific contextual infos
}

// FilterMultiTopicEmitted is a free log retrieval operation binding the contract event 0x815c4a45ed35542680545d6ab891cc82bbae48ed01cab76b527f3daf19b9f0ba.
//
// Solidity: event MultiTopicEmitted(uint256 indexed topic2, uint256 indexed topic3, uint256 indexed topic4, string message)
func (_Contract *ContractFilterer) FilterMultiTopicEmitted(opts *bind.FilterOpts, topic2 []*big.Int, topic3 []*big.Int, topic4 []*big.Int) (*ContractMultiTopicEmittedIterator, error) {

	var topic2Rule []interface{}
	for _, topic2Item := range topic2 {
		topic2Rule = append(topic2Rule, topic2Item)
	}
	var topic3Rule []interface{}
	for _, topic3Item := range topic3 {
		topic3Rule = append(topic3Rule, topic3Item)
	}
	var topic4Rule []interface{}
	for _, topic4Item := range topic4 {
		topic4Rule = append(topic4Rule, topic4Item)
	}

	logs, sub, err := _Contract.contract.FilterLogs(opts, "MultiTopicEmitted", topic2Rule, topic3Rule, topic4Rule)
	if err != nil {
		return nil, err
	}
	return &ContractMultiTopicEmittedIterator{contract: _Contract.contract, event: "MultiTopicEmitted", logs: logs, sub: sub}, nil
}

// WatchMultiTopicEmitted is a free log subscription operation binding the contract event 0x815c4a45ed35542680545d6ab891cc82bbae48ed01cab76b527f3daf19b9f0ba.
//
// Solidity: event MultiTopicEmitted(uint256 indexed topic2, uint256 indexed topic3, uint256 indexed topic4, string message)
func (_Contract *ContractFilterer) WatchMultiTopicEmitted(opts *bind.WatchOpts, sink chan<- *ContractMultiTopicEmitted, topic2 []*big.Int, topic3 []*big.Int, topic4 []*big.Int) (event.Subscription, error) {

	var topic2Rule []interface{}
	for _, topic2Item := range topic2 {
		topic2Rule = append(topic2Rule, topic2Item)
	}
	var topic3Rule []interface{}
	for _, topic3Item := range topic3 {
		topic3Rule = append(topic3Rule, topic3Item)
	}
	var topic4Rule []interface{}
	for _, topic4Item := range topic4 {
		topic4Rule = append(topic4Rule, topic4Item)
	}

	logs, sub, err := _Contract.contract.WatchLogs(opts, "MultiTopicEmitted", topic2Rule, topic3Rule, topic4Rule)
	if err != nil {
		return nil, err
	}
	return event.NewSubscription(func(quit <-chan struct{}) error {
		defer sub.Unsubscribe()
		for {
			select {
			case log := <-logs:
				// New log arrived, parse the event and forward to the user
				event := new(ContractMultiTopicEmitted)
				if err := _Contract.contract.UnpackLog(event, "MultiTopicEmitted", log); err != nil {
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

// ParseMultiTopicEmitted is a log parse operation binding the contract event 0x815c4a45ed35542680545d6ab891cc82bbae48ed01cab76b527f3daf19b9f0ba.
//
// Solidity: event MultiTopicEmitted(uint256 indexed topic2, uint256 indexed topic3, uint256 indexed topic4, string message)
func (_Contract *ContractFilterer) ParseMultiTopicEmitted(log types.Log) (*ContractMultiTopicEmitted, error) {
	event := new(ContractMultiTopicEmitted)
	if err := _Contract.contract.UnpackLog(event, "MultiTopicEmitted", log); err != nil {
		return nil, err
	}
	event.Raw = log
	return event, nil
}
