// Package types holds the DON-to-DON wire messages and the interfaces the layers above them speak:
// the Dispatcher a message is sent through, the Receiver it is delivered to, and the aggregator and
// hasher used to agree on responses.
//
// The proto keeps its original package name (remote) and field numbers, so the wire is unchanged by
// the move out of chainlink's core/capabilities/remote/types - only the Go import path differs.
// Nothing may generate a second copy of it: two registrations of don2don.MessageBody in one binary
// panic at init, so core imports this rather than keeping its own.
//
// The proto is compiled from the module root so the descriptor is registered under
// x/don2don/types/messages.proto rather than a bare messages.proto: descriptor file names are
// global, and a bare one collides with every other messages.proto a binary happens to link in.
//
//go:generate protoc -I ../../.. --go_out=../../.. --go_opt=paths=source_relative x/don2don/types/messages.proto
package types

import (
	"context"

	commoncap "github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/services"

	rage "github.com/smartcontractkit/capabilities/libs/x/rage"
)

const (
	MethodRegisterTrigger          = "RegisterTrigger"
	MethodUnregisterTrigger        = "UnregisterTrigger"
	MethodTriggerRegistrationCheck = "TriggerRegistrationCheck"
	MethodTriggerEvent             = "TriggerEvent"
	MethodExecute                  = "Execute"
	MethodTriggerEventAck          = "TriggerEventACK"
)

type Dispatcher interface {
	services.Service
	SetReceiver(capabilityID string, donID uint32, receiver Receiver) error
	RemoveReceiver(capabilityID string, donID uint32)
	SetReceiverForMethod(capabilityID string, donID uint32, method string, receiver Receiver) error
	RemoveReceiverForMethod(capabilityID string, donID uint32, method string)
	Send(peerID rage.PeerID, msgBody *MessageBody) error
}

type Receiver interface {
	Receive(ctx context.Context, msg *MessageBody)
}

type ReceiverService interface {
	services.Service
	Receiver
}

type Aggregator interface {
	Aggregate(eventID string, responses [][]byte) (commoncap.TriggerResponse, error)
}

// NOTE: this type will become part of the Registry (KS-108)
type DON struct {
	ID      string
	Members []rage.PeerID
	F       uint8
}

type MessageHasher interface {
	Hash(ctx context.Context, msg *MessageBody) ([32]byte, error)
}
