package signertest

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	commoncap "github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/services/servicetest"
	"github.com/smartcontractkit/chainlink/v2/core/capabilities/integration_tests/framework"
)

const defaultTickInterval = 12 * time.Second

func setupTestDon(ctx context.Context, t *testing.T, lggr logger.Logger,
	workflowDonInfo framework.DonConfiguration, triggerSink framework.TriggerFactory, targetSink framework.TargetFactory, signerPath string) (workflowDon *framework.DON) {
	donContext := framework.CreateDonContext(ctx, t)

	workflowDon = createTestWorkflowDon(ctx, t, lggr, workflowDonInfo, donContext, triggerSink, targetSink)

	workflowDon.AddStandardCapability("p2psigner-capabilities", signerPath, `""`)

	workflowDon.Initialise()

	servicetest.Run(t, workflowDon)

	donContext.WaitForCapabilitiesToBeExposed(t, workflowDon)

	workflowJob := GetWorkflowJob(t, workflowName, workflowOwnerID)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		time.Sleep(defaultTickInterval)
		err := workflowDon.AddJob(ctx, &workflowJob)
		require.NoError(t, err)
	}()
	wg.Wait()
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
