// Package envcompat reads SAGALORE_* settings from the process environment.
package envcompat

import (
	"os"
	"strings"
)

// Get returns the trimmed value of name.
func Get(name string) string {
	return strings.TrimSpace(os.Getenv(name))
}
