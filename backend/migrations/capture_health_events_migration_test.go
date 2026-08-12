package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCaptureHealthEventsMigrationDefinesMinuteBuckets(t *testing.T) {
	content, err := FS.ReadFile("191_capture_health_events.sql")
	require.NoError(t, err)

	sql := strings.Join(strings.Fields(string(content)), " ")
	for _, fragment := range []string{
		"CREATE TABLE IF NOT EXISTS capture_health_events",
		"minute_bucket TIMESTAMPTZ NOT NULL",
		"instance_id VARCHAR(255) NOT NULL",
		"reason VARCHAR(64) NOT NULL",
		"dropped_records BIGINT NOT NULL DEFAULT 0",
		"dropped_bytes BIGINT NOT NULL DEFAULT 0",
		"worker_queue_peak BIGINT NOT NULL DEFAULT 0",
		"writer_queue_peak BIGINT NOT NULL DEFAULT 0",
		"in_flight_bytes_peak BIGINT NOT NULL DEFAULT 0",
		"UNIQUE (minute_bucket, instance_id, reason)",
	} {
		require.Contains(t, sql, fragment)
	}
}
