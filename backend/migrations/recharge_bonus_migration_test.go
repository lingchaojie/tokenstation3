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
func TestHistoricalMigrationsAndUpstreamLayout(t *testing.T) {
	names, err := fs.Glob(FS, "*.sql")
	require.NoError(t, err)
	sort.Strings(names)
	historical := sha256.New()
	var additions []string
	count := 0
	for _, name := range names {
		if name >= "249_" {
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
	require.Equal(t, 302, count)
	require.Equal(t, "8ac079facfda532238723548f6ccc8d6d6f6a1fdce4294d14c3d2c51e5200b63", hex.EncodeToString(historical.Sum(nil)))
	require.Equal(t, []string{
		"249_group_i18n.sql",
		"250_scoped_admin_api_keys.sql",
		"251_add_usage_logs_api_key_cost_index_notx.sql",
		"252_group_force_openai_ultrafast.sql",
		"253_group_usage_rollup_nonblocking_build.sql",
		"254_channel_pricing_ultrafast_multiplier.sql",
	}, additions)
}
