package environment

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewSimpleScaler(t *testing.T) {
	scaler := NewSimpleScaler(5)
	require.NotNil(t, scaler)
	
	status := scaler.Status()
	assert.Equal(t, uint32(5), status.WantRings)
	assert.Empty(t, status.Status)
}

func TestSimpleScaler_Status(t *testing.T) {
	scaler := NewSimpleScaler(3)
	
	scaler.SetRingHealth(0, true)
	scaler.SetRingHealth(1, false)
	scaler.SetRingHealth(2, true)
	
	status := scaler.Status()
	
	assert.Equal(t, uint32(3), status.WantRings)
	assert.Len(t, status.Status, 3)
	assert.True(t, status.Status[0])
	assert.False(t, status.Status[1])
	assert.True(t, status.Status[2])
}

func TestSimpleScaler_SetRingHealth(t *testing.T) {
	scaler := NewSimpleScaler(2)
	
	// Initially no health status
	status := scaler.Status()
	assert.Empty(t, status.Status)
	
	// Set health for ring 0
	scaler.SetRingHealth(0, true)
	status = scaler.Status()
	assert.Len(t, status.Status, 1)
	assert.True(t, status.Status[0])
	
	// Update health for ring 0
	scaler.SetRingHealth(0, false)
	status = scaler.Status()
	assert.False(t, status.Status[0])
}

func TestSimpleScaler_SetWantRings(t *testing.T) {
	scaler := NewSimpleScaler(3)
	
	status := scaler.Status()
	assert.Equal(t, uint32(3), status.WantRings)
	
	scaler.SetWantRings(10)
	
	status = scaler.Status()
	assert.Equal(t, uint32(10), status.WantRings)
}

func TestConstants(t *testing.T) {
	// Verify constants are set correctly
	assert.Equal(t, 10, NodesPerRing)
	assert.Equal(t, 3, F)
}

