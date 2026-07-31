package migrations

import "embed"

// Files contains the control-plane database migrations.
//
//go:embed *.sql
var Files embed.FS
