// Package migrations contains only this application's independently authored schema.
package migrations

import "embed"

//go:embed *.sql
var Files embed.FS
