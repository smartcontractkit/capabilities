package plugin

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"time"

	"github.com/buraksezer/consistent"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/consensus/requests"
	"github.com/smartcontractkit/libocr/offchainreporting2plus/ocr3types"
	"github.com/smartcontractkit/libocr/offchainreporting2plus/types"
	"github.com/smartcontractkit/libocr/quorumhelper"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/smartcontractkit/capabilities/ring/internal/environment"
	"github.com/smartcontractkit/capabilities/ring/internal/pb"
	"github.com/smartcontractkit/capabilities/ring/internal/request"
	"github.com/smartcontractkit/capabilities/ring/internal/rings"
)

type ringPlugin struct {
	prior          *pb.Outcome
	store          *requests.Store[*request.Request]
	scaler         environment.Scaler
	requestTimeout time.Duration
	timeToSync     time.Duration
	f              int
	hashConfig     consistent.Config
}

func NewRingPlugin(
	store *requests.Store[*request.Request],
	scaler environment.Scaler,
	requestTimeout, timeToSync time.Duration,
	f int,
	hashConfig consistent.Config) ocr3types.ReportingPlugin[*pb.Outcome] {
	return &ringPlugin{
		prior:          &pb.Outcome{Routes: map[string]*pb.RouteResponse{}},
		scaler:         scaler,
		store:          store,
		requestTimeout: requestTimeout,
		timeToSync:     timeToSync,
		f:              f,
		hashConfig:     hashConfig,
	}
}

func (r *ringPlugin) Query(_ context.Context, _ ocr3types.OutcomeContext) (types.Query, error) {
	return nil, nil
}

func (r *ringPlugin) Observation(_ context.Context, outctx ocr3types.OutcomeContext, _ types.Query) (types.Observation, error) {
	observation := &pb.Observation{Status: r.scaler.Status(), Now: timestamppb.Now()}

	outstanding, err := r.store.FirstN(100000)
	if err != nil {
		return nil, err
	}

	hashes := make([]string, len(outstanding))
	for i, req := range outstanding {
		hashes[i] = req.Hash()
	}

	prior := &pb.Outcome{}
	if outctx.PreviousOutcome == nil {
		observation.Hashes = hashes
	} else if err := proto.Unmarshal(outctx.PreviousOutcome, prior); err != nil {
		return nil, err
	} else {
		unresolved := make([]string, 0, len(hashes))
		// Only include hashes that don't have routes yet
		for _, hash := range hashes {
			if _, ok := prior.Routes[hash]; !ok {
				unresolved = append(unresolved, hash)
			}
		}
		observation.Hashes = unresolved
	}

	r.prior = prior
	return proto.MarshalOptions{Deterministic: true}.Marshal(observation)
}

func (r *ringPlugin) ValidateObservation(_ context.Context, _ ocr3types.OutcomeContext, _ types.Query, ao types.AttributedObservation) error {
	obs := &pb.Observation{}
	if err := proto.Unmarshal(ao.Observation, obs); err != nil {
		return err
	}

	if obs.Now == nil {
		return fmt.Errorf("observation is missing now")
	} else if obs.Status == nil {
		return fmt.Errorf("observation is missing status")
	}

	return nil
}

func (r *ringPlugin) ObservationQuorum(_ context.Context, _ ocr3types.OutcomeContext, _ types.Query, aos []types.AttributedObservation) (quorumReached bool, err error) {
	return quorumhelper.ObservationCountReachesObservationQuorum(quorumhelper.QuorumTwoFPlusOne, environment.NodesPerRing, environment.F, aos), nil
}

func (r *ringPlugin) Outcome(_ context.Context, outctx ocr3types.OutcomeContext, _ types.Query, aos []types.AttributedObservation) (ocr3types.Outcome, error) {
	prior := &pb.Outcome{}
	if outctx.PreviousOutcome == nil {
		prior.Routes = map[string]*pb.RouteResponse{}
		prior.State = &pb.RoutingState{Id: 1, State: &pb.RoutingState_RoutableRings{RoutableRings: 1}}
	} else if err := proto.Unmarshal(outctx.PreviousOutcome, prior); err != nil {
		return nil, err
	}

	ds := newDonStats(len(aos), prior.Routes)
	for _, ao := range aos {
		obs := &pb.Observation{}
		if err := proto.Unmarshal(ao.Observation, obs); err != nil {
			return nil, err
		}
		ds.addObservation(obs)
	}

	nextState, table, err := ds.calculateNextState(r, prior)
	if err != nil {
		return nil, err
	}

	currentRoutes := map[string]*pb.RouteResponse{}
	for key, route := range prior.Routes {
		if route.ExpiresAt.AsTime().After(ds.now()) {
			currentRoutes[key] = route
		}
	}

	for hash := range ds.hashes {
		ringId := rings.Route(table, []byte(hash))
		currentRoutes[hash] = &pb.RouteResponse{
			Ring:      uint32(ringId),
			ExpiresAt: timestamppb.New(ds.now().Add(r.requestTimeout)),
		}
	}

	result := &pb.Outcome{
		State:  nextState,
		Routes: currentRoutes,
	}

	return proto.MarshalOptions{Deterministic: true}.Marshal(result)
}

func (r *ringPlugin) Reports(_ context.Context, _ uint64, outcome ocr3types.Outcome) ([]ocr3types.ReportPlus[*pb.Outcome], error) {
	info := &pb.Outcome{}
	if err := proto.Unmarshal(outcome, info); err != nil {
		return nil, err
	}

	return []ocr3types.ReportPlus[*pb.Outcome]{{
		ReportWithInfo: ocr3types.ReportWithInfo[*pb.Outcome]{
			Report: types.Report(outcome),
			Info:   info,
		},
	}}, nil
}

func (r *ringPlugin) ShouldAcceptAttestedReport(_ context.Context, _ uint64, _ ocr3types.ReportWithInfo[*pb.Outcome]) (bool, error) {
	return true, nil
}

func (r *ringPlugin) ShouldTransmitAcceptedReport(_ context.Context, _ uint64, _ ocr3types.ReportWithInfo[*pb.Outcome]) (bool, error) {
	return true, nil
}

func (r *ringPlugin) Close() error {
	return nil
}

type health struct {
	healthy   int
	unhealthy int
}

type donStats struct {
	wantRings   []uint32
	ringHealths map[uint32]*health
	nows        []time.Time
	hashes      map[string]bool
	priorRoutes map[string]*pb.RouteResponse
}

func newDonStats(numObs int, priorRoutes map[string]*pb.RouteResponse) *donStats {
	return &donStats{
		wantRings:   make([]uint32, 0, numObs),
		ringHealths: map[uint32]*health{},
		nows:        make([]time.Time, 0, numObs),
		hashes:      map[string]bool{},
		priorRoutes: priorRoutes,
	}
}

func (ds *donStats) addObservation(obs *pb.Observation) {
	ds.nows = append(ds.nows, obs.Now.AsTime())

	if obs.Status == nil {
		return
	}

	ds.wantRings = append(ds.wantRings, obs.Status.WantRings)

	for nodeId, alive := range obs.Status.Status {
		ringHealth := ds.ringHealths[nodeId]
		if ringHealth == nil {
			ringHealth = &health{}
			ds.ringHealths[nodeId] = ringHealth
		}

		if alive {
			ringHealth.healthy++
		} else {
			ringHealth.unhealthy++
		}
	}

	for _, hash := range obs.Hashes {
		if _, ok := ds.priorRoutes[hash]; !ok {
			ds.hashes[hash] = true
		}
	}
}

func (ds *donStats) now() time.Time {
	slices.SortFunc(ds.nows, time.Time.Compare)
	return ds.nows[len(ds.nows)/2]
}

func (ds *donStats) targetRings() uint32 {
	slices.Sort(ds.wantRings)
	return ds.wantRings[len(ds.wantRings)/2]
}

func (ds *donStats) tableFromHealth(f, target int, hashConfig consistent.Config) (*consistent.Consistent, bool) {
	members := make([]consistent.Member, 0, target)
	allHealthy := true

	for i := 0; i < target; i++ {
		ringHealth := ds.ringHealths[uint32(i)]
		if ringHealth == nil {
			allHealthy = false
		} else if ringHealth.healthy >= ringHealth.unhealthy && ringHealth.healthy > f {
			members = append(members, rings.RingMember(strconv.FormatInt(int64(i), 10)))
		} else {
			allHealthy = false
		}
	}

	if len(members) == 0 {
		fmt.Printf("Ring health %+v\n", ds.ringHealths)
		fmt.Println("No ring members found...")
		return rings.StableTable(1, hashConfig), false
	}

	return consistent.New(members, hashConfig), allHealthy
}

func (ds *donStats) calculateNextState(r *ringPlugin, prior *pb.Outcome) (*pb.RoutingState, *consistent.Consistent, error) {
	now := ds.now()

	switch ps := prior.State.State.(type) {
	case *pb.RoutingState_RoutableRings:
		targetRings := ds.targetRings()
		routingTable := rings.StableTable(int(ps.RoutableRings), r.hashConfig)

		if ps.RoutableRings == targetRings {
			return prior.State, routingTable, nil
		}

		return &pb.RoutingState{
			Id: prior.State.Id + 1,
			State: &pb.RoutingState_Transition{
				Transition: &pb.Transition{
					WantRings:        targetRings,
					LastStableCount:  ps.RoutableRings,
					ChangesSafeAfter: timestamppb.New(now.Add(r.timeToSync)),
				},
			},
		}, routingTable, nil

	case *pb.RoutingState_Transition:
		if now.Before(ps.Transition.ChangesSafeAfter.AsTime()) {
			return prior.State, rings.StableTable(int(ps.Transition.LastStableCount), r.hashConfig), nil
		}

		table, allHealthy := ds.tableFromHealth(r.f, int(ps.Transition.WantRings), r.hashConfig)

		if allHealthy {
			return &pb.RoutingState{
				Id: prior.State.Id + 1,
				State: &pb.RoutingState_RoutableRings{
					RoutableRings: ps.Transition.WantRings,
				},
			}, table, nil
		}

		return prior.State, table, nil

	default:
		return nil, nil, fmt.Errorf("unknown prior outcome type %T", ps)
	}
}
