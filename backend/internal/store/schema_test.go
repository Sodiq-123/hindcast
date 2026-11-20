package store

import (
	"strings"
	"testing"
)

func TestSplitSQL(t *testing.T) {
	src := `-- a comment; with a semicolon and it's quoted
CREATE TABLE a (x TEXT DEFAULT 'one;two'); -- trailing; comment
ALTER TABLE a ADD COLUMN IF NOT EXISTS "we;ird" INT;

`
	got := splitSQL(src)
	if len(got) != 2 {
		t.Fatalf("want 2 statements, got %d: %q", len(got), got)
	}
	if !strings.Contains(got[0], "'one;two'") || !strings.Contains(got[1], `"we;ird"`) || strings.Contains(got[0], "comment") {
		t.Fatalf("got %q", got)
	}
}

func TestSchemaFiles(t *testing.T) {
	st, err := schemaStatements()
	if err != nil {
		t.Fatal(err)
	}
	if len(st) < 50 || !strings.HasSuffix(st[0][0], "01_forecasts.sql") {
		t.Fatalf("schema files not loaded in order: %d statements, first from %s", len(st), st[0][0])
	}
	for _, s := range st {
		if strings.HasPrefix(s[1], "CREATE TABLE") && !strings.HasPrefix(s[1], "CREATE TABLE IF NOT EXISTS") {
			t.Errorf("%s: CREATE TABLE without IF NOT EXISTS can't run twice: %.60s", s[0], s[1])
		}
	}
}
