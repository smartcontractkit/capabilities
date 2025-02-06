package internal

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/smartcontractkit/capabilities/loadtestproxy/internal/pb"
	"google.golang.org/protobuf/proto"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/types/core"
	"github.com/smartcontractkit/chainlink-common/pkg/values"
	pb2 "github.com/smartcontractkit/chainlink-common/pkg/values/pb"
)

var _ pb.ProxyServer = (*Server)(nil)

var _ capabilities.BaseCapability = (*mockInfo)(nil)
var _ capabilities.TriggerCapability = (*mockTrigger)(nil)

func (m *mockTrigger) Info(ctx context.Context) (capabilities.CapabilityInfo, error) {
	return m.info.Info(ctx)
}

// TODO @george-dorin: rename
type mockInfo struct {
	info capabilities.CapabilityInfo
}

func (m *mockInfo) Info(ctx context.Context) (capabilities.CapabilityInfo, error) {
	return capabilities.CapabilityInfo{
		ID:             m.info.ID,
		CapabilityType: m.info.CapabilityType,
		Description:    m.info.Description,
		DON:            m.info.DON,
		IsLocal:        m.info.IsLocal,
	}, nil
}

var _ capabilities.TriggerExecutable = (*mockTrigger)(nil)

// TODO @george-dorin: rename
type mockTrigger struct {
	info  *mockInfo
	tChan chan capabilities.TriggerResponse
}

func NewMockTrigger(info *pb.CapabilityInfo) *mockTrigger {
	return &mockTrigger{
		info: &mockInfo{info: capabilities.CapabilityInfo{
			ID:             info.ID,
			CapabilityType: toRemoteCapEnum(info.CapabilityType),
			Description:    info.Description,
			DON:            nil,
			IsLocal:        info.IsLocal,
		}},
		tChan: make(chan capabilities.TriggerResponse, 1000),
	}
}

func (m *mockTrigger) RegisterTrigger(ctx context.Context, request capabilities.TriggerRegistrationRequest) (<-chan capabilities.TriggerResponse, error) {
	//TODO @george-dorin: every subscriber should have it's onw chan and we should save the config
	return m.tChan, nil
}

func (m *mockTrigger) UnregisterTrigger(ctx context.Context, request capabilities.TriggerRegistrationRequest) error {
	//TODO implement me
	panic("implement me")
}

type Server struct {
	pb.UnimplementedProxyServer
	impl         core.CapabilitiesRegistry
	triggerMocks map[string]*mockTrigger
	proxyIdMap   map[string]string
	lggr         logger.Logger
}

func NewMockServer(impl core.CapabilitiesRegistry, lggr logger.Logger) *Server {
	return &Server{
		impl:         impl,
		triggerMocks: make(map[string]*mockTrigger),
		proxyIdMap:   make(map[string]string),
		lggr:         lggr,
	}
}

func (s *Server) List(ctx context.Context, request *pb.ListRequest) (*pb.ListResponse, error) {
	s.lggr.Info("Got List request")
	caps, err := s.impl.List(ctx)
	if err != nil {
		return nil, err
	}
	infos := pb.ListResponse{}
	for _, c := range caps {
		i, err2 := c.Info(ctx)
		if err2 != nil {
			return nil, err2
		}
		pID := s.getOrSetProxyID(i.ID)
		infos.CapInfos = append(infos.CapInfos, &pb.CapabilityInfo{
			ProxyID:        pID,
			ID:             i.ID,
			CapabilityType: toLocalCapEnum(i.CapabilityType),
			Description:    i.Description,
			DON:            nil,
			IsLocal:        i.IsLocal,
		})
	}
	return &infos, nil
}

func (s *Server) CreateTrigger(ctx context.Context, info *pb.CapabilityInfo) (*pb.CreateTriggerResponse, error) {
	s.lggr.Info("Got CreateTrigger request")
	m := NewMockTrigger(info)
	pID := uuid.NewString()
	s.triggerMocks[pID] = m
	s.lggr.Infof("Creating Trigger with pID %s", pID)

	err := s.impl.Add(ctx, m)
	if err != nil {
		s.lggr.Error(err)
		return nil, err
	}
	s.lggr.Infof("Created Trigger with pID %s", pID)
	return &pb.CreateTriggerResponse{
		ProxyID: pID,
	}, nil
}

func (s *Server) SendTrigger(ctx context.Context, req *pb.SendTriggerRequest) (*pb.SendTriggerResponse, error) {
	m, found := s.triggerMocks[req.ProxyID]
	if !found {
		return nil, errors.New("cannot find trigger")
	}

	var o pb2.Map
	err := proto.Unmarshal(req.Payload, &o)
	if err != nil {
		return nil, err
	}

	fromProto, err := values.FromMapValueProto(&o)
	if err != nil {
		return nil, err
	}

	response := capabilities.TriggerResponse{
		Event: capabilities.TriggerEvent{
			TriggerType: m.info.info.ID,
			ID:          req.EventID,
			Outputs:     fromProto,
		},
		Err: nil,
	}
	m.tChan <- response

	return nil, nil
}

func (s *Server) getOrSetProxyID(id string) string {
	_, found := s.proxyIdMap[id]
	if !found {
		s.proxyIdMap[id] = uuid.New().String()
	}

	return s.proxyIdMap[id]
}

func toLocalCapEnum(c capabilities.CapabilityType) pb.CapabilityType {
	switch c {
	case capabilities.CapabilityTypeTrigger:
		return pb.CapabilityType_Trigger
	case capabilities.CapabilityTypeAction:
		return pb.CapabilityType_Action
	case capabilities.CapabilityTypeConsensus:
		return pb.CapabilityType_Consensus
	case capabilities.CapabilityTypeTarget:
		return pb.CapabilityType_Target
	default:
		return pb.CapabilityType_Unknown
	}
}

func toRemoteCapEnum(c pb.CapabilityType) capabilities.CapabilityType {
	switch c {
	case pb.CapabilityType_Trigger:
		return capabilities.CapabilityTypeTrigger
	case pb.CapabilityType_Action:
		return capabilities.CapabilityTypeAction
	case pb.CapabilityType_Consensus:
		return capabilities.CapabilityTypeConsensus
	case pb.CapabilityType_Target:
		return capabilities.CapabilityTypeTarget
	default:
		return capabilities.CapabilityTypeUnknown
	}
}
