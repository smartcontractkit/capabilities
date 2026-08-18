package main

import (
	"context"
	"crypto/sha256"
	"fmt"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/smartcontractkit/libocr/commontypes"
	ocr2types "github.com/smartcontractkit/libocr/offchainreporting2plus/types"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/services"

	"github.com/smartcontractkit/capabilities/libs/standalone/ocr"
)

// The two messages this app sends. A lap is a chain of hi, and the broadcast marks where one lap
// ends and the next begins - useful when reading four instances' output interleaved in one terminal.
const (
	messageHi            = "hi"
	messageStartingAgain = "starting again"
)

// configDigest identifies the group of oracles talking to each other. Every instance must use the
// same one, and libocr keys an endpoint by it, so it is a constant rather than a setting: there is
// one group here and it is this app's.
var configDigest = ocr2types.ConfigDigest(sha256.Sum256([]byte("cre/pingpong/v1")))

// endpointLimits are generous for messages this small; they exist because libocr requires them.
var endpointLimits = ocr2types.BinaryNetworkEndpointLimits{
	MaxMessageLength:          1024,
	MessagesRatePerOracle:     10,
	MessagesCapacityPerOracle: 100,
	BytesRatePerOracle:        10 * 1024,
	BytesCapacityPerOracle:    100 * 1024,
}

// pingPong passes a message round the ring of oracles over one binary network endpoint.
type pingPong struct {
	services.Service
	eng *services.Engine

	lggr      logger.Logger
	cfg       *Config
	factories *ocr.OCRFactories

	// me is this instance's position in cfg.Peers, which is also the oracle ID every other instance
	// addresses it by. Resolved in start, since it depends on the peer ID the proxy hosts for us.
	me commontypes.OracleID
	// oracles is how many of us there are, from cfg.Peers.
	oracles int

	endpoint commontypes.BinaryNetworkEndpoint
	// lapDone carries the hi that came back round to oracle 0, telling it to start the next lap.
	// Buffered so a lap completing while 0 is between waits is not lost.
	lapDone chan struct{}
}

var _ services.Service = (*pingPong)(nil)

func newPingPong(lggr logger.Logger, cfg *Config, factories *ocr.OCRFactories) *pingPong {
	p := &pingPong{lggr: lggr, cfg: cfg, factories: factories, lapDone: make(chan struct{}, 1)}
	p.Service, p.eng = services.Config{
		Name:  "PingPong",
		Start: p.start,
		Close: p.close,
	}.NewServiceEngine(lggr)
	return p
}

func (p *pingPong) start(ctx context.Context) error {
	peerIDs, locators, err := parsePeers(p.cfg.Peers)
	if err != nil {
		return err
	}
	// An oracle ID is a uint8, so a ring longer than that cannot address its own members.
	if len(peerIDs) < 2 || len(peerIDs) > math.MaxUint8 {
		return fmt.Errorf("--pingpong.peers needs between two and %d peers to pass a message between, got %d", math.MaxUint8, len(peerIDs))
	}
	p.oracles = len(peerIDs)

	// Which oracle this instance is, is not configured: it is where the peer the proxy hosts for us
	// appears in the ring. Being a position in a ring that was just bounded, it fits an oracle ID.
	me := slices.Index(peerIDs, p.factories.PeerID.String())
	if me < 0 || me > math.MaxUint8 {
		return fmt.Errorf("this instance's peer %s is not one of --pingpong.peers %v", p.factories.PeerID, peerIDs)
	}
	p.me = commontypes.OracleID(me)

	// Created through the proxy: the peer that dials, announces and encrypts is in the crecore
	// process at --ocr.proxy-address, and what comes back here is a plain endpoint. The peers'
	// addresses go with it, since that proxy has no other way of knowing where they are.
	endpoint, err := p.factories.OCR2Endpoint.NewEndpoint(configDigest, peerIDs, locators, 1, endpointLimits)
	if err != nil {
		return fmt.Errorf("failed to create endpoint: %w", err)
	}
	if err := endpoint.Start(); err != nil {
		return fmt.Errorf("failed to start endpoint: %w", err)
	}
	p.endpoint = endpoint

	p.lggr.Infow("Ping pong ready", "oracle", p.me, "peerID", p.factories.PeerID.String(), "oracles", p.oracles)

	p.eng.Go(p.receive)
	if p.me == 0 {
		p.eng.Go(p.lead)
	}
	return nil
}

// receive prints every message that arrives and passes the lap on.
func (p *pingPong) receive(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case msg := <-p.endpoint.Receive():
			fmt.Printf("I am %d and got the message %s from %d\n", p.me, msg.Msg, msg.Sender)
			p.forward(string(msg.Msg))
		}
	}
}

// forward continues the lap: everyone but the last says hi to the next oracle along, the last says
// hi back to oracle 0, and oracle 0 - whose hi can only have come from the last - takes that as the
// lap being complete.
func (p *pingPong) forward(message string) {
	if message != messageHi {
		return // the broadcast marking a new lap; oracle 0 is about to say hi anyway
	}

	switch {
	case p.me == 0:
		select {
		case p.lapDone <- struct{}{}:
		default: // a lap is already waiting to be counted
		}
	case int(p.me) == p.oracles-1:
		p.send(messageHi, 0)
	default:
		p.send(messageHi, p.me+1)
	}
}

// lead is oracle 0's job: start a lap, wait for it to come back, announce the next one and go again.
// It re-sends rather than waiting forever, because a hi sent before the peers have finished
// connecting is dropped like any other message to an unreachable peer, and one lost message would
// otherwise leave every instance waiting quietly.
func (p *pingPong) lead(ctx context.Context) {
	if !sleep(ctx, p.cfg.StartDelay.Duration()) {
		return
	}

	retry := time.NewTimer(p.cfg.RetryDelay.Duration())
	defer retry.Stop()

	for {
		p.send(messageHi, 1)
		retry.Reset(p.cfg.RetryDelay.Duration())

		select {
		case <-ctx.Done():
			return
		case <-p.lapDone:
			p.lggr.Infow("Lap complete, starting again")
			p.broadcast(messageStartingAgain)
			if !sleep(ctx, p.cfg.RoundDelay.Duration()) {
				return
			}
		case <-retry.C:
			p.lggr.Infow("Lap did not come back round, starting it again")
		}
	}
}

func (p *pingPong) send(message string, to commontypes.OracleID) {
	p.lggr.Debugw("Sending", "message", message, "to", to)
	p.endpoint.SendTo([]byte(message), to)
}

func (p *pingPong) broadcast(message string) {
	p.lggr.Debugw("Broadcasting", "message", message)
	p.endpoint.Broadcast([]byte(message))
}

func (p *pingPong) close() error {
	if p.endpoint != nil {
		return p.endpoint.Close()
	}
	return nil
}

// parsePeers splits peerID@host:port entries into the two things libocr wants them as: the peer IDs,
// in order, which is the oracle set and the ring; and the same peers as locators, which is how the
// hosting proxies are told where to find each other.
func parsePeers(peers []string) (peerIDs []string, locators []commontypes.BootstrapperLocator, err error) {
	peerIDs = make([]string, 0, len(peers))
	locators = make([]commontypes.BootstrapperLocator, 0, len(peers))

	for _, peer := range peers {
		peerID, address, found := strings.Cut(peer, "@")
		if !found || peerID == "" || address == "" {
			return nil, nil, fmt.Errorf("invalid --pingpong.peers entry %q: expected peerID@host:port", peer)
		}
		peerIDs = append(peerIDs, peerID)
		locators = append(locators, commontypes.BootstrapperLocator{PeerID: peerID, Addrs: []string{address}})
	}
	return peerIDs, locators, nil
}

// sleep waits for d, reporting false if the context was cancelled first.
func sleep(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
