package ecspressoapi

import (
	ecspresso "github.com/kayac/ecspresso/v2"
)

// Version reports the ecspresso library version this provider was
// built against. Sourced from the ecspresso package's own Version
// string so the value matches what `ecspresso version` prints.
func Version() string {
	return ecspresso.Version
}
