package config

import (
	"time"

	"github.com/smartcontractkit/capabilities/chain_capabilities/common/txevents"
)

type Config struct {
	ChainID                         uint64        `json:"chainId"`
	Network                         string        `json:"network"`
	LogTriggerPollInterval          time.Duration `json:"logTriggerPollInterval"`
	LogTriggerSendChannelBufferSize uint64        `json:"logTriggerSendChannelBufferSize"`
	LogTriggerLimitQueryLogSize     uint64        `json:"logTriggerLimitQueryLogSize"`
	CREForwarderAddress             string        `json:"creForwarderAddress"`
	ForwarderLookbackBlocks         int64         `json:"forwarderLookbackBlocks"` // defines how many blocks back to search for the ReportProcessed event (default 100).
	// The minimum amount of gas that the receiver contract must get to process the forwarder report. This is the default value used when the user doesn't specify a gas limit when invoking WriteReport.
	ReceiverGasMinimum uint64 `json:"receiverGasMinimum"`
	// Safety margin, in gas, added on top of the forwarder contract's internal gas reservation
	// to compute the minimum gas limit accepted for an explicit WriteReport gas limit
	// (ReceiverGasMinimum + forwarder overhead). It covers pre-route consumption not visible
	// in the contract constants (tx intrinsic cost, calldata, ecrecover per signature, storage).
	// 0 means "use the default" (see contracts.DefaultForwarderGasOverheadMargin). Set per chain
	// when the chain's gas metering or report/signature sizes differ materially from mainnet.
	ForwarderGasOverheadMargin uint64 `json:"forwarderGasOverheadMargin"`
	// DisableGasLimitCheck skips comparing a failed transmission tx's onchain gas limit against
	// the requested gas limit, treating the failed attempt as having had sufficient gas (no
	// resubmission). Set it for chains that don't honor a per-tx gas limit, where the sent limit
	// cannot be compared with the requested one. Default false.
	DisableGasLimitCheck          bool          `json:"disableGasLimitCheck"`
	NodeAddress                   string        `json:"nodeAddress"`
	ObservationPollerWorkersCount uint          `json:"observationPollerWorkersCount"`
	ObservationPollPeriod         time.Duration `json:"observationPollPeriod"`
	ChainHeightPollPeriod         time.Duration `json:"chainHeightPollPeriod"`
	UnknownRequestsTTL            time.Duration `json:"unknownRequestsTTL"`
	MaxUnknownRequestsCacheSize   int           `json:"maxUnknownRequestsCacheSize"`
	// DeltaStage for staggered transmission scheduling
	DeltaStage time.Duration `json:"deltaStage"`
	IsLocaL    bool          `json:"isLocal"` // use in integration-test to skip transmission scheduler initialization for log_trigger

	// TxEvents controls emission of ChainTransactionObserved events, which feed the CRE
	// Transactions Viewer through ChIP and the Blockchain Finality Service (PLEX-3409).
	TxEvents txevents.Config `json:"txEvents"`
}
