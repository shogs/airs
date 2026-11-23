package server

import (
	"encoding/json"
	"errors"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"airs/internal/notes"
	"airs/internal/render"
)

// maxNoteBody caps a note request body.
const maxNoteBody = 16 << 10

func (s *httpServer) routesNotes(mux *http.ServeMux) {
	mux.HandleFunc("POST /_airs/notes/{key}/{rel...}", s.postNote)
	mux.HandleFunc("PUT /_airs/notes/{key}/{rel...}", s.putNote)
}

func noteError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// noteDoc runs every check a note write must pass, in order, and returns the
// symlink-resolved document the note belongs to. It writes the error response
// itself when a check fails.
func (s *httpServer) noteDoc(w http.ResponseWriter, r *http.Request, dst any) (string, bool) {
	it, ok := s.byKey(r.PathValue("key"))
	if !ok {
		writeJSON(w, http.StatusGone, map[string]bool{"gone": true})
		return "", false
	}
	if !s.allowed(r, it) {
		noteError(w, http.StatusForbidden, "not shared on this network")
		return "", false
	}
	if !s.canWrite(r) {
		noteError(w, http.StatusForbidden, "notes can only be added on a trusted network")
		return "", false
	}
	abs, err := Resolve(it, r.PathValue("rel"))
	if err != nil {
		noteError(w, http.StatusNotFound, "no such document")
		return "", false
	}
	fi, err := os.Stat(abs)
	if _, md := render.ForPath(abs); err != nil || !fi.Mode().IsRegular() || !md {
		noteError(w, http.StatusNotFound, "no such document")
		return "", false
	}
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		noteError(w, http.StatusNotFound, "no such document")
		return "", false
	}
	if !s.notesOn(it, abs) || !s.notesOn(it, real) {
		noteError(w, http.StatusForbidden, "notes are turned off for this document")
		return "", false
	}
	if mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type")); err != nil || mt != "application/json" {
		noteError(w, http.StatusUnsupportedMediaType, "send the note as application/json")
		return "", false
	}
	// Browsers send Origin on cross-site requests; only pages from this server may write.
	if o := r.Header.Get("Origin"); o != "" && o != "http://"+r.Host {
		noteError(w, http.StatusForbidden, "notes can only be added from airs pages")
		return "", false
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxNoteBody)).Decode(dst); err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			noteError(w, http.StatusBadRequest, "note is too large")
		} else {
			noteError(w, http.StatusBadRequest, "bad request")
		}
		return "", false
	}
	return real, true
}

// saveError maps a notes.Writer error to a response.
func saveError(w http.ResponseWriter, err error) {
	var ie *notes.InputError
	var ce *notes.ConflictError
	switch {
	case errors.As(err, &ie):
		noteError(w, http.StatusBadRequest, ie.Msg)
	case errors.As(err, &ce):
		writeJSON(w, http.StatusConflict, map[string]any{"error": ce.Error(), "note": ce.Current})
	case errors.Is(err, notes.ErrNotFound):
		noteError(w, http.StatusNotFound, "note not found — it may have been removed on the Mac")
	default:
		noteError(w, http.StatusInternalServerError, "couldn't save note: "+err.Error())
	}
}

func (s *httpServer) postNote(w http.ResponseWriter, r *http.Request) {
	var in notes.Input
	doc, ok := s.noteDoc(w, r, &in)
	if !ok {
		return
	}
	n, err := s.notes.Append(doc, in, time.Now())
	if err != nil {
		saveError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, n)
}

type noteEdit struct {
	ID   string `json:"id"`
	Text string `json:"text"`
	Was  string `json:"was"`
}

func (s *httpServer) putNote(w http.ResponseWriter, r *http.Request) {
	var e noteEdit
	doc, ok := s.noteDoc(w, r, &e)
	if !ok {
		return
	}
	n, err := s.notes.Edit(doc, e.ID, e.Was, e.Text, time.Now())
	if err != nil {
		saveError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, n)
}
