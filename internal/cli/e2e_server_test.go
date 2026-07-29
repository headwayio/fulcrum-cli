package cli

import (
	"crypto/sha256"
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
	drafts    []map[string]any
	nextID    int64
	// Server-side edits to corpus documents, so a script can move a document
	// out from under the workspace and produce a real conflict.
	overrides map[string]string
	revisions int
	multiOrg  bool
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
	f := &fixtureServer{nextID: 101, overrides: map[string]string{}}
	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/agent_context/skills", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if len(f.drafts) == 0 && len(f.overrides) == 0 {
			w.Write(corpusFile("manifest.json"))
			return
		}
		var manifest map[string]any
		json.Unmarshal(corpusFile("manifest.json"), &manifest)
		docs := manifest["documents"].([]any)
		// Server-side edits move the document's digest, exactly as a real
		// edit would.
		for _, entry := range docs {
			row := entry.(map[string]any)
			if content, edited := f.overrides[row["slug"].(string)]; edited {
				row["digest"] = fmt.Sprintf("%x", sha256.Sum256([]byte(content)))
			}
		}
		// Session drafts append, like the server's creator-only rows.
		for _, d := range f.drafts {
			row := map[string]any{}
			for k, v := range d {
				if k != "content" {
					row[k] = v
				}
			}
			docs = append(docs, row)
		}
		manifest["documents"] = docs
		json.NewEncoder(w).Encode(manifest)
	})

	mux.HandleFunc("POST /api/agent_context/skills", func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			Name string `json:"name"`
		}
		json.NewDecoder(r.Body).Decode(&payload)
		f.mu.Lock()
		for _, d := range f.drafts {
			if d["slug"] == "skill-"+payload.Name {
				f.mu.Unlock()
				w.WriteHeader(http.StatusUnprocessableEntity)
				json.NewEncoder(w).Encode(map[string]any{
					"error": fmt.Sprintf("a skill named %q already exists", payload.Name),
					"code":  "skill_exists",
				})
				return
			}
		}
		f.mu.Unlock()
		content := fmt.Sprintf("---\nname: %s\ndescription: One line saying when an agent should reach for this skill.\n---\n\n# %s\n\nTemplate body.\n",
			payload.Name, payload.Name)
		draft := map[string]any{
			"slug": "skill-" + payload.Name, "filename": "skill-" + payload.Name + ".md",
			"format": "markdown", "digest": fmt.Sprintf("draft-digest-%s", payload.Name),
			"version": 1, "proposal_slug": "skill-" + payload.Name,
			"draft": true, "content": content,
		}
		f.mu.Lock()
		f.drafts = append(f.drafts, draft)
		f.mu.Unlock()
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(draft)
	})
	mux.HandleFunc("/api/agent_context/skills/", func(w http.ResponseWriter, r *http.Request) {
		requested := strings.TrimPrefix(r.URL.Path, "/api/agent_context/skills/")
		f.mu.Lock()
		content, edited := f.overrides[requested]
		f.mu.Unlock()
		if edited {
			w.Header().Set("Content-Type", "text/markdown")
			w.Write([]byte(content))
			return
		}

		switch requested {
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
			slug := strings.TrimPrefix(r.URL.Path, "/api/agent_context/skills/")
			f.mu.Lock()
			for _, d := range f.drafts {
				if d["slug"] == slug {
					f.mu.Unlock()
					w.Header().Set("Content-Type", "text/markdown")
					w.Write([]byte(d["content"].(string)))
					return
				}
			}
			f.mu.Unlock()
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
		f.mu.Lock()
		ambiguous := f.multiOrg
		f.mu.Unlock()
		if ambiguous {
			// What the server sends a token that reaches several orgs.
			w.WriteHeader(http.StatusUnprocessableEntity)
			json.NewEncoder(w).Encode(map[string]any{
				"error": "organization required: pass organization_id",
				"code":  "organization_required",
				"organizations": []map[string]any{
					{"id": 2, "name": "Headway"}, {"id": 21, "name": "Jono"},
				},
			})
			return
		}
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

// editDocument rewrites a corpus document server-side: the manifest digest
// moves and the body changes, which is what a real edit in Fulcrum looks
// like to a client that already synced.
func (f *fixtureServer) editDocument(slug, replace, with string) bool {
	base := map[string]string{
		"skill-corpus-writing-specs": "documents/skill-corpus-writing-specs.md",
		"estimation-rubric":          "documents/estimation-rubric.md",
	}[slug]
	if base == "" {
		return false
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	current, edited := f.overrides[slug]
	if !edited {
		current = string(corpusFile(filepath.FromSlash(base)))
	}
	if !strings.Contains(current, replace) {
		return false
	}
	f.overrides[slug] = strings.Replace(current, replace, with, 1)
	f.revisions++
	return true
}

func parseID(s string) int64 {
	id, _ := strconv.ParseInt(s, 10, 64)
	return id
}
