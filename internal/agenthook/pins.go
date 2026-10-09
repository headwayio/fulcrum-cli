package agenthook

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// PinsDir holds one file per harness session naming the card that session
// is working, under the user's config dir.
//
// WHY A SESSION PIN EXISTS. The checkout pin (`.fulcrum/current-work.json`)
// is one card per checkout, which is right for a developer at a keyboard and
// wrong the moment one harness session fans work out to subagents that each
// pick up a card: they share the checkout, so the hook would put every
// transcript on whichever card happened to be pinned. A session pin is
// written by the MCP bridge when start_work passes through carrying a
// session_ref — the same reference the hook later sees as the transcript's
// session id — so attribution follows the session, and the checkout pin is
// only the fallback.
//
// ONE FILE PER SESSION, not a map in the watermark file: several bridges and
// hooks run at once when subagents work in parallel, and a shared file would
// lose writes. A pin is never cleared by finish_work, because the turn that
// called it is still being written when the hook fires; it is replaced by the
// session's next start_work, and forgotten with the watermark after a month.
const PinsDir = "session-pins"

// SessionPin is what the hook needs to post a session's turns: the numeric
// ids, because it fires on its own and cannot turn "FUL-17" into a row.
type SessionPin struct {
	Feature   string    `json:"feature"`
	FeatureID int64     `json:"feature_id"`
	ProjectID int64     `json:"project_id"`
	Role      string    `json:"role,omitempty"`
	UpdatedAt time.Time `json:"updated_at"`
}

// WriteSessionPin records the card a session is working, replacing any
// earlier pin for the same session.
func WriteSessionPin(dir, sessionRef string, pin SessionPin) error {
	path := pinPath(dir, sessionRef)
	if path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	encoded, err := json.MarshalIndent(pin, "", "  ")
	if err != nil {
		return err
	}
	// Written whole then renamed, so a hook reading mid-write never sees a
	// torn file and falls back to the wrong card.
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(encoded, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// ReadSessionPin returns the card a session is working, or nil when the
// session never called start_work with its reference. Anything unreadable
// is treated as absent, for the same reason the watermark is.
func ReadSessionPin(dir, sessionRef string) *SessionPin {
	path := pinPath(dir, sessionRef)
	if path == "" {
		return nil
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var pin SessionPin
	if err := json.Unmarshal(content, &pin); err != nil || pin.FeatureID == 0 || pin.ProjectID == 0 {
		return nil
	}
	return &pin
}

// PruneSessionPins forgets pins nothing has refreshed in a month, on the
// watermark's schedule. Errors are ignored: a stale pin file costs nothing
// but a few bytes.
func PruneSessionPins(dir string, now time.Time) {
	entries, err := os.ReadDir(filepath.Join(dir, PinsDir))
	if err != nil {
		return
	}
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil || entry.IsDir() {
			continue
		}
		if now.Sub(info.ModTime()) > forgetAfter {
			_ = os.Remove(filepath.Join(dir, PinsDir, entry.Name()))
		}
	}
}

// pinPath maps a session reference onto a file name. References are ids a
// harness minted, so they are already path-safe in practice; anything else
// is replaced rather than trusted.
func pinPath(dir, sessionRef string) string {
	sessionRef = strings.TrimSpace(sessionRef)
	if dir == "" || sessionRef == "" {
		return ""
	}
	safe := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			return r
		}
		return '_'
	}, sessionRef)
	return filepath.Join(dir, PinsDir, safe+".json")
}
