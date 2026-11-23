// Package web holds the embedded HTML templates and static assets.
package web

import (
	"embed"
	"html/template"
	"io"
	"io/fs"
	"net/http"
)

//go:embed templates/*.html
var templateFS embed.FS

//go:embed static
var staticFS embed.FS

//go:embed static/style.css
var styleCSS string

var tmpl = template.Must(template.New("").Funcs(template.FuncMap{
	"style": func() template.CSS { return template.CSS(styleCSS) },
}).ParseFS(templateFS, "templates/*.html"))

// Render executes the named template (e.g. "page.html").
func Render(w io.Writer, name string, data any) error { return tmpl.ExecuteTemplate(w, name, data) }

// Static serves the embedded static directory.
var Static http.FileSystem = func() http.FileSystem {
	sub, err := fs.Sub(staticFS, "static")
	if err != nil {
		panic(err)
	}
	return http.FS(sub)
}()
