package main

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/smartcontractkit/libocr/offchainreporting2plus/ocr3types"

	creproxy "github.com/smartcontractkit/chainlink-protos/cre/impl/proxy"

	"github.com/smartcontractkit/capabilities/crecore/nodekeys"
	"github.com/smartcontractkit/capabilities/libs/standalone/ocr"
)

// signerServer signs on behalf of oracles that hold no keys.
//
// It is the same trade as the endpoint proxies beside it: this process has the
// node's identity, so it lends out what that identity can do rather than the
// identity itself. A capability running in another process decides what to sign
// - it runs the protocol - and this signs it with the node's OCR keys, which are
// the ones the registry lists as one of the DON's signers.
//
// It holds the keys rather than the keyrings, and asks for them when a call comes
// in: whether this node has an OCR key is the caller's business to discover, not a
// reason to refuse to start a process that also proxies a peer and lends chain
// accounts.
type signerServer struct {
	creproxy.UnimplementedSignerServer

	keys nodekeys.Keys
}

var _ creproxy.SignerServer = (*signerServer)(nil)

func newSignerServer(keys nodekeys.Keys) *signerServer {
	return &signerServer{keys: keys}
}

// keyrings is this node's OCR identity, or the reason it has none.
//
// FailedPrecondition rather than Internal: a node with no OCR key is not broken,
// it is a node that was never given one, and the caller is the one that can tell
// the difference.
func (s *signerServer) keyrings(ctx context.Context) (ocr.Keyrings, error) {
	keyrings, err := s.keys.OCR(ctx)
	if err != nil {
		return ocr.Keyrings{}, status.Errorf(codes.FailedPrecondition, "this node has no OCR keys to sign with: %s", err)
	}
	return keyrings, nil
}

func (s *signerServer) Keys(ctx context.Context, _ *creproxy.KeysRequest) (*creproxy.KeysReply, error) {
	keyrings, err := s.keyrings(ctx)
	if err != nil {
		return nil, err
	}

	offchain := keyrings.Offchain.OffchainPublicKey()
	config := keyrings.Offchain.ConfigEncryptionPublicKey()

	return &creproxy.KeysReply{
		OffchainPublicKey:         offchain[:],
		ConfigEncryptionPublicKey: config[:],
		OnchainPublicKey:          keyrings.Onchain.PublicKey(),
		MaxSignatureLength:        uint32(keyrings.Onchain.MaxSignatureLength()), //#nosec G115 - a signature length is small
	}, nil
}

func (s *signerServer) SignOffchain(ctx context.Context, req *creproxy.SignOffchainRequest) (*creproxy.SignatureReply, error) {
	keyrings, err := s.keyrings(ctx)
	if err != nil {
		return nil, err
	}

	signature, err := keyrings.Offchain.OffchainSign(req.GetMessage())
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &creproxy.SignatureReply{Signature: signature}, nil
}

func (s *signerServer) ConfigDiffieHellman(ctx context.Context, req *creproxy.ConfigDiffieHellmanRequest) (*creproxy.ConfigDiffieHellmanReply, error) {
	var point [32]byte
	if got := len(req.GetPoint()); got != len(point) {
		return nil, status.Errorf(codes.InvalidArgument, "point is %d bytes, want %d", got, len(point))
	}
	copy(point[:], req.GetPoint())

	keyrings, err := s.keyrings(ctx)
	if err != nil {
		return nil, err
	}

	shared, err := keyrings.Offchain.ConfigDiffieHellman(point)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &creproxy.ConfigDiffieHellmanReply{SharedSecret: shared[:]}, nil
}

func (s *signerServer) SignReport(ctx context.Context, req *creproxy.SignReportRequest) (*creproxy.SignatureReply, error) {
	var digest [32]byte
	if got := len(req.GetConfigDigest()); got != len(digest) {
		return nil, status.Errorf(codes.InvalidArgument, "config digest is %d bytes, want %d", got, len(digest))
	}
	copy(digest[:], req.GetConfigDigest())

	keyrings, err := s.keyrings(ctx)
	if err != nil {
		return nil, err
	}

	// Signed through the keyring rather than by reaching for the key: the oracle
	// asking for this signature verifies its peers' with the same keyring, so what a
	// round's bytes are is decided in one place.
	signature, err := keyrings.Onchain.Sign(digest, req.GetSeqNr(), ocr3types.ReportWithInfo[[]byte]{Report: req.GetReport()})
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &creproxy.SignatureReply{Signature: signature}, nil
}
