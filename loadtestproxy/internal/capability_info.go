package internal

import (
	"context"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
)

var _ capabilities.BaseCapability = (*capabilityInfo)(nil)

type capabilityInfo struct {
	info capabilities.CapabilityInfo
}

func (m *capabilityInfo) Info(ctx context.Context) (capabilities.CapabilityInfo, error) {
	return capabilities.CapabilityInfo{
		ID:             m.info.ID,
		CapabilityType: m.info.CapabilityType,
		Description:    m.info.Description,
		DON:            m.info.DON,
		IsLocal:        m.info.IsLocal,
	}, nil
}
