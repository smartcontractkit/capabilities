// Package remote serves an ocr.Keystore over gRPC as the OCRKeystore service, and is a client of it.
package remote

import (
	"context"
	"errors"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/smartcontractkit/capabilities/libs/capability/keystore/ocr"
	"github.com/smartcontractkit/capabilities/libs/capability/keystore/ocr/remote/pb"
)

// RegisterServer serves ks as the OCRKeystore service on s.
func RegisterServer(s grpc.ServiceRegistrar, ks ocr.Keystore) {
	pb.RegisterOCRKeystoreServer(s, &server{ks: ks})
}

type server struct {
	pb.UnimplementedOCRKeystoreServer
	ks ocr.Keystore
}

func (s *server) ListKeyBundles(ctx context.Context, req *pb.ListKeyBundlesRequest) (*pb.ListKeyBundlesReply, error) {
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

func (s *server) GetKeyBundle(ctx context.Context, req *pb.GetKeyBundleRequest) (*pb.GetKeyBundleReply, error) {
	kb, err := s.get(ctx, req.Id)
	if err != nil {
		return nil, err
	}

	return &pb.GetKeyBundleReply{KeyBundle: keyBundleToPB(kb)}, nil
}

func (s *server) SignOnchain(ctx context.Context, req *pb.SignOnchainRequest) (*pb.SignOnchainReply, error) {
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

func (s *server) VerifyOnchain(ctx context.Context, req *pb.VerifyOnchainRequest) (*pb.VerifyOnchainReply, error) {
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

func (s *server) SignOffchain(ctx context.Context, req *pb.SignOffchainRequest) (*pb.SignOffchainReply, error) {
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

func (s *server) ConfigDiffieHellman(ctx context.Context, req *pb.ConfigDiffieHellmanRequest) (*pb.ConfigDiffieHellmanReply, error) {
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

func (s *server) get(ctx context.Context, id string) (ocr.KeyBundle, error) {
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
	if errors.Is(err, ocr.ErrKeyBundleNotFound) {
		return status.Errorf(codes.NotFound, "failed to %s: %v", op, err)
	}

	return status.Errorf(codes.Internal, "failed to %s: %v", op, err)
}

func keyBundleToPB(kb ocr.KeyBundle) *pb.KeyBundle {
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
