package model

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestManifestRoundTripsBinaryIntegrityMetadata(t *testing.T) {
	// This catches an accidental loss or type change in the integrity metadata
	// that the spool reader needs to verify persisted payloads.
	want := Manifest{
		SpoolVersion:   1,
		CaptureVersion: 2,
		CaptureID:      uuid.New(),
		Request: BodyStat{
			ObservedBytes: 99,
			StoredBytes:   64,
			SHA256:        strings.Repeat("a", 64),
			Truncated:     true,
		},
	}

	b, err := json.Marshal(want)
	require.NoError(t, err)

	var got Manifest
	require.NoError(t, json.Unmarshal(b, &got))
	require.Equal(t, want, got)
}
