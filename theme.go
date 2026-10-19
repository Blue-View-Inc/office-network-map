package main

import (
	_ "embed"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

//go:embed assets/ui.css
var uiCSS string

//go:embed assets/ui.js
var uiJS string

// themeAssets are the only file types served from the -theme directory.
var themeAssets = map[string]string{
	".css": "text/css; charset=utf-8", ".woff2": "font/woff2", ".woff": "font/woff",
	".png": "image/png", ".svg": "image/svg+xml", ".jpg": "image/jpeg",
}

// themeDir is the optional -theme directory. Its stylesheets are linked after the built-in
// ones on every page; nothing from it is ever embedded or copied.
var themeDir string

// themeLinks is the <link> block for the theme's stylesheets ("" without a theme).
func themeLinks() string {
	if themeDir == "" {
		return ""
	}
	ents, err := os.ReadDir(themeDir)
	if err != nil {
		return ""
	}
	var names []string
	for _, e := range ents {
		if !e.IsDir() && strings.HasSuffix(strings.ToLower(e.Name()), ".css") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	var b strings.Builder
	for _, n := range names {
		b.WriteString(`<link rel="stylesheet" href="/theme/` + esc(n) + `">` + "\n")
	}
	return b.String()
}

// serveTheme serves one whitelisted file from the theme directory.
func (s *Server) serveTheme(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	ct, ok := themeAssets[strings.ToLower(filepath.Ext(name))]
	if themeDir == "" || !ok || name != filepath.Base(name) || strings.HasPrefix(name, ".") {
		http.NotFound(w, r)
		return
	}
	b, err := os.ReadFile(filepath.Join(themeDir, name))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Cache-Control", "no-cache")
	w.Write(b)
}
