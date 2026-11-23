package db

import (
	"fmt"
	"io/fs"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// Updates roll forward by migrating first and restarting second, and roll
// back by starting the previous build on the newer schema (docs/13 §2). Both
// need every migration to be additive ("expand"): while it runs, and after a
// rollback, an older core is serving on the new schema. These statements
// break an older core, so a migration that uses one must say why and how
// (a "-- contract: <plan>" line), and ship at least one release after the
// release that stopped using what it removes (docs/13 §2, "Backward
// compatibility").
var contracting = []struct {
	re   *regexp.Regexp
	what string
}{
	{regexp.MustCompile(`(?i)\bdrop\s+(table|column|view|type|schema|sequence)\b`), "drops something an older build may still read"},
	{regexp.MustCompile(`(?i)\balter\s+table\s+\S+\s+drop\s+(?:if\s+exists\s+)?[a-z_"]+\s*[,;]`), "drops a column an older build may still read"},
	{regexp.MustCompile(`(?i)\brename\b`), "renames something an older build still uses by its old name"},
	{regexp.MustCompile(`(?i)\balter\s+column\s+\S+\s+(set\s+data\s+)?type\b`), "changes a column's type under an older build"},
	{regexp.MustCompile(`(?i)\balter\s+column\s+\S+\s+set\s+not\s+null\b`), "refuses rows an older build still writes"},
	{regexp.MustCompile(`(?i)\btruncate\b`), "deletes data"},
}

// addColumn is a column added in an ALTER TABLE, up to the next comma or
// semicolon. NOT NULL with no DEFAULT refuses an older build's inserts.
var addColumn = regexp.MustCompile(`(?i)\badd\s+column\b[^,;]*`)

// breaks says what in sql would break an older build, if anything.
func breaks(sql string) []string {
	var out []string
	for _, c := range contracting {
		if m := c.re.FindString(sql); m != "" {
			out = append(out, fmt.Sprintf("%s (%q)", c.what, strings.TrimSpace(m)))
		}
	}
	for _, m := range addColumn.FindAllString(sql, -1) {
		l := strings.ToLower(m)
		if regexp.MustCompile(`\bnot\s+null\b`).MatchString(l) && !strings.Contains(l, "default") {
			out = append(out, fmt.Sprintf("adds a NOT NULL column with no default, which an older build's inserts leave empty (%q)", strings.TrimSpace(m)))
		}
	}
	return out
}

// Migrations that contract and shipped before this test. Not to be added to.
var grandfathered = map[string]string{
	"0009_people_group.sql": "renamed people.grp to group_name before any installation ran a release",
}

var lineComment = regexp.MustCompile(`--[^\n]*`)

func TestMigrationsOnlyAdd(t *testing.T) {
	names, err := fs.Glob(migrations, "migrations/*.sql")
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range names {
		name := strings.TrimPrefix(path, "migrations/")
		raw, err := migrations.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := grandfathered[name]; ok || strings.Contains(string(raw), "-- contract:") {
			continue
		}
		for _, b := range breaks(lineComment.ReplaceAllString(string(raw), "")) {
			t.Errorf("%s %s. Make it additive, or add a `-- contract: <why, and which release stopped using it>` line", name, b)
		}
	}
}

func TestContractCheckCatchesBreakingChanges(t *testing.T) {
	breaking := []string{
		"DROP TABLE people;",
		"ALTER TABLE people DROP COLUMN email;",
		"ALTER TABLE people DROP email;",
		"ALTER TABLE people RENAME COLUMN a TO b;",
		"ALTER TABLE people ALTER COLUMN score TYPE bigint;",
		"ALTER TABLE people ALTER COLUMN email SET NOT NULL;",
		"ALTER TABLE people ADD COLUMN nick text NOT NULL;",
		"TRUNCATE people;",
	}
	additive := []string{
		"CREATE TABLE x (id int);",
		"ALTER TABLE people ADD COLUMN nick text;",
		"ALTER TABLE people ADD COLUMN nick text NOT NULL DEFAULT '';",
		"ALTER TABLE people DROP CONSTRAINT people_email_key;",
		"DROP INDEX people_idx;",
		"DROP FUNCTION am_x(bytea);",
		"CREATE INDEX people_idx ON people (lower(email));",
	}
	hit := func(sql string) bool { return len(breaks(sql)) > 0 }
	for _, s := range breaking {
		if !hit(s) {
			t.Errorf("not caught: %s", s)
		}
	}
	for _, s := range additive {
		if hit(s) {
			t.Errorf("wrongly caught: %s", s)
		}
	}
}

// Migrations run in name order, on a fresh database and on one that has
// every earlier release's. A new migration that sorted before one already
// shipped would run in a different order on the two (CI also refuses a
// changed or deleted migration: .github/workflows/ci.yml).
func TestMigrationNamesAreOrdered(t *testing.T) {
	names, _ := fs.Glob(migrations, "migrations/*.sql")
	if !sort.StringsAreSorted(names) {
		t.Fatal("migrations are not in name order")
	}
	shape := regexp.MustCompile(`^migrations/\d{4}_[a-z0-9_]+\.sql$`)
	for _, n := range names {
		if !shape.MatchString(n) {
			t.Errorf("%s: name migrations NNNN_what.sql", n)
		}
	}
}
