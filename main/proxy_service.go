package main

import (
	"context"
	"crypto"
	"crypto/rand"
	"database/sql"
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

	"github.com/smartcontractkit/chainlink-common/keystore/corekeys/models"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/services"
	creproxy "github.com/smartcontractkit/chainlink-protos/cre/impl/proxy"
)

// keystorePasswordEnvVar is the keystore password used to decrypt the node's
// key ring. The proxy shares the node's DB (CL_DATABASE_URL) and this password.
const keystorePasswordEnvVar = "CL_PASSWORD_KEYSTORE"

// Config is the proxy peer + gRPC server configuration, populated from CLI
// flags (see main.go).
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

// loadPeerKeyring loads the P2P key from the node's keystore so the proxy uses
// the SAME peer identity as the node it fronts (other DON members expect this
// node's peer ID at this address). It reads the node's existing encrypted key
// ring (the legacy `encrypted_key_rings` table, in chainlink-common's
// corekeys/models format) and decrypts it with the keystore password. This is a
// deliberately small copy of core's keyManager.Unlock using only
// chainlink-common packages, so the proxy needn't import chainlink core.
//
// TODO: drop this once the keystore is migrated to chainlink-common's
// keystore.Keystore + pgstore (as chainlink-ccv already uses), after which the
// proxy can LoadKeystore from the shared table directly.
func loadPeerKeyring(ctx context.Context, ds *sqlx.DB) (*peerKeyring, error) {
	var encrypted []byte
	if err := ds.GetContext(ctx, &encrypted, "SELECT encrypted_keys FROM encrypted_key_rings LIMIT 1"); err != nil {
		return nil, fmt.Errorf("failed to read node key ring: %w", err)
	}
	kr, err := models.EncryptedKeyRing{EncryptedKeys: encrypted}.Decrypt(os.Getenv(keystorePasswordEnvVar))
	if err != nil {
		return nil, fmt.Errorf("failed to decrypt node key ring: %w", err)
	}
	for _, k := range kr.P2P {
		pub, perr := ragetypes.PeerPublicKeyFromGenericPublicKey(k.Public())
		if perr != nil {
			return nil, fmt.Errorf("failed to derive peer public key: %w", perr)
		}
		return &peerKeyring{signer: k, publicKey: pub}, nil
	}
	return nil, errors.New("no P2P key found in node key ring")
}

// peerKeyring is a ragetypes.PeerKeyring backed by the node's P2P key (a
// crypto.Signer), used in place of the deprecated PeerConfig.PrivKey field.
type peerKeyring struct {
	signer    crypto.Signer
	publicKey ragetypes.PeerPublicKey
}

var _ ragetypes.PeerKeyring = (*peerKeyring)(nil)

// Sign returns an EdDSA-Ed25519 signature over msg, as required by PeerKeyring.
func (k *peerKeyring) Sign(msg []byte) ([]byte, error) {
	return k.signer.Sign(rand.Reader, msg, crypto.Hash(0))
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

	sqlDB, err := s.db.Get(ctx)
	if err != nil {
		return fmt.Errorf("failed to get database: %w", err)
	}
	ds := sqlx.NewDb(sqlDB, "pgx")

	// Use the node's own P2P identity so the proxy is the same peer as the node.
	keyring, err := loadPeerKeyring(ctx, ds)
	if err != nil {
		return err
	}
	peerID := ragetypes.PeerIDFromKeyring(keyring)

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
	creproxy.RegisterEndpoint2ProxyServer(s.grpcServer, NewEndpoint2Server(peer.OCR3_1BinaryNetworkEndpointFactory()))
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
