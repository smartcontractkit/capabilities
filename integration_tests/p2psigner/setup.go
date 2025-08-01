package signertest

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	commoncap "github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/actions/vault"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/actions/vault/mock"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/services"
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
	workflowDon.AddTargetCapability(NewVaultAction())

	return workflowDon
}

var (
	_ capabilities.ExecutableCapability = &mock.Vault{}
)

type VaultActionFactory struct {
	services.StateMachine
	actionID   string
	targetName string
	version    string

	actions []mock.Vault
}

func NewVaultAction() *VaultActionFactory {
	return &VaultActionFactory{
		actionID:   vault.CapabilityID,
		targetName: strings.Split(vault.CapabilityID, "@")[0],
		version:    strings.Split(vault.CapabilityID, "@")[1],
	}
}

func (ts *VaultActionFactory) GetTargetVersion() string {
	return ts.version
}

func (ts *VaultActionFactory) GetTargetName() string {
	return ts.targetName
}

func (ts *VaultActionFactory) GetTargetID() string {
	return ts.actionID
}

func (ts *VaultActionFactory) Start(ctx context.Context) error {
	return ts.StartOnce("VaultActionFactoryService", func() error {
		return nil
	})
}

func (ts *VaultActionFactory) Close() error {
	return ts.StopOnce("VaultActionFactoryService", func() error {
		return nil
	})
}

func (ts *VaultActionFactory) CreateNewTarget(t *testing.T) capabilities.ExecutableCapability {
	target := mock.Vault{
		Fn: func(ctx context.Context, req *vault.GetSecretsRequest) (*vault.GetSecretsResponse, error) {
			if req == nil {
				return nil, errors.New("request cannot be nil")
			}
			// Mock response for testing purposes
			return &vault.GetSecretsResponse{
				Responses: []*vault.SecretResponse{{}},
			}, nil
		},
	}
	ts.actions = append(ts.actions, target)
	return &target
}
