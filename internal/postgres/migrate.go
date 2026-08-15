package postgres

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type migration struct {
	version int64
	name    string
	path    string
}

func ApplyMigrations(ctx context.Context, pool *pgxpool.Pool, directory string) error {
	migrations, err := loadMigrations(directory)
	if err != nil {
		return err
	}
	if _, err := pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version bigint PRIMARY KEY,
			name text NOT NULL,
			applied_at timestamptz NOT NULL DEFAULT now()
		)
	`); err != nil {
		return fmt.Errorf("create migration ledger: %w", err)
	}
	if err := baselineLegacySchema(ctx, pool); err != nil {
		return err
	}

	for _, migration := range migrations {
		var applied bool
		if err := pool.QueryRow(ctx,
			"SELECT EXISTS (SELECT 1 FROM schema_migrations WHERE version = $1)", migration.version,
		).Scan(&applied); err != nil {
			return fmt.Errorf("check migration %d: %w", migration.version, err)
		}
		if applied {
			continue
		}
		sql, err := os.ReadFile(migration.path)
		if err != nil {
			return fmt.Errorf("read migration %s: %w", migration.name, err)
		}
		if err := pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
			if _, err := tx.Exec(ctx, string(sql)); err != nil {
				return err
			}
			_, err := tx.Exec(ctx,
				"INSERT INTO schema_migrations (version, name) VALUES ($1, $2)",
				migration.version, migration.name,
			)
			return err
		}); err != nil {
			return fmt.Errorf("apply migration %s: %w", migration.name, err)
		}
	}
	return nil
}

func loadMigrations(directory string) ([]migration, error) {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return nil, fmt.Errorf("read migrations directory: %w", err)
	}
	migrations := make([]migration, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".up.sql") {
			continue
		}
		separator := strings.IndexByte(name, '_')
		if separator < 1 {
			return nil, fmt.Errorf("migration filename %q has no numeric prefix", name)
		}
		version, err := strconv.ParseInt(name[:separator], 10, 64)
		if err != nil || version < 1 {
			return nil, fmt.Errorf("migration filename %q has an invalid version", name)
		}
		migrations = append(migrations, migration{version: version, name: name, path: filepath.Join(directory, name)})
	}
	sort.Slice(migrations, func(i, j int) bool { return migrations[i].version < migrations[j].version })
	for i := 1; i < len(migrations); i++ {
		if migrations[i-1].version == migrations[i].version {
			return nil, fmt.Errorf("duplicate migration version %d", migrations[i].version)
		}
	}
	return migrations, nil
}

// Releases before the migration runner initialized fresh volumes through
// docker-entrypoint-initdb.d. Record those known schemas before applying new work.
func baselineLegacySchema(ctx context.Context, pool *pgxpool.Pool) error {
	var applied int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM schema_migrations").Scan(&applied); err != nil {
		return fmt.Errorf("count applied migrations: %w", err)
	}
	if applied != 0 {
		return nil
	}
	for _, known := range []struct {
		version int64
		name    string
		table   string
	}{
		{version: 1, name: "000001_initial.up.sql", table: "alerts"},
		{version: 2, name: "000002_stream_events.up.sql", table: "stream_events"},
		{version: 3, name: "000003_alert_history.up.sql", table: "alert_history"},
	} {
		var exists bool
		if err := pool.QueryRow(ctx, "SELECT to_regclass($1) IS NOT NULL", known.table).Scan(&exists); err != nil {
			return fmt.Errorf("check legacy table %s: %w", known.table, err)
		}
		if exists {
			if _, err := pool.Exec(ctx,
				"INSERT INTO schema_migrations (version, name) VALUES ($1, $2)", known.version, known.name,
			); err != nil {
				return fmt.Errorf("baseline migration %d: %w", known.version, err)
			}
		}
	}
	return nil
}
