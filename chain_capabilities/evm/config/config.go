// Package config is what the EVM capability needs told, as opposed to what it
// reads from the chain or is handed by the process hosting it.
//
// The chain's own settings are not here. Where the RPC is, how deep finality is
// and how often the log poller reads are the chain's, and they are configured
// where the chain is built (chainlink-evm's cre/evmchain) so that one set of
// evm.* settings describes one chain rather than two halves of it.
package config

import (
	"errors"
	"fmt"
	"time"

	"github.com/ethereum/go-ethereum/common"
)

type Config struct {
	// CREForwarderAddress is the contract a report is written through, and
	// ForwarderLookbackBlocks is how far back its ReportProcessed events are read
	// when this process has to work out whether a report already landed.
	// Neither is `validate:"required"`, which is checked when the configuration is
	// decoded: an embedded run starting a chain of its own has no forwarder to name
	// until it has deployed one. Both are still required to run - see Validate, which
	// says so after the chain, if there is going to be one, exists.
	CREForwarderAddress     string `json:"creForwarderAddress" usage:"address of the CRE forwarder contract reports are written through; defaulted to the one deployed on a simulated chain" example:"'0x0000000000000000000000000000000000000000'"`
	ForwarderLookbackBlocks int64  `json:"forwarderLookbackBlocks" usage:"how many blocks back to search for the forwarder's ReportProcessed event"`

	// ReceiverGasMinimum is the gas a receiving contract is guaranteed for
	// processing a report, used when a workflow names no limit of its own.
	ReceiverGasMinimum uint64 `json:"receiverGasMinimum" usage:"gas a receiver contract is guaranteed when a workflow names no limit of its own"`

	// NodeAddress is the account this node sends this chain's transactions from,
	// which the process holding the keys signs for, and the account the telemetry
	// this capability emits is reported under.
	//
	// It is also what narrows the keystore to this chain: the node holds a key per
	// chain it runs, and the store they share cannot say which is which. See
	// chain.OnlyAccount.
	NodeAddress string `json:"nodeAddress" usage:"the account this node sends this chain's transactions from; the node must hold its key" example:"'0x0000000000000000000000000000000000000000'"`

	// PrivateKeys are the keys an embedded run signs this chain's transactions with,
	// one per instance, in instance order. Empty - the ordinary case - leaves each
	// instance deriving its own from its index (see chain.DeterministicKeystore), and
	// a process running beside a node ignores this entirely: its keys are the node's,
	// reached through --keystore.proxy-address.
	//
	// It exists for pointing an embedded run at a real chain, where a derived account
	// is an account with no funds. It is a secret, so prefer CRE_EVM_PRIVATE_KEYS to a
	// flag, which every process on the machine can read.
	PrivateKeys []string `json:"privateKeys" usage:"private keys an embedded run signs with, one per instance; prefer the CRE_EVM_PRIVATE_KEYS env var to a flag" flagdocs:"noexample"`

	// LogTrigger* bound the log trigger: how often it reads, how much it will hold
	// for a workflow that is not keeping up, and how many logs one query returns.
	LogTriggerPollInterval          time.Duration `json:"logTriggerPollInterval" usage:"how often a registered log trigger reads the logs it matched"`
	LogTriggerSendChannelBufferSize uint64        `json:"logTriggerSendChannelBufferSize" usage:"how many matched logs are held for a workflow that is not keeping up"`
	LogTriggerLimitQueryLogSize     uint64        `json:"logTriggerLimitQueryLogSize" usage:"how many logs one query returns; must not exceed the send buffer"`

	// Observation* configure the poll that answers another node's request for what
	// this node saw, which is what the chain consensus round is made of.
	ObservationPollerWorkersCount uint          `json:"observationPollerWorkersCount" usage:"how many requests for this node's observation are answered at once"`
	ObservationPollPeriod         time.Duration `json:"observationPollPeriod" usage:"how often a pending request is re-checked for an answer"`

	// ChainHeightPollPeriod is how often this node reads the chain's height, which
	// is what its nodes agree on before answering a read.
	ChainHeightPollPeriod time.Duration `json:"chainHeightPollPeriod" usage:"how often the chain's height is read, for the height the DON agrees on"`

	// UnknownRequestsTTL is how long a request that arrived before this node knew
	// about it is kept, so a round is not lost to the order messages arrive in.
	UnknownRequestsTTL time.Duration `json:"unknownRequestsTTL" usage:"how long a request this node has not seen locally is kept before it is dropped"`

	// DeltaStage staggers transmission across the DON, so one agreed report is not
	// sent by every member at once. Zero disables it.
	DeltaStage time.Duration `json:"deltaStage" usage:"delay between DON members transmitting the same report; 0 sends without staggering"`

	// IsLocal skips the DON lookup that staggering needs, for a test running this
	// capability without a registry behind it.
	IsLocal bool `json:"isLocal" usage:"skip the DON lookup transmission scheduling needs, for local runs"`
}

// Default is what a setting keeps when it is not configured. The values that are
// zero here have no useful default - an address, a gas floor - and are required.
var Default = Config{
	ForwarderLookbackBlocks:       100,
	LogTriggerPollInterval:        time.Second,
	ObservationPollerWorkersCount: 10,
	ObservationPollPeriod:         2 * time.Second,
	ChainHeightPollPeriod:         time.Second,
	UnknownRequestsTTL:            10 * time.Second,
}

// Validate rejects what the capability cannot run with, so a misconfiguration is
// a startup error rather than the first request failing.
func (c Config) Validate() error {
	var errs []error

	if !common.IsHexAddress(c.CREForwarderAddress) {
		errs = append(errs, fmt.Errorf("--evm.cre-forwarder-address %q is not an address", c.CREForwarderAddress))
	}
	if c.ReceiverGasMinimum == 0 {
		errs = append(errs, errors.New("--evm.receiver-gas-minimum must be greater than 0"))
	}
	if c.LogTriggerPollInterval < 0 {
		errs = append(errs, fmt.Errorf("--evm.log-trigger-poll-interval must not be negative, got %s", c.LogTriggerPollInterval))
	}

	return errors.Join(errs...)
}
