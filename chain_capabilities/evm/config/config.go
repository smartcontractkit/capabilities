package config

import "time"

type Config struct {
	ChainID                         uint64        `json:"chainId"`
	Network                         string        `json:"network"`
	LogTriggerPollInterval          time.Duration `json:"logTriggerPollInterval"`
	LogTriggerSendChannelBufferSize uint64        `json:"logTriggerSendChannelBufferSize"`
	LogTriggerLimitQueryLogSize     uint64        `json:"logTriggerLimitQueryLogSize"`
	CREForwarderAddress             string        `json:"creForwarderAddress"`
	ForwarderLookbackBlocks         int64         `json:"forwarderLookbackBlocks"` // defines how many blocks back to search for the ReportProcessed event (default 100).
	// The minimum amount of gas that the receiver contract must get to process the forwarder report. This is the default value used when the user doesn't specify a gas limit when invoking WriteReport.
	ReceiverGasMinimum            uint64        `json:"receiverGasMinimum"`
	NodeAddress                   string        `json:"nodeAddress"`
	ObservationPollerWorkersCount uint          `json:"observationPollerWorkersCount"`
	ObservationPollPeriod         time.Duration `json:"observationPollPeriod"`
	ChainHeightPollPeriod         time.Duration `json:"chainHeightPollPeriod"`
	UnknownRequestsTTL            time.Duration `json:"unknownRequestsTTL"`
	// DeltaStage for staggered transmission scheduling
	DeltaStage time.Duration `json:"deltaStage"`
	IsLocaL    bool          `json:"isLocal"` // use in integration-test to skip transmission scheduler initialization for log_trigger
}
