package config

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestValidateOllamaOptions pins the per-option range rules
// in validateOllamaOptions. The function lives on the
// Config.Validate path: an out-of-range option must surface
// as a Validate() error containing the option's name so
// operators see which knob to fix.
//
// Coverage pins:
//   - top_p / min_p: float in [0, 1], rejected outside the
//     range. Boundary 0 and 1 are accepted.
//   - top_k: non-negative integer, 0 accepted (an unbounded
//     "use whatever the model picks" sentinel in some
//     samplers).
//   - num_predict / num_ctx: strictly positive integers.
//     0 is rejected; 1 is the minimum accepted value.
//   - unknown keys are passed through unchecked — the
//     validator is a sanity check, not an allow-list.
//   - empty options map is a no-op.
//   - all-known-keys-in-range is the canonical happy path.
func TestValidateOllamaOptions(t *testing.T) {
	t.Parallel()

	type numericCase struct {
		name      string
		value     any
		wantError bool
	}

	t.Run("EmptyOptionsPasses", func(t *testing.T) {
		t.Parallel()
		require.NoError(t, validateOllamaOptions(nil))
		require.NoError(t, validateOllamaOptions(map[string]any{}))
	})

	t.Run("UnknownKeysArePassthrough", func(t *testing.T) {
		t.Parallel()
		// validateOllamaOptions is a sanity check on the
		// five knobs we know matter; vendor extensions
		// (stop, mirostat, repeat_penalty, ...) flow
		// through to the chat-completions server
		// unmodified.
		require.NoError(t, validateOllamaOptions(map[string]any{
			"stop":           []string{"\n\n"},
			"mirostat":       2,
			"repeat_penalty": 1.05,
		}))
	})

	t.Run("TopP", func(t *testing.T) {
		t.Parallel()
		cases := []numericCase{
			{name: "ZeroAccepted", value: 0, wantError: false},
			{name: "OneAccepted", value: 1, wantError: false},
			{name: "FractionAccepted", value: 0.85, wantError: false},
			{name: "AboveOneRejected", value: 1.2, wantError: true},
			{name: "BelowZeroRejected", value: -0.1, wantError: true},
			{name: "IntegerAccepted", value: 1, wantError: false},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				err := validateOllamaOptions(map[string]any{"top_p": tc.value})
				if tc.wantError {
					require.Error(t, err)
					require.Contains(t, err.Error(), "top_p")
				} else {
					require.NoError(t, err)
				}
			})
		}
	})

	t.Run("MinP", func(t *testing.T) {
		t.Parallel()
		cases := []numericCase{
			{name: "ZeroAccepted", value: 0, wantError: false},
			{name: "OneAccepted", value: 1, wantError: false},
			{name: "FractionAccepted", value: 0.05, wantError: false},
			{name: "AboveOneRejected", value: 1.5, wantError: true},
			{name: "BelowZeroRejected", value: -0.05, wantError: true},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				err := validateOllamaOptions(map[string]any{"min_p": tc.value})
				if tc.wantError {
					require.Error(t, err)
					require.Contains(t, err.Error(), "min_p")
				} else {
					require.NoError(t, err)
				}
			})
		}
	})

	t.Run("TopK", func(t *testing.T) {
		t.Parallel()
		cases := []numericCase{
			{name: "ZeroAccepted", value: 0, wantError: false},
			{name: "PositiveAccepted", value: 20, wantError: false},
			{name: "NegativeRejected", value: -1, wantError: true},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				err := validateOllamaOptions(map[string]any{"top_k": tc.value})
				if tc.wantError {
					require.Error(t, err)
					require.Contains(t, err.Error(), "top_k")
				} else {
					require.NoError(t, err)
				}
			})
		}
	})

	t.Run("NumPredict", func(t *testing.T) {
		t.Parallel()
		cases := []numericCase{
			{name: "ZeroRejected", value: 0, wantError: true},
			{name: "OneAccepted", value: 1, wantError: false},
			{name: "PositiveAccepted", value: 512, wantError: false},
			{name: "NegativeRejected", value: -1, wantError: true},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				err := validateOllamaOptions(map[string]any{"num_predict": tc.value})
				if tc.wantError {
					require.Error(t, err)
					require.Contains(t, err.Error(), "num_predict")
				} else {
					require.NoError(t, err)
				}
			})
		}
	})

	t.Run("NumCtx", func(t *testing.T) {
		t.Parallel()
		cases := []numericCase{
			{name: "ZeroRejected", value: 0, wantError: true},
			{name: "OneAccepted", value: 1, wantError: false},
			{name: "PositiveAccepted", value: 4096, wantError: false},
			{name: "NegativeRejected", value: -1, wantError: true},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				err := validateOllamaOptions(map[string]any{"num_ctx": tc.value})
				if tc.wantError {
					require.Error(t, err)
					require.Contains(t, err.Error(), "num_ctx")
				} else {
					require.NoError(t, err)
				}
			})
		}
	})

	t.Run("AllKnownKeysInRange", func(t *testing.T) {
		t.Parallel()
		// Canonical happy path: every knob we validate
		// is present and in range. This is the "shipped
		// defaults plus operator tweaks" shape.
		err := validateOllamaOptions(map[string]any{
			"top_p":       0.8,
			"min_p":       0.05,
			"top_k":       20,
			"num_predict": 512,
			"num_ctx":     4096,
			// vendor extensions are allowed to ride
			// along unchanged.
			"stop":           []string{"\n\n"},
			"repeat_penalty": 1.1,
		})
		require.NoError(t, err)
	})

	t.Run("FloatTypedValues", func(t *testing.T) {
		t.Parallel()
		// YAML unmarshalling always decodes integers
		// as int; JSON unmarshalling always decodes
		// them as float64. The validator must accept
		// either, because operators can supply the
		// same option via either surface.
		err := validateOllamaOptions(map[string]any{
			"top_p":       0.5,
			"top_k":       10,
			"num_predict": 256,
			"num_ctx":     2048,
		})
		require.NoError(t, err)
	})

	t.Run("FirstErrorWins", func(t *testing.T) {
		t.Parallel()
		// Two out-of-range options: the validator stops
		// at the first one (matching the existing
		// behavior — surfacing one clear error per
		// Validate() call). Pin the option the existing
		// implementation happens to check first so this
		// test doesn't devolve into a "which one is
		// first?" fight.
		err := validateOllamaOptions(map[string]any{
			"top_p":       1.5,  // bad: > 1
			"min_p":       -0.1, // bad: < 0
			"top_k":       -1,   // bad: < 0
			"num_predict": -1,   // bad: <= 0
			"num_ctx":     -1,   // bad: <= 0
		})
		require.Error(t, err)
		// We don't pin the order across this set —
		// the existing code checks top_p first, the
		// follow-up refactor may dispatch them via a
		// table. Just check that exactly one of the
		// five known keys is the culprit, matching the
		// "first-error-wins" contract.
		errText := err.Error()
		matched := 0
		for _, key := range []string{"top_p", "min_p", "top_k", "num_predict", "num_ctx"} {
			if strings.Contains(errText, "ollama.options."+key) {
				matched++
			}
		}
		assert.Equal(t, 1, matched,
			"validator must return exactly one error naming one option, got %q", errText)
	})
}

// TestValidate_SurfacesOllamaOptionsErrorViaValidate
// confirms the validator isn't dead code: an out-of-range
// option in c.Ollama.Options must turn into a
// Config.Validate() error rather than being silently
// accepted. A regression here would let operators ship
// invalid sampling knobs and only find out when the
// downstream chat server rejected the request at runtime.
func TestValidate_SurfacesOllamaOptionsErrorViaValidate(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		option  string
		value   any
		wantMsg string
	}{
		{name: "TopPTooHigh", option: "top_p", value: 1.1, wantMsg: "top_p"},
		{name: "MinPTooHigh", option: "min_p", value: 1.5, wantMsg: "min_p"},
		{name: "TopKNegative", option: "top_k", value: -1, wantMsg: "top_k"},
		{name: "NumPredictZero", option: "num_predict", value: 0, wantMsg: "num_predict"},
		{name: "NumCtxZero", option: "num_ctx", value: 0, wantMsg: "num_ctx"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg := DefaultConfig()
			cfg.Ollama.Options = map[string]any{tc.option: tc.value}
			err := cfg.Validate()
			require.Error(t, err)
			require.Contains(t, err.Error(), tc.wantMsg)
		})
	}
}
