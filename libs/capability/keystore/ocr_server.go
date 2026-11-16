package keystore

import (
	"context"
	"errors"
	"fmt"
	"math"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	ocrtypes "github.com/smartcontractkit/libocr/offchainreporting2plus/types"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"

	"github.com/smartcontractkit/capabilities/libs/capability/keystore/pb"
)

// ErrKeyBundleNotFound is what an OCRKeystore returns, wrapped or not, for a bundle it does not have.
var ErrKeyBundleNotFound = errors.New("key bundle not found")

// OCRKeyBundle is one of the node's OCR2 key bundles.
type OCRKeyBundle interface {
	ID() string
	// ChainType is the bundle's corekeys.ChainType, e.g. "evm".
	ChainType() string
	ocrtypes.OnchainKeyring
	ocrtypes.OffchainKeyring
}

// OCRKeystore is what the node serves as the OCRKeystore service.
type OCRKeystore interface {
	// List returns the bundles of the given chain types, or every bundle when none are given.
	List(ctx context.Context, chainTypes ...string) ([]OCRKeyBundle, error)
	Get(ctx context.Context, id string) (OCRKeyBundle, error)
}

// RegisterOCRKeystoreServer serves ks as the OCRKeystore service on s.
func RegisterOCRKeystoreServer(s grpc.ServiceRegistrar, lggr logger.Logger, ks OCRKeystore) {
	pb.RegisterOCRKeystoreServer(s, &ocrKeystoreServer{lggr: logger.Named(lggr, "OCRKeystoreServer"), ks: ks})
}

type ocrKeystoreServer struct {
	pb.UnimplementedOCRKeystoreServer
	lggr logger.Logger
	ks   OCRKeystore
}

func (s *ocrKeystoreServer) ListKeyBundles(ctx context.Context, req *pb.ListKeyBundlesRequest) (*pb.ListKeyBundlesReply, error) {
	kbs, err := s.ks.List(ctx, req.ChainTypes...)
	if err != nil {
		return nil, keystoreError("list key bundles", err)
	}

	reply := &pb.ListKeyBundlesReply{KeyBundles: make([]*pb.KeyBundle, len(kbs))}
	for i, kb := range kbs {
		reply.KeyBundles[i] = keyBundleToPB(kb)
	}

	return reply, nil
}

func (s *ocrKeystoreServer) GetKeyBundle(ctx context.Context, req *pb.GetKeyBundleRequest) (*pb.GetKeyBundleReply, error) {
	kb, err := s.get(ctx, req.Id)
	if err != nil {
		return nil, err
	}

	return &pb.GetKeyBundleReply{KeyBundle: keyBundleToPB(kb)}, nil
}

func (s *ocrKeystoreServer) SignOnchain(ctx context.Context, req *pb.SignOnchainRequest) (*pb.SignOnchainReply, error) {
	rc, err := reportContextFromPB(req.ReportContext)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	kb, err := s.get(ctx, req.KeyBundleId)
	if err != nil {
		return nil, err
	}

	sig, err := kb.Sign(rc, req.Report)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to sign report with key bundle %s: %v", req.KeyBundleId, err)
	}

	return &pb.SignOnchainReply{Signature: sig}, nil
}

func (s *ocrKeystoreServer) VerifyOnchain(ctx context.Context, req *pb.VerifyOnchainRequest) (*pb.VerifyOnchainReply, error) {
	rc, err := reportContextFromPB(req.ReportContext)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	kb, err := s.get(ctx, req.KeyBundleId)
	if err != nil {
		return nil, err
	}

	return &pb.VerifyOnchainReply{Valid: kb.Verify(req.PublicKey, rc, req.Report, req.Signature)}, nil
}

func (s *ocrKeystoreServer) SignOffchain(ctx context.Context, req *pb.SignOffchainRequest) (*pb.SignOffchainReply, error) {
	kb, err := s.get(ctx, req.KeyBundleId)
	if err != nil {
		return nil, err
	}

	sig, err := kb.OffchainSign(req.Message)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to sign offchain with key bundle %s: %v", req.KeyBundleId, err)
	}

	return &pb.SignOffchainReply{Signature: sig}, nil
}

func (s *ocrKeystoreServer) ConfigDiffieHellman(ctx context.Context, req *pb.ConfigDiffieHellmanRequest) (*pb.ConfigDiffieHellmanReply, error) {
	var point [32]byte
	if len(req.Point) != len(point) {
		return nil, status.Errorf(codes.InvalidArgument, "point is %d bytes, want %d", len(req.Point), len(point))
	}

	copy(point[:], req.Point)
	kb, err := s.get(ctx, req.KeyBundleId)
	if err != nil {
		return nil, err
	}

	secret, err := kb.ConfigDiffieHellman(point)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to compute the config shared secret with key bundle %s: %v", req.KeyBundleId, err)
	}

	return &pb.ConfigDiffieHellmanReply{SharedSecret: secret[:]}, nil
}

func (s *ocrKeystoreServer) get(ctx context.Context, id string) (OCRKeyBundle, error) {
	if id == "" {
		return nil, status.Error(codes.InvalidArgument, "key bundle ID is required")
	}

	kb, err := s.ks.Get(ctx, id)
	if err != nil {
		return nil, keystoreError("get key bundle "+id, err)
	}

	return kb, nil
}

func keystoreError(op string, err error) error {
	if errors.Is(err, ErrKeyBundleNotFound) {
		return status.Errorf(codes.NotFound, "failed to %s: %v", op, err)
	}

	return status.Errorf(codes.Internal, "failed to %s: %v", op, err)
}

func keyBundleToPB(kb OCRKeyBundle) *pb.KeyBundle {
	offchainPublicKey := kb.OffchainPublicKey()
	configEncryptionPublicKey := kb.ConfigEncryptionPublicKey()
	return &pb.KeyBundle{
		Id:                        kb.ID(),
		ChainType:                 kb.ChainType(),
		OnchainPublicKey:          kb.PublicKey(),
		OnchainMaxSignatureLength: uint32(kb.MaxSignatureLength()), //nolint:gosec // signature lengths are small
		OffchainPublicKey:         offchainPublicKey[:],
		ConfigEncryptionPublicKey: configEncryptionPublicKey[:],
	}
}

func reportContextToPB(rc ocrtypes.ReportContext) *pb.ReportContext {
	return &pb.ReportContext{
		ConfigDigest: rc.ConfigDigest[:],
		Epoch:        rc.Epoch,
		Round:        uint32(rc.Round),
		ExtraHash:    rc.ExtraHash[:],
	}
}

func reportContextFromPB(rc *pb.ReportContext) (ocrtypes.ReportContext, error) {
	if rc == nil {
		return ocrtypes.ReportContext{}, errors.New("report context is required")
	}

	digest, err := ocrtypes.BytesToConfigDigest(rc.ConfigDigest)
	if err != nil {
		return ocrtypes.ReportContext{}, fmt.Errorf("invalid config digest: %w", err)
	}

	if rc.Round > math.MaxUint8 {
		return ocrtypes.ReportContext{}, fmt.Errorf("round %d does not fit in a uint8", rc.Round)
	}

	var extraHash [32]byte
	if len(rc.ExtraHash) != len(extraHash) {
		return ocrtypes.ReportContext{}, fmt.Errorf("extra hash is %d bytes, want %d", len(rc.ExtraHash), len(extraHash))
	}

	copy(extraHash[:], rc.ExtraHash)
	return ocrtypes.ReportContext{
		ReportTimestamp: ocrtypes.ReportTimestamp{ConfigDigest: digest, Epoch: rc.Epoch, Round: uint8(rc.Round)},
		ExtraHash:       extraHash,
	}, nil
}
