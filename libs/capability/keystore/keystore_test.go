package keystore

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc"

	"github.com/smartcontractkit/capabilities/libs/capability/keystore/ocr/ocrtest"
	"github.com/smartcontractkit/capabilities/libs/capability/keystore/ocr/remote/pb"
)

func TestRegisterServers_SkipsNilKeystores(t *testing.T) {
	t.Parallel()
	s := grpc.NewServer()
	RegisterServers(s, Keystores{})
	assert.Empty(t, s.GetServiceInfo())

	RegisterServers(s, Keystores{OCR: ocrtest.Keystore{}})
	assert.Contains(t, s.GetServiceInfo(), pb.OCRKeystore_ServiceDesc.ServiceName)
}
