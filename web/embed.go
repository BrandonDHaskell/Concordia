// Package web holds the vendored frontend assets and Go templates for the web
// view. There is no build step: htmx is a committed file, CSS is hand-written,
// and templates are html/template. See CLAUDE.md invariant 3.
package web

import "embed"

// Static holds the files served under /static/ (htmx, CSS).
//
//go:embed static
var Static embed.FS

// Templates holds the html/template sources for the web view.
//
//go:embed templates/*.html
var Templates embed.FS
