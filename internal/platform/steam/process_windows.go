package steam

import (
	"os/exec"
	"syscall"
)

func hideHelper(command *exec.Cmd) { command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true} }
