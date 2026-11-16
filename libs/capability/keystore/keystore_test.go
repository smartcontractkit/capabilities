package keystore

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"

	"github.com/smartcontractkit/capabilities/libs/capability/keystore/ocr/ocrtest"
	"github.com/smartcontractkit/capabilities/libs/capability/keystore/ocr/remote/pb"
)

func TestRegisterServers_SkipsNilKeystores(t *testing.T) {
	t.Parallel()
	s := grpc.NewServer()
	RegisterServers(s, logger.Test(t), Keystores{})
	assert.Empty(t, s.GetServiceInfo())

	RegisterServers(s, logger.Test(t), Keystores{OCR: ocrtest.Keystore{}})
	assert.Contains(t, s.GetServiceInfo(), pb.OCRKeystore_ServiceDesc.ServiceName)
}
