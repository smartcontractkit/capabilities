package config

import "time"

type Config struct {
	ChainID                uint64        `json:"chainId"`
	Network                string        `json:"network"`
	LogTriggerPollInterval time.Duration `json:"logTriggerPollInterval"`
	BlockDepth             int64         `json:"blockDepth"`
	CREForwarderAddress    string        `json:"creForwarderAddress"`
	// The minimum amount of gas that the receiver contract must get to process the forwarder report. This is the default value used when the user doesn't specify a gas limit when invoking WriteReport.
	ReceiverGasMinimum       uint64        `json:"receiverGasMinimum"`
	NodeAddress              string        `json:"nodeAddress"`
	ObservationPollersNumber uint          `json:"observationPollersNumber"`
	ObservationPollPeriod    time.Duration `json:"observationPollPeriod"`
	ChainHeightPollPeriod    time.Duration `json:"chainHeightPollPeriod"`
	UnknownRequestsTTL       time.Duration `json:"unknownRequestsTTL"`
}
