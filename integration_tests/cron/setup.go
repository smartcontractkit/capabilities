package crontest

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	commoncap "github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink/v2/core/capabilities/integration_tests/framework"
	"github.com/smartcontractkit/chainlink/v2/core/logger"
)

func setupCronTestDon(ctx context.Context, t *testing.T, lggr logger.SugaredLogger,
	workflowDonInfo framework.DonConfiguration,
	cronSchedule string, targetSink framework.TargetFactory, cronPath string) (workflowDon *framework.DON) {
	donContext := framework.CreateDonContext(ctx, t)

	workflowDon = createCronTestWorkflowDon(ctx, t, lggr, workflowDonInfo, donContext, targetSink)

	workflowDon.AddStandardCapability("cron-capabilities", cronPath, "")

	workflowDon.Initialise()
	workflowDon.Start(ctx, t)

	workflowJob := GetWorkflowJobCron(t, workflowName, workflowOwnerID, cronSchedule)

	err := workflowDon.AddJob(ctx, &workflowJob)
	require.NoError(t, err)

	return workflowDon
}

func createCronTestWorkflowDon(ctx context.Context, t *testing.T, lggr logger.SugaredLogger,
	workflowDonInfo framework.DonConfiguration,
	donContext framework.DonContext,
	targetFactory framework.TargetFactory) *framework.DON {
	workflowDon := framework.NewDON(ctx, t, lggr, workflowDonInfo,
		[]commoncap.DON{},
		donContext, true)

	workflowDon.AddTargetCapability(targetFactory)
	workflowDon.AddOCR3NonStandardCapability()
	return workflowDon
}
