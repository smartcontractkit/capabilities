package simulated

import (
	"context"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smartcontractkit/chainlink-evm/gethwrappers/keystone/generated/forwarder"
	creevm "github.com/smartcontractkit/chainlink-evm/pkg/cre/evm"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/standalone"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/types/core"

	"github.com/smartcontractkit/capabilities/chain_capabilities/evm/chain"
)

// chainDependency hands back one already-started chain, which is what the client
// dependency does for an embedded run told of none.
type chainDependency struct{ chain *creevm.SimulatedChain }

var _ standalone.BootstrapDependency[*creevm.SimulatedChain] = (*chainDependency)(nil)

func (d *chainDependency) Namespace() string                           { return "" }
func (d *chainDependency) Config() any                                 { return nil }
func (d *chainDependency) Dependencies() []standalone.BootstrapCommand { return nil }

func (d *chainDependency) ForEmbedding(int, int) standalone.BootstrapDependency[*creevm.SimulatedChain] {
	return d
}

func (d *chainDependency) Get(context.Context, standalone.CommonConfig) (*creevm.SimulatedChain, error) {
	return d.chain, nil
}

func simulatedChain(t *testing.T) *creevm.SimulatedChain {
	t.Helper()

	chain, err := creevm.StartSimulated(t.Context(), logger.Test(t), creevm.SimulatedConfig{BlockTime: 50 * time.Millisecond})
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, chain.Close()) })
	return chain
}

// keystores are the instances' own, derived the way an embedded run derives them.
func keystores(instance int) (core.Keystore, error) { return chain.DeterministicKeystore(instance) }

// TestDeployment covers what a run of four gets: every instance's account funded,
// and a forwarder that knows them as its DON - which is what has to be true
// before a report written by this run can land.
func TestDeployment(t *testing.T) {
	const instances = 4

	sim := simulatedChain(t)
	dep := Dependency(logger.Test(t), &chainDependency{chain: sim}, keystores).ForEmbedding(0, instances)

	deployment, err := dep.Get(t.Context(), standalone.CommonConfig{})
	require.NoError(t, err)
	require.NotNil(t, deployment)

	t.Run("the accounts the instances send from are funded", func(t *testing.T) {
		require.Len(t, deployment.Accounts, instances)

		for i, account := range deployment.Accounts {
			expected, err := chain.DeterministicAddress(i)
			require.NoError(t, err)
			assert.Equal(t, expected, account, "the account funded must be the one instance %d signs as", i)

			balance, err := sim.Backend().BalanceAt(t.Context(), account, nil)
			require.NoError(t, err)
			assert.Positive(t, balance.Sign(), "instance %d cannot send with an empty account", i)
		}
	})

	t.Run("the forwarder knows this run as its DON", func(t *testing.T) {
		require.True(t, deployment.Configured)

		contract, err := forwarder.NewKeystoneForwarderFilterer(deployment.Forwarder, sim.Backend())
		require.NoError(t, err)

		// Read back from what the contract emitted, since it exposes no getter for it.
		configs, err := contract.FilterConfigSet(&bind.FilterOpts{Context: t.Context()}, []uint32{Defaults.DonID}, nil)
		require.NoError(t, err)
		t.Cleanup(func() { assert.NoError(t, configs.Close()) })

		require.True(t, configs.Next(), "the forwarder was never told who this DON is")

		wanted, err := signers(instances)
		require.NoError(t, err)
		assert.Equal(t, wanted, configs.Event.Signers, "the DON must be the oracles this run signs with")

		// F is the largest the instance count allows: four members tolerate one fault,
		// and a report needs F+1 of their signatures.
		assert.Equal(t, uint8(1), configs.Event.F)
		assert.False(t, configs.Next(), "and must be configured once")
	})

	t.Run("the other contracts a local CRE run has are there too", func(t *testing.T) {
		for name, address := range map[string]common.Address{
			"capabilities registry": deployment.CapabilitiesRegistry,
			"workflow registry":     deployment.WorkflowRegistry,
			"balance reader":        deployment.BalanceReader,
		} {
			code, err := sim.Backend().CodeAt(t.Context(), address, nil)
			require.NoError(t, err)
			assert.NotEmpty(t, code, "%s is named but nothing was deployed there", name)
		}
	})

	t.Run("asking again is the same deployment", func(t *testing.T) {
		again, err := dep.Get(t.Context(), standalone.CommonConfig{})
		require.NoError(t, err)
		assert.Same(t, deployment, again, "the instances share a chain, and what is on it goes on once")
	})
}

// TestDeploymentTooSmallToWrite covers the honest state of a run below four: a
// chain to read and trigger on, and a forwarder with no DON, because the contract
// will not have one that cannot tolerate a fault.
func TestDeploymentTooSmallToWrite(t *testing.T) {
	sim := simulatedChain(t)
	dep := Dependency(logger.Test(t), &chainDependency{chain: sim}, keystores).ForEmbedding(0, 1)

	deployment, err := dep.Get(t.Context(), standalone.CommonConfig{})
	require.NoError(t, err)
	require.NotNil(t, deployment)

	assert.False(t, deployment.Configured)
	assert.NotEqual(t, common.Address{}, deployment.Forwarder, "it is still deployed, so a run can read it")
	assert.Len(t, deployment.Accounts, 1, "and the one instance is still funded")
}

// TestDeploymentWithoutASimulatedChain is a configured run, or an embedded one
// pointed at a real chain: what is deployed there was deployed by a deployment,
// and this has nothing to say about it.
func TestDeploymentWithoutASimulatedChain(t *testing.T) {
	dep := Dependency(logger.Test(t), &chainDependency{}, keystores).ForEmbedding(0, 4)

	deployment, err := dep.Get(t.Context(), standalone.CommonConfig{})
	require.NoError(t, err)
	assert.Nil(t, deployment)
}

// TestDeploymentAddressesAreFixed is the property a local run leans on: the
// addresses are the same every time, so a workflow, a test or a note in a
// terminal can name one before the chain it is on exists.
//
// They are the deploying account and a nonce, and that account is derived from a
// constant - so what has to hold is that nothing else this deployment does moves
// the nonces around. Funding is what would: it sends from the same account, and
// there is one transfer per instance.
func TestDeploymentAddressesAreFixed(t *testing.T) {
	deploy := func(t *testing.T, instances int) *Deployment {
		t.Helper()

		dep := Dependency(logger.Test(t), &chainDependency{chain: simulatedChain(t)}, keystores).ForEmbedding(0, instances)
		deployment, err := dep.Get(t.Context(), standalone.CommonConfig{})
		require.NoError(t, err)
		require.NotNil(t, deployment)
		return deployment
	}

	// Two chains, and a different number of accounts funded on each: neither the run
	// nor its size may decide where a contract lives.
	four, one := deploy(t, 4), deploy(t, 1)

	assert.Equal(t, four.CapabilitiesRegistry, one.CapabilitiesRegistry)
	assert.Equal(t, four.WorkflowRegistry, one.WorkflowRegistry)
	assert.Equal(t, four.Forwarder, one.Forwarder)
	assert.Equal(t, four.BalanceReader, one.BalanceReader)

	// Pinned, so that changing the order contracts are deployed in - which would move
	// every address after the one that moved - is a change someone has to mean.
	assert.Equal(t, "0x88aEF42dd4f3598beBE4c3e3cbE03e638f175330", four.CapabilitiesRegistry.Hex())
	assert.Equal(t, "0x380D3Dd74169897d48ad627F3ebc665ea158A0cE", four.WorkflowRegistry.Hex())
	assert.Equal(t, "0xCAaCC9d56B9516dF1D100C3E170d05c918C7d3c2", four.Forwarder.Hex())
	assert.Equal(t, "0xb465c41F08d657bAEE5f4771B98a538Ebd65312b", four.BalanceReader.Hex())
}
