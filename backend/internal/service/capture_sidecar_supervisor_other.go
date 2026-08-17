//go:build !linux

package service

import "os/exec"

func configureCaptureSidecarCommand(*exec.Cmd) {}
func bestEffortCaptureSidecarOOMScore(int)     {}
