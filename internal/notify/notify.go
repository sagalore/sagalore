// Package notify shows a desktop notification on macOS and Windows.
// The companion cannot type into WoW, so this is how the player hears that
// /reload is needed.
package notify

import (
	"os/exec"
	"strings"
	"unicode/utf8"
)

func clean(s string) string {
	s = strings.NewReplacer("\r\n", " ", "\n", " ", "\r", " ").Replace(s)
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) <= 240 {
		return s
	}
	return string([]rune(s)[:240])
}

func start(cmd *exec.Cmd) error {
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}
