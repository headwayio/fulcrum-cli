package cli

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

// fixtureServer is a tiny in-memory Fulcrum serving the vendored corpus:
// enough contract to exercise every CLI verb end to end without Rails.
type fixtureServer struct {
	*httptest.Server

	mu        sync.Mutex
	proposals []map[string]any
	nextID    int64
}

const fixtureToken = "corpus-token"

func corpusFile(parts ...string) []byte {
	raw, err := os.ReadFile(filepath.Join(append([]string{"..", "..", "corpus"}, parts...)...))
	if err != nil {
		panic(err)
	}
	return raw
}

func newFixtureServer() *fixtureServer {
	f := &fixtureServer{nextID: 101}
	mux := http.NewServeMux()

	mux.HandleFunc("/api/agent_context/skills", func(w http.ResponseWriter, r *http.Request) {
		w.Write(corpusFile("manifest.json"))
	})
	mux.HandleFunc("/api/agent_context/skills/", func(w http.ResponseWriter, r *http.Request) {
		switch strings.TrimPrefix(r.URL.Path, "/api/agent_context/skills/") {
		case "estimation-rubric":
			w.Header().Set("Content-Type", "text/markdown")
			w.Write(corpusFile("documents", "estimation-rubric.md"))
		case "estimation-rubric-source":
			w.Header().Set("Content-Type", "application/json")
			w.Write(corpusFile("documents", "estimation-rubric.json"))
		case "skill-corpus-writing-specs":
			w.Header().Set("Content-Type", "text/markdown")
			w.Write(corpusFile("documents", "skill-corpus-writing-specs.md"))
		default:
			w.WriteHeader(http.StatusNotFound)
			w.Write(corpusFile("errors", "unknown_document.json"))
		}
	})

	mux.HandleFunc("GET /api/agent_context/proposals", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		rows := make([]map[string]any, len(f.proposals))
		for i := range f.proposals {
			rows[len(f.proposals)-1-i] = f.proposals[i] // newest first
		}
		json.NewEncoder(w).Encode(map[string]any{"proposals": rows})
	})
	mux.HandleFunc("POST /api/agent_context/proposals", func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			Slug       string         `json:"slug"`
			Document   map[string]any `json:"document"`
			BaseDigest string         `json:"base_digest"`
			Note       string         `json:"note"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil || payload.Document == nil {
			w.WriteHeader(http.StatusUnprocessableEntity)
			json.NewEncoder(w).Encode(map[string]any{"errors": []string{"document is required"}})
			return
		}
		f.mu.Lock()
		id := f.nextID
		f.nextID++
		f.proposals = append(f.proposals, map[string]any{
			"id": id, "slug": payload.Slug, "status": "pending", "note": payload.Note,
			"base_digest": payload.BaseDigest, "based_on_current": true,
			"created_at": "2026-07-01T12:00:00Z", "resolved_at": nil,
			"resolved_by_name": nil, "changed_sections": []string{"components"},
		})
		f.mu.Unlock()
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]any{
			"id": id, "status": "pending", "based_on_current": true,
			"review_url": fmt.Sprintf("/knowledge_proposals/%d", id),
		})
	})

	mux.HandleFunc("GET /api/agent_context/projects", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"projects": []map[string]any{
			{"id": 1, "name": "Acme App", "client_name": "Acme",
				"has_architecture_profile": false, "architecture_collected_at": nil},
			{"id": 2, "name": "Fulcrum Dogfood", "client_name": "Headway",
				"has_architecture_profile": true, "architecture_collected_at": "2026-07-01T12:00:00Z"},
			{"id": 3, "name": "Fulcrum Website", "client_name": "Headway",
				"has_architecture_profile": false, "architecture_collected_at": nil},
		}})
	})

	mux.HandleFunc("POST /api/agent_context/architecture", func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			ProjectID int64          `json:"project_id"`
			Facts     map[string]any `json:"facts"`
		}
		json.NewDecoder(r.Body).Decode(&payload)
		if payload.ProjectID < 1 || payload.ProjectID > 3 {
			w.WriteHeader(http.StatusUnprocessableEntity)
			w.Write(corpusFile("errors", "unknown_project.json"))
			return
		}
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]any{
			"project_id": payload.ProjectID, "source": "local_scan",
			"collected_at": "2026-07-01T12:00:00Z",
		})
	})

	authed := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+fixtureToken {
			w.WriteHeader(http.StatusUnauthorized)
			w.Write(corpusFile("errors", "unauthorized.json"))
			return
		}
		mux.ServeHTTP(w, r)
	})

	f.Server = httptest.NewServer(authed)
	return f
}

// resolveProposalStatus is the `srv apply|reject <id>` control surface.
func (f *fixtureServer) resolveProposalStatus(id int64, status string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, p := range f.proposals {
		if p["id"] == id {
			p["status"] = status
			p["resolved_at"] = "2026-07-01T13:00:00Z"
			p["resolved_by_name"] = "Ada Admin"
			p["changed_sections"] = nil
			return true
		}
	}
	return false
}

func (f *fixtureServer) proposalCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.proposals)
}

func parseID(s string) int64 {
	id, _ := strconv.ParseInt(s, 10, 64)
	return id
}
