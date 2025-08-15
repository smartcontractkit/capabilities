package decryptertest

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/smartcontractkit/capabilities/integration_tests/utils"
	commoncap "github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/services/servicetest"
	"github.com/smartcontractkit/chainlink/v2/core/capabilities/integration_tests/framework"
	"github.com/smartcontractkit/chainlink/v2/core/logger"
	"github.com/smartcontractkit/chainlink/v2/core/services/gateway/handlers/capabilities"
)

func setupTestDon(ctx context.Context, t *testing.T, lggr logger.Logger,
	workflowDonInfo framework.DonConfiguration, triggerSink framework.TriggerFactory, targetSink framework.TargetFactory, decrypterPath string) (workflowDon *framework.DON) {
	// Use a workflow DON context so that a workflow Key is spawned.
	donContext := framework.CreateDonContextWithWorkflowRegistry(ctx, t, func(ctx context.Context, messageID string, req capabilities.Request) ([]byte, error) { return nil, nil }, utils.NoopComputeFetcherFactory{})

	workflowDon = createTestWorkflowDon(ctx, t, lggr, workflowDonInfo, donContext, triggerSink, targetSink)

	workflowDon.AddStandardCapability("decrypter-capabilities", decrypterPath, `""`)

	workflowDon.Initialise()

	servicetest.Run(t, workflowDon)

	donContext.WaitForCapabilitiesToBeExposed(t, workflowDon)

	workflowJob := GetWorkflowJob(t, workflowName, workflowOwnerID)

	err := workflowDon.AddJob(ctx, &workflowJob)
	require.NoError(t, err)

	return workflowDon
}

func createTestWorkflowDon(ctx context.Context, t *testing.T, lggr logger.Logger,
	workflowDonInfo framework.DonConfiguration,
	donContext framework.DonContext,
	triggerFactory framework.TriggerFactory,
	targetFactory framework.TargetFactory) *framework.DON {
	workflowDon := framework.NewDON(ctx, t, lggr, workflowDonInfo,
		[]commoncap.DON{},
		donContext, true, 1*time.Second)

	workflowDon.AddTriggerCapability(triggerFactory)
	workflowDon.AddOCR3NonStandardCapability()
	workflowDon.AddTargetCapability(targetFactory)
	return workflowDon
}
