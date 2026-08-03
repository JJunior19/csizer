package storage

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	_ "modernc.org/sqlite"

	embeddedmigrations "github.com/jorgeccarhuasaroni/containersize/migrations"
)

func TestDiscoverMigrationsValidation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		files     fstest.MapFS
		wantCount int
		wantError string
	}{
		{
			name: "orders and checksums exact bytes",
			files: fstest.MapFS{
				"002_second.sql": {Data: []byte("SELECT 2")},
				"001_first.sql":  {Data: []byte("SELECT 1\n")},
			},
			wantCount: 2,
		},
		{
			name:      "rejects malformed version",
			files:     fstest.MapFS{"1_first.sql": {Data: []byte("SELECT 1")}},
			wantError: "malformed migration filename",
		},
		{
			name: "rejects duplicate numeric version",
			files: fstest.MapFS{
				"001_first.sql": {Data: []byte("SELECT 1")},
				"001_other.sql": {Data: []byte("SELECT 2")},
			},
			wantError: "duplicate migration version",
		},
		{
			name: "rejects gaps",
			files: fstest.MapFS{
				"001_first.sql": {Data: []byte("SELECT 1")},
				"003_third.sql": {Data: []byte("SELECT 3")},
			},
			wantError: "migration version gap",
		},
		{
			name:      "rejects no migrations",
			files:     fstest.MapFS{"README.txt": {Data: []byte("none")}},
			wantError: "no embedded migrations",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			items, err := discoverMigrations(test.files)
			if test.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantError) {
					t.Fatalf("discoverMigrations() error = %v, want containing %q", err, test.wantError)
				}
				return
			}
			if err != nil {
				t.Fatalf("discoverMigrations() error = %v", err)
			}
			if len(items) != test.wantCount {
				t.Fatalf("migration count = %d, want %d", len(items), test.wantCount)
			}
			for index, item := range items {
				if item.version != index+1 {
					t.Errorf("migration %d version = %d, want %d", index, item.version, index+1)
				}
			}
			digest := sha256.Sum256([]byte("SELECT 1\n"))
			if items[0].checksum != hex.EncodeToString(digest[:]) {
				t.Errorf("checksum = %q, want SHA-256 of exact bytes", items[0].checksum)
			}
		})
	}
}

func TestMigrationAndVersionRecordAreAtomic(t *testing.T) {
	t.Parallel()

	databasePath := filepath.Join(t.TempDir(), "atomic migration.db")
	database, err := sql.Open("sqlite", databaseDSN(databasePath))
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })

	files := fstest.MapFS{
		"001_broken.sql": {Data: []byte(`
CREATE TABLE partial_table (id INTEGER PRIMARY KEY);
INSERT INTO missing_table(id) VALUES (1);`)},
	}
	if err := applyMigrations(t.Context(), database, files); err == nil {
		t.Fatal("applyMigrations() error = nil, want migration failure")
	}

	var count int
	if err := database.QueryRowContext(
		t.Context(),
		`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'partial_table'`,
	).Scan(&count); err != nil {
		t.Fatalf("query partial table error = %v", err)
	}
	if count != 0 {
		t.Errorf("partial table count = %d, want 0", count)
	}
	if err := database.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM schema_migrations`).Scan(&count); err != nil {
		t.Fatalf("query schema_migrations error = %v", err)
	}
	if count != 0 {
		t.Errorf("migration record count = %d, want 0", count)
	}
}

func TestAppliedMigrationChecksumDriftFails(t *testing.T) {
	t.Parallel()

	store := openTestStore(t, filepath.Join(t.TempDir(), "checksum drift.db"))
	original, err := fs.ReadFile(embeddedmigrations.Files, "001_initial.sql")
	if err != nil {
		t.Fatalf("read embedded migration error = %v", err)
	}
	drifted := append(append([]byte(nil), original...), '\n')
	err = applyMigrations(t.Context(), store.db, fstest.MapFS{
		"001_initial.sql": {Data: drifted},
	})
	if err == nil || !strings.Contains(err.Error(), "checksum drift") {
		t.Fatalf("applyMigrations(drifted) error = %v, want checksum drift", err)
	}
	assertMigrationCount(t, store.db, 1)
}

func TestImmediateTransactionRollbackFailureDiscardsConnection(t *testing.T) {
	t.Parallel()

	database, err := sql.Open("sqlite", databaseDSN(filepath.Join(t.TempDir(), "rollback failure.db")))
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	connection, err := database.Conn(t.Context())
	if err != nil {
		t.Fatalf("DB.Conn() error = %v", err)
	}
	defer func() { _ = connection.Close() }()
	primaryErr := errors.New("operation failed")
	err = withImmediateTransaction(t.Context(), connection, func() error {
		if _, err := connection.ExecContext(t.Context(), `ROLLBACK`); err != nil {
			t.Fatalf("force transaction end error = %v", err)
		}
		return primaryErr
	})
	if !errors.Is(err, primaryErr) {
		t.Fatalf("withImmediateTransaction() error = %v, want primary error", err)
	}
	if !strings.Contains(err.Error(), "rollback migration transaction") {
		t.Fatalf("withImmediateTransaction() error = %v, want rollback context", err)
	}
	if _, connectionErr := connection.ExecContext(t.Context(), `SELECT 1`); connectionErr == nil {
		t.Fatal("discarded migration connection remained usable")
	}
	if err := database.PingContext(t.Context()); err != nil {
		t.Fatalf("database did not replace discarded connection: %v", err)
	}
}

func TestDiscoverMigrationsReadError(t *testing.T) {
	t.Parallel()

	_, err := discoverMigrations(errorFS{})
	if err == nil || !strings.Contains(err.Error(), "read embedded migrations") {
		t.Fatalf("discoverMigrations() error = %v, want read error", err)
	}
}

type errorFS struct{}

func (errorFS) Open(string) (fs.File, error) {
	return nil, fs.ErrPermission
}
