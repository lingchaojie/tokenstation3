//go:build linux

package service

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
)

var captureSidecarWriteOOMScore = os.WriteFile

func configureCaptureSidecarCommand(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGTERM}
}

func bestEffortCaptureSidecarOOMScore(pid int) {
	_ = captureSidecarWriteOOMScore(filepath.Join("/proc", strconv.Itoa(pid), "oom_score_adj"), []byte("500"), 0o644)
}
