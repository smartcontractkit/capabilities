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
	"google.golang.org/protobuf/types/dynamicpb"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/pluginpb"

	_ "github.com/smartcontractkit/chainlink-protos/cre/go/tools/generator"

	"github.com/smartcontractkit/capabilities/libs/capability/feature"
)

var update = flag.Bool("update", false, "update the expected production protos in testdata/production")

var testFiles = []string{"client.proto", "shared.proto", "experimental.proto"}

// The production protos' paths, which they are written at and import each other by.
const (
	prodClient       = "capabilities/test/client/v1/client.proto"
	prodShared       = "capabilities/test/client/v1/shared.proto"
	prodExperimental = "capabilities/test/client/v1/experimental.proto"
)

// TestGenerate checks the production protos of testdata against testdata/production.
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

// TestProductionCompiles checks the production protos compile on their own, as the SDKs compile them, with the
// production directory as their include path. That includes a file with nothing in production.
func TestProductionCompiles(t *testing.T) {
	compiled, err := compiler(writeProduction(t)).Compile(t.Context(), prodClient, prodShared, prodExperimental)
	require.NoError(t, err)
	for _, fd := range compiled {
		assert.Equal(t, protoreflect.FullName("capabilities.test.client.v1"), fd.Package())
		assert.Empty(t, fd.Options().(*descriptorpb.FileOptions).GetGoPackage(), "the SDKs choose their own")
	}
}

// TestGenerateNothingInProduction checks a file with nothing in production still has a production file, with its
// services and none of their methods, so the production protos exist from a capability's first generation.
func TestGenerateNothingInProduction(t *testing.T) {
	files := run(t, "experimental.proto")
	require.Len(t, files, 1)

	compiled, err := compiler(writeFiles(t, files)).Compile(t.Context(), prodExperimental)
	require.NoError(t, err)
	assert.Zero(t, compiled[0].Messages().Len())
	require.Equal(t, 1, compiled[0].Services().Len())
	assert.Zero(t, compiled[0].Services().Get(0).Methods().Len())
}

// TestGenerateEmptyFile checks a file with nothing in it at all generates a valid production file.
func TestGenerateEmptyFile(t *testing.T) {
	files := run(t, "empty.proto")
	compiled, err := compiler(writeFiles(t, files)).Compile(t.Context(), "capabilities/test/client/v1/empty.proto")
	require.NoError(t, err)
	assert.Equal(t, protoreflect.FullName("capabilities.test.client.v1"), compiled[0].Package())
}

func writeProduction(t *testing.T) string {
	t.Helper()
	return writeFiles(t, run(t, testFiles...))
}

// writeFiles writes what the plugin wrote to a directory, returning the directory the production protos are in.
func writeFiles(t *testing.T, files map[string]string) string {
	t.Helper()

	dir := t.TempDir()
	for name, content := range files {
		file := filepath.Join(dir, filepath.FromSlash(name))
		require.NoError(t, os.MkdirAll(filepath.Dir(file), 0o755))
		require.NoError(t, os.WriteFile(file, []byte(content), 0o600))
	}

	return filepath.Join(dir, Dir)
}

// TestProductionAny checks that a production message is read as its development message through feature's Any
// wrappers, and that the reply is written back as production, so a production SDK can talk to a capability built
// from the development protos.
func TestProductionAny(t *testing.T) {
	compiled, err := compiler(writeProduction(t)).Compile(t.Context(), prodClient)
	require.NoError(t, err)
	prodRequest := dynamicpb.NewMessage(compiled[0].Messages().ByName("Request"))
	prodRequest.Set(prodRequest.Descriptor().Fields().ByName("message"), protoreflect.ValueOfString("hello"))

	dev, err := compiler("testdata").Compile(t.Context(), "client.proto")
	require.NoError(t, err)
	devRequest := dynamicpb.NewMessage(dev[0].Messages().ByName("Request"))

	in, err := anypb.New(prodRequest)
	require.NoError(t, err)
	production, err := feature.UnmarshalAny(in, devRequest)
	require.NoError(t, err)
	assert.True(t, production)
	assert.Equal(t, "hello", devRequest.Get(devRequest.Descriptor().Fields().ByName("message")).String())

	out, err := feature.MarshalAny(devRequest, production)
	require.NoError(t, err)
	assert.Equal(t, prodRequest.Descriptor().FullName(), out.MessageName())
	again := dynamicpb.NewMessage(prodRequest.Descriptor())
	require.NoError(t, out.UnmarshalTo(again))
	assert.True(t, proto.Equal(prodRequest, again))
}

// TestGenerateRejectsReleasedPackage checks the development protos are in a development package: the production
// package is the development one without its x, so a development one without it would be the production one.
func TestGenerateRejectsReleasedPackage(t *testing.T) {
	_, err := request(t, filepath.Join("testdata", "invalid"), "released.proto")
	assert.ErrorContains(t, err, "not a development package")
}

// TestGenerateReleasesWholeFeatures checks a released feature is in production everywhere it tags, across files, and
// an unreleased one nowhere.
func TestGenerateReleasesWholeFeatures(t *testing.T) {
	compiled, err := compiler(writeProduction(t)).Compile(t.Context(), prodClient, prodShared)
	require.NoError(t, err)

	client, shared := compiled[0], compiled[1]
	request := client.Messages().ByName("Request")
	assert.NotNil(t, request.Fields().ByName("message"), "launch")
	assert.NotNil(t, request.Fields().ByName("zone"), "timezones in client.proto")
	assert.NotNil(t, shared.Messages().ByName("Shared").Fields().ByName("zone"), "timezones in shared.proto")
	methods := client.Services().ByName("Client").Methods()
	assert.NotNil(t, methods.ByName("Execute"), "launch")
	assert.NotNil(t, methods.ByName("Zones"), "timezones")
	assert.NotNil(t, client.Messages().ByName("Retired"), "a message with a released field")

	assert.Nil(t, request.Fields().ByName("at"), "new_field")
	assert.Nil(t, shared.Messages().ByName("Shared").Fields().ByName("later"), "new_field in shared.proto")
	assert.Nil(t, methods.ByName("NewMethod"), "new_method")
	assert.Nil(t, client.Messages().ByName("NewRequest"), "only an unreleased method uses it")
	assert.Nil(t, client.Messages().ByName("Hidden"), "only an unreleased field uses it")
	assert.Nil(t, request.Messages().ByName("Unused"))

	values := client.Enums().ByName("Level").Values()
	assert.NotNil(t, values.ByName("LEVEL_HIGH"), "launch")
	assert.NotNil(t, values.ByName("LEVEL_MEDIUM"), "timezones")
	assert.Nil(t, values.ByName("LEVEL_LOW"), "new_field")
}

// TestGenerateRejectsUnreleasedZero checks a released enum's zero value is released too, as proto3 needs it.
func TestGenerateRejectsUnreleasedZero(t *testing.T) {
	_, err := request(t, filepath.Join("testdata", "invalid"), "zero.proto")
	assert.ErrorContains(t, err, "its first value, LEVEL_UNSPECIFIED, must be released too")
}

// TestGenerateRejectsMisspeltFeatures checks every feature tagged is listed, and every feature listed is tagged, so
// a misspelt name on either side fails rather than leaving what it tags unreleased.
func TestGenerateRejectsMisspeltFeatures(t *testing.T) {
	_, err := request(t, filepath.Join("testdata", "invalid"), "misspelt.proto")
	assert.ErrorContains(t, err, `capabilities.test.misspelt.x.v1.Misspelt.Get, capabilities.test.misspelt.x.v1.Request.id tagged "launch", which is neither released nor in progress`)
	assert.ErrorContains(t, err, `capabilities.test.misspelt.x.v1.Request.zone tagged "timezone", which is neither`)
	assert.ErrorContains(t, err, `feature "lanch" is listed but tags nothing`)
	assert.ErrorContains(t, err, `feature "timezones" is listed but tags nothing`)
}

func TestGenerateRejectsReleasedAndInProgress(t *testing.T) {
	_, err := request(t, filepath.Join("testdata", "invalid"), "both.proto")
	assert.ErrorContains(t, err, `feature "launch" is both released and in progress`)
}

func run(t *testing.T, files ...string) map[string]string {
	t.Helper()

	out, err := request(t, "testdata", files...)
	require.NoError(t, err)
	return out
}

// request compiles files from dir, then runs the plugin on them as protoc would, returning what it writes.
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

// compiler compiles protos from dir, with the feature and cre_metadata protos linked in, as they are into
// protoc-gen-cre.
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
