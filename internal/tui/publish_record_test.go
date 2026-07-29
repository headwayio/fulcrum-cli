package tui

import (
	"testing"

	"github.com/headwayio/fulcrum-cli/internal/api"
	"github.com/headwayio/fulcrum-cli/internal/state"
)

// The TUI must write the proposal down like the plain CLI does. Without the
// record the row keeps offering "publish", and when the proposal comes back
// applied the returning content reads as somebody else's edit.
func TestPublishRecordsTheSubmittedBytes(t *testing.T) {
	local := []byte("# mine\n\nWritten out.\n")
	deps := &fakeDeps{
		configured: true, serverURL: "http://srv", snapshot: draftSnapshot(),
		localDocs: map[string][]byte{"skill-mine": local},
		baseDocs:  map[string][]byte{"skill-mine": []byte("# mine\n\nTemplate.\n")},
		publishReceipt: &api.ProposalReceipt{
			ID: 11, Status: "pending", BasedOnCurrent: true, ReviewURL: "/knowledge_proposals/11",
		},
	}
	h := newTestModel(t, deps)
	waitContains(t, h, "skill-mine.md")
	h.Type("p")
	waitContains(t, h, "Note to the reviewer")
	h.Type("first pass")
	h.Send(enter())
	waitContains(t, h, "Proposal #11 submitted")

	finalFrame(t, h)
	if len(deps.publishedSHAs) != 1 {
		t.Fatalf("published %d times, want 1", len(deps.publishedSHAs))
	}
	// The hash of the bytes actually sent — not of a re-read, which an
	// editor saving mid-flight would have changed underneath us.
	if want := state.HexSHA256(local); deps.publishedSHAs[0] != want {
		t.Errorf("recorded %q, want the submitted bytes %q", deps.publishedSHAs[0], want)
	}
}
