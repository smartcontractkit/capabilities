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

	"google.golang.org/grpc"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/services"
	"github.com/smartcontractkit/chainlink-common/pkg/types/core"
	"github.com/smartcontractkit/chainlink-protos/cre/go/values"

	"github.com/smartcontractkit/capabilities/mock/internal/pb"
	"github.com/smartcontractkit/capabilities/mock/utils"
)

type MockRegistry struct {
	pb.UnimplementedMockCapabilityServer
	Triggers             map[string]*Trigger
	Executables          map[string]*Executable
	executableRequests   chan ExecutableRequest
	mu                   sync.RWMutex
	stopCh               services.StopChan
	grpcServer           *grpc.Server
	lggr                 logger.Logger
	capabilitiesRegistry core.CapabilitiesRegistry
}

func (m *MockRegistry) RemoveCapability(ctx context.Context, info *pb.RemoveCapabilityRequest) (*emptypb.Empty, error) {
	_, capType, err := m.findCapabilityByID(ctx, info.ID)
	if err != nil {
		return nil, err
	}

	switch capType {
	case capabilities.CapabilityTypeTrigger:
		m.Triggers[info.ID] = nil
		delete(m.Triggers, info.ID)
	case capabilities.CapabilityTypeAction, capabilities.CapabilityTypeConsensus, capabilities.CapabilityTypeTarget:
		m.Executables[info.ID] = nil
		delete(m.Executables, info.ID)
	default:
		return &emptypb.Empty{}, errors.New("capability type not supported")
	}

	return &emptypb.Empty{}, nil
}

func (m *MockRegistry) GetTriggerSubscribers(ctx context.Context, request *pb.GetTriggerSubscribersRequest) (*pb.GetTriggerSubscribersResponse, error) {
	// Get the trigger from the registry
	m.mu.RLock()
	trigger, found := m.Triggers[request.ID]
	m.mu.RUnlock()

	if !found {
		return nil, fmt.Errorf("trigger with ID %s not found", request.ID)
	}

	// Lock the trigger to safely access its subscribers
	trigger.mu.RLock()
	defer trigger.mu.RUnlock()

	workflowIDs := make([]string, 0)
	for _, sub := range trigger.Subscribers {
		workflowIDs = append(workflowIDs, sub.WorkflowID)
	}

	return &pb.GetTriggerSubscribersResponse{
		WorkflowIDs: workflowIDs,
	}, nil
}

func NewMockRegistry(lggr logger.Logger, capRegistry core.CapabilitiesRegistry) *MockRegistry {
	return &MockRegistry{
		Triggers:             make(map[string]*Trigger),
		Executables:          make(map[string]*Executable),
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
	case pb.CapabilityType_Target, pb.CapabilityType_Action, pb.CapabilityType_Consensus:
		return m.createExecutable(ctx, info)
	default:
		return &emptypb.Empty{}, errors.New("capability type not supported")
	}
}

func (m *MockRegistry) SendTriggerEvent(ctx context.Context, request *pb.SendTriggerEventRequest) (*emptypb.Empty, error) {
	m.mu.RLock()
	t, found := m.Triggers[request.TriggerID]
	m.mu.RUnlock()
	if !found {
		return &emptypb.Empty{}, errors.New("cannot find trigger")
	}

	if len(t.Subscribers) == 0 {
		m.lggr.Warnf("Did NOT SEND trigger event, trigger %s has 0 subscribers", t.ID)
		return &emptypb.Empty{}, nil
	}

	outputs, err := utils.BytesToMap(request.Outputs)
	if err != nil {
		return nil, err
	}

	m.lggr.Infow("Sending trigger event through mock trigger", "triggerID", request.TriggerID, "id", request.ID, "triggerType", request.TriggerType)

	m.lggr.Debugf("Mock trigger %s has %d subscribers", t.ID, len(t.Subscribers))

	ocrEvent := &capabilities.OCRTriggerEvent{}
	sigs := make([]capabilities.OCRAttributedOnchainSignature, 0)
	if request.OCREvent != nil {
		for _, s := range request.OCREvent.Sigs {
			sigs = append(sigs, capabilities.OCRAttributedOnchainSignature{
				Signature: s.Signature,
				Signer:    s.Signer,
			})
		}

		ocrEvent.Sigs = sigs
		ocrEvent.ConfigDigest = request.OCREvent.ConfigDigest
		ocrEvent.Report = request.OCREvent.Report
		ocrEvent.SeqNr = request.OCREvent.SeqNr
	}

	for triggerID, sub := range t.Subscribers {
		event := capabilities.TriggerEvent{
			TriggerType: request.TriggerType,
			ID:          request.ID,
			Outputs:     outputs,
			Payload:     request.Payload,
			OCREvent:    ocrEvent,
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
		TriggerID: request.RegistrationTriggerID,
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
		Config:  config,
		Payload: request.Payload,
		Method:  request.Method,
	})

	if err != nil {
		m.lggr.Error("could not register trigger", "err", err)
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
}

func (m *MockRegistry) UnregisterTrigger(ctx context.Context, request *pb.TriggerRegistrationRequest) (*emptypb.Empty, error) {
	t, err := m.capabilitiesRegistry.GetTrigger(ctx, request.TriggerID)

	if err != nil {
		return &emptypb.Empty{}, err
	}

	config, err := utils.BytesToMap(request.Config)
	if err != nil {
		return nil, err
	}

	return &emptypb.Empty{}, t.UnregisterTrigger(ctx, capabilities.TriggerRegistrationRequest{
		TriggerID: request.RegistrationTriggerID,
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
		Config:  config,
		Payload: request.Payload,
		Method:  request.Method,
	})
}

func (m *MockRegistry) HookExecutables(server pb.MockCapability_HookExecutablesServer) error {
	// MockServer will receive CapabilityResponse
	go m.incomingLoop(server)

	// Client will receive CapabilityRequest
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
	case pb.CapabilityType_Target, pb.CapabilityType_Action, pb.CapabilityType_Consensus:
		t, err = m.capabilitiesRegistry.GetExecutable(ctx, request.ID)
	default:
		return &emptypb.Empty{}, errors.New("capability type not supported")
	}

	if err != nil {
		return &emptypb.Empty{}, err
	}

	config, err := utils.BytesToMap(request.Config)
	if err != nil {
		return nil, err
	}

	return &emptypb.Empty{}, t.RegisterToWorkflow(ctx, capabilities.RegisterToWorkflowRequest{
		Metadata: capabilities.RegistrationMetadata{},
		Config:   config,
	})
}

func (m *MockRegistry) UnregisterFromWorkflow(ctx context.Context, request *pb.UnregisterFromWorkflowRequest) (*emptypb.Empty, error) {
	return nil, nil
}

func (m *MockRegistry) Execute(ctx context.Context, request *pb.ExecutableRequest) (*pb.CapabilityResponse, error) {
	e, err := m.capabilitiesRegistry.GetExecutable(ctx, request.ID)
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

	spendLimits := make([]capabilities.SpendLimit, 0)
	for _, s := range request.RequestMetadata.SpendLimit {
		spendLimits = append(spendLimits, capabilities.SpendLimit{
			SpendType: capabilities.CapabilitySpendType(s.SpendType),
			Limit:     s.Limit,
		})
	}

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
			SpendLimits:              spendLimits,
		},
		Config:        config,
		Inputs:        input,
		Payload:       request.Payload,
		ConfigPayload: request.ConfigPayload,
		Method:        request.Method,
		CapabilityId:  request.CapabilityId,
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

func (m *MockRegistry) createExecutable(ctx context.Context, info *pb.CapabilityInfo) (*emptypb.Empty, error) {
	c := NewExecutable(info, m.executableRequests)
	err := m.capabilitiesRegistry.Add(ctx, c)
	if err != nil {
		m.lggr.Error(err)
		return &emptypb.Empty{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Executables[info.ID] = c

	m.lggr.Infow("Created mock executable", "id", info.ID, "type", info.CapabilityType)
	return &emptypb.Empty{}, nil
}

func (m *MockRegistry) createTrigger(ctx context.Context, info *pb.CapabilityInfo) (*emptypb.Empty, error) {
	c := NewTrigger(info, m.lggr)
	err := m.capabilitiesRegistry.Add(ctx, c)
	if err != nil {
		m.lggr.Error(err)
		return &emptypb.Empty{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Triggers[info.ID] = c

	m.lggr.Infow("Created mock trigger", "id", info.ID)
	return &emptypb.Empty{}, nil
}

func (m *MockRegistry) GetTrigger(ctx context.Context, id string) (capabilities.TriggerCapability, error) {
	return m.capabilitiesRegistry.GetTrigger(ctx, id)
}
func (m *MockRegistry) GetExecutable(ctx context.Context, id string) (capabilities.Executable, error) {
	return m.capabilitiesRegistry.GetExecutable(ctx, id)
}

// FindCapabilityByID searches for a capability by ID in both Triggers and Executable maps
func (m *MockRegistry) findCapabilityByID(ctx context.Context, ID string) (capabilities.BaseCapability, capabilities.CapabilityType, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if trigger, found := m.Triggers[ID]; found {
		return trigger, trigger.CapabilityType, nil
	}

	if executable, found := m.Executables[ID]; found {
		return executable, executable.CapabilityType, nil
	}

	return nil, "", fmt.Errorf("capability with ID %s not found", ID)
}

func (m *MockRegistry) incomingLoop(server pb.MockCapability_HookExecutablesServer) {
	for {
		executeResponse, err := server.Recv()
		if errors.Is(err, io.EOF) {
			m.lggr.Warnf("Execute hook closed")
			return
		}
		if err != nil {
			m.lggr.Errorf("Error receiving message: %v", err)
			return
		}

		t, err := m.findLocalMockExecutable(executeResponse.ID)

		if err != nil {
			m.lggr.Errorw("Could not find capability", "err", err, "id", executeResponse.ID, "type", utils.ToCapabilityEnum(executeResponse.CapabilityType))
			continue
		}

		v, err := utils.BytesToMap(executeResponse.Value)
		if err != nil {
			m.lggr.Errorw("cannot convert value to bytes", "err", err)
		}
		t.ResponseChan <- capabilities.CapabilityResponse{
			Value: v,
		}
	}
}

func (m *MockRegistry) findLocalMockExecutable(ID string) (*Executable, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	t, found := m.Executables[ID]
	if !found {
		return nil, fmt.Errorf("capability %s not found", ID)
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
