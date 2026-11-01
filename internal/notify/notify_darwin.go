//go:build darwin

package notify

import "os/exec"

// Show posts a Notification Center banner. Arguments are passed as argv so a
// saga title cannot break out of the AppleScript string.
func Show(title, body string) error {
	cmd := exec.Command("osascript",
		"-e", "on run argv",
		"-e", "display notification (item 2 of argv) with title (item 1 of argv)",
		"-e", "end run",
		clean(title), clean(body),
	)
	return start(cmd)
}
