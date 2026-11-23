// Package render turns source documents into HTML, keyed by file extension.
package render

import (
	"html/template"
	"path/filepath"
	"strings"
)

// Doc is a rendered document.
type Doc struct {
	HTML  template.HTML
	Title string
}

// Options tune a render.
type Options struct {
	// AssetPrefix, when set, is prepended to relative image URLs.
	AssetPrefix string
}

// Renderer renders one document format.
type Renderer interface {
	Render(src []byte, opt Options) (Doc, error)
}

var byExt = map[string]Renderer{
	".md":       markdown,
	".markdown": markdown,
}

// ForPath returns the renderer for path's extension.
func ForPath(path string) (Renderer, bool) {
	r, ok := byExt[strings.ToLower(filepath.Ext(path))]
	return r, ok
}

var imageExts = map[string]bool{".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".svg": true, ".webp": true, ".avif": true}

// IsImage reports whether path has an image extension.
func IsImage(path string) bool { return imageExts[strings.ToLower(filepath.Ext(path))] }
