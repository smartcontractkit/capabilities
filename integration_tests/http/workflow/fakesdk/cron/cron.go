// Package cron is a minimal stand-in for github.com/smartcontractkit/cre-sdk-go/capabilities/scheduler/cron.
package cron

import (
	"time"

	"github.com/smartcontractkit/capabilities/integration_tests/http/workflow/fakesdk/cre"
)

type Config struct {
	Schedule string
}

type Payload struct {
	ScheduledExecutionTime time.Time
}

type trigger struct {
	config *Config
}

func (trigger) ID() string { return "cron-trigger@1.0.0" }

func Trigger(config *Config) cre.Trigger[*Payload] {
	return trigger{config: config}
}
