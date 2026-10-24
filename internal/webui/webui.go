// Package webui embeds the browser-based control interface served by LED Link.
package webui

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed assets
var assets embed.FS

// FS returns the embedded UI file system rooted at the asset directory.
func FS() fs.FS {
	sub, err := fs.Sub(assets, "assets")
	if err != nil {
		panic(err) // the embed directive above guarantees this directory exists
	}
	return sub
}

// Handler serves the embedded single-page interface.
func Handler() http.Handler {
	return http.FileServer(http.FS(FS()))
}
