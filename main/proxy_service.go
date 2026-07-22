package main

import (
	"context"
	"crypto/ed25519"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/prometheus/client_golang/prometheus"
	"google.golang.org/grpc"

	"github.com/smartcontractkit/libocr/commontypes"
	"github.com/smartcontractkit/libocr/networking"
	"github.com/smartcontractkit/libocr/networking/rageping"
	ocr2types "github.com/smartcontractkit/libocr/offchainreporting2plus/types"
	"github.com/smartcontractkit/libocr/ragep2p"
	ragetypes "github.com/smartcontractkit/libocr/ragep2p/types"

	"github.com/smartcontractkit/capabilities/libs/standalone"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/services"
	creproxy "github.com/smartcontractkit/chainlink-protos/cre/impl/proxy"
)

// p2pPrivateKeyEnvVar holds the hex-encoded ed25519 key (a 32-byte seed or a
// 64-byte private key) that determines this proxy's peer identity. It is read
// from the environment rather than a flag so the secret never appears in the
// process table.
const p2pPrivateKeyEnvVar = "CL_P2P_PRIVATE_KEY"

// Config is the proxy peer + gRPC server configuration, populated from CLI
// flags (see main.go) plus the private key from the environment.
type Config struct {
	// ProxyListenAddress is the address the proxy gRPC server listens on.
	ProxyListenAddress string
	// ListenAddresses are the rage p2p V2 listen addresses (host:port).
	ListenAddresses []string
	// AnnounceAddresses are the rage p2p V2 announce addresses; when empty the
	// listen addresses are used.
	AnnounceAddresses []string
	// DeltaReconcile / DeltaDial configure rage p2p discovery/dial cadence.
	DeltaReconcile time.Duration
	DeltaDial      time.Duration
	// IncomingBufferSize / OutgoingBufferSize configure per-remote message
	// buffers on the shared endpoint config.
	IncomingBufferSize int
	OutgoingBufferSize int
}

// loadPrivateKey reads and decodes the ed25519 key from p2pPrivateKeyEnvVar.
func loadPrivateKey() (ed25519.PrivateKey, error) {
	raw := os.Getenv(p2pPrivateKeyEnvVar)
	if raw == "" {
		return nil, fmt.Errorf("%s must be set", p2pPrivateKeyEnvVar)
	}
	b, err := hex.DecodeString(raw)
	if err != nil {
		return nil, fmt.Errorf("%s must be hex-encoded: %w", p2pPrivateKeyEnvVar, err)
	}
	switch len(b) {
	case ed25519.SeedSize:
		return ed25519.NewKeyFromSeed(b), nil
	case ed25519.PrivateKeySize:
		return b, nil
	default:
		return nil, fmt.Errorf("%s must decode to %d (seed) or %d (private key) bytes, got %d",
			p2pPrivateKeyEnvVar, ed25519.SeedSize, ed25519.PrivateKeySize, len(b))
	}
}

// peerKeyring is a ragetypes.PeerKeyring backed by an ed25519 private key. It is
// used in place of the deprecated PeerConfig.PrivKey field.
type peerKeyring struct {
	privKey   ed25519.PrivateKey
	publicKey ragetypes.PeerPublicKey
}

var _ ragetypes.PeerKeyring = (*peerKeyring)(nil)

func newPeerKeyring(privKey ed25519.PrivateKey) (*peerKeyring, error) {
	pub, err := ragetypes.PeerPublicKeyFromGenericPublicKey(privKey.Public())
	if err != nil {
		return nil, fmt.Errorf("failed to derive peer public key: %w", err)
	}
	return &peerKeyring{privKey: privKey, publicKey: pub}, nil
}

// Sign returns an EdDSA-Ed25519 signature over msg, as required by PeerKeyring.
func (k *peerKeyring) Sign(msg []byte) ([]byte, error) {
	return ed25519.Sign(k.privKey, msg), nil
}

func (k *peerKeyring) PublicKey() ragetypes.PeerPublicKey {
	return k.publicKey
}

// proxyService runs a single shared rage (libocr) peer and exposes it over
// gRPC. Today it serves the OCR BinaryNetworkEndpoint proxy; the same peer also
// yields a PeerGroupFactory, which is the seam for the future DON-to-DON proxy
// so both share one connection and one discoverer.
//
// NOTE: per the standalone framework, Start blocks until shutdown (the
// Bootstrapper returns the result of Start directly).
type proxyService struct {
	services.Service
	eng *services.Engine

	cfg  *Config
	lggr logger.Logger
	db   standalone.Dependency[*sql.DB]

	grpcServer *grpc.Server
	peerCloser io.Closer
}

var _ services.Service = (*proxyService)(nil)

// newProxyService builds the proxy service using the standard
// services.Config/Engine pattern, so its lifecycle and health integrate with
// the bootstrapper's aggregated health report.
func newProxyService(cfg *Config, lggr logger.Logger, db standalone.Dependency[*sql.DB]) *proxyService {
	s := &proxyService{cfg: cfg, lggr: lggr, db: db}
	s.Service, s.eng = services.Config{
		Name:  "P2PProxy",
		Start: s.start,
		Close: s.close,
	}.NewServiceEngine(lggr)
	return s
}

func (s *proxyService) start(ctx context.Context) error {
	if len(s.cfg.ListenAddresses) == 0 {
		return errors.New("at least one --listen-addresses is required")
	}

	privKey, err := loadPrivateKey()
	if err != nil {
		return err
	}
	keyring, err := newPeerKeyring(privKey)
	if err != nil {
		return err
	}
	peerID := ragetypes.PeerIDFromKeyring(keyring)

	sqlDB, err := s.db.Get(ctx)
	if err != nil {
		return fmt.Errorf("failed to get database: %w", err)
	}

	ds := sqlx.NewDb(sqlDB, "pgx")
	discovererDB := NewOCRDiscovererDatabase(ds, peerID.String())

	s.lggr.Infow("Starting p2p proxy peer",
		"peerID", peerID.String(),
		"listenAddresses", s.cfg.ListenAddresses,
		"announceAddresses", s.cfg.AnnounceAddresses,
	)

	peer, err := networking.NewPeer(networking.PeerConfig{
		PeerKeyring:          keyring,
		Logger:               logger.NewOCRWrapper(s.lggr, false, func(string) {}),
		V2ListenAddresses:    s.cfg.ListenAddresses,
		V2AnnounceAddresses:  s.cfg.AnnounceAddresses,
		V2DeltaReconcile:     s.cfg.DeltaReconcile,
		V2DeltaDial:          s.cfg.DeltaDial,
		V2DiscovererDatabase: discovererDB,
		V2EndpointConfig: networking.EndpointConfigV2{
			IncomingMessageBufferSize: s.cfg.IncomingBufferSize,
			OutgoingMessageBufferSize: s.cfg.OutgoingBufferSize,
		},
		MetricsRegisterer:            prometheus.DefaultRegisterer,
		LatencyMetricsServiceConfigs: rageping.DefaultConfigs(),
	})
	if err != nil {
		return fmt.Errorf("failed to create rage peer: %w", err)
	}
	s.peerCloser = peer

	// The shared peer backs both surfaces over the same rage connection and
	// discoverer: OCR endpoints and DON-to-DON peer groups.
	ocrFactory := peer.OCR2BinaryNetworkEndpointFactory()
	pgFactory := peer.PeerGroupFactory()

	lis, err := net.Listen("tcp", s.cfg.ProxyListenAddress)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", s.cfg.ProxyListenAddress, err)
	}

	s.grpcServer = grpc.NewServer()
	creproxy.RegisterBinaryNetworkEndpointProxyServer(s.grpcServer, NewServer(ocrFactory))
	creproxy.RegisterPeerGroupProxyServer(s.grpcServer, NewPeerGroupServer(newNetworkingPeerGroupFactory(pgFactory)))

	// Gracefully stop the gRPC server when the engine cancels this context on
	// Close; run the (blocking) Serve in a tracked goroutine so start returns
	// promptly, per the services.Engine contract.
	s.eng.Go(func(ctx context.Context) {
		<-ctx.Done()
		s.grpcServer.GracefulStop()
	})
	s.eng.Go(func(context.Context) {
		s.lggr.Infow("p2p proxy serving", "address", lis.Addr().String())
		if err := s.grpcServer.Serve(lis); err != nil {
			s.eng.Errorw("proxy gRPC server stopped", "err", err)
		}
	})
	return nil
}

// close tears down the rage peer. The gRPC server is gracefully stopped by the
// goroutine started in start once the engine cancels its context.
func (s *proxyService) close() error {
	if s.peerCloser != nil {
		return s.peerCloser.Close()
	}
	return nil
}

// newNetworkingPeerGroupFactory adapts a libocr networking.PeerGroupFactory to
// the common-local creproxy.PeerGroupFactory expected by the proxy server.
// This adapter lives here (rather than in chainlink-common) so that common does
// not need to import libocr/networking, which would pull in go-ethereum.
func newNetworkingPeerGroupFactory(inner networking.PeerGroupFactory) creproxy.PeerGroupFactory {
	return networkingPeerGroupFactory{inner: inner}
}

type networkingPeerGroupFactory struct {
	inner networking.PeerGroupFactory
}

func (a networkingPeerGroupFactory) NewPeerGroup(configDigest [32]byte, peerIDs []string, bootstrappers []creproxy.BootstrapperInfo) (creproxy.PeerGroup, error) {
	locators := make([]commontypes.BootstrapperLocator, len(bootstrappers))
	for i, b := range bootstrappers {
		locators[i] = commontypes.BootstrapperLocator{PeerID: b.PeerID, Addrs: b.Addrs}
	}
	pg, err := a.inner.NewPeerGroup(ocr2types.ConfigDigest(configDigest), peerIDs, locators)
	if err != nil {
		return nil, err
	}
	return networkingPeerGroup{inner: pg}, nil
}

type networkingPeerGroup struct {
	inner networking.PeerGroup
}

func (a networkingPeerGroup) NewStream(remotePeerID string, args creproxy.StreamArgs) (creproxy.PeerGroupStream, error) {
	st, err := a.inner.NewStream(remotePeerID, networking.NewStreamArgs1{
		StreamName:         args.StreamName,
		OutgoingBufferSize: args.OutgoingBufferSize,
		IncomingBufferSize: args.IncomingBufferSize,
		MaxMessageLength:   args.MaxMessageLength,
		MessagesLimit:      ragep2p.TokenBucketParams{Rate: args.MessagesLimit.Rate, Capacity: args.MessagesLimit.Capacity},
		BytesLimit:         ragep2p.TokenBucketParams{Rate: args.BytesLimit.Rate, Capacity: args.BytesLimit.Capacity},
	})
	if err != nil {
		return nil, err
	}
	// networking.Stream's method set matches creproxy.PeerGroupStream.
	return st, nil
}

func (a networkingPeerGroup) Close() error {
	return a.inner.Close()
}
