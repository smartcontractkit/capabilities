package internal

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"sync"

	"github.com/smartcontractkit/capabilities/loadtestproxy/internal/pb"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/types/core"
	"github.com/smartcontractkit/chainlink-common/pkg/values"
	pb2 "github.com/smartcontractkit/chainlink-common/pkg/values/pb"
)

var _ pb.ProxyServer = (*Server)(nil)

func (m *trigger) Info(ctx context.Context) (capabilities.CapabilityInfo, error) {
	return m.info.Info(ctx)
}

type ExecutableRequest struct {
	ID      string
	capType pb.CapabilityType
	request capabilities.CapabilityRequest
}

type Server struct {
	pb.UnimplementedProxyServer
	impl               core.CapabilitiesRegistry
	triggers           map[string]*trigger    //Id->trigger
	targets            map[string]*executable //Id->trigger
	action             map[string]*executable //Id->trigger
	consensus          map[string]*executable //Id->trigger
	executableRequests chan ExecutableRequest
	lggr               logger.Logger
	mu                 sync.RWMutex
}

func NewServer(impl core.CapabilitiesRegistry, lggr logger.Logger) *Server {
	return &Server{
		impl:               impl,
		triggers:           make(map[string]*trigger),
		action:             make(map[string]*executable),
		targets:            make(map[string]*executable),
		consensus:          make(map[string]*executable),
		lggr:               lggr,
		executableRequests: make(chan ExecutableRequest, 1000),
		mu:                 sync.RWMutex{},
	}
}

func (s *Server) findExecutable(ID string, capType pb.CapabilityType) (*executable, error) {
	var t *executable
	var found bool

	s.mu.RLock()
	defer s.mu.RUnlock()

	switch capType {
	case pb.CapabilityType_Target:
		t, found = s.targets[ID]
	case pb.CapabilityType_Action:
		t, found = s.action[ID]
	case pb.CapabilityType_Consensus:
		t, found = s.consensus[ID]
	default:
		return nil, errors.New(fmt.Sprintf("capability %s not supported", capType))
	}

	if !found {
		return nil, errors.New(fmt.Sprintf("capability %s not found", ID))
	}

	return t, nil
}

func (s *Server) HookExecutables(server pb.Proxy_HookExecutablesServer) error {

	//Server will receive CapabilityResponse
	go func() {
		for {
			executeResponse, err := server.Recv()
			if err == io.EOF {
				log.Println("Server closed the stream")
				return
			}
			if err != nil {
				log.Fatalf("Error receiving message: %v", err)
			}

			t, err := s.findExecutable(executeResponse.ID, executeResponse.CapabilityType)

			if err != nil {
				s.lggr.Errorw("could not find capability", "err", err)
				continue
			}

			v, err := bytesToMap(executeResponse.Value)
			if err != nil {
				s.lggr.Errorw("cannot convert value to bytes", "err", err)
			}
			t.responseChan <- capabilities.CapabilityResponse{
				Value: v,
			}
		}
	}()

	//Client will receive CapabilityRequest
	for {
		select {
		case <-server.Context().Done():
			s.lggr.Info("client disconnected")
			return nil
		case d := <-s.executableRequests:
			s.lggr.Debugw("received execute request", "ID", d.ID, "type", d.capType, "metadata", d.request.Metadata, "input", d.request.Inputs, "config", d.request.Config)

			config, err := mapToBytes(d.request.Config)
			if err != nil {
				return err
			}
			inputs, err := mapToBytes(d.request.Inputs)
			if err != nil {
				return err
			}
			err = server.Send(&pb.ExecutableRequest{
				ID:             d.ID,
				CapabilityType: d.capType,
				RequestMetadata: &pb.Metadata{
					WorkflowID:               d.request.Metadata.WorkflowID,
					WorkflowOwner:            d.request.Metadata.WorkflowOwner,
					WorkflowExecutionID:      d.request.Metadata.WorkflowExecutionID,
					WorkflowName:             d.request.Metadata.WorkflowName,
					WorkflowDonID:            d.request.Metadata.WorkflowDonID,
					WorkflowDonConfigVersion: d.request.Metadata.WorkflowDonConfigVersion,
					ReferenceID:              d.request.Metadata.ReferenceID,
					DecodedWorkflowName:      d.request.Metadata.DecodedWorkflowName,
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

func (s *Server) RegisterToWorkflow(ctx context.Context, request *pb.RegisterToWorkflowRequest) (*emptypb.Empty, error) {
	var t capabilities.ExecutableCapability
	var err error
	switch request.CapabilityType {
	case pb.CapabilityType_Target:
		t, err = s.findTarget(ctx, request.ID)
	case pb.CapabilityType_Action:
		t, err = s.findAction(ctx, request.ID)
	case pb.CapabilityType_Consensus:
		t, err = s.findConsensus(ctx, request.ID)
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

func (s *Server) UnregisterFromWorkflow(ctx context.Context, request *pb.UnregisterFromWorkflowRequest) (*emptypb.Empty, error) {
	return nil, nil
}

func (s *Server) Execute(ctx context.Context, request *pb.ExecutableRequest) (*pb.CapabilityResponse, error) {
	e, err := s.getExecutable(ctx, request.ID, request.CapabilityType)
	if err != nil {
		return nil, err
	}

	config, err := bytesToMap(request.Config)
	if err != nil {
		return nil, err
	}
	s.lggr.Debugw("Before bytesToMap", "data", request.Inputs)

	input, err := bytesToMap(request.Inputs)
	if err != nil {
		return nil, err
	}
	s.lggr.Debugw("After bytesToMap", "data", input)

	s.lggr.Debugw("execute call", "ID", request.ID, "cap type", request.CapabilityType, "metadata", request.RequestMetadata, "config", config, "inputs", input)

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
	responseBytes, err := mapToBytes(response.Value)
	if err != nil {
		return nil, err
	}
	return &pb.CapabilityResponse{
		Value: responseBytes,
	}, nil
}

func (s *Server) RegisterTrigger(request *pb.TriggerRegistrationRequest, server pb.Proxy_RegisterTriggerServer) error {
	t, err := s.findTrigger(server.Context(), request.TriggerID)
	if err != nil {
		return err
	}

	config, err := bytesToMap(request.Config)
	if err != nil {
		return err
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
		case <-server.Context().Done():
			s.lggr.Info("client disconnected from trigger")
			return nil
		case triggerResponse := <-triggerResponsesChan:
			s.lggr.Debugw("got trigger response", "response", triggerResponse)
			b, err2 := mapToBytes(triggerResponse.Event.Outputs)
			if err2 != nil {
				s.lggr.Error(err)
				continue
			}

			s.lggr.Debug("sending trigger event")

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
				s.lggr.Errorw("failed to send trigger response", "err", err)
			}

			s.lggr.Infow("trigger event sent", "event", event)
		}

	}

	return nil
}

func (s *Server) UnregisterTrigger(ctx context.Context, request *pb.TriggerRegistrationRequest) (*emptypb.Empty, error) {
	return nil, nil
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
		infos.CapInfos = append(infos.CapInfos, &pb.CapabilityInfo{
			ID:             i.ID,
			CapabilityType: toLocalCapEnum(i.CapabilityType),
			Description:    i.Description,
			DON:            nil,
			IsLocal:        i.IsLocal,
		})
	}
	return &infos, nil
}

func (s *Server) CreateCapability(ctx context.Context, info *pb.CapabilityInfo) (*emptypb.Empty, error) {
	s.lggr.Infof("CreateCapability request %v+", info)

	switch info.CapabilityType {
	case pb.CapabilityType_Trigger:
		return s.createTrigger(ctx, info)
	case pb.CapabilityType_Target:
		return s.createTarget(ctx, info)
	case pb.CapabilityType_Action:
		return s.createAction(ctx, info)
	case pb.CapabilityType_Consensus:
		return s.createConsensus(ctx, info)
	default:
		return &emptypb.Empty{}, errors.New("capability type not supported")
	}

	return &emptypb.Empty{}, nil
}

func (s *Server) createAction(ctx context.Context, info *pb.CapabilityInfo) (*emptypb.Empty, error) {
	c := NewExecutable(info, s.executableRequests)
	err := s.impl.Add(ctx, c)
	if err != nil {
		s.lggr.Error(err)
		return &emptypb.Empty{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.action[info.ID] = c
	return &emptypb.Empty{}, nil
}

func (s *Server) createConsensus(ctx context.Context, info *pb.CapabilityInfo) (*emptypb.Empty, error) {
	c := NewExecutable(info, s.executableRequests)
	err := s.impl.Add(ctx, c)
	if err != nil {
		s.lggr.Error(err)
		return &emptypb.Empty{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.consensus[info.ID] = c
	return &emptypb.Empty{}, nil
}

func (s *Server) createTarget(ctx context.Context, info *pb.CapabilityInfo) (*emptypb.Empty, error) {
	c := NewExecutable(info, s.executableRequests)
	err := s.impl.Add(ctx, c)
	if err != nil {
		s.lggr.Error(err)
		return &emptypb.Empty{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.targets[info.ID] = c
	return &emptypb.Empty{}, nil
}

func (s *Server) createTrigger(ctx context.Context, info *pb.CapabilityInfo) (*emptypb.Empty, error) {
	c := NewTrigger(info)
	err := s.impl.Add(ctx, c)
	if err != nil {
		s.lggr.Error(err)
		return &emptypb.Empty{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.triggers[info.ID] = c
	return &emptypb.Empty{}, nil
}

func (s *Server) SendTriggerEvent(ctx context.Context, req *pb.SendTriggerEventRequest) (*emptypb.Empty, error) {
	s.lggr.Info("sending trigger event")
	s.mu.RLock()
	m, found := s.triggers[req.ID]
	s.mu.RUnlock()
	if !found {
		return &emptypb.Empty{}, errors.New("cannot find trigger")
	}
	s.lggr.Info("found trigger")

	var o pb2.Map
	err := proto.Unmarshal(req.Payload, &o)
	if err != nil {
		return &emptypb.Empty{}, err
	}

	fromProto, err := values.FromMapValueProto(&o)
	if err != nil {
		return &emptypb.Empty{}, err
	}

	s.lggr.Info("creating response")
	response := capabilities.TriggerResponse{
		Event: capabilities.TriggerEvent{
			TriggerType: m.info.ID,
			ID:          req.EventID,
			Outputs:     fromProto,
		},
		Err: nil,
	}
	s.lggr.Info("sent response")
	m.tChan <- response

	return &emptypb.Empty{}, nil
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

func mapToBytes(m *values.Map) ([]byte, error) {
	if m == nil {
		return nil, nil
	}

	pm := make(map[string]*pb2.Value)
	for k, v := range m.Underlying {
		pm[k] = values.Proto(v)
	}
	bytes, err := proto.Marshal(pb2.NewMapValue(pm))
	if err != nil {
		return nil, err
	}
	return bytes, nil
}
func bytesToMap(b []byte) (*values.Map, error) {
	var o pb2.Value
	if err := proto.Unmarshal(b, &o); err != nil {
		return nil, err
	}

	vm := values.Map{Underlying: make(map[string]values.Value)}
	for k, v := range o.GetMapValue().Fields {
		val, err := values.FromProto(v)
		if err != nil {
			return nil, err
		}
		vm.Underlying[k] = val
	}

	return &vm, nil
}

func (s *Server) findTrigger(ctx context.Context, id string) (capabilities.TriggerCapability, error) {
	t, err := s.impl.GetTrigger(ctx, id)
	if err != nil {
		return nil, err
	}
	return t, nil
}

func (s *Server) getExecutable(ctx context.Context, ID string, capType pb.CapabilityType) (capabilities.ExecutableCapability, error) {
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

func (s *Server) findTarget(ctx context.Context, id string) (capabilities.TargetCapability, error) {
	t, err := s.impl.GetTarget(ctx, id)
	if err != nil {
		return nil, err
	}
	return t, nil
}

func (s *Server) findAction(ctx context.Context, id string) (capabilities.ActionCapability, error) {
	t, err := s.impl.GetAction(ctx, id)
	if err != nil {
		return nil, err
	}
	return t, nil
}

func (s *Server) findConsensus(ctx context.Context, id string) (capabilities.ConsensusCapability, error) {
	t, err := s.impl.GetConsensus(ctx, id)
	if err != nil {
		return nil, err
	}
	return t, nil
}
