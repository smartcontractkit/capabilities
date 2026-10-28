package server

import (
	"fmt"

	"google.golang.org/protobuf/proto"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"

	"github.com/smartcontractkit/capabilities/libs/capability/feature"
)

// Respond sends output as its production message if the request was one, as feature.UnmarshalAny reported.
func Respond(output proto.Message, production bool, metadata capabilities.ResponseMetadata, attestation *capabilities.OCRAttestation) (capabilities.CapabilityResponse, error) {
	payload, err := feature.MarshalAny(output, production)
	if err != nil {
		return capabilities.CapabilityResponse{}, fmt.Errorf("failed to write the response: %w", err)
	}

	return capabilities.CapabilityResponse{Payload: payload, Metadata: metadata, OCRAttestation: attestation}, nil
}

// ForwardTriggers sends each event as its production message if the registration was one, until events closes or
// stop is closed.
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

				payload, err := feature.MarshalAny(event.Trigger, production)
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
