package trigger

import (
	"context"
	"time"

	"github.com/go-co-op/gocron/v2"
	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

type CronMonitor struct {
	metrics *Metrics
}

var _ gocron.Monitor = (*CronMonitor)(nil)

var (
	PromExecutionTimeMS = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name: "capability_trigger_cron_execution_time_ms",
			Help: "Metric representing the execution time in milliseconds, by TriggerID",
		},
		[]string{"trigger_id"},
	)
)

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
	// TBD rgd migration - need to fix histogram support in otel metrics from a loop
	PromExecutionTimeMS.WithLabelValues(name).Observe(float64(endTime.Sub(startTime).Milliseconds()))
}
