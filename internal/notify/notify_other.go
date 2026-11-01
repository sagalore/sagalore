//go:build !darwin && !windows

package notify

import "os/exec"

// Show uses notify-send when a desktop session has it. The API process and
// Linux CI have neither, and the error is only logged.
func Show(title, body string) error {
	bin, err := exec.LookPath("notify-send")
	if err != nil {
		return err
	}
	return start(exec.Command(bin, clean(title), clean(body)))
}
