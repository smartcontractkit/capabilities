package trigger

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestServiceConfig_RequestCacheTTL_Defaults(t *testing.T) {
	t.Parallel()
	cfg := ServiceConfig{}
	appliedCfg := applyDefaults(cfg)
	require.Equal(t, uint32(defaultRequestCacheTTL), appliedCfg.RequestCacheTTL)
}

func TestServiceConfig_RequestCacheTTL_CustomValue(t *testing.T) {
	t.Parallel()
	customTTL := uint32(3600)
	cfg := ServiceConfig{
		RequestCacheTTL: customTTL,
	}
	appliedCfg := applyDefaults(cfg)
	require.Equal(t, customTTL, appliedCfg.RequestCacheTTL)
}

func TestServiceConfig_RequestCacheTTL_JSONMarshalling(t *testing.T) {
	t.Parallel()
	cfg := ServiceConfig{
		RequestCacheTTL:              7200, // 2 hours
		MetadataBatchSize:            25,
		SendChannelBufferSize:        500,
		MaxAuthorizedKeysPerWorkflow: 50,
	}
	jsonData, err := json.Marshal(cfg)
	require.NoError(t, err)
	var unmarshalledCfg ServiceConfig
	err = json.Unmarshal(jsonData, &unmarshalledCfg)
	require.NoError(t, err)

	require.Equal(t, cfg.RequestCacheTTL, unmarshalledCfg.RequestCacheTTL)
	require.Equal(t, cfg.MetadataBatchSize, unmarshalledCfg.MetadataBatchSize)
	require.Equal(t, cfg.SendChannelBufferSize, unmarshalledCfg.SendChannelBufferSize)
	require.Equal(t, cfg.MaxAuthorizedKeysPerWorkflow, unmarshalledCfg.MaxAuthorizedKeysPerWorkflow)
}

func TestServiceConfig_AllDefaults(t *testing.T) {
	t.Parallel()
	cfg := ServiceConfig{}
	appliedCfg := applyDefaults(cfg)

	require.Equal(t, uint16(defaultMetadataBatchSize), appliedCfg.MetadataBatchSize)
	require.Equal(t, uint16(defaultSendChannelBufferSize), appliedCfg.SendChannelBufferSize)
	require.Equal(t, uint16(defaultMaxAuthorizedKeysPerWorkflow), appliedCfg.MaxAuthorizedKeysPerWorkflow)
	require.Equal(t, uint32(defaultRequestCacheTTL), appliedCfg.RequestCacheTTL)

}
