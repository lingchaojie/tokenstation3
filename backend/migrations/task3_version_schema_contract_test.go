package migrations

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

func TestTask3MigrationVersionMap(t *testing.T) {
	t.Parallel()

	entries, err := FS.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	want := map[int]string{
		229: "229_capture_spool_alert_rules.sql",
		230: "230_channel_image_input_price.sql",
		231: "231_add_usage_log_native_compaction_v2.sql",
		232: "232_add_usage_log_requested_reasoning_effort.sql",
		233: "233_user_restrict_public_groups.sql",
	}
	got := make(map[int][]string)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		var version int
		if _, err := fmt.Sscanf(entry.Name(), "%03d_", &version); err != nil {
			continue
		}
		if version >= 229 {
			got[version] = append(got[version], entry.Name())
		}
	}
	for version, filename := range want {
		files := got[version]
		sort.Strings(files)
		if len(files) != 1 || files[0] != filename {
			t.Errorf("migration %03d: got %v, want [%s]", version, files, filename)
		}
	}
	for version, files := range got {
		if version > 233 {
			continue
		}
		if _, ok := want[version]; !ok {
			t.Errorf("unexpected migration version %03d: %v", version, files)
		}
	}
}

func TestTask3MigrationSQLContracts(t *testing.T) {
	t.Parallel()

	native := task3MigrationSQL(t, "231_add_usage_log_native_compaction_v2.sql")
	requireTask3SQLMatch(t, native, `(?is)ADD\s+COLUMN\s+IF\s+NOT\s+EXISTS\s+native_compaction_v2\s+BOOLEAN\s+NOT\s+NULL\s+DEFAULT\s+FALSE`)
	requireTask3SQLMatch(t, native, `(?is)COMMENT\s+ON\s+COLUMN\s+usage_logs\.native_compaction_v2`)
	if strings.Contains(strings.ToUpper(native), "INDEX") {
		t.Fatalf("native_compaction_v2 migration must not create an index:\n%s", native)
	}

	reasoning := task3MigrationSQL(t, "232_add_usage_log_requested_reasoning_effort.sql")
	requireTask3SQLMatch(t, reasoning, `(?is)ADD\s+COLUMN\s+IF\s+NOT\s+EXISTS\s+requested_reasoning_effort\s+VARCHAR\s*\(\s*20\s*\)`)
	upperReasoning := strings.ToUpper(task3SQLWithoutLineComments(reasoning))
	for _, forbidden := range []string{"NOT NULL", "DEFAULT", "UPDATE USAGE_LOGS", "CREATE INDEX"} {
		if strings.Contains(upperReasoning, forbidden) {
			t.Errorf("requested_reasoning_effort migration contains forbidden %q", forbidden)
		}
	}

	restrict := task3MigrationSQL(t, "233_user_restrict_public_groups.sql")
	requireTask3SQLMatch(t, restrict, `(?is)ADD\s+COLUMN\s+IF\s+NOT\s+EXISTS\s+restrict_public_groups\s+BOOLEAN\s+NOT\s+NULL\s+DEFAULT\s+FALSE`)
	if !strings.Contains(restrict, "user_allowed_groups") {
		t.Error("restrict_public_groups migration must document reuse of user_allowed_groups")
	}
}

func TestTask3EntSourceSchemaContracts(t *testing.T) {
	t.Parallel()

	usage := task3ReadSource(t, "usage_log.go")
	requireTask3SQLMatch(t, usage, `field\.Bool\("native_compaction_v2"\)\.\s*Default\(false\)`)
	requireTask3SQLMatch(t, usage, `field\.String\("requested_reasoning_effort"\)\.\s*MaxLen\(20\)\.\s*Optional\(\)\.\s*Nillable\(\)`)

	user := task3ReadSource(t, "user.go")
	requireTask3SQLMatch(t, user, `field\.Bool\("restrict_public_groups"\)\.\s*Default\(false\)`)
	requireTask3SQLMatch(t, user, `edge\.To\("allowed_groups",\s*Group\.Type\)\.\s*Through\("user_allowed_groups",\s*UserAllowedGroup\.Type\)`)
}

func TestTask3GeneratedEntSchemaContracts(t *testing.T) {
	t.Parallel()

	schema := task3ReadEntGenerated(t, "migrate", "schema.go")
	requireTask3SQLMatch(t, schema, `\{Name:\s*"requested_reasoning_effort",\s*Type:\s*field\.TypeString,\s*Nullable:\s*true,\s*Size:\s*20\}`)
	requireTask3SQLMatch(t, schema, `\{Name:\s*"native_compaction_v2",\s*Type:\s*field\.TypeBool,\s*Default:\s*false\}`)
	requireTask3SQLMatch(t, schema, `\{Name:\s*"restrict_public_groups",\s*Type:\s*field\.TypeBool,\s*Default:\s*false\}`)
}

func task3MigrationSQL(t *testing.T, name string) string {
	t.Helper()
	body, err := FS.ReadFile(name)
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return string(body)
}

func task3ReadSource(t *testing.T, name string) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("..", "ent", "schema", name))
	if err != nil {
		t.Fatalf("read Ent schema %s: %v", name, err)
	}
	return string(body)
}

func task3ReadEntGenerated(t *testing.T, parts ...string) string {
	t.Helper()
	path := filepath.Join(append([]string{"..", "ent"}, parts...)...)
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read generated Ent schema %s: %v", path, err)
	}
	return string(body)
}

func requireTask3SQLMatch(t *testing.T, body, pattern string) {
	t.Helper()
	if !regexp.MustCompile(pattern).MatchString(body) {
		t.Errorf("content does not match %q:\n%s", pattern, body)
	}
}

func task3SQLWithoutLineComments(body string) string {
	lines := strings.Split(body, "\n")
	statements := lines[:0]
	for _, line := range lines {
		if !strings.HasPrefix(strings.TrimSpace(line), "--") {
			statements = append(statements, line)
		}
	}
	return strings.Join(statements, "\n")
}
