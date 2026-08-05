package projectctx

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// CurrentWorkFile pins the card being worked on in this checkout.
//
// It is what lets where_am_i — and, later, the telemetry hooks — answer with
// no arguments. Hooks in particular cannot ask the model anything: they fire
// on their own, so the only way they know which card a session belongs to is
// to read it from the working directory.
const CurrentWorkFile = "current-work.json"

// CurrentWork is the pin. Written by `fulcrum work`, read by anything that
// needs to know what this checkout is currently about.
type CurrentWork struct {
	Feature   string `json:"feature"`
	FeatureID int64  `json:"feature_id,omitempty"`
	Name      string `json:"name,omitempty"`
	ProjectID int64  `json:"project_id"`
	Role      string `json:"role,omitempty"`
	// StartedAt is recorded for a human reading the file; the SERVER stamps
	// the instants that telemetry is actually measured from, because the thing
	// being measured should not also be the clock.
	StartedAt string `json:"started_at,omitempty"`
}

// ReadCurrentWork returns the pin for a checkout, or nil when nothing is
// pinned. A checkout with no pin is an ordinary state.
func ReadCurrentWork(root string) *CurrentWork {
	content, err := os.ReadFile(filepath.Join(root, Dir, CurrentWorkFile))
	if err != nil {
		return nil
	}
	var work CurrentWork
	if err := json.Unmarshal(content, &work); err != nil {
		return nil
	}
	if strings.TrimSpace(work.Feature) == "" {
		return nil
	}
	return &work
}

func WriteCurrentWork(root string, work *CurrentWork) error {
	dir := filepath.Join(root, Dir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	encoded, err := json.MarshalIndent(work, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, CurrentWorkFile), append(encoded, '\n'), 0o644)
}

// ClearCurrentWork removes the pin. Absent is the same as cleared, so this is
// idempotent.
func ClearCurrentWork(root string) error {
	err := os.Remove(filepath.Join(root, Dir, CurrentWorkFile))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}
