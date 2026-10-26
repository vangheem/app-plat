package userui

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed static/app.css static/htmx.min.js
var staticFS embed.FS

func staticHandler() http.Handler {
	sub, err := fs.Sub(staticFS, "static")
	if err != nil {
		panic(err)
	}
	return http.StripPrefix("/static/", http.FileServer(http.FS(sub)))
}
