//go:build unit

package service

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/capture/model"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestRecordReconstructingUnitTransportCapsEachBodyDirectionAt32MiB(t *testing.T) {
	records := make(chan *CaptureRecord, 1)
	pool := NewConversationCapturePoolForUnitTest(records)
	t.Cleanup(pool.Stop)
	begin := model.Begin{
		CaptureID: uuid.New(),
		Platform:  PlatformAnthropic,
		Policy: model.ContentPolicy{
			StoreRequestBody:  true,
			StoreResponseBody: true,
		},
	}
	attempt, ok := pool.Begin(context.Background(), begin)
	require.True(t, ok)
	chunk := make([]byte, 1<<20)
	for i := 0; i < 40; i++ {
		require.True(t, attempt.WriteRequest(chunk))
		require.True(t, attempt.WriteResponse(chunk))
	}
	require.True(t, attempt.Finalize(model.Final{HTTPStatus: 200, ResponseComplete: true}))
	require.True(t, attempt.Commit())

	record := <-records
	require.Len(t, record.RawRequest, 32<<20)
	require.Len(t, record.RawResponse, 32<<20)
	require.True(t, record.Truncated)
}

func TestRecordReconstructingUnitTransportPublishesOnlyCommittedAttempt(t *testing.T) {
	records := make(chan *CaptureRecord, 1)
	pool := NewConversationCapturePoolForUnitTest(records)
	t.Cleanup(pool.Stop)
	begin := model.Begin{CaptureID: uuid.New(), Platform: PlatformOpenAI, Policy: model.ContentPolicy{StoreResponseBody: true}}
	aborted, ok := pool.Begin(context.Background(), begin)
	require.True(t, ok)
	require.True(t, aborted.WriteResponse([]byte("aborted")))
	aborted.Abort()

	begin.CaptureID = uuid.New()
	committed, ok := pool.Begin(context.Background(), begin)
	require.True(t, ok)
	require.True(t, committed.WriteResponse([]byte("committed")))
	require.True(t, committed.Finalize(model.Final{HTTPStatus: 503, ResponseComplete: true}))
	require.True(t, committed.Commit())

	select {
	case record := <-records:
		require.Equal(t, []byte("committed"), record.RawResponse)
		require.Equal(t, 503, record.HTTPStatus)
	default:
		t.Fatal("committed attempt was not reconstructed synchronously")
	}
	select {
	case extra := <-records:
		t.Fatalf("aborted attempt unexpectedly published: %+v", extra)
	default:
	}
}
