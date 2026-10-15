package capability

import (
	"encoding"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"time"

	"github.com/go-viper/mapstructure/v2"
	"github.com/iancoleman/strcase"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"

	commonconfig "github.com/smartcontractkit/chainlink-common/pkg/config"
)

var envPrefixes = []string{"CRE", "CL"}

var (
	durationType        = reflect.TypeFor[time.Duration]()
	configDurationType  = reflect.TypeFor[commonconfig.Duration]()
	textUnmarshalerType = reflect.TypeFor[encoding.TextUnmarshaler]()
)

type boundField struct {
	name string
	key  string
	envs []string
	flag *pflag.Flag
}

// set reports whether the user supplied a value. Unset fields keep whatever target held when it
// was bound, rather than a default round-tripped through its text form - a SecretString's text
// form is redacted.
func (f boundField) set(v *viper.Viper) bool {
	if f.flag.Changed || v.InConfig(f.key) {
		return true
	}
	for _, env := range f.envs {
		if _, ok := os.LookupEnv(env); ok {
			return true
		}
	}
	return false
}

// bindFlags binds the exported fields of the struct target points at as persistent flags on cmd,
// named namespace.kebab-case-field, and fills them in before cmd runs from, in order of
// precedence, the flag, the CRE_/CL_ env var, or the config file.
func bindFlags(cmd *cobra.Command, namespace string, target any) error {
	rv := reflect.ValueOf(target)
	if rv.Kind() != reflect.Pointer || rv.Elem().Kind() != reflect.Struct {
		return fmt.Errorf("can only bind flags to a pointer to a struct, got %T", target)
	}
	elem := rv.Elem()

	v := viper.New()
	var fields []boundField
	for i := range elem.NumField() {
		sf := elem.Type().Field(i)
		if !sf.IsExported() || sf.Anonymous {
			continue
		}

		key := namespace + "." + strcase.ToKebab(sf.Name)
		flag, err := addFlag(cmd.PersistentFlags(), key, sf.Tag.Get("usage"), elem.Field(i))
		if err != nil {
			return err
		}
		if err := v.BindPFlag(key, flag); err != nil {
			return err
		}

		suffix := strings.ToUpper(strings.NewReplacer(".", "_", "-", "_").Replace(key))
		envs := make([]string, len(envPrefixes))
		for j, p := range envPrefixes {
			envs[j] = p + "_" + suffix
		}
		if err := v.BindEnv(append([]string{key}, envs...)...); err != nil {
			return err
		}

		fields = append(fields, boundField{name: sf.Name, key: key, envs: envs, flag: flag})
	}

	prev := cmd.PersistentPreRunE
	cmd.PersistentPreRunE = func(c *cobra.Command, args []string) error {
		if err := readConfigFile(c, v); err != nil {
			return err
		}
		values := map[string]any{}
		for _, f := range fields {
			if f.set(v) {
				values[f.name] = v.Get(f.key)
			}
		}
		if err := decode(values, target); err != nil {
			return fmt.Errorf("invalid %s settings: %w", namespace, err)
		}
		if prev != nil {
			return prev(c, args)
		}
		return nil
	}
	return nil
}

func addFlag(fs *pflag.FlagSet, name, usage string, field reflect.Value) (*pflag.Flag, error) {
	t := field.Type()
	switch {
	case t == configDurationType:
		fs.Duration(name, field.Interface().(commonconfig.Duration).Duration(), usage)
	case t == durationType:
		fs.Duration(name, time.Duration(field.Int()), usage)
	case reflect.PointerTo(t).Implements(textUnmarshalerType):
		var def string
		if !field.IsZero() {
			def = fmt.Sprint(field.Interface())
		}
		fs.String(name, def, usage)
	case t.Kind() == reflect.Slice && t.Elem().Kind() == reflect.String:
		fs.StringSlice(name, field.Interface().([]string), usage)
	case t.Kind() == reflect.String:
		fs.String(name, field.String(), usage)
	case t.Kind() == reflect.Bool:
		fs.Bool(name, field.Bool(), usage)
	case t.Kind() >= reflect.Int && t.Kind() <= reflect.Int64:
		fs.Int64(name, field.Int(), usage)
	case t.Kind() >= reflect.Uint && t.Kind() <= reflect.Uint64:
		fs.Uint64(name, field.Uint(), usage)
	case t.Kind() == reflect.Float32 || t.Kind() == reflect.Float64:
		fs.Float64(name, field.Float(), usage)
	default:
		return nil, fmt.Errorf("cannot bind %s: unsupported type %s", name, t)
	}
	return fs.Lookup(name), nil
}

// readConfigFile reads --config, or ./config.toml if it exists when --config is unset.
func readConfigFile(cmd *cobra.Command, v *viper.Viper) error {
	path, _ := cmd.Flags().GetString("config")
	if path != "" {
		v.SetConfigFile(path)
		if err := v.ReadInConfig(); err != nil {
			return fmt.Errorf("failed to read config file %q: %w", path, err)
		}
		return nil
	}

	v.AddConfigPath(".")
	v.SetConfigName("config")
	v.SetConfigType("toml")
	if err := v.ReadInConfig(); err != nil {
		if errors.As(err, &viper.ConfigFileNotFoundError{}) {
			return nil
		}
		return fmt.Errorf("failed to parse config file: %w", err)
	}
	return nil
}

// decode is weakly typed because env vars, and the flags viper does not parse itself (durations,
// unsigned ints), arrive as strings.
func decode(values map[string]any, target any) error {
	d, err := mapstructure.NewDecoder(&mapstructure.DecoderConfig{
		Result:           target,
		WeaklyTypedInput: true,
		DecodeHook: mapstructure.ComposeDecodeHookFunc(
			mapstructure.StringToTimeDurationHookFunc(),
			mapstructure.StringToSliceHookFunc(","),
			mapstructure.TextUnmarshallerHookFunc(),
		),
	})
	if err != nil {
		return err
	}
	return d.Decode(values)
}
