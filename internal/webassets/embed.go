// Package webassets embeds the independently authored frontend build.
package webassets

import "embed"

//go:embed all:dist
var Files embed.FS
