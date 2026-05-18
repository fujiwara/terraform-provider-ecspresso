package ecspressoapi

import (
	"strings"
	"testing"

	ecspresso "github.com/kayac/ecspresso/v2"
)

func TestVersion(t *testing.T) {
	t.Parallel()
	v := Version()
	if v == "" {
		t.Fatal("Version() returned empty string")
	}
	if !strings.HasPrefix(v, "v") {
		t.Errorf("Version() = %q, expected a version string starting with 'v'", v)
	}
	if v != ecspresso.Version {
		t.Errorf("Version() = %q, want %q (ecspresso.Version)", v, ecspresso.Version)
	}
}
