//go:build linux

package service

import (
	"os"
	"os/exec"
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"
)

// Losing Pdeathsig can orphan a capture child after an unexpected parent exit.
func TestConfigureCaptureSidecarCommandSetsParentDeathSIGTERM(t *testing.T) {
	cmd := exec.Command("unused")
	configureCaptureSidecarCommand(cmd)
	require.NotNil(t, cmd.SysProcAttr)
	require.Equal(t, syscall.SIGTERM, cmd.SysProcAttr.Pdeathsig)
}

// OOM adjustment is explicitly best-effort, but when /proc accepts it the
// child must receive the bounded value rather than an unbounded process score.
func TestBestEffortCaptureSidecarOOMScoreWrites500(t *testing.T) {
	previous := captureSidecarWriteOOMScore
	var path string
	var content []byte
	captureSidecarWriteOOMScore = func(name string, data []byte, _ os.FileMode) error {
		path, content = name, append([]byte(nil), data...)
		return os.ErrPermission
	}
	t.Cleanup(func() { captureSidecarWriteOOMScore = previous })

	bestEffortCaptureSidecarOOMScore(123)
	require.Equal(t, "/proc/123/oom_score_adj", path)
	require.Equal(t, []byte("500"), content)
}
