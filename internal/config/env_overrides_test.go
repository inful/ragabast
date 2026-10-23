package config

import (
	"fmt"
	"reflect"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// envField is one env-tagged config field: the variable name and the
// path to the field inside the Config struct.
type envField struct {
	name  string
	index []int
}

// collectEnvFields walks the Config struct and returns every field
// that carries an `env:"..."` tag.
//
// The walk is what keeps this test honest: a knob that is added to
// the config structs without an override in ApplyEnvOverrides shows
// up here automatically and fails the test.
func collectEnvFields() []envField {
	var out []envField
	var walk func(v reflect.Value, prefix []int)
	walk = func(v reflect.Value, prefix []int) {
		typ := v.Type()
		for i := range typ.NumField() {
			f := typ.Field(i)
			if f.PkgPath != "" {
				continue // unexported
			}
			idx := append(append([]int{}, prefix...), i)
			if tag := f.Tag.Get("env"); tag != "" {
				out = append(out, envField{name: tag, index: idx})
				continue
			}
			if fv := v.Field(i); fv.Kind() == reflect.Struct {
				walk(fv, idx)
			}
		}
	}
	walk(reflect.ValueOf(Config{}), nil)
	return out
}

// envProbe returns an env var value that must change the field, and
// whether the field's type has a probe at all.
//
// The probe is chosen per kind so the same test body works for every
// knob: strings get a marker, ints a value no default uses, bools the
// opposite of the default.
func envProbe(f envField, cfg *Config) (string, bool) {
	field := reflect.ValueOf(cfg).Elem().FieldByIndex(f.index)
	switch field.Interface().(type) {
	case string:
		return "zz-env-probe", true
	case bool:
		return strconv.FormatBool(!field.Bool()), true
	case int:
		return "987654321", true
	case time.Duration:
		return "987654h", true
	case float64:
		return "1.25", true
	case *float64:
		// OLLAMA_TEMPERATURE.
		return "1.25", true
	case []string:
		// Comma-separated list: one element is enough to move
		// the field off its default.
		return "zz-env-probe", true
	case []AuthToken:
		return "zz-env-probe", true
	case []OAuthProvider:
		return `[{"name":"zz-env-probe"}]`, true
	case map[string]any:
		return `{"zz_env_probe":1}`, true
	}
	return "", false
}

// TestApplyEnvOverrides_EveryEnvTaggedFieldIsOverridable pins that
// every knob the config structs advertise through an `env:"..."` tag
// is actually honored by ApplyEnvOverrides.
//
// Ten of them were not: PROCESSING_MAX_CHUNK_SIZE,
// PROCESSING_MIN_CHUNK_SIZE, PROCESSING_CHUNK_OVERLAP,
// SERVER_ENABLE_CORS, SERVER_READ_TIMEOUT, SERVER_WRITE_TIMEOUT,
// SERVER_MCP_HTTP_ENABLED, VECTOR_DB_DIMENSION,
// OLLAMA_EMBEDDING_CONCURRENCY and OLLAMA_EMBEDDING_DIMENSIONS all
// kept their default value no matter what the environment said,
// while the same YAML keys worked. Every one of them is in the
// README's env-var table.
func TestApplyEnvOverrides_EveryEnvTaggedFieldIsOverridable(t *testing.T) {
	for _, f := range collectEnvFields() {
		t.Run(f.name, func(t *testing.T) {
			cfg := DefaultConfig()
			before := fieldSnapshot(cfg, f.index)

			probe, ok := envProbe(f, cfg)
			require.True(t, ok, "no probe for field type %s", f.index)

			t.Setenv(f.name, probe)
			cfg.ApplyEnvOverrides()

			after := fieldSnapshot(cfg, f.index)
			require.NotEqual(t, before, after,
				"env var %s is documented but ApplyEnvOverrides ignores it", f.name)
		})
	}
}

// fieldSnapshot renders a config field as a comparable string.
//
// A string snapshot (rather than the value itself) is what makes the
// comparison reliable: ApplyEnvOverrides merges into the default
// Options map in place, so a value snapshot taken before the call
// would still see the mutation.
func fieldSnapshot(cfg *Config, index []int) string {
	return fmt.Sprintf("%v", reflect.ValueOf(cfg).Elem().FieldByIndex(index).Interface())
}
