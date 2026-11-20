package remote

import (
	"errors"
	"fmt"
	"math"

	ocrtypes "github.com/smartcontractkit/libocr/offchainreporting2plus/types"

	"github.com/smartcontractkit/capabilities/libs/capability/keystore/ocr/remote/pb"
)

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
		ConfigDigest: digest, Epoch: rc.Epoch, Round: uint8(rc.Round),
		ExtraHash: extraHash,
	}, nil
}
