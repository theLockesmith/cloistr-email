// Package configs carries the SQL the service applies to its own database.
// It is embedded in the binary so the image needs nothing at runtime.
package configs

import "embed"

// SQL holds schema.sql (the baseline) and migrations/*.sql.
//
//go:embed schema.sql migrations/*.sql
var SQL embed.FS
