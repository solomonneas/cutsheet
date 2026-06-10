// Package web embeds the built single-page UI. The go:embed directive must
// live in the directory that contains dist/, so this thin package only
// exposes the file system; serving logic lives in internal/webui.
//
// dist/ is a committed build artifact (see implementation-notes.md): keeping
// it in git means `go build` and `go install` work without Node. Rebuild it
// with `make ui` after changing anything under web/src.
package web

import "embed"

//go:embed all:dist
var Dist embed.FS
