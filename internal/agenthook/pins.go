package agenthook

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
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
// lose writes.
//
// A PIN IS A WINDOW. One session can work several cards in turn — an
// orchestrator opens a QA episode on one card mid-session, finishes it, and
// moves on — so a pin claims only the turns between its start_work
// (StartedAt) and its finish_work (FinishedAt), not the session's whole
// history before it or everything after it. finish_work does not delete the
// pin, because the turn that called it is still being written when the hook
// fires and must still post; it closes the window instead. The pin is
// replaced by the session's next start_work, and forgotten with the
// watermark after a month.
const PinsDir = "session-pins"

// SessionPin is what the hook needs to post a session's turns: the numeric
// ids, because it fires on its own and cannot turn "FUL-17" into a row, and
// the window those turns must fall in.
//
// A zero StartedAt or FinishedAt leaves that side of the window open. A pin
// written by a bridge that predates windows has neither, and claims every
// turn the session has not yet posted, exactly as it always did.
type SessionPin struct {
	Feature    string    `json:"feature"`
	FeatureID  int64     `json:"feature_id"`
	ProjectID  int64     `json:"project_id"`
	Role       string    `json:"role,omitempty"`
	StartedAt  time.Time `json:"started_at,omitzero"`
	FinishedAt time.Time `json:"finished_at,omitzero"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// Windowed reports whether the pin bounds the turns it claims at all.
func (p SessionPin) Windowed() bool {
	return !p.StartedAt.IsZero() || !p.FinishedAt.IsZero()
}

// Covers reports whether a turn overlaps the pin's window. Overlap, not
// containment: the turn that called start_work began before the pin existed
// and ends after it, and the turn that called finish_work began before the
// window closed; both are the card's work.
func (p SessionPin) Covers(turn Turn) bool {
	if !p.StartedAt.IsZero() && turn.EndedAt.Before(p.StartedAt) {
		return false
	}
	if !p.FinishedAt.IsZero() && turn.StartedAt.After(p.FinishedAt) {
		return false
	}
	return true
}

// Names reports whether a feature reference, as a model passed it to
// finish_work, is the card this pin is for: the reference start_work was
// given, in any case, or the card's numeric id.
func (p SessionPin) Names(feature string) bool {
	feature = strings.TrimSpace(feature)
	return feature != "" &&
		(strings.EqualFold(feature, p.Feature) || feature == strconv.FormatInt(p.FeatureID, 10))
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

// FinishSessionPin closes the window of a session's pin when finish_work
// names the card it is for, and reports whether it did. A finish_work for
// any other card leaves the pin alone: closing it would stop the session's
// own card from being recorded on the strength of somebody else's episode.
func FinishSessionPin(dir, sessionRef, feature string, now time.Time) (bool, error) {
	pin := ReadSessionPin(dir, sessionRef)
	if pin == nil || !pin.Names(feature) {
		return false, nil
	}
	pin.FinishedAt = now.UTC()
	pin.UpdatedAt = now.UTC()
	if err := WriteSessionPin(dir, sessionRef, *pin); err != nil {
		return false, err
	}
	return true, nil
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
