// Package storage owns ContainerSize's local SQLite persistence.
package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"modernc.org/sqlite"

	"github.com/jorgeccarhuasaroni/containersize/migrations"
)

const maxOpenConnections = 4

// Store provides concurrent-safe access to ContainerSize's SQLite database.
type Store struct {
	db   *sql.DB
	path string
}

// Open creates or opens a database, configures its connection pool, and applies
// all embedded migrations.
func Open(ctx context.Context, path string) (*Store, error) {
	if path == "" {
		return nil, errors.New("open storage: database path is empty")
	}

	absolutePath, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("open storage: resolve database path: %w", err)
	}
	canonicalParent, err := prepareDatabaseParent(filepath.Dir(absolutePath))
	if err != nil {
		return nil, fmt.Errorf("open storage: prepare database directory: %w", err)
	}
	databasePath := filepath.Join(canonicalParent, filepath.Base(absolutePath))

	created, err := prepareDatabaseFile(databasePath)
	if err != nil {
		return nil, fmt.Errorf("open storage: prepare database file: %w", err)
	}
	if created {
		if err := os.Chmod(databasePath, 0o600); err != nil {
			return nil, fmt.Errorf("open storage: secure database file: %w", err)
		}
	}

	database, err := sql.Open("sqlite", databaseDSN(databasePath))
	if err != nil {
		return nil, fmt.Errorf("open storage: initialize SQLite: %w", err)
	}
	database.SetMaxOpenConns(maxOpenConnections)
	database.SetMaxIdleConns(maxOpenConnections)

	store := &Store{db: database, path: databasePath}
	if err := pingSQLite(ctx, database); err != nil {
		return nil, closeAfterOpenError(database, fmt.Errorf("open storage: ping SQLite: %w", err))
	}
	if err := applyMigrations(ctx, database, migrations.Files); err != nil {
		return nil, closeAfterOpenError(database, fmt.Errorf("open storage: migrate database: %w", err))
	}

	return store, nil
}

func prepareDatabaseParent(path string) (string, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(path, 0o700); err != nil {
			return "", fmt.Errorf("create database parent %q: %w", path, err)
		}
		info, err = os.Lstat(path)
	}
	if err != nil {
		return "", fmt.Errorf("inspect database parent %q: %w", path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("database parent %q is a symbolic link", path)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("database parent %q is not a directory", path)
	}

	canonicalParent, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", fmt.Errorf("canonicalize database parent %q: %w", path, err)
	}
	canonicalParent, err = filepath.Abs(canonicalParent)
	if err != nil {
		return "", fmt.Errorf("resolve canonical database parent %q: %w", canonicalParent, err)
	}
	if err := validateCanonicalDirectoryChain(canonicalParent); err != nil {
		return "", err
	}
	return canonicalParent, nil
}

func validateCanonicalDirectoryChain(path string) error {
	volume := filepath.VolumeName(path)
	current := volume + string(filepath.Separator)
	relative := strings.TrimPrefix(path, current)
	components := []string{""}
	if relative != "" {
		components = append(components, strings.Split(relative, string(filepath.Separator))...)
	}
	for _, component := range components {
		if component != "" {
			current = filepath.Join(current, component)
		}
		info, err := os.Lstat(current)
		if err != nil {
			return fmt.Errorf("inspect canonical database ancestor %q: %w", current, err)
		}
		if !info.IsDir() {
			return fmt.Errorf("canonical database ancestor %q is not a directory", current)
		}
		writableByOthers := info.Mode().Perm()&0o022 != 0
		isFinalParent := filepath.Clean(current) == filepath.Clean(path)
		isSticky := info.Mode()&os.ModeSticky != 0
		if writableByOthers && (isFinalParent || !isSticky) {
			return fmt.Errorf("canonical database ancestor %q has insecure permissions %04o: group or other write access is not allowed", current, info.Mode().Perm())
		}
	}
	return nil
}

func pingSQLite(ctx context.Context, database *sql.DB) error {
	retryContext, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	for {
		err := database.PingContext(retryContext)
		if err == nil || !isSQLiteBusy(err) {
			return err
		}
		timer := time.NewTimer(10 * time.Millisecond)
		select {
		case <-retryContext.Done():
			timer.Stop()
			return errors.Join(err, retryContext.Err())
		case <-timer.C:
		}
	}
}

func isSQLiteBusy(err error) bool {
	var sqliteErr *sqlite.Error
	return errors.As(err, &sqliteErr) && sqliteErr.Code()&0xff == 5
}

// Close releases all database connections.
func (store *Store) Close() error {
	if store == nil || store.db == nil {
		return nil
	}
	if err := store.db.Close(); err != nil {
		return fmt.Errorf("close storage: %w", err)
	}
	return nil
}

// Path returns the absolute path of the open database.
func (store *Store) Path() string {
	return store.path
}

func prepareDatabaseFile(path string) (bool, error) {
	info, err := os.Lstat(path)
	if err == nil {
		return false, validateDatabaseFile(path, info)
	}
	if !errors.Is(err, os.ErrNotExist) {
		return false, err
	}

	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
	if err == nil {
		if closeErr := file.Close(); closeErr != nil {
			return true, closeErr
		}
		return true, nil
	}
	if errors.Is(err, os.ErrExist) {
		info, lstatErr := os.Lstat(path)
		if lstatErr != nil {
			return false, lstatErr
		}
		return false, validateDatabaseFile(path, info)
	}
	return false, err
}

func validateDatabaseFile(path string, info os.FileInfo) error {
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("database path %q is a symbolic link", path)
	}
	if info.IsDir() {
		return fmt.Errorf("database path %q is a directory", path)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("database path %q is not a regular file", path)
	}
	return nil
}

func closeAfterOpenError(database *sql.DB, operationErr error) error {
	if closeErr := database.Close(); closeErr != nil {
		return errors.Join(operationErr, fmt.Errorf("close storage after failure: %w", closeErr))
	}
	return operationErr
}

func databaseDSN(path string) string {
	databaseURL := &url.URL{Scheme: "file", Path: path}
	query := url.Values{}
	query.Add("_pragma", "busy_timeout(5000)")
	query.Add("_pragma", "foreign_keys(ON)")
	query.Add("_pragma", "journal_mode(WAL)")
	query.Add("_pragma", "synchronous(NORMAL)")
	databaseURL.RawQuery = query.Encode()
	return databaseURL.String()
}
