package main

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

// Bootstrap includes the ordinary flag parser and setup detection. A capture
// child must dispatch before either path, even when invoked with --help.
func TestRunServerMainDispatchesCaptureBeforeBootstrap(t *testing.T) {
	previousCommand, previousBootstrap := captureSidecarCommand, captureSidecarBootstrap
	called := 0
	captureSidecarCommand = func(args []string) error {
		called++
		require.Equal(t, []string{"--help"}, args)
		return nil
	}
	captureSidecarBootstrap = func() { t.Fatal("normal bootstrap ran for capture-sidecar") }
	t.Cleanup(func() { captureSidecarCommand, captureSidecarBootstrap = previousCommand, previousBootstrap })

	runServerMain([]string{"server", "capture-sidecar", "--help"})
	require.Equal(t, 1, called)
}

func TestRunServerMainSyncsBeforeSidecarFailureExit(t *testing.T) {
	previousCommand, previousSync, previousExit := captureSidecarCommand, captureSidecarSync, captureSidecarExit
	order := []string{}
	captureSidecarCommand = func([]string) error { return errors.New("failed") }
	captureSidecarSync = func() { order = append(order, "sync") }
	captureSidecarExit = func(code int) { require.Equal(t, 1, code); order = append(order, "exit") }
	t.Cleanup(func() {
		captureSidecarCommand, captureSidecarSync, captureSidecarExit = previousCommand, previousSync, previousExit
	})
	runServerMain([]string{"server", "capture-sidecar"})
	require.Equal(t, []string{"sync", "exit"}, order)
}
