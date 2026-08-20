package main

import (
	"context"
	"errors"

	"github.com/smartcontractkit/libocr/networking"
	ragetypes "github.com/smartcontractkit/libocr/ragep2p/types"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	commonsrv "github.com/smartcontractkit/chainlink-common/pkg/services"
	"github.com/smartcontractkit/chainlink-common/pkg/types/core"

	"github.com/smartcontractkit/capabilities/libs/standalone/rage"
	don2don "github.com/smartcontractkit/capabilities/libs/x/don2don"
	xrage "github.com/smartcontractkit/capabilities/libs/x/rage"
)

// DispatcherConfig is the subset of don2don.DispatcherConfig a binary fills in from flags. Field
// names and usage strings mirror don2don.DispatcherConfig so the two stay easy to compare.
type DispatcherConfig struct {
	SupportedVersion   int `usage:"version stamped on every outgoing DON-to-DON message"`
	ReceiverBufferSize int `usage:"how many messages may queue for one receiver before they are dropped"`

	RateLimitGlobalRPS      float64 `usage:"inbound DON-to-DON messages allowed per second, across all senders"`
	RateLimitGlobalBurst    int     `usage:"inbound DON-to-DON message burst allowance, across all senders"`
	RateLimitPerSenderRPS   float64 `usage:"inbound DON-to-DON messages allowed per second, per sender"`
	RateLimitPerSenderBurst int     `usage:"inbound DON-to-DON message burst allowance, per sender"`
}

var defaultDispatcherConfig = DispatcherConfig{
	SupportedVersion:        1,
	ReceiverBufferSize:      100,
	RateLimitGlobalRPS:      100,
	RateLimitGlobalBurst:    100,
	RateLimitPerSenderRPS:   10,
	RateLimitPerSenderBurst: 10,
}

func (c DispatcherConfig) don2don() don2don.DispatcherConfig {
	return don2don.DispatcherConfig{
		SupportedVersion:   c.SupportedVersion,
		ReceiverBufferSize: c.ReceiverBufferSize,
		RateLimit: don2don.DispatcherRateLimit{
			GlobalRPS:      c.RateLimitGlobalRPS,
			GlobalBurst:    c.RateLimitGlobalBurst,
			PerSenderRPS:   c.RateLimitPerSenderRPS,
			PerSenderBurst: c.RateLimitPerSenderBurst,
		},
		// This process always sends over the shared peer: there is no "legacy external peer" here,
		// unlike core.
		SendToSharedPeer: true,
	}
}

// dispatcherService runs don2don.Dispatcher over the same rage connection the OCR proxy serves,
// so this process does the real DON-to-DON work instead of merely fronting core's.
type dispatcherService struct {
	commonsrv.Service
	eng *commonsrv.Engine

	cfg       DispatcherConfig
	factories *rage.Factories
	registry  core.CapabilitiesRegistry
	lggr      logger.Logger
}

// newDispatcherService builds the service using the standard services.Config/Engine pattern, so
// its lifecycle and health integrate with the bootstrapper's aggregated health report.
func newDispatcherService(cfg DispatcherConfig, lggr logger.Logger, factories *rage.Factories, registry core.CapabilitiesRegistry) *dispatcherService {
	s := &dispatcherService{cfg: cfg, factories: factories, registry: registry, lggr: lggr}
	s.Service, s.eng = commonsrv.Config{
		Name:  "Dispatcher",
		Start: s.start,
	}.NewServiceEngine(lggr)
	return s
}

func (s *dispatcherService) start(ctx context.Context) error {
	if s.factories.PeerGroup == nil {
		return errors.New("no PeerGroup factory: the ocr dependency did not host a real peer")
	}

	sharedPeer := xrage.NewDon2DonSharedPeer(peerSource{s.factories}, nil, s.lggr)
	if err := sharedPeer.Start(ctx); err != nil {
		return err
	}

	dispatcher, err := don2don.NewDispatcher(s.cfg.don2don(), nil, sharedPeer, signer{s.factories.Keyring}, s.registry, s.lggr)
	if err != nil {
		_ = sharedPeer.Close()
		return err
	}
	if err := dispatcher.Start(ctx); err != nil {
		_ = sharedPeer.Close()
		return err
	}

	s.eng.Go(func(ctx context.Context) {
		<-ctx.Done()
		_ = dispatcher.Close()
		_ = sharedPeer.Close()
	})
	return nil
}

// peerSource adapts rage.Factories's peer group factory and identity into xrage.PeerSource.
type peerSource struct {
	factories *rage.Factories
}

func (p peerSource) PeerGroupFactory() networking.PeerGroupFactory { return p.factories.PeerGroup }
func (p peerSource) PeerID() ragetypes.PeerID                      { return p.factories.PeerID }

// signer adapts the peer's own keyring into rage.Signer: the keyring is already unlocked by the
// time this process has a peer, so Initialize has nothing to do.
type signer struct {
	keyring ragetypes.PeerKeyring
}

func (signer) Initialize() error { return nil }

func (s signer) Sign(data []byte) ([]byte, error) { return s.keyring.Sign(data) }
