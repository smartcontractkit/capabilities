package capability

import (
	"testing"

	"github.com/samber/do/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMultiInjector(t *testing.T) {
	m := MultiInjector{NewInjector(), NewInjector()}

	conf := &testConfig{}
	require.NoError(t, m.ProvideConfig("test", conf))
	require.NoError(t, m.ProvideValue(&depB{val: "b"}))

	for _, i := range m {
		got, err := do.Invoke[*testConfig](i.Do())
		require.NoError(t, err)
		assert.Same(t, conf, got)
		assert.Equal(t, "b", do.MustInvoke[*depB](i.Do()).val)
		assert.Len(t, i.configs, 1)
	}
}

func TestMultiInjectorNamedAndProviders(t *testing.T) {
	m := MultiInjector{NewInjector(), NewInjector()}

	conf := &testConfig{}
	require.NoError(t, m.Provide(func(do.Injector) (*depB, error) { return &depB{val: "b"}, nil }))
	require.NoError(t, m.ProvideNamed("a", func(do.Injector) (*depA, error) { return &depA{}, nil }))
	require.NoError(t, m.ProvideNamedConfig("test", "conf", conf))
	require.NoError(t, m.ProvideNamedValue("b", &depB{val: "named"}))

	for _, i := range m {
		assert.Equal(t, "b", do.MustInvoke[*depB](i.Do()).val)
		assert.NotNil(t, do.MustInvokeNamed[*depA](i.Do(), "a"))
		assert.Same(t, conf, do.MustInvokeNamed[*testConfig](i.Do(), "conf"))
		assert.Equal(t, "named", do.MustInvokeNamed[*depB](i.Do(), "b").val)
	}
}
