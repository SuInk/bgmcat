package web

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var content embed.FS

func FS() fs.FS { sub, _ := fs.Sub(content, "dist"); return sub }
