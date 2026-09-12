package ui

import (
	"embed"
	"io/fs"
)

// Assets contains the production Web build, generated before compiling Server.
//
//go:embed dist
var assets embed.FS

func Assets() fs.FS {
	result, err := fs.Sub(assets, "dist")
	if err != nil {
		panic(err)
	}
	return result
}
