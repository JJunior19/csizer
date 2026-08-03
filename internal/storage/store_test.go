package storage

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	embeddedmigrations "github.com/jorgeccarhuasaroni/containersize/migrations"
)

func TestDatabaseDSNEscapesPathAndRepeatsPragmas(t *testing.T) {
	t.Parallel()

	path := "/tmp/Container Size/database ? one.db"
	parsed, err := url.Parse(databaseDSN(path))
	if err != nil {
		t.Fatalf("url.Parse() error = %v", err)
	}
	if parsed.Scheme != "file" || parsed.Path != path {
		t.Errorf("parsed DSN = scheme %q path %q, want file and %q", parsed.Scheme, parsed.Path, path)
	}
	wantPragmas := []string{
		"busy_timeout(5000)",
		"foreign_keys(ON)",
		"journal_mode(WAL)",
		"synchronous(NORMAL)",
	}
	if got := parsed.Query()["_pragma"]; !reflect.DeepEqual(got, wantPragmas) {
		t.Errorf("DSN pragmas = %v, want %v", got, wantPragmas)
	}
}

func TestOpenCreatesExactSchemaAndConfiguresEveryConnection(t *testing.T) {
	t.Parallel()

	parent := filepath.Join(t.TempDir(), "existing parent with spaces")
	if err := os.Mkdir(parent, 0o750); err != nil {
		t.Fatalf("Mkdir() error = %v", err)
	}
	databasePath := filepath.Join(parent, "container size.db")
	store := openTestStore(t, databasePath)
	canonicalPath := canonicalDatabasePath(t, databasePath)

	assertPermissions(t, parent, 0o750)
	assertPermissions(t, databasePath, 0o600)
	if store.Path() != canonicalPath {
		t.Errorf("Path() = %q, want canonical %q", store.Path(), canonicalPath)
	}

	wantTables := []string{
		"container_events",
		"container_instances",
		"metric_samples",
		"minute_rollups",
		"schema_migrations",
		"tracking_sessions",
		"workload_settings",
		"workloads",
	}
	if got := schemaObjects(t, store.db, "table"); !reflect.DeepEqual(got, wantTables) {
		t.Errorf("tables = %v, want %v", got, wantTables)
	}
	wantIndexes := []string{
		"idx_container_events_container_instance_timestamp",
		"idx_container_events_workload_timestamp",
		"idx_container_instances_workload_id",
		"idx_metric_samples_session_timestamp",
		"idx_metric_samples_timestamp",
		"idx_tracking_sessions_container_instance_id",
		"idx_tracking_sessions_workload_started_at",
	}
	if got := schemaObjects(t, store.db, "index"); !reflect.DeepEqual(got, wantIndexes) {
		t.Errorf("indexes = %v, want %v", got, wantIndexes)
	}

	exactColumns := map[string][]string{
		"workloads": {
			"id", "workload_key", "display_name", "compose_project", "compose_service",
			"image_repository", "tracking_enabled", "created_at", "updated_at",
		},
		"container_instances": {
			"id", "workload_id", "container_id", "container_name", "image_name", "image_digest",
			"architecture", "operating_system", "docker_host_id", "started_at", "stopped_at",
			"exit_code", "oom_killed", "created_at",
		},
		"tracking_sessions": {
			"id", "workload_id", "container_instance_id", "started_at", "ended_at",
			"status", "label", "collector_version",
		},
		"metric_samples": {
			"id", "session_id", "timestamp", "cpu_usage_cores", "cpu_percent_host",
			"memory_usage_bytes", "memory_cache_bytes", "memory_working_set_bytes",
			"memory_limit_bytes", "pids", "network_rx_bytes", "network_tx_bytes",
			"block_read_bytes", "block_write_bytes", "activity_state",
		},
		"container_events": {
			"id", "workload_id", "container_instance_id", "timestamp", "event_type",
			"exit_code", "metadata_json",
		},
		"minute_rollups": {
			"workload_id", "minute", "sample_count", "active_sample_count", "cpu_avg",
			"cpu_p95", "cpu_p99", "cpu_max", "memory_avg_bytes", "memory_p95_bytes",
			"memory_p99_bytes", "memory_max_bytes",
		},
		"workload_settings": {
			"workload_id", "active_sample_interval_seconds", "idle_sample_interval_seconds",
			"raw_retention_days", "startup_window_seconds", "default_provider", "default_profile",
		},
		"schema_migrations": {"version", "name", "checksum", "applied_at"},
	}
	for table, want := range exactColumns {
		if got := tableColumns(t, store.db, table); !reflect.DeepEqual(got, want) {
			t.Errorf("%s columns = %v, want %v", table, got, want)
		}
	}
	exactForeignKeys := map[string][]string{
		"container_instances": {"workload_id->workloads.id"},
		"tracking_sessions":   {"container_instance_id->container_instances.id", "workload_id->workloads.id"},
		"metric_samples":      {"session_id->tracking_sessions.id"},
		"container_events":    {"container_instance_id->container_instances.id", "workload_id->workloads.id"},
		"minute_rollups":      {"workload_id->workloads.id"},
		"workload_settings":   {"workload_id->workloads.id"},
	}
	for table, want := range exactForeignKeys {
		if got := tableForeignKeys(t, store.db, table); !reflect.DeepEqual(got, want) {
			t.Errorf("%s foreign keys = %v, want %v", table, got, want)
		}
	}
	assertColumnDefault(t, store.db, "workloads", "tracking_enabled", "1")
	assertColumnDefault(t, store.db, "container_instances", "oom_killed", "0")
	assertColumnDefault(t, store.db, "workload_settings", "active_sample_interval_seconds", "2")
	assertColumnDefault(t, store.db, "workload_settings", "idle_sample_interval_seconds", "10")
	assertColumnDefault(t, store.db, "workload_settings", "raw_retention_days", "30")
	assertColumnDefault(t, store.db, "workload_settings", "startup_window_seconds", "20")
	assertColumnDefault(t, store.db, "workload_settings", "default_profile", "'balanced'")
	assertColumnNullable(t, store.db, "tracking_sessions", "collector_version")
	assertColumnNullable(t, store.db, "container_events", "metadata_json")
	assertCompositePrimaryKey(t, store.db, "minute_rollups", map[string]int{"workload_id": 1, "minute": 2})

	migrationBytes, err := fs.ReadFile(embeddedmigrations.Files, "001_initial.sql")
	if err != nil {
		t.Fatalf("read embedded migration error = %v", err)
	}
	digest := sha256.Sum256(migrationBytes)
	var version int
	var name, checksum string
	if err := store.db.QueryRowContext(
		t.Context(),
		`SELECT version, name, checksum FROM schema_migrations`,
	).Scan(&version, &name, &checksum); err != nil {
		t.Fatalf("query migration error = %v", err)
	}
	if version != 1 || name != "001_initial.sql" || checksum != hex.EncodeToString(digest[:]) {
		t.Errorf("migration = (%d, %q, %q), want version 1, exact name and checksum", version, name, checksum)
	}

	connections := make([]*sql.Conn, 0, maxOpenConnections)
	for range maxOpenConnections {
		connection, err := store.db.Conn(t.Context())
		if err != nil {
			t.Fatalf("DB.Conn() error = %v", err)
		}
		connections = append(connections, connection)
	}
	defer func() {
		for _, connection := range connections {
			_ = connection.Close()
		}
	}()
	for index, connection := range connections {
		assertPragmas(t, connection, index)
	}
}

func TestOpenRelativePathIsAbsoluteAndPrivate(t *testing.T) {
	absolutePath := filepath.Join(t.TempDir(), "relative path with spaces", "container size.db")
	workingDirectory, err := filepath.Abs(".")
	if err != nil {
		t.Fatalf("filepath.Abs() error = %v", err)
	}
	relativePath, err := filepath.Rel(workingDirectory, absolutePath)
	if err != nil {
		t.Fatalf("filepath.Rel() error = %v", err)
	}
	store := openTestStore(t, relativePath)
	canonicalPath := canonicalDatabasePath(t, absolutePath)
	if store.Path() != canonicalPath {
		t.Errorf("Path() = %q, want canonical %q", store.Path(), canonicalPath)
	}
	assertPermissions(t, filepath.Dir(absolutePath), 0o700)
	assertPermissions(t, absolutePath, 0o600)
}

func TestOpenRejectsUnsafePaths(t *testing.T) {
	tests := []struct {
		name      string
		prepare   func(*testing.T) string
		wantError string
	}{
		{
			name: "directory target",
			prepare: func(t *testing.T) string {
				path := filepath.Join(t.TempDir(), "database directory")
				if err := os.Mkdir(path, 0o700); err != nil {
					t.Fatalf("Mkdir() error = %v", err)
				}
				return path
			},
			wantError: "is a directory",
		},
		{
			name: "symbolic link target",
			prepare: func(t *testing.T) string {
				parent := t.TempDir()
				target := filepath.Join(parent, "target.db")
				if err := os.WriteFile(target, nil, 0o600); err != nil {
					t.Fatalf("WriteFile() error = %v", err)
				}
				link := filepath.Join(parent, "link.db")
				if err := os.Symlink(target, link); err != nil {
					t.Skipf("symbolic links unsupported: %v", err)
				}
				return link
			},
			wantError: "symbolic link",
		},
		{
			name: "invalid parent",
			prepare: func(t *testing.T) string {
				parent := filepath.Join(t.TempDir(), "parent-file")
				if err := os.WriteFile(parent, nil, 0o600); err != nil {
					t.Fatalf("WriteFile() error = %v", err)
				}
				return filepath.Join(parent, "database.db")
			},
			wantError: "is not a directory",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := Open(t.Context(), test.prepare(t))
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("Open() error = %v, want containing %q", err, test.wantError)
			}
		})
	}
}

func TestOpenRejectsInsecureParentWithoutChangingPermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix permission bits are not enforced on Windows")
	}

	tests := []struct {
		name string
		mode os.FileMode
	}{
		{name: "group writable", mode: 0o770},
		{name: "other writable", mode: 0o702},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			parent := filepath.Join(t.TempDir(), "insecure parent")
			if err := os.Mkdir(parent, 0o700); err != nil {
				t.Fatalf("Mkdir() error = %v", err)
			}
			if err := os.Chmod(parent, test.mode); err != nil {
				t.Skipf("cannot set Unix permission bits: %v", err)
			}
			info, err := os.Stat(parent)
			if err != nil {
				t.Fatalf("Stat() error = %v", err)
			}
			if info.Mode().Perm()&0o022 == 0 {
				t.Skipf("platform did not retain insecure mode %04o", test.mode)
			}

			_, err = Open(t.Context(), filepath.Join(parent, "database.db"))
			if err == nil || !strings.Contains(err.Error(), "insecure permissions") {
				t.Fatalf("Open() error = %v, want insecure permissions", err)
			}
			info, err = os.Stat(parent)
			if err != nil {
				t.Fatalf("Stat(after Open) error = %v", err)
			}
			if info.Mode().Perm() != test.mode {
				t.Errorf("parent permissions = %04o, want unchanged %04o", info.Mode().Perm(), test.mode)
			}
		})
	}
}

func TestOpenRejectsSymbolicLinkParent(t *testing.T) {
	realParent := filepath.Join(t.TempDir(), "real parent")
	if err := os.Mkdir(realParent, 0o700); err != nil {
		t.Fatalf("Mkdir() error = %v", err)
	}
	linkParent := filepath.Join(filepath.Dir(realParent), "linked parent")
	if err := os.Symlink(realParent, linkParent); err != nil {
		t.Skipf("symbolic links unsupported: %v", err)
	}
	_, err := Open(t.Context(), filepath.Join(linkParent, "database.db"))
	if err == nil || !strings.Contains(err.Error(), "database parent") || !strings.Contains(err.Error(), "symbolic link") {
		t.Fatalf("Open() error = %v, want symbolic-link parent error", err)
	}
}

func TestOpenCanonicalizesSecureAncestorSymlink(t *testing.T) {
	targetRoot := filepath.Join(t.TempDir(), "secure target")
	targetParent := filepath.Join(targetRoot, "database parent")
	if err := os.MkdirAll(targetParent, 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	aliasRoot := t.TempDir()
	alias := filepath.Join(aliasRoot, "target alias")
	if err := os.Symlink(targetRoot, alias); err != nil {
		t.Skipf("symbolic links unsupported: %v", err)
	}
	originalPath := filepath.Join(alias, "database parent", "container size.db")
	store := openTestStore(t, originalPath)
	wantPath := filepath.Join(targetParent, "container size.db")
	wantPath = canonicalDatabasePath(t, wantPath)
	if store.Path() != wantPath {
		t.Errorf("Path() = %q, want canonical target %q", store.Path(), wantPath)
	}
	if _, err := os.Stat(wantPath); err != nil {
		t.Fatalf("canonical database was not created: %v", err)
	}
}

func TestOpenAllowsStickyWritableIntermediateAncestor(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix sticky and permission bits are not enforced on Windows")
	}
	stickyAncestor := filepath.Join(t.TempDir(), "sticky ancestor")
	secureParent := filepath.Join(stickyAncestor, "secure final parent")
	if err := os.MkdirAll(secureParent, 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.Chmod(stickyAncestor, 0o1777); err != nil {
		t.Skipf("cannot set sticky Unix permission bits: %v", err)
	}
	info, err := os.Stat(stickyAncestor)
	if err != nil {
		t.Fatalf("Stat() error = %v", err)
	}
	if info.Mode()&os.ModeSticky == 0 || info.Mode().Perm()&0o022 == 0 {
		t.Skipf("platform did not retain sticky writable mode: got %v", info.Mode())
	}
	store := openTestStore(t, filepath.Join(secureParent, "database.db"))
	if store.Path() != canonicalDatabasePath(t, filepath.Join(secureParent, "database.db")) {
		t.Errorf("Path() = %q, want canonical path under sticky ancestor", store.Path())
	}
}

func TestOpenRejectsStickyWritableFinalParent(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix sticky and permission bits are not enforced on Windows")
	}
	parent := filepath.Join(t.TempDir(), "sticky final parent")
	if err := os.Mkdir(parent, 0o700); err != nil {
		t.Fatalf("Mkdir() error = %v", err)
	}
	if err := os.Chmod(parent, 0o1777); err != nil {
		t.Skipf("cannot set sticky Unix permission bits: %v", err)
	}
	info, err := os.Stat(parent)
	if err != nil {
		t.Fatalf("Stat() error = %v", err)
	}
	if info.Mode()&os.ModeSticky == 0 || info.Mode().Perm()&0o022 == 0 {
		t.Skipf("platform did not retain sticky writable mode: got %v", info.Mode())
	}
	_, err = Open(t.Context(), filepath.Join(parent, "database.db"))
	if err == nil || !strings.Contains(err.Error(), "insecure permissions") {
		t.Fatalf("Open() error = %v, want insecure final parent", err)
	}
	info, err = os.Stat(parent)
	if err != nil {
		t.Fatalf("Stat(after Open) error = %v", err)
	}
	if info.Mode()&os.ModeSticky == 0 || info.Mode().Perm() != 0o777 {
		t.Errorf("final parent mode = %v, want unchanged sticky 0777", info.Mode())
	}
}

func TestOpenRejectsNonStickyWritableCanonicalAncestor(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix permission bits are not enforced on Windows")
	}
	targetRoot := filepath.Join(t.TempDir(), "insecure target")
	targetParent := filepath.Join(targetRoot, "secure final parent")
	if err := os.MkdirAll(targetParent, 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.Chmod(targetRoot, 0o770); err != nil {
		t.Skipf("cannot set Unix permission bits: %v", err)
	}
	info, err := os.Stat(targetRoot)
	if err != nil {
		t.Fatalf("Stat() error = %v", err)
	}
	if info.Mode().Perm()&0o022 == 0 {
		t.Skip("platform did not retain insecure ancestor permissions")
	}
	alias := filepath.Join(t.TempDir(), "target alias")
	if err := os.Symlink(targetRoot, alias); err != nil {
		t.Skipf("symbolic links unsupported: %v", err)
	}
	originalPath := filepath.Join(alias, "secure final parent", "database.db")
	_, err = Open(t.Context(), originalPath)
	if err == nil || !strings.Contains(err.Error(), "canonical database ancestor") || !strings.Contains(err.Error(), "insecure permissions") {
		t.Fatalf("Open() error = %v, want insecure canonical ancestor", err)
	}
	info, err = os.Stat(targetRoot)
	if err != nil {
		t.Fatalf("Stat(after Open) error = %v", err)
	}
	if info.Mode().Perm() != 0o770 {
		t.Errorf("ancestor permissions = %04o, want unchanged 0770", info.Mode().Perm())
	}
}

func TestConcurrentOpenAppliesMigrationOnce(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "concurrent open", "database.db")
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	const callers = 8
	start := make(chan struct{})
	results := make(chan *Store, callers)
	errorsChannel := make(chan error, callers)
	var group sync.WaitGroup
	for range callers {
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			store, err := Open(ctx, databasePath)
			if err != nil {
				errorsChannel <- err
				return
			}
			results <- store
		}()
	}
	close(start)
	group.Wait()
	close(results)
	close(errorsChannel)
	for err := range errorsChannel {
		t.Errorf("concurrent Open() error = %v", err)
	}
	var stores []*Store
	for store := range results {
		stores = append(stores, store)
	}
	defer func() {
		for _, store := range stores {
			_ = store.Close()
		}
	}()
	if len(stores) != callers {
		t.Fatalf("successful opens = %d, want %d", len(stores), callers)
	}
	assertMigrationCount(t, stores[0].db, 1)
}

func TestOpenIsIdempotentAndEnforcesForeignKeys(t *testing.T) {
	t.Parallel()

	databasePath := filepath.Join(t.TempDir(), "database with spaces", "container size.db")
	store := openTestStore(t, databasePath)
	if err := store.Close(); err != nil {
		t.Fatalf("first Close() error = %v", err)
	}
	store, err := Open(t.Context(), databasePath)
	if err != nil {
		t.Fatalf("second Open() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	assertMigrationCount(t, store.db, 1)

	_, err = store.db.ExecContext(t.Context(), `
INSERT INTO container_instances (
    workload_id, container_id, container_name, image_name, created_at
) VALUES (999, 'missing-workload', 'missing', 'image', '2026-01-01T00:00:00Z')`)
	if err == nil {
		t.Fatal("foreign-key violating insert succeeded")
	}
}

func TestWALAllowsWriterCommitWhileReaderHoldsSnapshot(t *testing.T) {
	t.Parallel()

	store := openTestStore(t, filepath.Join(t.TempDir(), "wal concurrency", "database.db"))
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	reader, err := store.db.Conn(ctx)
	if err != nil {
		t.Fatalf("reader connection error = %v", err)
	}
	defer func() { _ = reader.Close() }()
	writer, err := store.db.Conn(ctx)
	if err != nil {
		t.Fatalf("writer connection error = %v", err)
	}
	defer func() { _ = writer.Close() }()

	readTransaction, err := reader.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin read transaction error = %v", err)
	}
	defer func() { _ = readTransaction.Rollback() }()
	var count int
	if err := readTransaction.QueryRowContext(ctx, `SELECT COUNT(*) FROM workloads`).Scan(&count); err != nil {
		t.Fatalf("establish read snapshot error = %v", err)
	}

	writeDone := make(chan error, 1)
	go func() {
		transaction, beginErr := writer.BeginTx(ctx, nil)
		if beginErr != nil {
			writeDone <- beginErr
			return
		}
		_, execErr := transaction.ExecContext(ctx, `
INSERT INTO workloads (workload_key, display_name, created_at, updated_at)
VALUES ('wal-test', 'WAL test', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`)
		if execErr != nil {
			_ = transaction.Rollback()
			writeDone <- execErr
			return
		}
		writeDone <- transaction.Commit()
	}()
	select {
	case err := <-writeDone:
		if err != nil {
			t.Fatalf("writer commit while reader active error = %v", err)
		}
	case <-ctx.Done():
		t.Fatalf("writer did not finish while reader held snapshot: %v", ctx.Err())
	}
	if err := readTransaction.QueryRowContext(ctx, `SELECT COUNT(*) FROM workloads`).Scan(&count); err != nil {
		t.Fatalf("read snapshot after writer error = %v", err)
	}
	if count != 0 {
		t.Errorf("reader snapshot count = %d, want 0", count)
	}
	if err := readTransaction.Commit(); err != nil {
		t.Fatalf("commit read transaction error = %v", err)
	}
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM workloads`).Scan(&count); err != nil {
		t.Fatalf("read committed write error = %v", err)
	}
	if count != 1 {
		t.Errorf("committed workload count = %d, want 1", count)
	}
}

func openTestStore(t *testing.T, path string) *Store {
	t.Helper()
	store, err := Open(t.Context(), path)
	if err != nil {
		t.Fatalf("Open(%q) error = %v", path, err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func schemaObjects(t *testing.T, database *sql.DB, objectType string) []string {
	t.Helper()
	rows, err := database.QueryContext(t.Context(), `
SELECT name FROM sqlite_master
WHERE type = ? AND name NOT LIKE 'sqlite_%' AND name NOT LIKE 'sqlite_autoindex_%'
ORDER BY name`, objectType)
	if err != nil {
		t.Fatalf("query %s objects error = %v", objectType, err)
	}
	defer func() { _ = rows.Close() }()
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan %s name error = %v", objectType, err)
		}
		names = append(names, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate %s objects error = %v", objectType, err)
	}
	sort.Strings(names)
	return names
}

func tableColumns(t *testing.T, database *sql.DB, table string) []string {
	t.Helper()
	rows, err := database.QueryContext(t.Context(), "PRAGMA table_info("+table+")")
	if err != nil {
		t.Fatalf("table_info(%s) error = %v", table, err)
	}
	defer func() { _ = rows.Close() }()
	var columns []string
	for rows.Next() {
		var cid, notNull, primaryKey int
		var name, columnType string
		var defaultValue sql.NullString
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			t.Fatalf("scan table_info(%s) error = %v", table, err)
		}
		columns = append(columns, name)
	}
	return columns
}

func tableForeignKeys(t *testing.T, database *sql.DB, table string) []string {
	t.Helper()
	rows, err := database.QueryContext(t.Context(), "PRAGMA foreign_key_list("+table+")")
	if err != nil {
		t.Fatalf("foreign_key_list(%s) error = %v", table, err)
	}
	defer func() { _ = rows.Close() }()
	var foreignKeys []string
	for rows.Next() {
		var id, sequence int
		var referencedTable, fromColumn, toColumn, onUpdate, onDelete, match string
		if err := rows.Scan(
			&id,
			&sequence,
			&referencedTable,
			&fromColumn,
			&toColumn,
			&onUpdate,
			&onDelete,
			&match,
		); err != nil {
			t.Fatalf("scan foreign_key_list(%s) error = %v", table, err)
		}
		if onUpdate != "NO ACTION" || onDelete != "NO ACTION" {
			t.Errorf("%s foreign key %s has update/delete actions %s/%s", table, fromColumn, onUpdate, onDelete)
		}
		foreignKeys = append(foreignKeys, fromColumn+"->"+referencedTable+"."+toColumn)
	}
	sort.Strings(foreignKeys)
	return foreignKeys
}

func assertColumnDefault(t *testing.T, database *sql.DB, table, column, want string) {
	t.Helper()
	rows, err := database.QueryContext(t.Context(), "PRAGMA table_info("+table+")")
	if err != nil {
		t.Fatalf("table_info(%s) error = %v", table, err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var cid, notNull, primaryKey int
		var name, columnType string
		var defaultValue sql.NullString
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			t.Fatalf("scan table_info(%s) error = %v", table, err)
		}
		if name == column {
			if !defaultValue.Valid || defaultValue.String != want {
				t.Errorf("%s.%s default = %q, want %q", table, column, defaultValue.String, want)
			}
			return
		}
	}
	t.Errorf("column %s.%s not found", table, column)
}

func assertColumnNullable(t *testing.T, database *sql.DB, table, column string) {
	t.Helper()
	rows, err := database.QueryContext(t.Context(), "PRAGMA table_info("+table+")")
	if err != nil {
		t.Fatalf("table_info(%s) error = %v", table, err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var cid, notNull, primaryKey int
		var name, columnType string
		var defaultValue sql.NullString
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			t.Fatalf("scan table_info(%s) error = %v", table, err)
		}
		if name == column {
			if notNull != 0 {
				t.Errorf("%s.%s NOT NULL = %d, want nullable", table, column, notNull)
			}
			return
		}
	}
	t.Errorf("column %s.%s not found", table, column)
}

func assertCompositePrimaryKey(t *testing.T, database *sql.DB, table string, want map[string]int) {
	t.Helper()
	rows, err := database.QueryContext(t.Context(), "PRAGMA table_info("+table+")")
	if err != nil {
		t.Fatalf("table_info(%s) error = %v", table, err)
	}
	defer func() { _ = rows.Close() }()
	got := make(map[string]int)
	for rows.Next() {
		var cid, notNull, primaryKey int
		var name, columnType string
		var defaultValue sql.NullString
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			t.Fatalf("scan table_info(%s) error = %v", table, err)
		}
		if primaryKey > 0 {
			got[name] = primaryKey
		}
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s primary key = %v, want %v", table, got, want)
	}
}

func assertPermissions(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat(%q) error = %v", path, err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Errorf("permissions for %q = %o, want %o", path, got, want)
	}
}

func canonicalDatabasePath(t *testing.T, path string) string {
	t.Helper()
	canonicalParent, err := filepath.EvalSymlinks(filepath.Dir(path))
	if err != nil {
		t.Fatalf("EvalSymlinks(%q) error = %v", filepath.Dir(path), err)
	}
	canonicalParent, err = filepath.Abs(canonicalParent)
	if err != nil {
		t.Fatalf("filepath.Abs(%q) error = %v", canonicalParent, err)
	}
	return filepath.Join(canonicalParent, filepath.Base(path))
}

func assertMigrationCount(t *testing.T, database *sql.DB, want int) {
	t.Helper()
	var got int
	if err := database.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM schema_migrations`).Scan(&got); err != nil {
		t.Fatalf("query migration count error = %v", err)
	}
	if got != want {
		t.Errorf("migration count = %d, want %d", got, want)
	}
}

func assertPragmas(t *testing.T, connection *sql.Conn, connectionIndex int) {
	t.Helper()
	var journalMode string
	if err := connection.QueryRowContext(t.Context(), `PRAGMA journal_mode`).Scan(&journalMode); err != nil {
		t.Fatalf("connection %d journal_mode error = %v", connectionIndex, err)
	}
	if journalMode != "wal" {
		t.Errorf("connection %d journal_mode = %q, want wal", connectionIndex, journalMode)
	}
	for name, want := range map[string]int{"synchronous": 1, "foreign_keys": 1, "busy_timeout": 5000} {
		var got int
		if err := connection.QueryRowContext(t.Context(), fmt.Sprintf("PRAGMA %s", name)).Scan(&got); err != nil {
			t.Fatalf("connection %d %s error = %v", connectionIndex, name, err)
		}
		if got != want {
			t.Errorf("connection %d %s = %d, want %d", connectionIndex, name, got, want)
		}
	}
}
