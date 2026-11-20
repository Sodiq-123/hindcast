package store

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
)

// The schema lives in schema/*.sql, one file per area, applied in file-name
// order on every start (later files may refer to tables from earlier ones).
// Every statement must be safe to run again: CREATE … IF NOT EXISTS,
// ADD COLUMN IF NOT EXISTS and so on. To change a table, add a statement to
// its file rather than editing an existing one, so databases that already
// have the old shape are brought up to date. Trading dates are DATE, moments
// TIMESTAMPTZ, documents JSONB so they can be queried.
//
//go:embed schema/*.sql
var schemaFiles embed.FS

// schemaStatements returns every statement, in order, with the file it came from.
func schemaStatements() ([][2]string, error) {
	names, err := fs.Glob(schemaFiles, "schema/*.sql")
	if err != nil {
		return nil, err
	}
	sort.Strings(names)
	var out [][2]string
	for _, n := range names {
		b, err := schemaFiles.ReadFile(n)
		if err != nil {
			return nil, err
		}
		for _, st := range splitSQL(string(b)) {
			out = append(out, [2]string{n, st})
		}
	}
	return out, nil
}

// splitSQL splits a file into statements at semicolons, ignoring semicolons
// inside -- comments and quoted text. Comments are dropped.
func splitSQL(src string) []string {
	var out []string
	var cur strings.Builder
	flush := func() {
		if s := strings.TrimSpace(cur.String()); s != "" {
			out = append(out, s)
		}
		cur.Reset()
	}
	for i := 0; i < len(src); i++ {
		c := src[i]
		switch {
		case c == '-' && i+1 < len(src) && src[i+1] == '-':
			for i < len(src) && src[i] != '\n' {
				i++
			}
			cur.WriteByte('\n')
		case c == '\'' || c == '"':
			end := i + 1
			for end < len(src) && src[end] != c {
				end++
			}
			cur.WriteString(src[i:min(end+1, len(src))])
			i = end
		case c == ';':
			flush()
		default:
			cur.WriteByte(c)
		}
	}
	flush()
	return out
}

// applySchema runs each statement separately. Postgres refuses even
// CREATE INDEX IF NOT EXISTS to a user who doesn't own the table, so when the
// database was set up by a different user, statements for objects that
// already exist are skipped rather than failing startup.
func applySchema(ctx context.Context, db *sql.DB) error {
	stmts, err := schemaStatements()
	if err != nil {
		return err
	}
	for _, st := range stmts {
		file, stmt := st[0], st[1]
		_, err := db.ExecContext(ctx, stmt)
		if err == nil {
			continue
		}
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "42501" {
			if name := createdObject(stmt); name != "" {
				var exists bool
				if db.QueryRowContext(ctx, `SELECT to_regclass($1) IS NOT NULL`, name).Scan(&exists) == nil && exists {
					continue
				}
			}
			var user string
			_ = db.QueryRowContext(ctx, `SELECT current_user`).Scan(&user)
			return fmt.Errorf("database user %q lacks privileges (%s). Make it the owner, e.g. as a superuser: "+
				"ALTER DATABASE <db> OWNER TO %s; and ALTER TABLE <each table> OWNER TO %s", user, pgErr.Message, user, user)
		}
		return fmt.Errorf("applying schema (%s): %w", strings.TrimPrefix(file, "schema/"), err)
	}
	return nil
}

// createdObject extracts the name from "CREATE TABLE|INDEX IF NOT EXISTS name".
func createdObject(stmt string) string {
	_, rest, ok := strings.Cut(stmt, "IF NOT EXISTS ")
	if !ok {
		return ""
	}
	f := strings.Fields(rest)
	if len(f) == 0 {
		return ""
	}
	return strings.TrimSuffix(strings.SplitN(f[0], "(", 2)[0], "(")
}
