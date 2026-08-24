// Package simulated is what a local run needs put on the chain it was given, when
// that chain is one this process started for itself.
//
// The chain is chainlink-evm's (see its cre/evm.SimulatedChain): every consumer of
// that dependency gets one when it is pointed at nothing, so this is not about
// having a chain. It is about what a deployment would have done to it - fund the
// accounts the instances send from, deploy the forwarder they write reports
// through, and tell that forwarder who they are - which is CRE's business and not
// a chain library's.
//
// It does for an embedded run what the local CRE's deployment does around a node,
// so that "embed" with nothing configured is a DON that can actually write.
package simulated

import (
	"context"
	"fmt"

	"github.com/ethereum/go-ethereum/common"

	"github.com/smartcontractkit/chainlink-evm/gethwrappers/keystone/generated/balance_reader"
	"github.com/smartcontractkit/chainlink-evm/gethwrappers/keystone/generated/forwarder"
	capabilities_registry_v2 "github.com/smartcontractkit/chainlink-evm/gethwrappers/workflow/generated/capabilities_registry_wrapper_v2"
	workflow_registry_v2 "github.com/smartcontractkit/chainlink-evm/gethwrappers/workflow/generated/workflow_registry_wrapper_v2"
	creevm "github.com/smartcontractkit/chainlink-evm/pkg/cre/evm"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/standalone"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/types/core"
)

// Deployment is what was put on the simulated chain for this run.
//
// It is what the local CRE deploys around a node for the EVM capability: the
// forwarder the environment itself puts on every chain a DON writes through, and
// the two contracts its EVM tests deploy beside it - a balance reader to read and
// a data feeds cache to write into.
//
// Every address here is the same on every run. They are created by one account
// whose key is derived from a constant, on a chain that starts empty, in the
// order below - so each is that account and a nonce, and nothing about a
// particular run (how many instances, whether they were funded first) moves them.
// See deploy, where that order is the point.
type Deployment struct {
	// Forwarder is the deployed CRE forwarder: what a report is transmitted through,
	// configured with this run's instances as its DON so that it accepts what they
	// sign and nothing else.
	Forwarder common.Address

	// Configured says whether that last part happened. The contract requires a DON
	// that can tolerate a fault - F of at least one, with 3F < N - so a run of fewer
	// than four instances has a forwarder deployed and no DON on it: a chain to read,
	// trigger and observe on, and too few members to write through.
	Configured bool

	// CapabilitiesRegistry and WorkflowRegistry are what "env start" deploys on the
	// registry chain before anything else (DeployV2RegistryContractsSequence): the
	// first says which DONs exist and what they can do, the second which workflows are
	// registered against them.
	//
	// They are deployed empty. Registering this run's DON, its nodes and its
	// capabilities in them is configuration rather than deployment, and an embedded run
	// does not need it: its capability registry is in process, which is what embedding
	// means. They are here so that what is on this chain is what is on the local CRE's,
	// and so that anything reading them finds a contract rather than an empty address.
	CapabilitiesRegistry common.Address
	WorkflowRegistry     common.Address

	// BalanceReader is what the local CRE's example workflows read through - "env start
	// --with-example" deploys one - so a workflow written against that has it here too.
	BalanceReader common.Address

	// Accounts are the instances' own, funded in instance order.
	Accounts []common.Address
}

// DefaultReceiverGasMinimum is the gas a receiving contract is guaranteed on a
// simulated chain when nothing said otherwise, matching the local CRE's own
// default so that a workflow behaves here as it does there.
const DefaultReceiverGasMinimum = 500

// Config is what this run puts on the chain, as opposed to the chain itself.
type Config struct {
	// DonID is the DON the forwarder is configured for, which has to be the DON the
	// instances report as. It defaults to the same 1 --capabilities.capability-don-id
	// does.
	DonID uint32 `usage:"DON ID the simulated chain's forwarder is configured for; must match --capabilities.capability-don-id"`
}

// Defaults are what a run that says nothing gets.
var Defaults = Config{DonID: 1}

// Dependency returns what was deployed on the simulated chain, or nil when there
// is no simulated chain - which is every configured run, and any embedded run
// pointed at a real chain.
//
// keystores is how it learns which accounts to fund: the same function the
// instances themselves sign with, asked for each of them, so what is funded is
// what will send.
func Dependency(
	lggr logger.Logger,
	chain standalone.BootstrapDependency[*creevm.SimulatedChain],
	keystores func(instance int) (core.Keystore, error),
) standalone.BootstrapDependency[*Deployment] {
	return &dependency{lggr: lggr, chain: chain, keystores: keystores}
}

type dependency struct {
	lggr      logger.Logger
	chain     standalone.BootstrapDependency[*creevm.SimulatedChain]
	keystores func(instance int) (core.Keystore, error)

	// cfg and instances are the embedded form's; a configured run has neither, and
	// nothing to deploy.
	cfg       *Config
	instances int

	started    bool
	deployment *Deployment
	err        error

	// embedded is the one embedded form, since the bootstrapper asks for one per
	// instance and once more to collect settings: they are one DON on one chain, and
	// the settings read have to be the settings bound to flags.
	embedded *dependency
}

var _ standalone.BootstrapDependency[*Deployment] = (*dependency)(nil)

func (d *dependency) Namespace() string { return "simulated" }

func (d *dependency) Config() any {
	if d.cfg == nil {
		return nil
	}
	return d.cfg
}

func (d *dependency) Dependencies() []standalone.BootstrapCommand {
	return []standalone.BootstrapCommand{d.chain}
}

func (d *dependency) ForEmbedding(i, instances int) standalone.BootstrapDependency[*Deployment] {
	if d.embedded == nil {
		cfg := Defaults
		d.embedded = &dependency{lggr: d.lggr, keystores: d.keystores, cfg: &cfg}
	}
	// The largest wins: this is called once per instance with the real count, and once
	// before any of them as (0, 1) to collect the settings to register. What is funded
	// and configured should be the run, not the probe.
	d.embedded.instances = max(d.embedded.instances, instances)
	d.embedded.chain = d.chain.ForEmbedding(i, instances)
	return d.embedded
}

// Get funds and deploys once, however many instances ask: they share a chain, and
// what goes on it goes on it once.
func (d *dependency) Get(ctx context.Context, cc standalone.CommonConfig) (*Deployment, error) {
	chain, err := d.chain.Get(ctx, cc)
	if err != nil {
		return nil, err
	}
	if chain == nil || d.cfg == nil {
		// A real chain: what is on it was put there by a deployment, and this has nothing
		// to say about it.
		return nil, nil
	}

	if !d.started {
		d.started = true
		d.deployment, d.err = d.deploy(ctx, chain)
	}
	return d.deployment, d.err
}

// deploy puts the contracts on the chain and then funds the accounts.
//
// That order is load-bearing. A contract's address is the deploying account and
// its nonce, so deploying first - before anything else this account sends - puts
// every contract at an address that depends on nothing but this list and the
// order of it. Funding first would move them with the instance count; configuring
// the forwarder in between would move them for a run too small to configure it.
//
// So: deploy everything, then configure, then fund. Adding a contract to the end
// of the list leaves the ones before it where they were.
func (d *dependency) deploy(ctx context.Context, chain *creevm.SimulatedChain) (*Deployment, error) {
	deployment := &Deployment{}
	var err error

	// In the order "env start" deploys them: the registries first, since everything
	// else on a real environment is registered in them, then the forwarder a DON
	// writes through, then what the examples read.
	if deployment.CapabilitiesRegistry, err = deployCapabilitiesRegistry(ctx, chain); err != nil {
		return nil, err
	}
	if deployment.WorkflowRegistry, err = deployWorkflowRegistry(ctx, chain); err != nil {
		return nil, err
	}

	forwarder, err := deployForwarder(ctx, chain)
	if err != nil {
		return nil, err
	}
	deployment.Forwarder = forwarder.address

	if deployment.BalanceReader, err = deployBalanceReader(ctx, chain); err != nil {
		return nil, err
	}

	signers, err := signers(d.instances)
	if err != nil {
		return nil, err
	}
	if deployment.Configured, err = configureForwarder(ctx, d.lggr, chain, forwarder, d.cfg.DonID, signers); err != nil {
		return nil, err
	}

	if deployment.Accounts, err = d.accounts(ctx); err != nil {
		return nil, err
	}
	for _, account := range deployment.Accounts {
		if err := chain.Fund(ctx, account, creevm.DefaultSimulatedConfig.FundingAmount()); err != nil {
			return nil, err
		}
	}

	d.lggr.Infow("Deployed the contracts the local CRE sets up",
		"capabilitiesRegistry", deployment.CapabilitiesRegistry, "workflowRegistry", deployment.WorkflowRegistry,
		"forwarder", deployment.Forwarder, "forwarderHasDON", deployment.Configured,
		"balanceReader", deployment.BalanceReader, "funded", deployment.Accounts)

	return deployment, nil
}

// accounts are the instances' own, asked of the keystores they sign with.
func (d *dependency) accounts(ctx context.Context) ([]common.Address, error) {
	accounts := make([]common.Address, 0, d.instances)
	for instance := range d.instances {
		keystore, err := d.keystores(instance)
		if err != nil {
			return nil, fmt.Errorf("failed to read the account of instance %d: %w", instance, err)
		}
		held, err := keystore.Accounts(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to read the account of instance %d: %w", instance, err)
		}
		if len(held) != 1 {
			return nil, fmt.Errorf("instance %d holds %d accounts, want one to fund", instance, len(held))
		}
		if !common.IsHexAddress(held[0]) {
			return nil, fmt.Errorf("instance %d holds %q, which is not an address to fund", instance, held[0])
		}
		accounts = append(accounts, common.HexToAddress(held[0]))
	}
	return accounts, nil
}

// deployed is a contract on the chain: where it is, and the binding to call it.
type deployed struct {
	address  common.Address
	contract *forwarder.KeystoneForwarder
}

// deployForwarder deploys the CRE forwarder, which is what a report is
// transmitted through. The local CRE's environment puts one on every chain a DON
// writes to; this is that, for a chain that has no environment around it.
func deployForwarder(ctx context.Context, chain *creevm.SimulatedChain) (deployed, error) {
	address, tx, contract, err := forwarder.DeployKeystoneForwarder(chain.Transactor(), chain.Backend())
	if err != nil {
		return deployed{}, fmt.Errorf("failed to deploy the forwarder: %w", err)
	}
	if err := chain.Mined(ctx, tx); err != nil {
		return deployed{}, fmt.Errorf("the forwarder was not deployed: %w", err)
	}
	return deployed{address: address, contract: contract}, nil
}

// deployBalanceReader deploys the contract the local CRE's EVM read tests read
// through, so a workflow written against those has it here too.
func deployBalanceReader(ctx context.Context, chain *creevm.SimulatedChain) (common.Address, error) {
	address, tx, _, err := balance_reader.DeployBalanceReader(chain.Transactor(), chain.Backend())
	if err != nil {
		return common.Address{}, fmt.Errorf("failed to deploy the balance reader: %w", err)
	}
	if err := chain.Mined(ctx, tx); err != nil {
		return common.Address{}, fmt.Errorf("the balance reader was not deployed: %w", err)
	}
	return address, nil
}

// deployCapabilitiesRegistry deploys the registry that says which DONs exist and
// what they can do. See Deployment.CapabilitiesRegistry for why it is left empty.
//
// CanAddOneNodeDONs is what the local CRE's deployment passes: false.
func deployCapabilitiesRegistry(ctx context.Context, chain *creevm.SimulatedChain) (common.Address, error) {
	address, tx, _, err := capabilities_registry_v2.DeployCapabilitiesRegistry(
		chain.Transactor(), chain.Backend(), capabilities_registry_v2.CapabilitiesRegistryConstructorParams{})
	if err != nil {
		return common.Address{}, fmt.Errorf("failed to deploy the capabilities registry: %w", err)
	}
	if err := chain.Mined(ctx, tx); err != nil {
		return common.Address{}, fmt.Errorf("the capabilities registry was not deployed: %w", err)
	}
	return address, nil
}

// deployWorkflowRegistry deploys the registry a workflow is registered in, the
// other half of what "env start" puts on the registry chain.
func deployWorkflowRegistry(ctx context.Context, chain *creevm.SimulatedChain) (common.Address, error) {
	address, tx, _, err := workflow_registry_v2.DeployWorkflowRegistry(chain.Transactor(), chain.Backend())
	if err != nil {
		return common.Address{}, fmt.Errorf("failed to deploy the workflow registry: %w", err)
	}
	if err := chain.Mined(ctx, tx); err != nil {
		return common.Address{}, fmt.Errorf("the workflow registry was not deployed: %w", err)
	}
	return address, nil
}

// configureForwarder tells the forwarder who this run's oracles are, which is
// what the local CRE's deployment does around a real DON: a report reaches a
// receiver only if the signatures on it are theirs.
//
// It reports whether that happened. The contract requires a DON that can tolerate
// a fault - F of at least one, with 3F < N - so a run of fewer than four
// instances is left with a forwarder and no DON on it, which is the honest state
// for a run that small: reading, triggering and observing all work without one.
func configureForwarder(
	ctx context.Context,
	lggr logger.Logger,
	chain *creevm.SimulatedChain,
	forwarder deployed,
	donID uint32,
	signers []common.Address,
) (bool, error) {
	// F is the largest fault tolerance the oracle count allows, which is what the OCR
	// configuration these same instances run under uses.
	f := (len(signers) - 1) / 3
	if f < 1 {
		lggr.Warnw("The simulated chain's forwarder has no DON, so reports cannot be written through it",
			"instances", len(signers), "reason", "the forwarder requires F >= 1 with 3F < N, so at least four instances",
			"forwarder", forwarder.address)
		return false, nil
	}

	tx, err := forwarder.contract.SetConfig(chain.Transactor(), donID, 1, uint8(f), signers) //#nosec G115 - an embedded run has a handful of instances
	if err != nil {
		return false, fmt.Errorf("failed to configure the forwarder: %w", err)
	}
	if err := chain.Mined(ctx, tx); err != nil {
		return false, fmt.Errorf("the forwarder was not configured: %w", err)
	}

	lggr.Infow("Configured the forwarder with this run's oracles",
		"forwarder", forwarder.address, "donID", donID, "f", f, "signers", signers)

	return true, nil
}
