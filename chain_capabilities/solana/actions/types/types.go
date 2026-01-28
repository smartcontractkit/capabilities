package types

import "github.com/gagliardetto/solana-go"

type TransmissionState uint8

const (
	TransmissionStateNotAttempted TransmissionState = iota
	TransmissionStateSucceeded
	TransmissionStateFailed
)

type TransmissionInfo struct {
	State     TransmissionState
	Signature solana.Signature
}
