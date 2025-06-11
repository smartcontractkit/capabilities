package utils

import (
	"encoding/json"
	"testing"
)

type CronConfig struct {
	FastestScheduleIntervalSeconds int `json:"fastestScheduleIntervalSeconds"`
}

func GetCronConfig(t *testing.T, fastestIntervalSeconds int) string {
	config := CronConfig{
		FastestScheduleIntervalSeconds: fastestIntervalSeconds,
	}

	jsonConfig, err := json.Marshal(config)
	if err != nil {
		t.Fatalf("failed to marshal config: %v", err)
	}
	return "'" + string(jsonConfig) + "'"
}
