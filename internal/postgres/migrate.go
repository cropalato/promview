package postgres

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
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
	lock, err := pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("acquire migration lock connection: %w", err)
	}
	if _, err := lock.Exec(ctx, "SELECT pg_advisory_lock($1)", int64(0x50726f6d76696577)); err != nil {
		lock.Release()
		return fmt.Errorf("acquire migration lock: %w", err)
	}
	defer releaseMigrationLock(lock)

	if _, err := lock.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version bigint PRIMARY KEY,
			name text NOT NULL,
			applied_at timestamptz NOT NULL DEFAULT now()
		)
	`); err != nil {
		return fmt.Errorf("create migration ledger: %w", err)
	}
	if err := baselineLegacySchema(ctx, lock); err != nil {
		return err
	}

	for _, migration := range migrations {
		var applied bool
		if err := lock.QueryRow(ctx,
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
		if err := pgx.BeginFunc(ctx, lock, func(tx pgx.Tx) error {
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

func releaseMigrationLock(connection *pgxpool.Conn) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var unlocked bool
	err := connection.QueryRow(ctx, "SELECT pg_advisory_unlock($1)", int64(0x50726f6d76696577)).Scan(&unlocked)
	if err != nil || !unlocked {
		_ = connection.Hijack().Close(ctx)
		return
	}
	connection.Release()
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
func baselineLegacySchema(ctx context.Context, connection *pgxpool.Conn) error {
	var applied int
	if err := connection.QueryRow(ctx, "SELECT count(*) FROM schema_migrations").Scan(&applied); err != nil {
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
		{version: 4, name: "000004_auth_sources.up.sql", table: "sessions"},
		{version: 5, name: "000005_oidc_transactions.up.sql", table: "oidc_login_transactions"},
		{version: 7, name: "000007_oidc_authorization.up.sql", table: "users"},
	} {
		var exists bool
		if err := connection.QueryRow(ctx, "SELECT to_regclass($1) IS NOT NULL", known.table).Scan(&exists); err != nil {
			return fmt.Errorf("check legacy table %s: %w", known.table, err)
		}
		if exists {
			if _, err := connection.Exec(ctx,
				"INSERT INTO schema_migrations (version, name) VALUES ($1, $2)", known.version, known.name,
			); err != nil {
				return fmt.Errorf("baseline migration %d: %w", known.version, err)
			}
		}
	}
	var notificationMetadataExists bool
	if err := connection.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM information_schema.columns
			WHERE table_schema = 'public'
				AND table_name = 'stream_events'
				AND column_name = 'severity'
		)
	`).Scan(&notificationMetadataExists); err != nil {
		return fmt.Errorf("check stream notification metadata: %w", err)
	}
	if notificationMetadataExists {
		if _, err := connection.Exec(ctx,
			"INSERT INTO schema_migrations (version, name) VALUES ($1, $2)",
			6, "000006_stream_notification_metadata.up.sql",
		); err != nil {
			return fmt.Errorf("baseline migration 6: %w", err)
		}
	}
	return nil
}

// PendingMigrations names the migrations on disk this database has not applied,
// in the order they would run. Empty means the schema matches the binary.
//
// A binary is only as compatible with a database as its migrations make it.
// Running a newer promview against an older schema does not degrade gracefully:
// the alert queries name columns that do not exist yet, so every read answers
// 500 and the console is simply down. That is worth refusing to start over,
// which is what this exists to let the caller do.
func PendingMigrations(ctx context.Context, pool *pgxpool.Pool, directory string) ([]string, error) {
	migrations, err := loadMigrations(directory)
	if err != nil {
		return nil, err
	}
	applied, err := appliedVersions(ctx, pool)
	if err != nil {
		return nil, err
	}
	pending := make([]string, 0)
	for _, migration := range migrations {
		if !applied[migration.version] {
			pending = append(pending, migration.name)
		}
	}
	return pending, nil
}

func appliedVersions(ctx context.Context, pool *pgxpool.Pool) (map[int64]bool, error) {
	rows, err := pool.Query(ctx, "SELECT version FROM schema_migrations")
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == undefinedTableCode {
			// No ledger means nothing has ever been applied. That is a full
			// pending set, not a failure: a database nobody has migrated is
			// precisely the case worth catching.
			return map[int64]bool{}, nil
		}
		return nil, fmt.Errorf("read applied migrations: %w", err)
	}
	defer rows.Close()
	applied := map[int64]bool{}
	for rows.Next() {
		var version int64
		if err := rows.Scan(&version); err != nil {
			return nil, fmt.Errorf("scan applied migration: %w", err)
		}
		applied[version] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate applied migrations: %w", err)
	}
	return applied, nil
}

// undefinedTableCode is PostgreSQL's undefined_table.
const undefinedTableCode = "42P01"
