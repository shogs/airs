package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"

	"airs/internal/api"
	"airs/internal/notes"
	"airs/internal/project"
	"airs/internal/render"
	"airs/internal/state"
)

func (d *Daemon) controlHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /items", d.ctlList)
	mux.HandleFunc("POST /items", d.ctlAdd)
	mux.HandleFunc("GET /items/{query...}", d.ctlFind)
	mux.HandleFunc("DELETE /items/{query...}", d.ctlRemove)
	mux.HandleFunc("GET /notes/{query...}", d.ctlNotes)
	mux.HandleFunc("POST /trust", d.ctlTrust)
	mux.HandleFunc("DELETE /trust", d.ctlUntrust)
	mux.HandleFunc("GET /status", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, d.status()) })
	mux.HandleFunc("POST /stop", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]bool{"stopping": true})
		d.stop()
	})
	return mux
}

func ctlError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, api.ErrorResponse{Error: msg})
}

func (d *Daemon) view(it state.Item) api.ItemView {
	info, trusted := d.ns.Get()
	v := api.ItemView{
		ID: it.ID, Name: it.Name(), Path: it.Path, IsDir: it.IsDir, Project: it.Project, AnyNetwork: it.AnyNetwork,
		Notes: d.cfg.Notes && !it.NoNotes, URL: ItemURL(BaseURL(info, d.port), it), Status: api.StatusShared,
	}
	if !it.IsDir {
		v.NotesPath = notesFile(it, it.Path)
	}
	if _, err := os.Stat(it.Path); err != nil {
		v.Status = api.StatusMissing
	} else if !Allowed(it, d.cfg, trusted, false) {
		v.Status = api.StatusBlocked
	}
	return v
}

func (d *Daemon) status() api.StatusResponse {
	info, trusted := d.ns.Get()
	fp := d.fingerprint()
	return api.StatusResponse{
		BaseURL: BaseURL(info, d.port), NetworkTrusted: trusted, NetworkLabel: d.store.TrustedLabel(fp),
		Fingerprint: fp, AnyNetwork: d.cfg.AnyNetwork, PID: os.Getpid(),
	}
}

func (d *Daemon) ctlList(w http.ResponseWriter, r *http.Request) {
	out := []api.ItemView{}
	for _, it := range d.store.Items() {
		out = append(out, d.view(it))
	}
	writeJSON(w, 200, out)
}

func (d *Daemon) ctlAdd(w http.ResponseWriter, r *http.Request) {
	var req api.AddRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		ctlError(w, 400, "bad request")
		return
	}
	if !filepath.IsAbs(req.Path) {
		ctlError(w, 400, "path must be absolute")
		return
	}
	fi, err := os.Stat(req.Path)
	if err != nil {
		ctlError(w, 400, "no such file or folder: "+req.Path)
		return
	}
	if !fi.IsDir() {
		if _, ok := render.ForPath(req.Path); !ok {
			ctlError(w, 400, "airs only shows markdown for now (.md, .markdown)")
			return
		}
	}
	if req.Project != "" {
		if err := project.Valid(req.Project); err != nil {
			ctlError(w, 400, err.Error())
			return
		}
	}
	notesDir := ""
	if req.NotesDir != "" {
		if fi.IsDir() {
			ctlError(w, 400, "--notes-dir is for files; a folder's notes stay beside each file")
			return
		}
		if !filepath.IsAbs(req.NotesDir) {
			ctlError(w, 400, "notes folder must be absolute")
			return
		}
		if err := os.MkdirAll(req.NotesDir, 0o755); err != nil {
			ctlError(w, 400, "can't create notes folder: "+err.Error())
			return
		}
		if notesDir, err = filepath.EvalSymlinks(req.NotesDir); err != nil {
			ctlError(w, 400, "can't use notes folder: "+err.Error())
			return
		}
	}
	canon, err := filepath.EvalSymlinks(req.Path)
	if err != nil {
		canon = filepath.Clean(req.Path)
	}
	it, existed, err := d.store.Add(canon, fi.IsDir(), state.Flags{AnyNetwork: req.Any, NoNotes: req.NoNotes,
		Project: req.Project, MoveProject: req.MoveProject, NotesDir: notesDir})
	if err != nil {
		ctlError(w, 500, err.Error())
		return
	}
	if err := d.hub.Watch(it.ID, it.Path, it.IsDir, watchPaths(it)...); err != nil {
		d.logf("watch %s: %v", it.Path, err)
	}
	d.cancelIdle()
	_, trusted := d.ns.Get()
	writeJSON(w, 200, api.AddResponse{Item: d.view(it), Existed: existed, NetworkTrusted: trusted})
}

// findError writes the response for a failed lookup; it reports whether err was nil.
func (d *Daemon) findError(w http.ResponseWriter, query string, err error) bool {
	var amb *state.AmbiguousError
	switch {
	case err == nil:
		return true
	case errors.As(err, &amb):
		resp := api.ErrorResponse{Error: amb.Error()}
		for _, m := range amb.Matches {
			resp.Matches = append(resp.Matches, d.view(m))
		}
		writeJSON(w, 409, resp)
	case errors.Is(err, state.ErrNotFound):
		ctlError(w, 404, "nothing shared matches "+query)
	default:
		ctlError(w, 500, err.Error())
	}
	return false
}

// canonicalQuery resolves symlinks in an absolute path query so it matches the
// stored (resolved) path.
func canonicalQuery(q string) string {
	if filepath.IsAbs(q) {
		if c, err := filepath.EvalSymlinks(q); err == nil {
			return c
		}
	}
	return q
}

func (d *Daemon) ctlFind(w http.ResponseWriter, r *http.Request) {
	it, err := d.store.Find(canonicalQuery(r.PathValue("query")))
	if d.findError(w, r.PathValue("query"), err) {
		writeJSON(w, 200, d.view(it))
	}
}

// ctlNotes returns the notes of a shared file, or of a file inside a shared folder.
func (d *Daemon) ctlNotes(w http.ResponseWriter, r *http.Request) {
	q := r.PathValue("query")
	cq := canonicalQuery(q)
	it, err := d.store.Find(cq)
	doc := it.Path
	if errors.Is(err, state.ErrNotFound) && filepath.IsAbs(cq) {
		var ok bool
		if it, _, ok = d.store.Containing(cq); ok {
			err, doc = nil, cq
		}
	}
	if errors.Is(err, state.ErrNotFound) {
		ctlError(w, 404, "not shared: "+q)
		return
	}
	if !d.findError(w, q, err) {
		return
	}
	if it.IsDir && doc == it.Path {
		ctlError(w, 400, it.Name()+" is a folder; ask for a file inside it")
		return
	}
	p := notesFile(it, doc)
	ns, err := notes.ReadFile(p)
	if err != nil {
		ctlError(w, 500, err.Error())
		return
	}
	if ns == nil {
		ns = []notes.Note{}
	}
	writeJSON(w, 200, api.NotesResponse{Path: p, Doc: doc, Notes: ns})
}

func (d *Daemon) ctlRemove(w http.ResponseWriter, r *http.Request) {
	it, err := d.store.Remove(canonicalQuery(r.PathValue("query")))
	if !d.findError(w, r.PathValue("query"), err) {
		return
	}
	d.hub.Unwatch(it.ID)
	v := d.view(it)
	v.Status = ""
	writeJSON(w, 200, v)
	d.checkIdle()
}

func (d *Daemon) ctlTrust(w http.ResponseWriter, r *http.Request) {
	var req api.TrustRequest
	json.NewDecoder(r.Body).Decode(&req)
	d.pollNet()
	fp := d.fingerprint()
	if fp == "" {
		ctlError(w, 409, "can't identify this network (no router found), so it can't be trusted")
		return
	}
	if err := d.store.Trust(state.Network{Fingerprint: fp, Label: req.Label}); err != nil {
		ctlError(w, 500, err.Error())
		return
	}
	d.pollNet()
	writeJSON(w, 200, d.status())
}

func (d *Daemon) ctlUntrust(w http.ResponseWriter, r *http.Request) {
	d.pollNet()
	d.store.Untrust(d.fingerprint())
	d.pollNet()
	writeJSON(w, 200, d.status())
}

// watchPaths are the extra files watched with an item: a file's notes file.
// A folder's notes files are inside it and already watched.
func watchPaths(it state.Item) []string {
	if it.IsDir {
		return nil
	}
	return []string{notesFile(it, it.Path)}
}
