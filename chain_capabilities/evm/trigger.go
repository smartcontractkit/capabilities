package main

import (
	"context"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
)

func (c *capability) RegisterTrigger(_ context.Context, _ capabilities.TriggerRegistrationRequest) (<-chan capabilities.TriggerResponse, error) {
	//TODO implement me
	panic("implement me")
}

func (c *capability) UnregisterTrigger(_ context.Context, _ capabilities.TriggerRegistrationRequest) error {
	//TODO implement me
	panic("implement me")
}
