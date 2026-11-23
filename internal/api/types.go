// Package api defines the JSON types of the airs control API (Unix socket).
// The CLI and a future menu bar app speak this API.
package api

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
	AnyNetwork bool   `json:"any_network"`
	URL        string `json:"url"`
	Status     string `json:"status"`
}

type AddRequest struct {
	Path string `json:"path"`
	Any  bool   `json:"any"`
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
