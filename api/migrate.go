package main

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

func migrate(ctx context.Context, pool *pgxpool.Pool) error {
	_, err := pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version integer PRIMARY KEY,
			applied_at timestamptz NOT NULL DEFAULT now()
		)`)
	if err != nil {
		return fmt.Errorf("schema_migrations: %w", err)
	}
	entries, err := fs.ReadDir(migrationFS, "migrations")
	if err != nil {
		return err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		ver, err := strconv.Atoi(strings.SplitN(e.Name(), "_", 2)[0])
		if err != nil {
			return fmt.Errorf("migration name %s: %w", e.Name(), err)
		}
		var one int
		err = pool.QueryRow(ctx, `SELECT 1 FROM schema_migrations WHERE version=$1`, ver).Scan(&one)
		if err == nil {
			continue
		}
		body, err := migrationFS.ReadFile("migrations/" + e.Name())
		if err != nil {
			return err
		}
		tx, err := pool.Begin(ctx)
		if err != nil {
			return err
		}
		for _, stmt := range splitSQL(string(body)) {
			if _, err := tx.Exec(ctx, stmt); err != nil {
				_ = tx.Rollback(ctx)
				return fmt.Errorf("migration %s: %w", e.Name(), err)
			}
		}
		if _, err := tx.Exec(ctx, `INSERT INTO schema_migrations (version) VALUES ($1)`, ver); err != nil {
			_ = tx.Rollback(ctx)
			return err
		}
		if err := tx.Commit(ctx); err != nil {
			return err
		}
	}
	return nil
}

// splitSQL splits on semicolons that are not inside quotes, dollar-quotes, or
// line comments. Migration 007 defines a plpgsql function, and the body
// contains semicolons.
func splitSQL(sql string) []string {
	var out []string
	var b strings.Builder
	i := 0
	for i < len(sql) {
		if strings.HasPrefix(sql[i:], "--") {
			for i < len(sql) && sql[i] != '\n' {
				b.WriteByte(sql[i])
				i++
			}
			continue
		}
		if sql[i] == '\'' {
			b.WriteByte(sql[i])
			i++
			for i < len(sql) {
				b.WriteByte(sql[i])
				if sql[i] == '\'' {
					if i+1 < len(sql) && sql[i+1] == '\'' {
						b.WriteByte(sql[i+1])
						i += 2
						continue
					}
					i++
					break
				}
				i++
			}
			continue
		}
		if tag, ok := dollarTag(sql, i); ok {
			rest := sql[i+len(tag):]
			end := strings.Index(rest, tag)
			if end < 0 {
				b.WriteString(sql[i:])
				break
			}
			end = i + len(tag) + end + len(tag)
			b.WriteString(sql[i:end])
			i = end
			continue
		}
		if sql[i] == ';' {
			if s := strings.TrimSpace(b.String()); s != "" {
				out = append(out, s)
			}
			b.Reset()
			i++
			continue
		}
		b.WriteByte(sql[i])
		i++
	}
	if s := strings.TrimSpace(b.String()); s != "" {
		out = append(out, s)
	}
	return out
}

func dollarTag(sql string, i int) (string, bool) {
	if i >= len(sql) || sql[i] != '$' {
		return "", false
	}
	j := i + 1
	for j < len(sql) {
		c := sql[j]
		if c == '$' {
			return sql[i : j+1], true
		}
		if (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && (c < '0' || c > '9') && c != '_' {
			return "", false
		}
		j++
	}
	return "", false
}
