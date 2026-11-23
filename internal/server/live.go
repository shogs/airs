package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"airs/internal/state"
	"airs/internal/watch"
)

const (
	ssePing      = 25 * time.Second
	sseGateCheck = 2 * time.Second
)

func (s *httpServer) routesLive(mux *http.ServeMux) {
	mux.HandleFunc("GET /_airs/events/{key}", s.events)
	mux.HandleFunc("GET /_airs/render/{key}/{rel...}", s.renderJSON)
}

func (s *httpServer) byKey(key string) (state.Item, bool) {
	for _, it := range s.store.Items() {
		if it.Key() == key {
			return it, true
		}
	}
	return state.Item{}, false
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

// liveItem resolves {key}: 410 when the item is gone (or the key is stale),
// 403 when the network gate blocks it.
func (s *httpServer) liveItem(w http.ResponseWriter, r *http.Request) (state.Item, bool) {
	it, ok := s.byKey(r.PathValue("key"))
	if !ok {
		writeJSON(w, http.StatusGone, map[string]bool{"gone": true})
		return it, false
	}
	if !s.allowed(r, it) {
		writeJSON(w, http.StatusForbidden, map[string]bool{"forbidden": true})
		return it, false
	}
	return it, true
}

func (s *httpServer) renderJSON(w http.ResponseWriter, r *http.Request) {
	it, ok := s.liveItem(w, r)
	if !ok {
		return
	}
	d, err := s.renderDoc(r, it, r.PathValue("rel"))
	if errors.Is(err, ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]bool{"missing": true})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, d)
}

// events streams Server-Sent Events: "ready" once subscribed, "change" on
// edits, "gone" when the item stops being served. The stream is closed if the
// network gate starts blocking the item; the client then sees 403 on reconnect.
func (s *httpServer) events(w http.ResponseWriter, r *http.Request) {
	it, ok := s.liveItem(w, r)
	if !ok {
		return
	}
	fl, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	ch, cancel := s.hub.Subscribe(it.ID)
	defer cancel()
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Accel-Buffering", "no")
	send := func(ev string) {
		fmt.Fprintf(w, "event: %s\ndata: {}\n\n", ev)
		fl.Flush()
	}
	send("ready")
	ping := time.NewTicker(ssePing)
	defer ping.Stop()
	gate := time.NewTicker(sseGateCheck)
	defer gate.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case ev, ok := <-ch:
			if !ok {
				return
			}
			send(string(ev.Kind))
			if ev.Kind == watch.Gone {
				return
			}
		case <-ping.C:
			fmt.Fprint(w, ": ping\n\n")
			fl.Flush()
		case <-gate.C:
			if !s.allowed(r, it) {
				return
			}
		}
	}
}
