package production

import (
	"flag"
	"os"
	"path"
	"path/filepath"
	"testing"

	"github.com/bufbuild/protocompile"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/compiler/protogen"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/pluginpb"

	_ "github.com/smartcontractkit/chainlink-protos/cre/go/tools/generator"
)

var update = flag.Bool("update", false, "update the expected production protos in testdata/production")

var testFiles = []string{"client.proto", "shared.proto", "experimental.proto"}

// The production protos' paths, which they are written at and import each other by.
const (
	prodClient       = "capabilities/test/client/v1/client.proto"
	prodShared       = "capabilities/test/client/v1/shared.proto"
	prodExperimental = "capabilities/test/client/v1/experimental.proto"
)

func TestGenerate(t *testing.T) {
	files := run(t, testFiles...)

	if *update {
		require.NoError(t, os.RemoveAll(filepath.Join("testdata", Dir)))
		for name, content := range files {
			require.NoError(t, os.MkdirAll(filepath.Dir(filepath.Join("testdata", name)), 0o755))
			require.NoError(t, os.WriteFile(filepath.Join("testdata", name), []byte(content), 0o600))
		}

		return
	}

	expected := map[string]string{}
	for _, name := range []string{prodClient, prodShared, prodExperimental} {
		b, err := os.ReadFile(filepath.Join("testdata", Dir, filepath.FromSlash(name)))
		require.NoError(t, err)
		expected[path.Join(Dir, name)] = string(b)
	}

	assert.Equal(t, expected, files)
}

func TestGenerateRejectsReleasedPackage(t *testing.T) {
	_, err := request(t, filepath.Join("testdata", "invalid"), "released.proto")
	assert.ErrorContains(t, err, "not a development package")
}

func TestGenerateRejectsUnreleasedZero(t *testing.T) {
	_, err := request(t, filepath.Join("testdata", "invalid"), "zero.proto")
	assert.ErrorContains(t, err, "its first value, LEVEL_UNSPECIFIED, must be released too")
}

func TestGenerateRejectsUnknownFeatures(t *testing.T) {
	_, err := request(t, filepath.Join("testdata", "invalid"), "unknown.proto")
	assert.ErrorContains(t, err, `capabilities.test.unknown.x.v1.Unknown.Get, capabilities.test.unknown.x.v1.Request.id tagged "launch", which is neither released nor in progress`)
	assert.ErrorContains(t, err, `capabilities.test.unknown.x.v1.Request.zone tagged "timezone", which is neither`)
	assert.ErrorContains(t, err, `feature "lanch" is listed but tags nothing`)
	assert.ErrorContains(t, err, `feature "timezones" is listed but tags nothing`)
}

func TestGenerateRejectsUntagged(t *testing.T) {
	_, err := request(t, filepath.Join("testdata", "invalid"), "untagged.proto")
	assert.ErrorContains(t, err, "capabilities.test.untagged.x.v1.Untagged.Get, capabilities.test.untagged.x.v1.Request.forgotten, "+
		"capabilities.test.untagged.x.v1.Level.LEVEL_HIGH have no feature")
	assert.NotContains(t, err.Error(), "LabelsEntry")
}

func TestGenerateRejectsListsInTwoFiles(t *testing.T) {
	_, err := request(t, filepath.Join("testdata", "invalid"), "split_a.proto", "split_b.proto")
	assert.ErrorContains(t, err, "features are listed in split_a.proto and split_b.proto: list them in one file")
}

func TestGenerateRejectsNoLists(t *testing.T) {
	_, err := request(t, "testdata", "experimental.proto")
	assert.ErrorContains(t, err, "no file lists the features")
}

func TestGenerateRejectsReleasedAndInProgress(t *testing.T) {
	_, err := request(t, filepath.Join("testdata", "invalid"), "both.proto")
	assert.ErrorContains(t, err, `feature "launch" is listed more than once`)
}

func run(t *testing.T, files ...string) map[string]string {
	t.Helper()

	out, err := request(t, "testdata", files...)
	require.NoError(t, err)
	return out
}

func request(t *testing.T, dir string, files ...string) (map[string]string, error) {
	t.Helper()

	compiled, err := compiler(dir).Compile(t.Context(), files...)
	require.NoError(t, err)

	req := &pluginpb.CodeGeneratorRequest{FileToGenerate: files}
	seen := map[string]bool{}
	var add func(fd protoreflect.FileDescriptor)
	add = func(fd protoreflect.FileDescriptor) {
		if seen[fd.Path()] {
			return
		}

		seen[fd.Path()] = true
		imports := fd.Imports()
		for i := range imports.Len() {
			add(imports.Get(i).FileDescriptor)
		}

		file := protodesc.ToFileDescriptorProto(fd)
		if file.GetOptions().GetGoPackage() == "" {
			if file.Options == nil {
				file.Options = &descriptorpb.FileOptions{}
			}

			file.Options.GoPackage = new("example.com/" + string(fd.Package().Name()))
		}

		req.ProtoFile = append(req.ProtoFile, file)
	}

	for _, fd := range compiled {
		add(fd)
	}

	// Through bytes, as protoc sends it, so the options are read with the Go types linked in, as the plugin reads them.
	b, err := proto.Marshal(req)
	require.NoError(t, err)

	req = &pluginpb.CodeGeneratorRequest{}
	require.NoError(t, proto.Unmarshal(b, req))
	plugin, err := protogen.Options{}.New(req)
	require.NoError(t, err)
	if err = Generate(plugin); err != nil {
		return nil, err
	}

	res := plugin.Response()
	require.Empty(t, res.GetError())

	out := map[string]string{}
	for _, file := range res.File {
		out[file.GetName()] = file.GetContent()
	}

	return out, nil
}

func compiler(dir string) *protocompile.Compiler {
	return &protocompile.Compiler{
		Resolver: protocompile.WithStandardImports(protocompile.CompositeResolver{
			&protocompile.SourceResolver{ImportPaths: []string{dir}},
			protocompile.ResolverFunc(func(path string) (protocompile.SearchResult, error) {
				fd, err := protoregistry.GlobalFiles.FindFileByPath(path)
				if err != nil {
					return protocompile.SearchResult{}, err
				}

				return protocompile.SearchResult{Desc: fd}, nil
			}),
		}),
		SourceInfoMode: protocompile.SourceInfoStandard,
	}
}
