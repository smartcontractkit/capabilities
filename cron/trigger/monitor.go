package trigger

import (
	"context"
	"time"

	"github.com/go-co-op/gocron/v2"
	"github.com/google/uuid"
)

type CronMonitor struct {
	metrics *Metrics
}

var _ gocron.Monitor = (*CronMonitor)(nil)

func NewCronMonitor(m *Metrics) gocron.Monitor {
	return &CronMonitor{
		metrics: m,
	}
}

// Hooks into gocron after the job has finished a run. Proceeds RecordJobTiming.
func (cm *CronMonitor) IncrementJob(_ uuid.UUID, name string, _ []string, status gocron.JobStatus) {
	cm.metrics.IncTriggeredCount(context.Background(), string(status))
}

// Hooks into gocron after the job has finished a run
func (cm *CronMonitor) RecordJobTiming(startTime, endTime time.Time, _ uuid.UUID, name string, _ []string) {
}
