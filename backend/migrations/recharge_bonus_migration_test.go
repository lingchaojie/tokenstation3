package migrations

import (
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"sort"
	"testing"

	"github.com/stretchr/testify/require"
)

// Deployed migrations are immutable: adding a new upstream migration in the
// historical range can silently alter upgrade ordering or checksum validation.
func TestUpstreamB8deMigrationPolicy(t *testing.T) {
	names, err := fs.Glob(FS, "*.sql")
	require.NoError(t, err)
	sort.Strings(names)
	historical := sha256.New()
	var additions []string
	count := 0
	for _, name := range names {
		if name >= "246_" {
			additions = append(additions, name)
			continue
		}
		data, err := FS.ReadFile(name)
		require.NoError(t, err)
		_, _ = historical.Write([]byte(name + "\x00"))
		_, _ = historical.Write(data)
		_, _ = historical.Write([]byte{0})
		count++
	}
	require.Equal(t, 299, count)
	require.Equal(t, "91d3b89ae4f24abb05733b3b3d8f4a0b98b0d69e5a7d733e982b2e3f9b2bf6f7", hex.EncodeToString(historical.Sum(nil)))
	require.Equal(t, []string{
		"246_add_payment_order_bonus_amount.sql",
		"247_add_typesafe_platform.sql",
	}, additions)
}
