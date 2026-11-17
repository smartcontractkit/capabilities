package protofetch

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/reflect/protoregistry"

	_ "github.com/smartcontractkit/chainlink-protos/cre/go/sdk"
	_ "github.com/smartcontractkit/chainlink-protos/cre/go/tools/generator"
	_ "github.com/smartcontractkit/chainlink-protos/cre/go/values/pb"
)

// TestGoPackages checks each fetched proto is generated to the Go package it maps to, by finding the Go types
// registered for it in that package.
func TestGoPackages(t *testing.T) {
	for file, goPkg := range goPackages {
		fd, err := protoregistry.GlobalFiles.FindFileByPath(file)
		require.NoError(t, err, file)
		require.Positive(t, fd.Messages().Len(), file)
		mt, err := protoregistry.GlobalTypes.FindMessageByName(fd.Messages().Get(0).FullName())
		require.NoError(t, err, file)
		assert.Contains(t, typePath(mt.Zero().Interface()), goPkg, file)
	}
}

func TestOutDir(t *testing.T) {
	dir := t.TempDir()
	out, err := OutDir(dir)
	require.NoError(t, err)
	assert.Empty(t, out, "a module without a protofetch.toml fetches nothing")

	require.NoError(t, os.WriteFile(filepath.Join(dir, configName), []byte("proto_out_dir = \"protos/.deps\"\n"), 0o600))
	out, err = OutDir(dir)
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(dir, "protos", ".deps"), out)

	require.NoError(t, os.WriteFile(filepath.Join(dir, configName), []byte("name = \"x\"\n"), 0o600))
	_, err = OutDir(dir)
	assert.ErrorContains(t, err, "must set proto_out_dir")
}

func typePath(v any) string {
	return reflect.TypeOf(v).Elem().PkgPath()
}
