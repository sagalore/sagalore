// Package addon embeds the Sagalore addon so the companion can install it.
package addon

import "embed"

//go:embed Sagalore
var Files embed.FS
