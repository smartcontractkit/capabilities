package main

import (
	"context"
	"errors"
	"fmt"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/smartcontractkit/chainlink-common/keystore/corekeys"
	"github.com/smartcontractkit/chainlink-common/keystore/corekeys/ocr2key"
	creproxy "github.com/smartcontractkit/chainlink-protos/cre/impl/proxy"
)

// signerServer signs on behalf of oracles that hold no keys.
//
// It is the same trade as the endpoint proxies beside it: this process has the
// node's identity, so it lends out what that identity can do rather than the
// identity itself. A capability running in another process decides what to sign
// - it runs the protocol - and this signs it with the node's OCR key, which is
// the key the registry lists as one of the DON's signers.
type signerServer struct {
	creproxy.UnimplementedSignerServer

	bundle ocr2key.KeyBundle
}

var _ creproxy.SignerServer = (*signerServer)(nil)

// newSignerServer refuses a bundle it cannot be verified against.
//
// A caller checks signatures itself, since verification needs no secret, and it
// has to check them the way they were made. Rather than let that be discovered
// one rejected signature at a time - which looks like a DON that will not agree
// rather than like a misconfiguration - a bundle of another chain type is
// refused here, where it is still a startup error.
func newSignerServer(bundle ocr2key.KeyBundle) (*signerServer, error) {
	if bundle == nil {
		return nil, errors.New("no OCR2 key bundle to sign with")
	}
	if ct := bundle.ChainType(); ct != corekeys.EVM {
		return nil, fmt.Errorf("OCR2 key bundle %s is for %s; signing on behalf of a capability is only supported for %s keys",
			bundle.ID(), ct, corekeys.EVM)
	}
	return &signerServer{bundle: bundle}, nil
}

func (s *signerServer) Keys(context.Context, *creproxy.KeysRequest) (*creproxy.KeysReply, error) {
	offchain := s.bundle.OffchainPublicKey()
	config := s.bundle.ConfigEncryptionPublicKey()

	return &creproxy.KeysReply{
		OffchainPublicKey:         offchain[:],
		ConfigEncryptionPublicKey: config[:],
		OnchainPublicKey:          s.bundle.PublicKey(),
		MaxSignatureLength:        uint32(s.bundle.MaxSignatureLength()), //#nosec G115 - a signature length is small
	}, nil
}

func (s *signerServer) SignOffchain(_ context.Context, req *creproxy.SignOffchainRequest) (*creproxy.SignatureReply, error) {
	signature, err := s.bundle.OffchainSign(req.GetMessage())
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &creproxy.SignatureReply{Signature: signature}, nil
}

func (s *signerServer) ConfigDiffieHellman(_ context.Context, req *creproxy.ConfigDiffieHellmanRequest) (*creproxy.ConfigDiffieHellmanReply, error) {
	var point [32]byte
	if got := len(req.GetPoint()); got != len(point) {
		return nil, status.Errorf(codes.InvalidArgument, "point is %d bytes, want %d", got, len(point))
	}
	copy(point[:], req.GetPoint())

	shared, err := s.bundle.ConfigDiffieHellman(point)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &creproxy.ConfigDiffieHellmanReply{SharedSecret: shared[:]}, nil
}

func (s *signerServer) SignReport(_ context.Context, req *creproxy.SignReportRequest) (*creproxy.SignatureReply, error) {
	var digest [32]byte
	if got := len(req.GetConfigDigest()); got != len(digest) {
		return nil, status.Errorf(codes.InvalidArgument, "config digest is %d bytes, want %d", got, len(digest))
	}
	copy(digest[:], req.GetConfigDigest())

	// Signed through the shared helper rather than the bundle directly: the oracle
	// asking for this signature verifies its peers' with the same code, so what a
	// round's bytes are is decided in one place.
	signature, err := ocr2key.SignOCR3Report(s.bundle, digest, req.GetSeqNr(), req.GetReport())
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &creproxy.SignatureReply{Signature: signature}, nil
}
