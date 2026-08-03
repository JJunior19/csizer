package storage

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"database/sql/driver"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"sort"
	"strconv"
	"time"
)

var migrationFilename = regexp.MustCompile(`^([0-9]{3})_([a-z0-9][a-z0-9_]*)\.sql$`)

type migration struct {
	version  int
	name     string
	script   []byte
	checksum string
}

type appliedMigration struct {
	name     string
	checksum string
}

func applyMigrations(ctx context.Context, database *sql.DB, migrationFiles fs.FS) (err error) {
	items, err := discoverMigrations(migrationFiles)
	if err != nil {
		return err
	}
	connection, err := database.Conn(ctx)
	if err != nil {
		return fmt.Errorf("acquire migration connection: %w", err)
	}
	defer func() {
		if closeErr := connection.Close(); closeErr != nil {
			err = errors.Join(err, fmt.Errorf("close migration connection: %w", closeErr))
		}
	}()

	if err := withImmediateTransaction(ctx, connection, func() error {
		if err := ensureMigrationTable(ctx, connection); err != nil {
			return err
		}
		applied, err := appliedMigrations(ctx, connection)
		if err != nil {
			return err
		}
		return validateAppliedMigrations(items, applied)
	}); err != nil {
		return err
	}

	for _, item := range items {
		item := item
		if err := withImmediateTransaction(ctx, connection, func() error {
			applied, err := appliedMigrations(ctx, connection)
			if err != nil {
				return err
			}
			if err := validateAppliedMigrations(items, applied); err != nil {
				return err
			}
			if _, ok := applied[item.version]; ok {
				return nil
			}
			if item.version != len(applied)+1 {
				return fmt.Errorf("apply migration %q: expected next version %d", item.name, len(applied)+1)
			}
			if _, err := connection.ExecContext(ctx, string(item.script)); err != nil {
				return fmt.Errorf("execute migration %q: %w", item.name, err)
			}
			if _, err := connection.ExecContext(
				ctx,
				`INSERT INTO schema_migrations(version, name, checksum, applied_at) VALUES (?, ?, ?, ?)`,
				item.version,
				item.name,
				item.checksum,
				formatTimestamp(time.Now()),
			); err != nil {
				return fmt.Errorf("record migration %q: %w", item.name, err)
			}
			return nil
		}); err != nil {
			return err
		}
	}
	return nil
}

func discoverMigrations(migrationFiles fs.FS) ([]migration, error) {
	entries, err := fs.ReadDir(migrationFiles, ".")
	if err != nil {
		return nil, fmt.Errorf("read embedded migrations: %w", err)
	}

	items := make([]migration, 0, len(entries))
	versions := make(map[int]string)
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		matches := migrationFilename.FindStringSubmatch(entry.Name())
		if matches == nil {
			if path.Ext(entry.Name()) == ".sql" {
				return nil, fmt.Errorf("malformed migration filename %q", entry.Name())
			}
			continue
		}
		version, err := strconv.Atoi(matches[1])
		if err != nil || version < 1 {
			return nil, fmt.Errorf("invalid migration version in %q", entry.Name())
		}
		if previous, exists := versions[version]; exists {
			return nil, fmt.Errorf("duplicate migration version %d in %q and %q", version, previous, entry.Name())
		}
		contents, err := fs.ReadFile(migrationFiles, entry.Name())
		if err != nil {
			return nil, fmt.Errorf("read migration %q: %w", entry.Name(), err)
		}
		digest := sha256.Sum256(contents)
		versions[version] = entry.Name()
		items = append(items, migration{
			version:  version,
			name:     entry.Name(),
			script:   contents,
			checksum: hex.EncodeToString(digest[:]),
		})
	}

	sort.Slice(items, func(i, j int) bool { return items[i].version < items[j].version })
	if len(items) == 0 {
		return nil, errors.New("no embedded migrations found")
	}
	for index, item := range items {
		expected := index + 1
		if item.version != expected {
			return nil, fmt.Errorf("migration version gap: expected %d, found %d", expected, item.version)
		}
	}
	return items, nil
}

func ensureMigrationTable(ctx context.Context, connection *sql.Conn) error {
	const statement = `
CREATE TABLE IF NOT EXISTS schema_migrations (
    version INTEGER PRIMARY KEY,
    name TEXT NOT NULL,
    checksum TEXT NOT NULL,
    applied_at TEXT NOT NULL
)`
	if _, err := connection.ExecContext(ctx, statement); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}
	return nil
}

func appliedMigrations(ctx context.Context, connection *sql.Conn) (map[int]appliedMigration, error) {
	rows, err := connection.QueryContext(ctx, `SELECT version, name, checksum FROM schema_migrations ORDER BY version`)
	if err != nil {
		return nil, fmt.Errorf("read applied migrations: %w", err)
	}
	defer func() { _ = rows.Close() }()

	applied := make(map[int]appliedMigration)
	for rows.Next() {
		var version int
		var record appliedMigration
		if err := rows.Scan(&version, &record.name, &record.checksum); err != nil {
			return nil, fmt.Errorf("scan applied migration: %w", err)
		}
		applied[version] = record
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate applied migrations: %w", err)
	}
	return applied, nil
}

func validateAppliedMigrations(items []migration, applied map[int]appliedMigration) error {
	for version, record := range applied {
		if version < 1 {
			return fmt.Errorf("database has invalid migration version %d", version)
		}
		if version > len(items) {
			return fmt.Errorf("database has unknown migration version %d", version)
		}
		expected := items[version-1]
		if expected.name != record.name {
			return fmt.Errorf("migration version %d name drift: database has %q, embedded migration is %q", version, record.name, expected.name)
		}
		if expected.checksum != record.checksum {
			return fmt.Errorf("migration version %d checksum drift for %q", version, expected.name)
		}
	}
	for version := 1; version <= len(applied); version++ {
		if _, ok := applied[version]; !ok {
			return fmt.Errorf("applied migration version gap: missing %d", version)
		}
	}
	return nil
}

func withImmediateTransaction(ctx context.Context, connection *sql.Conn, operation func() error) (err error) {
	if _, err := connection.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		return fmt.Errorf("begin immediate migration transaction: %w", err)
	}
	defer func() {
		if err != nil {
			if _, rollbackErr := connection.ExecContext(context.WithoutCancel(ctx), `ROLLBACK`); rollbackErr != nil {
				err = errors.Join(err, fmt.Errorf("rollback migration transaction: %w", rollbackErr))
				if discardErr := connection.Raw(func(any) error { return driver.ErrBadConn }); discardErr != nil && !errors.Is(discardErr, driver.ErrBadConn) {
					err = errors.Join(err, fmt.Errorf("discard migration connection: %w", discardErr))
				}
			}
		}
	}()
	if err := operation(); err != nil {
		return err
	}
	if _, err := connection.ExecContext(ctx, `COMMIT`); err != nil {
		return fmt.Errorf("commit migration transaction: %w", err)
	}
	return nil
}
