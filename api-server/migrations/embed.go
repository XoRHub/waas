// Package migrations embeds the SQL migration files into the binary so they
// are applied automatically at startup, with no external file dependency.
package migrations

import "embed"

//go:embed *.sql
var FS embed.FS
