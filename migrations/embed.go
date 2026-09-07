// Package migrations embeds the numbered, forward-only SQL migration files so
// the binary carries its own schema. Files are named NNNN_name.up.sql and are
// applied in filename order.
package migrations

import "embed"

// FS holds the migration files.
//
//go:embed *.up.sql
var FS embed.FS
