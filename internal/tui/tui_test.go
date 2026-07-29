package tui

import (
	"bytes"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/exp/teatest/v2"

	"github.com/headwayio/fulcrum-cli/internal/api"
	"github.com/headwayio/fulcrum-cli/internal/state"
)

// harness wraps a TestModel with a CUMULATIVE output buffer: teatest's
// WaitFor starts each call with an empty read buffer, so a needle whose
// bytes an earlier wait already consumed would never match again.
type harness struct {
	tm  *teatest.TestModel
	buf []byte
}

// Every test pins the color profile to Ascii — unpinned goldens flake across
// terminals — and pins the glamour style to notty for the same reason.
func newTestModel(t *testing.T, deps Deps) *harness {
	t.Helper()
	tm := teatest.NewTestModel(t, New(deps, "test", "notty"),
		teatest.WithInitialTermSize(80, 24),
		teatest.WithProgramOptions(tea.WithColorProfile(colorprofile.Ascii)),
	)
	return &harness{tm: tm}
}

func (h *harness) Type(s string)    { h.tm.Type(s) }
func (h *harness) Send(msg tea.Msg) { h.tm.Send(msg) }

func waitContains(t *testing.T, h *harness, needle string) {
	t.Helper()
	teatest.WaitFor(t, h.tm.Output(), func(bts []byte) bool {
		h.buf = append(h.buf, bts...)
		return bytes.Contains(h.buf, []byte(needle))
	}, teatest.WithDuration(3*time.Second))
}

// finalFrame quits and returns the last full frame from the final model —
// a deterministic, readable golden (the raw stream is delta-rendered).
func finalFrame(t *testing.T, h *harness) []byte {
	t.Helper()
	if err := h.tm.Quit(); err != nil {
		t.Fatal(err)
	}
	app, ok := h.tm.FinalModel(t).(*App)
	if !ok {
		t.Fatal("final model is not *App")
	}
	return []byte(app.View().Content)
}

func enter() tea.KeyPressMsg { return tea.KeyPressMsg{Code: tea.KeyEnter} }

// --- first-run auth ---

func TestAuthScreenPrintsMintURL(t *testing.T) {
	deps := &fakeDeps{
		configured: false,
		serverURL:  "http://localhost:3100",
		validateManifest: &api.Manifest{
			Organization: api.Organization{ID: 1, Name: "Corpus Primary Organization"},
			User:         &api.User{Email: "developer@corpus.usefulcrum.test"},
		},
		snapshot: allStatesSnapshot(),
	}
	h := newTestModel(t, deps)

	// The exact member-settings URL is on screen before anything is typed.
	waitContains(t, h, "http://localhost:3100/settings/developer")
	waitContains(t, h, "shown exactly once")

	h.Send(enter()) // accept the prefilled server URL
	h.Type("tok-secret")
	h.Send(enter())

	// Live validation + save land on the document list, org name in the
	// header. (The transient "Connected to…" notice frame can be coalesced
	// away by the renderer, so only durable frames are asserted.)
	waitContains(t, h, "Corpus Primary Organization · documents")
	waitContains(t, h, "= synced")
	if deps.savedToken != "tok-secret" || deps.savedURL != "http://localhost:3100" {
		t.Errorf("saved login = %q %q", deps.savedURL, deps.savedToken)
	}

	teatest.RequireEqualOutput(t, finalFrame(t, h))
}

func TestAuthScreenMasksToken(t *testing.T) {
	deps := &fakeDeps{configured: false, serverURL: "http://localhost:3100"}
	h := newTestModel(t, deps)
	waitContains(t, h, "settings/developer")
	h.Send(enter())
	h.Type("supersecret")
	waitContains(t, h, "***")

	out := finalFrame(t, h)
	if bytes.Contains(out, []byte("supersecret")) {
		t.Error("the token must never render in the clear")
	}
}

// --- document list ---

func TestListShowsEveryBadgeGlyphAndWord(t *testing.T) {
	deps := &fakeDeps{configured: true, serverURL: "http://srv", snapshot: allStatesSnapshot()}
	h := newTestModel(t, deps)

	waitContains(t, h, "Corpus Primary Organization")
	for _, label := range []string{
		"= synced", "~ drifted", "v behind", "! CONFLICTED",
		"^ proposed", "x missing", "+ applied #7 (re-sync)",
	} {
		waitContains(t, h, label)
	}
	waitContains(t, h, "7 document(s), 5 stale")

	teatest.RequireEqualOutput(t, finalFrame(t, h))
}

func TestListOfflineHeaderIsHonest(t *testing.T) {
	snapshot := allStatesSnapshot()
	snapshot.Reachable = false
	snapshot.NetErr = "dial tcp: connection refused"
	deps := &fakeDeps{configured: true, serverURL: "http://srv", snapshot: snapshot}
	h := newTestModel(t, deps)

	waitContains(t, h, "OFFLINE")
	waitContains(t, h, "showing last-known state")

	// Network verbs are disabled honestly, not hidden: sync says why.
	h.Type("s")
	waitContains(t, h, "offline — sync needs the server")

	teatest.RequireEqualOutput(t, finalFrame(t, h))
}

func TestPublishGreyedOnGeneratedMarkdown(t *testing.T) {
	deps := &fakeDeps{configured: true, serverURL: "http://srv", snapshot: allStatesSnapshot()}
	h := newTestModel(t, deps)
	waitContains(t, h, "synced.md")

	h.Type("p") // cursor starts on the generated markdown row
	waitContains(t, h, "generated rendering — edit the .json document and publish that")
}

// --- publish wizard ---

func TestPublishWizardFlow(t *testing.T) {
	deps := &fakeDeps{
		configured: true,
		serverURL:  "http://srv",
		snapshot:   allStatesSnapshot(),
		localDocs:  map[string][]byte{"doc-drifted": []byte("{\"status\": \"edited\"}\n")},
		baseDocs:   map[string][]byte{"doc-drifted": []byte("{\"status\": \"original\"}\n")},
		publishReceipt: &api.ProposalReceipt{
			ID: 55, Status: "pending", BasedOnCurrent: false,
			ReviewURL: "/knowledge_proposals/55",
		},
	}
	h := newTestModel(t, deps)
	waitContains(t, h, "drifted.json")

	h.Type("j") // move to the drifted row
	h.Type("p")
	waitContains(t, h, `-{"status": "original"}`)
	waitContains(t, h, `+{"status": "edited"}`)

	// The note is required: enter without one refuses.
	h.Send(enter())
	waitContains(t, h, "the reviewer note is required")

	h.Type("widened per client call")
	h.Send(enter())

	// based_on_current arrives verbatim, flagged when false.
	waitContains(t, h, "Proposal #55 submitted")
	waitContains(t, h, "based_on_current: false")
	waitContains(t, h, "http://srv/knowledge_proposals/55")

	h.Type("o")
	waitContains(t, h, "opened http://srv/knowledge_proposals/55")
	if len(deps.opened) != 1 || deps.opened[0] != "http://srv/knowledge_proposals/55" {
		t.Errorf("opened = %v", deps.opened)
	}
	if len(deps.published) != 1 {
		t.Fatalf("published = %v", deps.published)
	}
	// The proposal went to the PROPOSAL slug with the last-sync base digest.
	if deps.published[0] != "doc-drifted-target base=aaaabbbbccccdddd note=widened per client call" {
		t.Errorf("published = %q", deps.published[0])
	}

	teatest.RequireEqualOutput(t, finalFrame(t, h))
}

// --- reader ---

func TestReaderRendersFrontmatterAsHeader(t *testing.T) {
	deps := &fakeDeps{
		configured: true, serverURL: "http://srv", snapshot: allStatesSnapshot(),
		localDocs: map[string][]byte{
			"doc-synced": []byte("---\nname: estimation-rubric\ndigest: abc123\n---\n\n# Heading\n\nBody text here.\n"),
		},
	}
	h := newTestModel(t, deps)
	waitContains(t, h, "synced.md")

	h.Send(enter())
	waitContains(t, h, "name: estimation-rubric") // frontmatter → header lines
	waitContains(t, h, "Heading")
	waitContains(t, h, "Body text here.")

	teatest.RequireEqualOutput(t, finalFrame(t, h))
}

// --- diff views ---

func TestDiffStructuralJSONDefault(t *testing.T) {
	deps := &fakeDeps{
		configured: true, serverURL: "http://srv", snapshot: allStatesSnapshot(),
		localDocs: map[string][]byte{"doc-drifted": []byte(`{"status":"edited","hours":6}`)},
		baseDocs:  map[string][]byte{"doc-drifted": []byte(`{"status":"original","hours":4,"gone":true}`)},
	}
	h := newTestModel(t, deps)
	waitContains(t, h, "drifted.json")

	h.Type("j")
	h.Send(enter()) // drifted → diff screen, structural by default for JSON
	waitContains(t, h, `~ hours  4 → 6`)
	waitContains(t, h, `~ status  "original" → "edited"`)
	waitContains(t, h, `- gone = true`)

	// u toggles to the unified text diff and back.
	h.Type("u")
	waitContains(t, h, "--- a/drifted.json")
	waitContains(t, h, "u structured view")

	teatest.RequireEqualOutput(t, finalFrame(t, h))
}

func TestDiffThreeWayConflicted(t *testing.T) {
	deps := &fakeDeps{
		configured: true, serverURL: "http://srv", snapshot: allStatesSnapshot(),
		localDocs:  map[string][]byte{"doc-conflict": []byte(`{"status":"mine","shared":"me"}`)},
		baseDocs:   map[string][]byte{"doc-conflict": []byte(`{"status":"base","shared":"base"}`)},
		remoteDocs: map[string][]byte{"doc-conflict": []byte(`{"status":"base","shared":"them","extra":1}`)},
	}
	h := newTestModel(t, deps)
	waitContains(t, h, "conflicted.json")

	h.Type("jjj") // cursor to the conflicted row
	h.Send(enter())
	waitContains(t, h, "YOUR EDITS (base → local)")
	waitContains(t, h, "REMOTE CHANGES (base → remote)")
	// The path both sides touched is named as the real conflict.
	waitContains(t, h, "BOTH SIDES CHANGED:")
	waitContains(t, h, "shared")

	teatest.RequireEqualOutput(t, finalFrame(t, h))
}

// --- reader: behind-doc remote preview + re-sync ---

func TestReaderBehindPreviewsRemote(t *testing.T) {
	deps := &fakeDeps{
		configured: true, serverURL: "http://srv", snapshot: allStatesSnapshot(),
		localDocs:  map[string][]byte{"doc-behind": []byte("# Old local copy\n")},
		remoteDocs: map[string][]byte{"doc-behind": []byte("# Fresh remote content\n")},
		syncLines:  []string{"synced behind.md"},
	}
	h := newTestModel(t, deps)
	waitContains(t, h, "behind.md")

	h.Type("jj") // cursor to the behind row
	h.Send(enter())
	waitContains(t, h, "remote moved")
	waitContains(t, h, "Old local copy")

	h.Type("v") // preview what the server has before re-syncing
	// Needle avoids the "viewing " prefix shared with the previous banner:
	// the cell-diff renderer only emits changed spans, so a needle spanning
	// unchanged and changed cells never appears contiguously in the stream.
	waitContains(t, h, "REMOTE version")
	waitContains(t, h, "Fresh remote content")

	h.Type("s") // safe re-sync: back on the list with the result
	waitContains(t, h, "synced behind.md")

	teatest.RequireEqualOutput(t, finalFrame(t, h))
}

// --- $EDITOR handoff ---

func TestEditorHandoffRefreshes(t *testing.T) {
	deps := &fakeDeps{
		configured: true, serverURL: "http://srv", snapshot: allStatesSnapshot(),
		localDocs: map[string][]byte{"doc-synced": []byte("# doc\n")},
	}
	h := newTestModel(t, deps)
	waitContains(t, h, "synced.md")
	before := deps.refreshes

	h.Type("e") // fake editor exits immediately; the list must reclassify
	teatest.WaitFor(t, h.tm.Output(), func([]byte) bool {
		return deps.refreshes > before
	}, teatest.WithDuration(3*time.Second))
}

// --- merging a conflicted document ---

func TestMergeConflictedFromTheList(t *testing.T) {
	deps := &fakeDeps{configured: true, serverURL: "http://srv", snapshot: allStatesSnapshot()}
	h := newTestModel(t, deps)
	waitContains(t, h, "conflicted.json")

	// m is offered on the conflicted row and refused elsewhere.
	h.Type("m")
	waitContains(t, h, "merge applies to conflicted documents")
	if len(deps.merged) != 0 {
		t.Fatalf("merge ran on a non-conflicted row: %v", deps.merged)
	}

	h.Type("jjj") // to the conflicted row
	waitContains(t, h, "m merge")
	h.Type("m")
	waitContains(t, h, "merged cleanly")
	waitContains(t, h, "ready to publish")
	if len(deps.merged) != 1 || deps.merged[0] != "doc-conflict" {
		t.Errorf("merged = %v", deps.merged)
	}
}

func TestMergeReportsConflictMarkers(t *testing.T) {
	deps := &fakeDeps{
		configured: true, serverURL: "http://srv", snapshot: allStatesSnapshot(),
		mergeConflicts: 2,
	}
	h := newTestModel(t, deps)
	waitContains(t, h, "conflicted.json")

	h.Type("jjj")
	h.Type("m")
	waitContains(t, h, "2 conflict(s)")
	waitContains(t, h, "press e to resolve")
}

// --- discarding local edits ---

func TestDiscardAsksBeforeDestroying(t *testing.T) {
	deps := &fakeDeps{configured: true, serverURL: "http://srv", snapshot: allStatesSnapshot()}
	h := newTestModel(t, deps)
	waitContains(t, h, "drifted.json")

	h.Type("j") // the drifted row
	waitContains(t, h, "x discard mine")

	// Anything but y keeps the edits — one keypress never destroys work.
	h.Type("x")
	waitContains(t, h, "discard your edits to drifted.json?")
	h.Type("n")
	waitContains(t, h, "kept your edits")
	if len(deps.discarded) != 0 {
		t.Fatalf("declining still discarded: %v", deps.discarded)
	}

	h.Type("x")
	h.Type("y")
	waitContains(t, h, "reverted")
	waitContains(t, h, "your version kept at")
	if len(deps.discarded) != 1 || deps.discarded[0] != "doc-drifted" {
		t.Errorf("discarded = %v", deps.discarded)
	}
}

func TestDiscardRefusedWithoutLocalEdits(t *testing.T) {
	deps := &fakeDeps{configured: true, serverURL: "http://srv", snapshot: allStatesSnapshot()}
	h := newTestModel(t, deps)
	waitContains(t, h, "synced.md")

	h.Type("x") // cursor starts on the synced row
	waitContains(t, h, "nothing local to discard")
	if len(deps.discarded) != 0 {
		t.Errorf("discarded = %v", deps.discarded)
	}
}

// --- new-skill draft flow ---

func TestDraftSkillFlow(t *testing.T) {
	snapshot := allStatesSnapshot()
	snapshot.SkillDrafts = true
	deps := &fakeDeps{configured: true, serverURL: "http://srv", snapshot: snapshot}
	h := newTestModel(t, deps)
	waitContains(t, h, "synced.md")

	h.Type("n")
	waitContains(t, h, "New skill")
	waitContains(t, h, "Only you see the draft until you publish")

	// Bad names are refused client-side before any request.
	h.Type("Bad Name")
	h.Send(enter())
	waitContains(t, h, "kebab-case")
	if len(deps.drafted) != 0 {
		t.Fatalf("bad name must not reach the server: %v", deps.drafted)
	}

	for range "Bad Name" {
		h.Send(tea.KeyPressMsg{Code: tea.KeyBackspace})
	}
	h.Type("writing-migrations")
	h.Send(enter())

	waitContains(t, h, "draft skill-writing-migrations.md created")
	if len(deps.drafted) != 1 || deps.drafted[0] != "writing-migrations" {
		t.Errorf("drafted = %v", deps.drafted)
	}
}

func TestDraftRowIsBadged(t *testing.T) {
	snapshot := allStatesSnapshot()
	snapshot.Rows = append(snapshot.Rows, Row{
		Slug: "skill-my-draft", Filename: "skill-my-draft.md", Format: "markdown",
		ProposalSlug: "skill-my-draft", RemoteDigest: "dddddddddddd",
		Classification: state.Synced, Draft: true,
	})
	deps := &fakeDeps{configured: true, serverURL: "http://srv", snapshot: snapshot}
	h := newTestModel(t, deps)
	waitContains(t, h, "skill-my-draft.md")
	waitContains(t, h, "draft (only you)")
}

// --- mid-session 401 ---

func Test401RaisesReauthModal(t *testing.T) {
	deps := &fakeDeps{
		configured: true,
		serverURL:  "http://srv",
		refreshErr: &api.Error{Status: 401, Method: "GET", Path: "/api/agent_context/skills",
			Code: "unauthorized", ServerMessage: "unauthorized: pass Authorization: Bearer <token>"},
	}
	h := newTestModel(t, deps)

	waitContains(t, h, "Session rejected (401)")
	waitContains(t, h, "rotated or revoked")

	// l routes into the login screen with the mint hint.
	h.Type("l")
	waitContains(t, h, "Connect to Fulcrum")
	waitContains(t, h, "settings/developer")

	teatest.RequireEqualOutput(t, finalFrame(t, h))
}
