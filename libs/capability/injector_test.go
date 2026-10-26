package capability

import (
	"testing"

	"github.com/samber/do/v2"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smartcontractkit/chainlink-common/x/config/cli"
	"github.com/smartcontractkit/chainlink-common/x/config/markup/tomlmarkup"
)

type depB struct{ val string }

type depA struct{ b *depB }

type testConfig struct{ Name string }

// Tests here are not parallel: cli.New sets cobra globals.
func newCmd(t *testing.T) (*cobra.Command, *cli.Binder) {
	t.Helper()

	b, err := cli.New(cli.Options{Markup: tomlmarkup.New()})
	require.NoError(t, err)
	return &cobra.Command{Use: "app", RunE: func(*cobra.Command, []string) error { return nil }}, b
}

func parse(t *testing.T, i *Injector, args ...string) {
	t.Helper()

	cmd, b := newCmd(t)
	require.NoError(t, i.RegisterConfig(cmd, b))
	cmd.SetArgs(args)
	require.NoError(t, cmd.Execute())
}

func TestInjector(t *testing.T) {
	s := NewInjector()
	conf := &testConfig{}
	require.NoError(t, s.ProvideConfig("test", conf))
	require.Error(t, s.ProvideConfig("dup", &testConfig{}))

	got, err := do.Invoke[*testConfig](s.injector)
	require.NoError(t, err)
	assert.Same(t, conf, got)

	parse(t, s, "--test.name", "set")
	assert.Equal(t, "set", conf.Name)

	cmd, b := newCmd(t)
	cmd.PersistentFlags().String(cli.ConfigFlagName, "", "")
	require.Error(t, s.RegisterConfig(cmd, b))
}

func TestInjector_Must(t *testing.T) {
	s := NewInjector()
	// Each provides a different key, so only the second call of each is a duplicate.
	for name, provide := range map[string]func(){
		"Provide":            func() { s.MustProvide(func(do.Injector) (*depB, error) { return &depB{}, nil }) },
		"ProvideValue":       func() { s.MustProvideValue(&depA{}) },
		"ProvideNamed":       func() { s.MustProvideNamed("a", func(do.Injector) (*depA, error) { return &depA{}, nil }) },
		"ProvideNamedValue":  func() { s.MustProvideNamedValue("b", &depB{}) },
		"ProvideConfig":      func() { s.MustProvideConfig("test", &testConfig{}) },
		"ProvideNamedConfig": func() { s.MustProvideNamedConfig("named", "conf", &testConfig{}) },
	} {
		t.Run(name, func(t *testing.T) {
			assert.NotPanics(t, provide)
			assert.Panics(t, provide)
		})
	}
}

func TestInjector_Configs(t *testing.T) {
	s := NewInjector()
	assert.Empty(t, s.Configs())

	conf := &testConfig{}
	named := &testConfig{}
	require.NoError(t, s.ProvideConfig("test", conf))
	require.NoError(t, s.ProvideNamedConfig("named", "conf", named))
	require.NoError(t, s.ProvideValue(&depB{}))

	configs := s.Configs()
	require.Len(t, configs, 2)
	assert.Same(t, conf, configs[0])
	assert.Same(t, named, configs[1])

	clone := s.Clone()
	replacement := &testConfig{}
	require.NoError(t, clone.ProvideConfig("test", replacement))
	assert.Same(t, replacement, clone.Configs()[0])
	assert.Same(t, conf, s.Configs()[0])
}

func TestInjector_Clone(t *testing.T) {
	orig := NewInjector()
	conf := &testConfig{}
	require.NoError(t, orig.ProvideConfig("test", conf))
	require.NoError(t, orig.Provide(func(do.Injector) (*depB, error) { return &depB{val: "original"}, nil }))
	require.NoError(t, orig.Provide(func(i do.Injector) (*depA, error) {
		b, err := do.Invoke[*depB](i)
		return &depA{b: b}, err
	}))

	clone := orig.Clone()
	require.NoError(t, clone.ProvideValue(&depB{val: "override"}))

	a, err := do.Invoke[*depA](clone.Do())
	require.NoError(t, err)
	assert.Equal(t, "override", a.b.val)
	a, err = do.Invoke[*depA](orig.injector)
	require.NoError(t, err)
	assert.Equal(t, "original", a.b.val)

	parse(t, clone, "--test.name", "cloned")
	assert.Equal(t, "cloned", conf.Name)
}

func TestInjector_CloneOverridesConfig(t *testing.T) {
	orig := NewInjector()
	original := &testConfig{}
	require.NoError(t, orig.ProvideConfig("test", original))

	clone := orig.Clone()
	replacement := &testConfig{}
	require.NoError(t, clone.ProvideConfig("test", replacement))

	parse(t, clone, "--test.name", "replaced")
	assert.Equal(t, "replaced", replacement.Name)
	assert.Empty(t, original.Name)
	assert.Same(t, replacement, do.MustInvoke[*testConfig](clone.Do()))
}
