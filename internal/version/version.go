package version

import (
	_ "embed"
	"strings"
)

//go:embed VERSION
var baseline string

// Override is supplied by release builds; source builds use VERSION.
var Override string

func Current() string {
	if Override != "" {
		return Override
	}
	return strings.TrimSpace(baseline)
}
