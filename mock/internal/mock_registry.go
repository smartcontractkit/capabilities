package internal

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"sync"
	"time"

	"github.com/smartcontractkit/capabilities/mock/internal/pb"
	"github.com/smartcontractkit/capabilities/mock/utils"
	"google.golang.org/grpc"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/services"
	"github.com/smartcontractkit/chainlink-common/pkg/types/core"
	"github.com/smartcontractkit/chainlink-common/pkg/values"
)

type MockRegistry struct {
	pb.UnimplementedMockCapabilityServer
	Triggers             map[string]*Trigger
	Targets              map[string]*Executable
	Action               map[string]*Executable
	Consensus            map[string]*Executable
	executableRequests   chan ExecutableRequest
	mu                   sync.RWMutex
	stopCh               services.StopChan
	grpcServer           *grpc.Server
	lggr                 logger.Logger
	capabilitiesRegistry core.CapabilitiesRegistry
}

func NewMockRegistry(lggr logger.Logger, capRegistry core.CapabilitiesRegistry) *MockRegistry {
	return &MockRegistry{
		Triggers:             make(map[string]*Trigger),
		Targets:              make(map[string]*Executable),
		Action:               make(map[string]*Executable),
		Consensus:            make(map[string]*Executable),
		executableRequests:   make(chan ExecutableRequest),
		mu:                   sync.RWMutex{},
		stopCh:               make(services.StopChan),
		grpcServer:           nil,
		lggr:                 lggr,
		capabilitiesRegistry: capRegistry,
	}
}

func (m *MockRegistry) List(ctx context.Context, request *pb.ListRequest) (*pb.ListResponse, error) {
	m.lggr.Info("Got List request")
	caps, err := m.capabilitiesRegistry.List(ctx)
	if err != nil {
		return nil, err
	}
	infos := pb.ListResponse{}
	for _, c := range caps {
		i, err2 := c.Info(ctx)
		if err2 != nil {
			return nil, err2
		}
		infos.CapInfos = append(infos.CapInfos, &pb.CapabilityInfo{
			ID:             i.ID,
			CapabilityType: utils.ToMockServerEnum(i.CapabilityType),
			Description:    i.Description,
			DON:            nil,
			IsLocal:        i.IsLocal,
		})
	}
	return &infos, nil
}

func (m *MockRegistry) CreateCapability(ctx context.Context, info *pb.CapabilityInfo) (*emptypb.Empty, error) {
	m.lggr.Infow("Creating mock capability", "id", info.ID, "type", info.CapabilityType)

	switch info.CapabilityType {
	case pb.CapabilityType_Trigger:
		return m.createTrigger(ctx, info)
	case pb.CapabilityType_Target:
		return m.createTarget(ctx, info)
	case pb.CapabilityType_Action:
		return m.createAction(ctx, info)
	case pb.CapabilityType_Consensus:
		return m.createConsensus(ctx, info)
	default:
		return &emptypb.Empty{}, errors.New("capability type not supported")
	}

	return &emptypb.Empty{}, nil
}

func (m *MockRegistry) SendTriggerEvent(ctx context.Context, request *pb.SendTriggerEventRequest) (*emptypb.Empty, error) {
	m.mu.RLock()
	t, found := m.Triggers[request.ID]
	m.mu.RUnlock()
	if !found {
		return &emptypb.Empty{}, errors.New("cannot find trigger")
	}

	if len(t.Subscribers) == 0 {
		m.lggr.Warnf("Did NOT SEND trigger event, trigger %s has 0 subscribers", t.ID)
		return &emptypb.Empty{}, nil
	}

	outputs, err := utils.BytesToMap(request.Payload)
	if err != nil {
		return nil, err
	}

	m.lggr.Infow("Sending trigger event through mock trigger", "id", request.ID, "a", request.EventID, "payload", outputs)

	m.lggr.Debugf("Mock trigger %s has %d subscribers", t.ID, len(t.Subscribers))

	for triggerID, sub := range t.Subscribers {
		event := capabilities.TriggerEvent{
			TriggerType: t.ID,
			ID:          request.EventID,
			Outputs:     outputs,
		}

		sub.Ch <- capabilities.TriggerResponse{
			Event: event,
			Err:   nil,
		}
		m.lggr.Infow("Sent mock trigger event", "triggerID", triggerID, "outputs", outputs)
	}

	return &emptypb.Empty{}, nil
}

func (m *MockRegistry) RegisterTrigger(request *pb.TriggerRegistrationRequest, server pb.MockCapability_RegisterTriggerServer) error {
	t, err := m.capabilitiesRegistry.GetTrigger(server.Context(), request.TriggerID)
	if err != nil {
		return err
	}
	config := &values.Map{}
	if request.Config != nil {
		config, err = utils.BytesToMap(request.Config)
		if err != nil {
			return err
		}
	}

	triggerResponsesChan, err := t.RegisterTrigger(server.Context(), capabilities.TriggerRegistrationRequest{
		TriggerID: request.TriggerID,
		Metadata: capabilities.RequestMetadata{
			WorkflowID:               request.Metadata.WorkflowID,
			WorkflowOwner:            request.Metadata.WorkflowOwner,
			WorkflowExecutionID:      request.Metadata.WorkflowExecutionID,
			WorkflowName:             request.Metadata.WorkflowName,
			WorkflowDonID:            request.Metadata.WorkflowDonID,
			WorkflowDonConfigVersion: request.Metadata.WorkflowDonConfigVersion,
			ReferenceID:              request.Metadata.ReferenceID,
			DecodedWorkflowName:      request.Metadata.DecodedWorkflowName,
		},
		Config: config,
	})

	if err != nil {
		return err
	}

	for {
		select {
		case <-m.stopCh:
			return nil
		case <-server.Context().Done():
			m.lggr.Info("client disconnected from trigger")
			return nil
		case triggerResponse, ok := <-triggerResponsesChan:
			if !ok {
				m.lggr.Warn("triggerResponsesChan closed, ending stream")
				return nil
			}
			m.lggr.Infow("got trigger response", "response", triggerResponse)
			b, err2 := utils.MapToBytes(triggerResponse.Event.Outputs)
			if err2 != nil {
				m.lggr.Error(err2)
				continue
			}

			m.lggr.Infow("sending trigger event")

			errString := ""
			if triggerResponse.Err != nil {
				errString = triggerResponse.Err.Error()
			}

			event := &pb.TriggerResponse{
				TriggerEvent: &pb.TriggerEvent{
					TriggerType: triggerResponse.Event.TriggerType,
					ID:          triggerResponse.Event.ID,
					Outputs:     b,
				},
				Error: errString,
			}

			if err = server.Send(event); err != nil {
				m.lggr.Errorw("failed to send trigger response", "err", err)
			}

			m.lggr.Infow("trigger event sent", "event", event)
		}
	}

	return nil
}

func (m *MockRegistry) UnregisterTrigger(ctx context.Context, request *pb.TriggerRegistrationRequest) (*emptypb.Empty, error) {
	return nil, nil
}

func (m *MockRegistry) HookExecutables(server pb.MockCapability_HookExecutablesServer) error {
	//MockServer will receive CapabilityResponse
	go m.incomingLoop(server)

	//Client will receive CapabilityRequest
	for {
		select {
		case <-m.stopCh:
			return nil
		case <-server.Context().Done():
			m.lggr.Info("client disconnected")
			return nil
		case d := <-m.executableRequests:
			m.lggr.Debugw("received execute request", "ID", d.ID, "type", d.CapType, "metadata", d.Request.Metadata, "input", d.Request.Inputs, "config", d.Request.Config)

			config, err := utils.MapToBytes(d.Request.Config)
			if err != nil {
				return err
			}
			inputs, err := utils.MapToBytes(d.Request.Inputs)
			if err != nil {
				return err
			}
			err = server.Send(&pb.ExecutableRequest{
				ID:             d.ID,
				CapabilityType: d.CapType,
				RequestMetadata: &pb.Metadata{
					WorkflowID:               d.Request.Metadata.WorkflowID,
					WorkflowOwner:            d.Request.Metadata.WorkflowOwner,
					WorkflowExecutionID:      d.Request.Metadata.WorkflowExecutionID,
					WorkflowName:             d.Request.Metadata.WorkflowName,
					WorkflowDonID:            d.Request.Metadata.WorkflowDonID,
					WorkflowDonConfigVersion: d.Request.Metadata.WorkflowDonConfigVersion,
					ReferenceID:              d.Request.Metadata.ReferenceID,
					DecodedWorkflowName:      d.Request.Metadata.DecodedWorkflowName,
				},
				Config: config,
				Inputs: inputs,
			})
			if err != nil {
				return err
			}
		}
	}
}

func (m *MockRegistry) RegisterToWorkflow(ctx context.Context, request *pb.RegisterToWorkflowRequest) (*emptypb.Empty, error) {
	var t capabilities.ExecutableCapability
	var err error
	switch request.CapabilityType {
	case pb.CapabilityType_Target:
		t, err = m.findTarget(ctx, request.ID)
	case pb.CapabilityType_Action:
		t, err = m.findAction(ctx, request.ID)
	case pb.CapabilityType_Consensus:
		t, err = m.findConsensus(ctx, request.ID)
	default:
		return &emptypb.Empty{}, errors.New("capability type not supported")
	}

	if err != nil {
		return &emptypb.Empty{}, err
	}

	return &emptypb.Empty{}, t.RegisterToWorkflow(ctx, capabilities.RegisterToWorkflowRequest{
		Metadata: capabilities.RegistrationMetadata{},
		Config:   nil,
	})
}

func (m *MockRegistry) UnregisterFromWorkflow(ctx context.Context, request *pb.UnregisterFromWorkflowRequest) (*emptypb.Empty, error) {
	return nil, nil
}

func (m *MockRegistry) Execute(ctx context.Context, request *pb.ExecutableRequest) (*pb.CapabilityResponse, error) {
	e, err := m.getExecutable(ctx, request.ID, request.CapabilityType)
	if err != nil {
		return nil, err
	}

	config, err := utils.BytesToMap(request.Config)
	if err != nil {
		return nil, err
	}

	input, err := utils.BytesToMap(request.Inputs)
	if err != nil {
		return nil, err
	}

	m.lggr.Debugw("execute call", "ID", request.ID, "cap type", request.CapabilityType, "metadata", request.RequestMetadata, "config", config, "inputs", input)

	response, err := e.Execute(ctx, capabilities.CapabilityRequest{
		Metadata: capabilities.RequestMetadata{
			WorkflowID:               request.RequestMetadata.WorkflowID,
			WorkflowOwner:            request.RequestMetadata.WorkflowOwner,
			WorkflowExecutionID:      request.RequestMetadata.WorkflowExecutionID,
			WorkflowName:             request.RequestMetadata.WorkflowName,
			WorkflowDonID:            request.RequestMetadata.WorkflowDonID,
			WorkflowDonConfigVersion: request.RequestMetadata.WorkflowDonConfigVersion,
			ReferenceID:              request.RequestMetadata.ReferenceID,
			DecodedWorkflowName:      request.RequestMetadata.DecodedWorkflowName,
		},
		Config: config,
		Inputs: input,
	})
	if err != nil {
		return nil, err
	}
	responseBytes, err := utils.MapToBytes(response.Value)
	if err != nil {
		return nil, err
	}
	return &pb.CapabilityResponse{
		Value: responseBytes,
	}, nil
}

func (s *MockRegistry) createAction(ctx context.Context, info *pb.CapabilityInfo) (*emptypb.Empty, error) {
	c := NewExecutable(info, s.executableRequests)
	err := s.capabilitiesRegistry.Add(ctx, c)
	if err != nil {
		s.lggr.Error(err)
		return &emptypb.Empty{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Action[info.ID] = c

	s.lggr.Infow("Created mock action", "id", info.ID)
	return &emptypb.Empty{}, nil
}

func (s *MockRegistry) createConsensus(ctx context.Context, info *pb.CapabilityInfo) (*emptypb.Empty, error) {
	c := NewExecutable(info, s.executableRequests)
	err := s.capabilitiesRegistry.Add(ctx, c)
	if err != nil {
		s.lggr.Error(err)
		return &emptypb.Empty{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Consensus[info.ID] = c

	s.lggr.Infow("Created mock consensus", "id", info.ID)
	return &emptypb.Empty{}, nil
}

func (s *MockRegistry) createTarget(ctx context.Context, info *pb.CapabilityInfo) (*emptypb.Empty, error) {
	c := NewExecutable(info, s.executableRequests)
	err := s.capabilitiesRegistry.Add(ctx, c)
	if err != nil {
		s.lggr.Error(err)
		return &emptypb.Empty{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Targets[info.ID] = c

	s.lggr.Infow("Created mock target", "id", info.ID)
	return &emptypb.Empty{}, nil
}

func (s *MockRegistry) createTrigger(ctx context.Context, info *pb.CapabilityInfo) (*emptypb.Empty, error) {
	c := NewTrigger(info, s.lggr)
	err := s.capabilitiesRegistry.Add(ctx, c)
	if err != nil {
		s.lggr.Error(err)
		return &emptypb.Empty{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Triggers[info.ID] = c

	s.lggr.Infow("Created mock trigger", "id", info.ID)
	return &emptypb.Empty{}, nil
}

func (s *MockRegistry) GetTrigger(ctx context.Context, id string) (capabilities.TriggerCapability, error) {
	t, ok := s.Triggers[id]
	if !ok {
		return nil, fmt.Errorf("cannot find trigger %s", id)
	}
	return t, nil
}
func (s *MockRegistry) GetTarget(ctx context.Context, id string) (capabilities.TargetCapability, error) {
	t, ok := s.Targets[id]
	if !ok {
		return nil, fmt.Errorf("cannot find target %s", id)
	}
	return t, nil
}

func (s *MockRegistry) findTrigger(ctx context.Context, id string) (capabilities.TriggerCapability, error) {
	t, err := s.capabilitiesRegistry.GetTrigger(ctx, id)
	if err != nil {
		return nil, err
	}
	return t, nil
}

func (s *MockRegistry) getExecutable(ctx context.Context, ID string, capType pb.CapabilityType) (capabilities.ExecutableCapability, error) {
	switch capType {
	case pb.CapabilityType_Target:
		return s.findTarget(ctx, ID)
	case pb.CapabilityType_Action:
		return s.findAction(ctx, ID)
	case pb.CapabilityType_Consensus:
		return s.findConsensus(ctx, ID)
	default:
		return nil, errors.New("capability type not supported")
	}
}

func (s *MockRegistry) findTarget(ctx context.Context, id string) (capabilities.TargetCapability, error) {
	t, err := s.capabilitiesRegistry.GetTarget(ctx, id)
	if err != nil {
		return nil, err
	}
	return t, nil
}

func (s *MockRegistry) findAction(ctx context.Context, id string) (capabilities.ActionCapability, error) {
	t, err := s.capabilitiesRegistry.GetAction(ctx, id)
	if err != nil {
		return nil, err
	}
	return t, nil
}

func (s *MockRegistry) findConsensus(ctx context.Context, id string) (capabilities.ConsensusCapability, error) {
	t, err := s.capabilitiesRegistry.GetConsensus(ctx, id)
	if err != nil {
		return nil, err
	}
	return t, nil
}

func (s *MockRegistry) incomingLoop(server pb.MockCapability_HookExecutablesServer) {
	for {
		executeResponse, err := server.Recv()
		if err == io.EOF {
			s.lggr.Warnf("Execute hook closed")
			return
		}
		if err != nil {
			s.lggr.Errorf("Error receiving message: %v", err)
			return
		}

		t, err := s.findExecutable(executeResponse.ID, executeResponse.CapabilityType)

		if err != nil {
			s.lggr.Errorw("Could not find capability", "err", err, "id", executeResponse.ID, "type", utils.ToCapabilityEnum(executeResponse.CapabilityType))
			continue
		}

		v, err := utils.BytesToMap(executeResponse.Value)
		if err != nil {
			s.lggr.Errorw("cannot convert value to bytes", "err", err)
		}
		t.ResponseChan <- capabilities.CapabilityResponse{
			Value: v,
		}
	}
}

func (s *MockRegistry) findExecutable(ID string, capType pb.CapabilityType) (*Executable, error) {
	var t *Executable
	var found bool

	s.mu.RLock()
	defer s.mu.RUnlock()

	switch capType {
	case pb.CapabilityType_Target:
		t, found = s.Targets[ID]
	case pb.CapabilityType_Action:
		t, found = s.Action[ID]
	case pb.CapabilityType_Consensus:
		t, found = s.Consensus[ID]
	default:
		return nil, errors.New(fmt.Sprintf("capability %s not supported", capType))
	}

	if !found {
		return nil, errors.New(fmt.Sprintf("capability %s not found", ID))
	}

	return t, nil
}

func (m *MockRegistry) Start(port int) error {
	m.lggr.Info("Starting mock capability server")
	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}
	m.grpcServer = grpc.NewServer([]grpc.ServerOption{
		grpc.KeepaliveParams(keepalive.ServerParameters{
			Time:    30 * time.Second, // Send keepalive ping every 30s
			Timeout: 10 * time.Second, // Wait 10s for ping response
		}),
	}...)
	pb.RegisterMockCapabilityServer(m.grpcServer, m)
	if err2 := m.grpcServer.Serve(lis); err2 != nil {
		m.lggr.Error("gRPC server failed to serve: ", err2)
	}
	return nil
}
