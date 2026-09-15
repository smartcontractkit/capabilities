package ui

import (
	"testing"

	"github.com/jhump/protoreflect/desc"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/pb"
)

// unaryOnlyService is a real registered service with no streaming methods.
func unaryOnlyService() protoreflect.ServiceDescriptor {
	return pb.File_capabilities_proto.Services().ByName("BaseCapability")
}

// The synthetic service names the trigger's own input, so the page draws the
// trigger's real configuration form rather than something invented here.
func TestSubscriptionServiceTakesTheTriggerInput(t *testing.T) {
	synthetic, err := subscriptionService(testService())
	require.NoError(t, err)
	require.NotNil(t, synthetic)

	assert.Equal(t,
		testService().FullName()+protoreflect.FullName(SubscriptionsSuffix),
		synthetic.FullName())

	// Executable.Execute is the streaming method, so it is the one offered.
	md := synthetic.Methods().ByName("Execute")
	require.NotNil(t, md, "the streaming method should be offered as a unary one")

	assert.False(t, md.IsStreamingServer(), "the whole point is that it does not stream")
	assert.False(t, md.IsStreamingClient())

	// The same descriptor the real method names, not a copy: a copy would resolve
	// to a different Go type and the form would be filling in the wrong message.
	real := testService().Methods().ByName("Execute")
	assert.Equal(t, real.Input().FullName(), md.Input().FullName())
	assert.Equal(t, protoreflect.FullName("google.protobuf.Empty"), md.Output().FullName())
}

// Non-streaming methods are already callable, so they are not offered twice.
func TestSubscriptionServiceSkipsUnaryMethods(t *testing.T) {
	synthetic, err := subscriptionService(testService())
	require.NoError(t, err)
	require.NotNil(t, synthetic)

	assert.Equal(t, 1, synthetic.Methods().Len())
	assert.Nil(t, synthetic.Methods().ByName("RegisterToWorkflow"))
}

// A service with nothing streaming gets no synthetic service at all, rather than
// an empty one the page would list with no methods under it.
func TestSubscriptionServiceIsNilWithoutTriggers(t *testing.T) {
	synthetic, err := subscriptionService(unaryOnlyService())
	require.NoError(t, err)
	assert.Nil(t, synthetic)
}

// The form generator renders from jhump descriptors, so a synthetic file has to
// survive being wrapped as one.
func TestSubscriptionServiceCanBeWrapped(t *testing.T) {
	synthetic, err := subscriptionService(testService())
	require.NoError(t, err)
	require.NotNil(t, synthetic)

	wrapped, err := desc.WrapFile(synthetic.ParentFile())
	require.NoError(t, err)

	service := wrapped.FindService(string(synthetic.FullName()))
	require.NotNil(t, service)

	method := service.FindMethodByName("Execute")
	require.NotNil(t, method)
	// The wrapped input has to resolve, or the form has no fields to draw.
	require.NotNil(t, method.GetInputType())
	assert.NotEmpty(t, method.GetInputType().GetFields())
}
