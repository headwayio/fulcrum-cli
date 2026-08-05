package agenthook

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// StateFile is the watermark: how far telemetry has been posted for each
// harness session.
//
// It lives in the USER'S CONFIG DIR, not the checkout. A watermark is a fact
// about this machine's posting, not about the project, and a teammate who
// clones the repository must not inherit one — nor should it turn up in a
// diff. Session references are globally unique, so one file serves every
// checkout.
//
// LOSING IT IS SAFE. The server dedupes on (session_ref, turn_index), so a
// missing watermark costs a re-send and nothing else. That is deliberate:
// the correctness guarantee lives on the server, and this is only here to
// keep a hook that fires on every turn from re-sending the whole session
// every time.
const StateFile = "telemetry-state.json"

// forgetAfter prunes sessions nothing has touched in a month, so the file
// does not grow for the life of the install.
const forgetAfter = 30 * 24 * time.Hour

type State struct {
	Sessions map[string]*SessionState `json:"sessions"`
}

type SessionState struct {
	PostedThrough int    `json:"posted_through"`
	Feature       string `json:"feature,omitempty"`
	UpdatedAt     string `json:"updated_at,omitempty"`
}

// LoadState reads the watermark. Anything unreadable or corrupt is treated as
// empty rather than fatal — see the note on StateFile.
func LoadState(dir string) *State {
	state := &State{Sessions: map[string]*SessionState{}}
	content, err := os.ReadFile(filepath.Join(dir, StateFile))
	if err != nil {
		return state
	}
	if err := json.Unmarshal(content, state); err != nil {
		return &State{Sessions: map[string]*SessionState{}}
	}
	if state.Sessions == nil {
		state.Sessions = map[string]*SessionState{}
	}
	return state
}

// PostedThrough is the highest turn index already sent for a session.
func (s *State) PostedThrough(sessionRef string) int {
	if entry := s.Sessions[sessionRef]; entry != nil {
		return entry.PostedThrough
	}
	return 0
}

// Record advances the watermark. It never moves BACKWARDS: a transcript that
// is read while being written can come up short, and rewinding would re-send
// turns the server already has.
func (s *State) Record(sessionRef, feature string, through int, now time.Time) {
	entry := s.Sessions[sessionRef]
	if entry == nil {
		entry = &SessionState{}
		s.Sessions[sessionRef] = entry
	}
	if through > entry.PostedThrough {
		entry.PostedThrough = through
	}
	entry.Feature = feature
	entry.UpdatedAt = now.UTC().Format(time.RFC3339)
}

// Save writes the watermark back, pruning sessions that have gone quiet.
func (s *State) Save(dir string, now time.Time) error {
	s.prune(now)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	encoded, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, StateFile), append(encoded, '\n'), 0o600)
}

func (s *State) prune(now time.Time) {
	for ref, entry := range s.Sessions {
		stamp, err := time.Parse(time.RFC3339, entry.UpdatedAt)
		if err != nil {
			continue
		}
		if now.Sub(stamp) > forgetAfter {
			delete(s.Sessions, ref)
		}
	}
}

// Refs lists the sessions on record, newest first. Only used for reporting.
func (s *State) Refs() []string {
	refs := make([]string, 0, len(s.Sessions))
	for ref := range s.Sessions {
		refs = append(refs, ref)
	}
	sort.Slice(refs, func(i, j int) bool {
		return s.Sessions[refs[i]].UpdatedAt > s.Sessions[refs[j]].UpdatedAt
	})
	return refs
}
