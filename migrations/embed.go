// Package migrations exposes ContainerSize's embedded SQLite migrations.
package migrations

import "embed"

// Files contains the versioned SQL migrations bundled with the binary.
//
//go:embed *.sql
var Files embed.FS
