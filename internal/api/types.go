// Package api defines the JSON types of the airs control API (Unix socket).
// The CLI and a future menu bar app speak this API.
package api

import "airs/internal/notes"

// Item statuses.
const (
	StatusShared  = "shared"
	StatusBlocked = "blocked"
	StatusMissing = "missing"
)

type ItemView struct {
	ID         int    `json:"id"`
	Name       string `json:"name"`
	Path       string `json:"path"`
	IsDir      bool   `json:"is_dir"`
	Project    string `json:"project"`
	AnyNetwork bool   `json:"any_network"`
	Notes      bool   `json:"notes"` // readers may add notes (config and item allow it)
	URL        string `json:"url"`
	Status     string `json:"status"`
	NotesPath  string `json:"notes_path,omitempty"` // file items: where their notes are kept
}

type AddRequest struct {
	Path    string `json:"path"`
	Any     bool   `json:"any"`
	NoNotes bool   `json:"no_notes"`
	// Project is the item's project; empty means work it out from the path.
	// An item already shared moves to it only when MoveProject is set.
	Project     string `json:"project"`
	MoveProject bool   `json:"move_project"`
	// NotesDir (file items only) keeps the item's notes there instead of beside it.
	NotesDir string `json:"notes_dir"`
}

// NotesResponse is a document's notes and where they're kept.
type NotesResponse struct {
	Path  string       `json:"path"`
	Doc   string       `json:"doc"`
	Notes []notes.Note `json:"notes"`
}

type AddResponse struct {
	Item           ItemView `json:"item"`
	Existed        bool     `json:"existed"`
	NetworkTrusted bool     `json:"network_trusted"`
}

type StatusResponse struct {
	BaseURL        string `json:"base_url"`
	NetworkTrusted bool   `json:"network_trusted"`
	NetworkLabel   string `json:"network_label,omitempty"`
	Fingerprint    string `json:"fingerprint,omitempty"`
	AnyNetwork     bool   `json:"any_network"`
	PID            int    `json:"pid"`
}

type TrustRequest struct {
	Label string `json:"label"`
}

type ErrorResponse struct {
	Error   string     `json:"error"`
	Matches []ItemView `json:"matches,omitempty"`
}
