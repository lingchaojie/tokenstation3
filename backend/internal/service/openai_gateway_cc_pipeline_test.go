//go:build unit

package service

import (
	"errors"
	"net/http/httptest"
	"os"
	"runtime"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type stagedConvertedFailingResponseWriter struct {
	gin.ResponseWriter
	accept int
	wrote  int
	err    error
}

func (w *stagedConvertedFailingResponseWriter) Write(p []byte) (int, error) {
	remaining := w.accept - w.wrote
	if remaining <= 0 {
		return 0, w.err
	}
	if remaining > len(p) {
		remaining = len(p)
	}
	n, _ := w.ResponseWriter.Write(p[:remaining])
	w.wrote += n
	return n, w.err
}

func TestStagedConvertedStreamSpilledCommitFailureCleansStageAndClassifiesDelivery(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("first-output staging is intentionally memory-only on Windows")
	}
	gin.SetMode(gin.TestMode)
	sentinel := errors.New("forced downstream write failure")
	cleanupSentinel := errors.New("forced stage cleanup failure")
	for _, tc := range []struct {
		name          string
		acceptedBytes int
		wantCommitted bool
	}{
		{name: "zero bytes remains failover safe", acceptedBytes: 0, wantCommitted: false},
		{name: "partial bytes is committed", acceptedBytes: 1024, wantCommitted: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Exercise enough independent spills to catch an accidentally retained
			// descriptor deterministically without relying on process-global FD counts.
			for iteration := 0; iteration < 32; iteration++ {
				recorder := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(recorder)
				failingWriter := &stagedConvertedFailingResponseWriter{
					ResponseWriter: c.Writer,
					accept:         tc.acceptedBytes,
					err:            sentinel,
				}
				c.Writer = failingWriter

				stage := newDefaultOpenAIFirstOutputStage()
				_, err := stage.WriteString(strings.Repeat("x", openAIFirstOutputStageMemoryLimit+1024))
				require.NoError(t, err)
				require.NotNil(t, stage.tempFile, "fixture must use the disk spill path")
				stage.cleanupErr = cleanupSentinel
				spillFile := stage.tempFile
				spillPath := stage.tempPath
				staged := &stagedConvertedStream{pending: stage}

				err = staged.write(c, func() {}, "", true)

				var clientErr *stagedConvertedClientWriteError
				require.ErrorAs(t, err, &clientErr)
				require.ErrorIs(t, err, sentinel)
				require.ErrorIs(t, err, cleanupSentinel, "CommitTo must not hide stage cleanup errors")
				require.Equal(t, tc.acceptedBytes, failingWriter.wrote)
				require.Equal(t, tc.wantCommitted, staged.committed,
					"commit classification must follow bytes actually delivered downstream")
				require.Nil(t, staged.pending, "the converted stream must release its completed stage")
				require.True(t, stage.closed)
				require.Nil(t, stage.tempFile)
				_, statErr := spillFile.Stat()
				require.ErrorIs(t, statErr, os.ErrClosed, "the retained spill descriptor must be closed deterministically")
				_, pathErr := os.Stat(spillPath)
				require.ErrorIs(t, pathErr, os.ErrNotExist, "the plaintext spill path must remain unlinked")
				require.ErrorIs(t, staged.close(), cleanupSentinel, "cleanup failure must remain observable after delivery failure")
				require.NoError(t, staged.close(), "reported cleanup failure must be taken exactly once")
			}
		})
	}
}

func TestStagedConvertedStreamFullDeliveryCleanupFailureDoesNotBecomeDeliveryFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("first-output staging is intentionally memory-only on Windows")
	}
	gin.SetMode(gin.TestMode)
	payload := strings.Repeat("z", openAIFirstOutputStageMemoryLimit+1024)
	cleanupSentinel := errors.New("forced stage cleanup failure")
	stage := newDefaultOpenAIFirstOutputStage()
	_, err := stage.WriteString(payload)
	require.NoError(t, err)
	require.NotNil(t, stage.tempFile, "fixture must use the disk spill path")
	stage.cleanupErr = cleanupSentinel

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	staged := &stagedConvertedStream{pending: stage}
	headerWrites := 0

	err = staged.write(c, func() { headerWrites++ }, "", true)

	require.NoError(t, err, "fully delivered bytes must not be reclassified as a downstream failure")
	require.True(t, staged.committed)
	require.Equal(t, payload, recorder.Body.String())
	require.Equal(t, 1, headerWrites)
	require.ErrorIs(t, staged.close(), cleanupSentinel,
		"cleanup failure must remain explicitly observable without triggering retry/fallback")
}
