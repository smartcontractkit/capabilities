package feature

import (
	"fmt"

	"google.golang.org/protobuf/proto"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
)

// Respond is the response a capability's output is sent as, its production message if the request was, as
// UnmarshalAny reported.
func Respond(output proto.Message, production bool, metadata capabilities.ResponseMetadata, attestation *capabilities.OCRAttestation) (capabilities.CapabilityResponse, error) {
	payload, err := MarshalAny(output, production)
	if err != nil {
		return capabilities.CapabilityResponse{}, fmt.Errorf("failed to write the response: %w", err)
	}
	return capabilities.CapabilityResponse{Payload: payload, Metadata: metadata, OCRAttestation: attestation}, nil
}

// ForwardTriggers sends each event a capability sends, as its production message if the registration was, until
// the capability stops sending or stop is closed.
func ForwardTriggers[O proto.Message](stop <-chan struct{}, triggerType string, production bool, events <-chan capabilities.TriggerAndId[O]) <-chan capabilities.TriggerResponse {
	responses := make(chan capabilities.TriggerResponse)
	go func() {
		defer close(responses)
		for {
			select {
			case event, open := <-events:
				if !open {
					return
				}
				payload, err := MarshalAny(event.Trigger, production)
				response := capabilities.TriggerResponse{Event: capabilities.TriggerEvent{TriggerType: triggerType, ID: event.Id, Payload: payload}, Err: err}
				select {
				case responses <- response:
				case <-stop:
					return
				}
			case <-stop:
				return
			}
		}
	}()
	return responses
}
