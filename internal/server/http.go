// Package server is the airs background daemon: LAN HTTP, live reload, and
// the control API.
package server

import (
	"bytes"
	"encoding/json"
	"html/template"
	"log"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"airs/internal/config"
	"airs/internal/notes"
	"airs/internal/qr"
	"airs/internal/render"
	"airs/internal/state"
	"airs/internal/watch"
	"airs/internal/web"
)

type httpServer struct {
	store *state.Store
	cfg   config.Config
	ns    *NetState
	hub   *watch.Hub
}

// NewHTTP returns the LAN handler.
func NewHTTP(store *state.Store, cfg config.Config, ns *NetState, hub *watch.Hub) http.Handler {
	s := &httpServer{store: store, cfg: cfg, ns: ns, hub: hub}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.index)
	mux.Handle("GET /_airs/static/", http.StripPrefix("/_airs/static/", http.FileServer(web.Static)))
	mux.HandleFunc("GET /_airs/asset/{slug}/{rel...}", s.asset)
	mux.HandleFunc("GET /_airs/qr", s.qrPNG)
	mux.HandleFunc("GET /_airs/", func(w http.ResponseWriter, r *http.Request) { s.notFound(w) })
	mux.HandleFunc("GET /{slug}", s.item)
	mux.HandleFunc("GET /{slug}/{rel...}", s.itemPath)
	s.routesLive(mux)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !hostAllowed(r.Host) {
			http.Error(w, "airs: unexpected Host header", http.StatusForbidden)
			return
		}
		mux.ServeHTTP(w, r)
	})
}

func (s *httpServer) allowed(r *http.Request, it state.Item) bool {
	info, trusted := s.ns.Get()
	return Allowed(it, s.cfg, trusted && arrivedOn(r, info.LocalIP), isLocal(r))
}

func (s *httpServer) canWrite(r *http.Request) bool {
	info, trusted := s.ns.Get()
	return CanWrite(s.cfg, trusted && arrivedOn(r, info.LocalIP), isLocal(r))
}

// notesOn reports whether notes are switched on for the document at docAbs.
func (s *httpServer) notesOn(it state.Item, docAbs string) bool {
	_, md := render.ForPath(docAbs)
	return s.cfg.Notes && !it.NoNotes && md && !notes.IsNotesFile(docAbs)
}

func (s *httpServer) page(w http.ResponseWriter, status int, name string, data any) {
	var buf bytes.Buffer
	if err := web.Render(&buf, name, data); err != nil {
		log.Printf("render %s: %v", name, err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	w.Write(buf.Bytes())
}

func (s *httpServer) notFound(w http.ResponseWriter) {
	s.page(w, http.StatusNotFound, "notfound.html", nil)
}

func (s *httpServer) forbidden(w http.ResponseWriter) {
	s.page(w, http.StatusForbidden, "forbidden.html", nil)
}

// lookup finds the item for a request and applies the network gate.
func (s *httpServer) lookup(w http.ResponseWriter, r *http.Request) (state.Item, bool) {
	it, ok := s.store.BySlug(r.PathValue("slug"))
	if !ok {
		s.notFound(w)
		return it, false
	}
	if !s.allowed(r, it) {
		s.forbidden(w)
		return it, false
	}
	return it, true
}

type link struct {
	Name, Href string
	IsDir      bool
}

func (s *httpServer) index(w http.ResponseWriter, r *http.Request) {
	var items []link
	for _, it := range s.store.Items() {
		if s.allowed(r, it) {
			items = append(items, link{Name: it.Name(), Href: ItemPath(it), IsDir: it.IsDir})
		}
	}
	s.page(w, http.StatusOK, "index.html", map[string]any{"Items": items})
}

func (s *httpServer) item(w http.ResponseWriter, r *http.Request) {
	it, ok := s.lookup(w, r)
	if !ok {
		return
	}
	if it.IsDir {
		http.Redirect(w, r, ItemPath(it), http.StatusMovedPermanently)
		return
	}
	s.renderPage(w, r, it, "")
}

func (s *httpServer) itemPath(w http.ResponseWriter, r *http.Request) {
	it, ok := s.lookup(w, r)
	if !ok {
		return
	}
	if !it.IsDir {
		s.notFound(w)
		return
	}
	rel := r.PathValue("rel")
	trailing := rel == "" || strings.HasSuffix(rel, "/")
	rel = strings.TrimSuffix(rel, "/")
	abs, err := Resolve(it, rel)
	if err != nil {
		s.notFound(w)
		return
	}
	fi, err := os.Stat(abs)
	if err != nil {
		s.notFound(w)
		return
	}
	switch {
	case fi.IsDir() && !trailing:
		http.Redirect(w, r, r.URL.EscapedPath()+"/", http.StatusMovedPermanently)
	case fi.IsDir():
		s.listing(w, it, rel, abs)
	case trailing:
		s.notFound(w)
	default:
		if _, ok := render.ForPath(abs); ok {
			s.renderPage(w, r, it, rel)
			return
		}
		serveRaw(w, r, abs)
	}
}

func (s *httpServer) asset(w http.ResponseWriter, r *http.Request) {
	it, ok := s.lookup(w, r)
	if !ok {
		return
	}
	if it.IsDir {
		s.notFound(w)
		return
	}
	rel := r.PathValue("rel")
	abs, err := Resolve(it, rel)
	if err != nil || rel == "" || !s.references(it, rel) {
		s.notFound(w)
		return
	}
	serveRaw(w, r, abs)
}

// references reports whether a single-file item's markdown links to image rel,
// so sharing ~/Desktop/todo.md does not expose every image on the Desktop.
func (s *httpServer) references(it state.Item, rel string) bool {
	src, err := os.ReadFile(it.Path)
	if err != nil {
		return false
	}
	r, ok := render.ForPath(it.Path)
	if !ok {
		return false
	}
	d, err := r.Render(src, render.Options{})
	if err != nil {
		return false
	}
	for _, img := range d.Images {
		if u, err := url.PathUnescape(img); err == nil && path.Clean(u) == path.Clean(rel) {
			return true
		}
	}
	return false
}

func serveRaw(w http.ResponseWriter, r *http.Request, abs string) {
	f, err := os.Open(abs)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil || fi.IsDir() {
		http.NotFound(w, r)
		return
	}
	http.ServeContent(w, r, fi.Name(), fi.ModTime(), f)
}

func (s *httpServer) listing(w http.ResponseWriter, it state.Item, rel, abs string) {
	ents, err := os.ReadDir(abs)
	if err != nil {
		s.notFound(w)
		return
	}
	var dirs, files []link
	for _, e := range ents {
		name := e.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		isDir := e.IsDir()
		if e.Type()&os.ModeSymlink != 0 {
			if _, err := Resolve(it, path.Join(rel, name)); err != nil {
				continue // symlink pointing outside the folder
			}
			fi, err := os.Stat(filepath.Join(abs, name))
			if err != nil {
				continue
			}
			isDir = fi.IsDir()
		}
		switch {
		case isDir:
			dirs = append(dirs, link{Name: name + "/", Href: url.PathEscape(name) + "/", IsDir: true})
		default:
			if _, ok := render.ForPath(name); ok {
				files = append(files, link{Name: name, Href: url.PathEscape(name)})
			}
		}
	}
	byName := func(l []link) {
		sort.Slice(l, func(i, j int) bool { return strings.ToLower(l[i].Name) < strings.ToLower(l[j].Name) })
	}
	byName(dirs)
	byName(files)
	title := it.Name() + rel
	if rel != "" {
		title += "/"
	}
	urlPath := ItemPath(it) + escapeRel(rel)
	if rel != "" {
		urlPath += "/"
	}
	s.page(w, http.StatusOK, "listing.html", map[string]any{
		"Title":   title,
		"Entries": append(dirs, files...),
		"URL":     urlPath,
		"DocJSON": docJSON(map[string]any{"key": it.Key(), "kind": "listing"}),
	})
}

func escapeRel(rel string) string {
	if rel == "" {
		return ""
	}
	parts := strings.Split(rel, "/")
	for i, p := range parts {
		parts[i] = url.PathEscape(p)
	}
	return strings.Join(parts, "/")
}

// renderedDoc is the shared result used by full pages and live-reload JSON.
type renderedDoc struct {
	HTML         template.HTML `json:"html"`
	Title        string        `json:"title"`
	Markdown     string        `json:"markdown"`
	NotesEnabled bool          `json:"notesEnabled"` // this request may add/edit notes
	Notes        []notes.Note  `json:"notes"`
}

func (s *httpServer) renderDoc(r *http.Request, it state.Item, rel string) (renderedDoc, error) {
	abs, err := Resolve(it, rel)
	if err != nil {
		return renderedDoc{}, err
	}
	src, err := os.ReadFile(abs)
	if err != nil {
		return renderedDoc{}, ErrNotFound
	}
	rd, ok := render.ForPath(abs)
	if !ok {
		return renderedDoc{}, ErrNotFound
	}
	opt := render.Options{}
	if !it.IsDir {
		opt.AssetPrefix = "/_airs/asset/" + url.PathEscape(it.Slug) + "/"
	}
	d, err := rd.Render(src, opt)
	if err != nil {
		return renderedDoc{}, err
	}
	title := d.Title
	if title == "" {
		title = filepath.Base(abs)
	}
	doc := renderedDoc{HTML: d.HTML, Title: title, Markdown: string(src), Notes: []notes.Note{}}
	if s.notesOn(it, abs) {
		ns, err := notes.Read(abs)
		if err != nil {
			log.Printf("notes for %s: %v", abs, err)
		}
		if ns != nil {
			doc.Notes = ns
		}
		doc.NotesEnabled = s.canWrite(r)
	}
	return doc, nil
}

func (s *httpServer) renderPage(w http.ResponseWriter, r *http.Request, it state.Item, rel string) {
	d, err := s.renderDoc(r, it, rel)
	if err != nil {
		s.notFound(w)
		return
	}
	crumb := it.Name()
	urlPath := ItemPath(it)
	file := filepath.Base(it.Path)
	if rel != "" {
		crumb += rel
		urlPath += escapeRel(rel)
		file = path.Base(rel)
	}
	s.page(w, http.StatusOK, "page.html", map[string]any{
		"Title": d.Title,
		"Crumb": crumb,
		"Body":  d.HTML,
		"URL":   urlPath,
		"DocJSON": docJSON(map[string]any{"key": it.Key(), "kind": "doc", "rel": rel, "file": file, "title": d.Title, "markdown": d.Markdown,
			"notes": d.Notes, "notesEnabled": d.NotesEnabled}),
	})
}

// docJSON is safe to place inside <script type="application/json">:
// encoding/json escapes <, > and & so "</script>" cannot appear.
func docJSON(v any) template.JS {
	b, _ := json.Marshal(v)
	return template.JS(b)
}

// qrPNG encodes BaseURL + u. Only paths on this server are accepted.
func (s *httpServer) qrPNG(w http.ResponseWriter, r *http.Request) {
	u := r.URL.Query().Get("u")
	if !strings.HasPrefix(u, "/") || strings.HasPrefix(u, "//") {
		http.Error(w, "u must be a path on this server", http.StatusBadRequest)
		return
	}
	info, _ := s.ns.Get()
	png, err := qr.PNG(BaseURL(info, s.cfg.Port)+u, 256)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "no-store")
	w.Write(png)
}
