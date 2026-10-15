//go:build !windows

package steam

import "os/exec"

func hideHelper(command *exec.Cmd) {}
