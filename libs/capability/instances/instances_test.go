package instances

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	caperrors "github.com/smartcontractkit/chainlink-common/pkg/capabilities/errors"
	valuespb "github.com/smartcontractkit/chainlink-protos/cre/go/values/pb"
)

// fake is an untyped capability: it answers with its input plus reply, or fails with err, and streams its
// events, each "id" or "id=payload".
type fake struct {
	capabilities.Executable
	capabilities.TriggerExecutable
	reply  string
	err    caperrors.Error
	events []string
}

func (f fake) Execute(_ context.Context, request capabilities.CapabilityRequest) (capabilities.CapabilityResponse, error) {
	if f.err != nil {
		return capabilities.CapabilityResponse{}, f.err
	}
	in := &valuespb.Value{}
	if err := request.Payload.UnmarshalTo(in); err != nil {
		return capabilities.CapabilityResponse{}, err
	}
	payload, err := anypb.New(valuespb.NewStringValue(in.GetStringValue() + f.reply))
	return capabilities.CapabilityResponse{Payload: payload}, err
}

func (f fake) RegisterTrigger(ctx context.Context, _ capabilities.TriggerRegistrationRequest) (<-chan capabilities.TriggerResponse, error) {
	if f.err != nil {
		return nil, f.err
	}
	ch := make(chan capabilities.TriggerResponse)
	go func() {
		defer close(ch)
		for _, e := range f.events {
			id, value, found := strings.Cut(e, "=")
			if !found {
				value = id
			}
			payload, err := anypb.New(valuespb.NewStringValue(value))
			select {
			case ch <- capabilities.TriggerResponse{Event: capabilities.TriggerEvent{ID: id, Payload: payload}, Err: err}:
			case <-ctx.Done():
				return
			}
		}
	}()
	return ch, nil
}

func (f fake) UnregisterTrigger(context.Context, capabilities.TriggerRegistrationRequest) error {
	if f.err != nil {
		return f.err
	}
	return nil
}

var (
	in       = valuespb.NewStringValue("in-")
	metadata = capabilities.RequestMetadata{}
)

func failed(msg string) caperrors.Error {
	return caperrors.NewPublicUserError(errors.New(msg), caperrors.InvalidArgument)
}

func TestExecuteAgreeing(t *testing.T) {
	got, err := TypedExecute[*valuespb.Value](t.Context(), []fake{{reply: "a"}, {reply: "a"}}, "Do", metadata, in)
	require.NoError(t, err)
	assert.Equal(t, "in-a", got.Response.GetStringValue())
}

func TestExecuteDisagreeing(t *testing.T) {
	_, err := TypedExecute[*valuespb.Value](t.Context(), []fake{{reply: "a"}, {reply: "b"}, {err: failed("no")}}, "Do", metadata, in)

	var mismatch *MismatchError[capabilities.CapabilityResponse]
	require.ErrorAs(t, err, &mismatch)
	require.Len(t, mismatch.Results, 3)
	got := &valuespb.Value{}
	require.NoError(t, mismatch.Results[1].Value.Payload.UnmarshalTo(got))
	assert.Equal(t, "in-b", got.GetStringValue())
	assert.Error(t, mismatch.Results[2].Err)
	assert.Contains(t, err.Error(), "instance 1: ")
	assert.Contains(t, err.Error(), "instance 2: error")
}

func TestExecuteAgreeingOnAnError(t *testing.T) {
	_, err := TypedExecute[*valuespb.Value](t.Context(), []fake{{err: failed("no")}, {err: failed("no")}}, "Do", metadata, in)

	var capErr caperrors.Error
	require.ErrorAs(t, err, &capErr, "an error they agree on is returned as it is")
	var mismatch *MismatchError[capabilities.CapabilityResponse]
	assert.False(t, errors.As(err, &mismatch))
}

func TestRegisterTrigger(t *testing.T) {
	ch, err := TypedRegisterTrigger[*valuespb.Value](t.Context(),
		[]fake{{events: []string{"1", "2", "3=a"}}, {events: []string{"2", "1", "3=b"}}}, "Trigger", "t", metadata, in)
	require.NoError(t, err)

	var agreed []string
	for range 2 {
		e := <-ch
		require.Nil(t, e.Mismatch)
		assert.Equal(t, e.Id, e.Trigger.GetStringValue())
		agreed = append(agreed, e.Id)
	}
	assert.ElementsMatch(t, []string{"1", "2"}, agreed, "matched by ID, whatever order each instance sends them in")

	e := <-ch
	require.NotNil(t, e.Mismatch, "the same ID with different payloads")

	_, open := <-ch
	assert.False(t, open, "closes when an instance's channel does")
}

func TestRegisterTriggerReportsEventsNotEveryInstanceSent(t *testing.T) {
	ch, err := TypedRegisterTrigger[*valuespb.Value](t.Context(),
		[]fake{{events: []string{"1", "2"}}, {events: []string{"1"}}}, "Trigger", "t", metadata, in)
	require.NoError(t, err)

	require.Nil(t, (<-ch).Mismatch)

	e := <-ch
	require.NotNil(t, e.Mismatch)
	assert.Contains(t, e.Mismatch.Error(), "no event")

	_, open := <-ch
	assert.False(t, open)
}

func TestMatchEventsDuplicates(t *testing.T) {
	for _, tt := range []struct {
		name     string
		in       []instanceResponse
		mismatch bool
	}{
		{name: "the same again is ignored", in: responses(t, "0:a", "0:a", "1:a")},
		{name: "a different one before every instance sent it is a mismatch", in: responses(t, "0:a", "0:b", "1:a"), mismatch: true},
		// Each ID is sent once, with what every instance sent first, so a later copy cannot change it.
		{name: "a different one after every instance sent it is ignored", in: responses(t, "0:a", "1:a", "0:b")},
	} {
		t.Run(tt.name, func(t *testing.T) {
			in := make(chan instanceResponse, len(tt.in))
			for _, r := range tt.in {
				in <- r
			}
			close(in)

			out := make(chan Event, len(tt.in))
			matchEvents(t.Context(), in, 2, out)
			close(out)

			e := <-out
			if tt.mismatch {
				require.NotNil(t, e.Mismatch)
				assert.Contains(t, e.Mismatch.Error(), "twice, differently")
			} else {
				require.Nil(t, e.Mismatch)
			}
			_, open := <-out
			assert.False(t, open, "one event per ID")
		})
	}
}

// responses are "instance:value" events, all with ID "1".
func responses(t *testing.T, events ...string) []instanceResponse {
	var rs []instanceResponse
	for _, e := range events {
		instance, value, _ := strings.Cut(e, ":")
		rs = append(rs, instanceResponse{instance: int(instance[0] - '0'), response: triggerResponse(t, "1", value)})
	}
	return rs
}

func triggerResponse(t *testing.T, id, value string) capabilities.TriggerResponse {
	payload, err := anypb.New(valuespb.NewStringValue(value))
	require.NoError(t, err)
	return capabilities.TriggerResponse{Event: capabilities.TriggerEvent{ID: id, Payload: payload}}
}

func TestRegisterTriggerErrors(t *testing.T) {
	_, err := TypedRegisterTrigger[*valuespb.Value](t.Context(), []fake{{err: failed("no")}, {err: failed("no")}}, "Trigger", "t", metadata, in)
	var capErr caperrors.Error
	require.ErrorAs(t, err, &capErr, "an error they agree on is returned as it is")

	_, err = TypedRegisterTrigger[*valuespb.Value](t.Context(), []fake{{}, {err: failed("no")}}, "Trigger", "t", metadata, in)
	var mismatch *MismatchError[<-chan capabilities.TriggerResponse]
	require.ErrorAs(t, err, &mismatch)
}

func TestUnregisterTrigger(t *testing.T) {
	require.NoError(t, TypedUnregisterTrigger(t.Context(), []fake{{}, {}}, "Trigger", "t", metadata, in))

	var mismatch *MismatchError[struct{}]
	require.ErrorAs(t, TypedUnregisterTrigger(t.Context(), []fake{{}, {err: failed("no")}}, "Trigger", "t", metadata, in), &mismatch)
}

// errFake sends one event per entry: a response error, or a payload of the wrong type.
type errFake struct {
	capabilities.TriggerExecutable
	responseErr bool
}

func (f errFake) RegisterTrigger(context.Context, capabilities.TriggerRegistrationRequest) (<-chan capabilities.TriggerResponse, error) {
	ch := make(chan capabilities.TriggerResponse, 1)
	if f.responseErr {
		ch <- capabilities.TriggerResponse{Event: capabilities.TriggerEvent{ID: "2"}, Err: errors.New("no event")}
	} else {
		payload, _ := anypb.New(&valuespb.Map{})
		ch <- capabilities.TriggerResponse{Event: capabilities.TriggerEvent{ID: "1", Payload: payload}}
	}
	close(ch)
	return ch, nil
}

func (errFake) UnregisterTrigger(context.Context, capabilities.TriggerRegistrationRequest) error {
	return nil
}

func TestRegisterTriggerEventErrors(t *testing.T) {
	ch, err := TypedRegisterTrigger[*valuespb.Value](t.Context(), []errFake{{responseErr: true}, {responseErr: true}}, "Trigger", "t", metadata, in)
	require.NoError(t, err)
	e := <-ch
	require.Nil(t, e.Mismatch)
	assert.EqualError(t, e.Err, "no event", "an error they agree on is the event's")
	assert.Equal(t, "2", e.Id, "and keeps its ID")

	ch, err = TypedRegisterTrigger[*valuespb.Value](t.Context(), []errFake{{}, {}}, "Trigger", "t", metadata, in)
	require.NoError(t, err)
	e = <-ch
	assert.Error(t, e.Err, "a payload of another type cannot be unpacked")
}

// chanFake registers by handing back its channel.
type chanFake struct {
	capabilities.TriggerExecutable
	ch chan capabilities.TriggerResponse
}

func (f chanFake) RegisterTrigger(context.Context, capabilities.TriggerRegistrationRequest) (<-chan capabilities.TriggerResponse, error) {
	return f.ch, nil
}

func (chanFake) UnregisterTrigger(context.Context, capabilities.TriggerRegistrationRequest) error {
	return nil
}

func TestRegisterTriggerWaitsForEveryInstance(t *testing.T) {
	idle, closed := make(chan capabilities.TriggerResponse), make(chan capabilities.TriggerResponse)
	close(closed)

	ctx, cancel := context.WithCancel(t.Context())
	events, err := RegisterTrigger(ctx, []chanFake{{ch: idle}, {ch: closed}}, capabilities.TriggerRegistrationRequest{})
	require.NoError(t, err)

	select {
	case e, open := <-events:
		t.Fatalf("an idle instance may still send, so nothing should arrive yet: %v %v", e, open)
	case <-time.After(50 * time.Millisecond):
	}

	cancel()
	select {
	case _, open := <-events:
		assert.False(t, open, "closes once ctx is done")
	case <-time.After(5 * time.Second):
		t.Fatal("still open after ctx was cancelled")
	}
}

func TestNoInstances(t *testing.T) {
	_, err := Execute(t.Context(), []fake{}, capabilities.CapabilityRequest{})
	require.ErrorContains(t, err, "no instances")
	_, err = RegisterTrigger(t.Context(), []fake(nil), capabilities.TriggerRegistrationRequest{})
	require.ErrorContains(t, err, "no instances")
	require.ErrorContains(t, UnregisterTrigger(t.Context(), []fake{}, capabilities.TriggerRegistrationRequest{}), "no instances")
}

// mutatingFake changes the request it is given, as a capability validating its input in place may.
type mutatingFake struct{ capabilities.Executable }

func (mutatingFake) Execute(_ context.Context, request capabilities.CapabilityRequest) (capabilities.CapabilityResponse, error) {
	request.Payload.TypeUrl = "changed"
	request.Payload.Value = append(request.Payload.Value, 0)
	return capabilities.CapabilityResponse{}, nil
}

func TestExecuteGivesEachInstanceItsOwnRequest(t *testing.T) {
	payload, err := anypb.New(in)
	require.NoError(t, err)
	want := proto.Clone(payload)

	_, err = Execute(t.Context(), []mutatingFake{{}, {}, {}}, capabilities.CapabilityRequest{Payload: payload})
	require.NoError(t, err)
	assert.True(t, proto.Equal(want, payload), "the caller's request is untouched")
}

func TestUnregisterTriggerDistinguishesNilFromAnEmptyError(t *testing.T) {
	var mismatch *MismatchError[struct{}]
	err := UnregisterTrigger(t.Context(), []emptyErrFake{{}, {err: errors.New("")}}, capabilities.TriggerRegistrationRequest{})
	require.ErrorAs(t, err, &mismatch)
	assert.Contains(t, err.Error(), `error "", instance 0: error <nil>`)
}

type emptyErrFake struct {
	capabilities.TriggerExecutable
	err error
}

func (f emptyErrFake) UnregisterTrigger(context.Context, capabilities.TriggerRegistrationRequest) error {
	return f.err
}

func TestSamePayloadComparesMessagesNotBytes(t *testing.T) {
	a, b := mapEntry(t, "x"), mapEntry(t, "y")
	xy := &anypb.Any{TypeUrl: "type.googleapis.com/values.v1.Map", Value: append(slices.Clone(a), b...)}
	yx := &anypb.Any{TypeUrl: "type.googleapis.com/values.v1.Map", Value: append(slices.Clone(b), a...)}
	require.False(t, bytes.Equal(xy.Value, yx.Value), "the encodings differ")

	assert.True(t, samePayload(xy, yx))
	assert.False(t, samePayload(xy, &anypb.Any{TypeUrl: xy.TypeUrl, Value: a}))
}

// mapEntry is the encoding of a values Map holding only key, so entries can be concatenated in any order.
func mapEntry(t *testing.T, key string) []byte {
	b, err := proto.Marshal(&valuespb.Map{Fields: map[string]*valuespb.Value{key: valuespb.NewStringValue(key)}})
	require.NoError(t, err)
	return b
}

func TestCloneGivesEachRequestItsOwnSpendLimits(t *testing.T) {
	limits := []capabilities.SpendLimit{{SpendType: "a"}}
	clone := cloneTriggerRequest(capabilities.TriggerRegistrationRequest{Metadata: capabilities.RequestMetadata{SpendLimits: limits}})
	clone.Metadata.SpendLimits[0].SpendType = "b"
	assert.Equal(t, capabilities.CapabilitySpendType("a"), limits[0].SpendType)

	executeClone := cloneCapabilityRequest(capabilities.CapabilityRequest{Metadata: capabilities.RequestMetadata{SpendLimits: limits}})
	executeClone.Metadata.SpendLimits[0].SpendType = "b"
	assert.Equal(t, capabilities.CapabilitySpendType("a"), limits[0].SpendType)
}

// registrationFake registers or fails, and records whether it was unregistered.
type registrationFake struct {
	capabilities.TriggerExecutable
	err          error
	unregistered *atomic.Bool
}

func (f registrationFake) RegisterTrigger(context.Context, capabilities.TriggerRegistrationRequest) (<-chan capabilities.TriggerResponse, error) {
	if f.err != nil {
		return nil, f.err
	}
	return make(chan capabilities.TriggerResponse), nil
}

func (f registrationFake) UnregisterTrigger(context.Context, capabilities.TriggerRegistrationRequest) error {
	f.unregistered.Store(true)
	return nil
}

func TestRegisterTriggerRollsBackWhenRegistrationsDisagree(t *testing.T) {
	registered, failed := &atomic.Bool{}, &atomic.Bool{}
	_, err := RegisterTrigger(t.Context(),
		[]registrationFake{{unregistered: registered}, {err: errors.New("no"), unregistered: failed}}, capabilities.TriggerRegistrationRequest{})

	var mismatch *MismatchError[<-chan capabilities.TriggerResponse]
	require.ErrorAs(t, err, &mismatch)
	assert.True(t, registered.Load(), "the instance that registered is unregistered again")
	assert.False(t, failed.Load(), "the one that failed had nothing to undo")
}
