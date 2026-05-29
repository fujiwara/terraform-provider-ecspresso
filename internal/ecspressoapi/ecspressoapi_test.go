package ecspressoapi

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	ecspresso "github.com/kayac/ecspresso/v2"
)

func TestIsConfigLoadError(t *testing.T) {
	base := errors.New("failed to evaluate jsonnet: foo is not found in tfstate")

	if IsConfigLoadError(base) {
		t.Errorf("a plain error must not be classified as a config load error")
	}
	if IsConfigLoadError(nil) {
		t.Errorf("nil must not be classified as a config load error")
	}

	cle := &ConfigLoadError{err: base}
	if !IsConfigLoadError(cle) {
		t.Errorf("ConfigLoadError must be recognised")
	}
	// Preserves the wrapped message and unwraps for errors.Is/As.
	if cle.Error() != base.Error() {
		t.Errorf("Error() should surface the wrapped message, got %q", cle.Error())
	}
	if !errors.Is(cle, base) {
		t.Errorf("errors.Is should reach the wrapped error")
	}
	// Survives further wrapping with %w.
	if !IsConfigLoadError(fmt.Errorf("describe failed: %w", cle)) {
		t.Errorf("IsConfigLoadError must see through additional %%w wrapping")
	}
}

func TestFuncPrefixWarning(t *testing.T) {
	tfstate := func(prefix string) ecspresso.ConfigPlugin {
		return ecspresso.ConfigPlugin{Name: "tfstate", FuncPrefix: prefix}
	}

	tests := []struct {
		name      string
		plugins   []ecspresso.ConfigPlugin
		prefix    string
		wantWarns bool
	}{
		{
			// Plugins-less mode (kayac/ecspresso#1031): the injected
			// instance is the only tfstate source. Not a mistake.
			name:      "no tfstate plugin declared",
			plugins:   nil,
			prefix:    "",
			wantWarns: false,
		},
		{
			name:      "single plugin, default prefix matches",
			plugins:   []ecspresso.ConfigPlugin{tfstate("")},
			prefix:    "",
			wantWarns: false,
		},
		{
			name:      "single plugin, custom prefix matches",
			plugins:   []ecspresso.ConfigPlugin{tfstate("tf_")},
			prefix:    "tf_",
			wantWarns: false,
		},
		{
			// Case B from the manual repro: the config's tfstate plugin
			// uses tf_ but tfstate_values is injected at the default "".
			// The values silently never reach tf_tfstate(...).
			name:      "single plugin, prefix mismatch",
			plugins:   []ecspresso.ConfigPlugin{tfstate("tf_")},
			prefix:    "",
			wantWarns: true,
		},
		{
			// Case A: the injected default prefix matches one plugin; a
			// second, prefixed plugin reads its own file. Intended.
			name:      "multiple plugins, injected prefix matches one",
			plugins:   []ecspresso.ConfigPlugin{tfstate(""), tfstate("net_")},
			prefix:    "",
			wantWarns: false,
		},
		{
			name:      "multiple plugins, none matches",
			plugins:   []ecspresso.ConfigPlugin{tfstate("a_"), tfstate("b_")},
			prefix:    "",
			wantWarns: true,
		},
		{
			// Non-tfstate plugins must never trigger the warning.
			name:      "only non-tfstate plugins declared",
			plugins:   []ecspresso.ConfigPlugin{{Name: "cfn"}, {Name: "ssm"}},
			prefix:    "",
			wantWarns: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := funcPrefixWarning(tt.plugins, tt.prefix)
			if tt.wantWarns && got == "" {
				t.Fatalf("expected a warning, got none")
			}
			if !tt.wantWarns && got != "" {
				t.Fatalf("expected no warning, got: %s", got)
			}
			if tt.wantWarns && !strings.Contains(got, "tfstate_func_prefix") {
				t.Errorf("warning should mention tfstate_func_prefix, got: %s", got)
			}
		})
	}
}
