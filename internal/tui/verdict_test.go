package tui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"

	"github.com/headwayio/fulcrum-cli/internal/api"
	"github.com/headwayio/fulcrum-cli/internal/state"
)

// The clean case is the one most people will see, and it has to say what it
// means for them without naming an API field.
func TestPublishVerdictSaysWhatACleanBaseMeans(t *testing.T) {
	deps := &fakeDeps{
		configured: true, serverURL: "http://srv", snapshot: allStatesSnapshot(),
		localDocs: map[string][]byte{"doc-drifted": []byte("{\"a\": 2}\n")},
		baseDocs:  map[string][]byte{"doc-drifted": []byte("{\"a\": 1}\n")},
		publishReceipt: &api.ProposalReceipt{
			ID: 6, Status: "pending", BasedOnCurrent: true,
			ReviewURL: "/knowledge_proposals/6",
		},
	}
	h := newTestModel(t, deps)
	waitContains(t, h, "drifted.json")
	h.Type("j")
	h.Type("p")
	waitContains(t, h, "Note to the reviewer")
	h.Type("tightened the wording")
	h.Send(enter())

	waitContains(t, h, "Proposal #6 submitted")
	waitContains(t, h, "the reviewer can apply it as-is")

	frame := stripANSI(string(finalFrame(t, h)))
	if strings.Contains(frame, "based_on_current") {
		t.Errorf("the API field name reached the screen:\n%s", frame)
	}
	for _, line := range strings.Split(frame, "\n") {
		if lipgloss.Width(line) > 80 {
			t.Errorf("verdict line wraps at 80 columns: %q", line)
		}
	}
}

// A draft has no team version, so the clean-base sentence would be false —
// and approving one is what reveals it org-wide, which is the thing worth
// saying.
func TestPublishVerdictForADraftTalksAboutPublishing(t *testing.T) {
	deps := &fakeDeps{
		configured: true, serverURL: "http://srv", snapshot: draftSnapshot(),
		localDocs: map[string][]byte{"skill-mine": []byte("# mine\n\nWritten out.\n")},
		baseDocs:  map[string][]byte{"skill-mine": []byte("# mine\n\nTemplate.\n")},
		publishReceipt: &api.ProposalReceipt{
			ID: 9, Status: "pending", BasedOnCurrent: true,
			ReviewURL: "/knowledge_proposals/9",
		},
	}
	h := newTestModel(t, deps)
	waitContains(t, h, "skill-mine.md")
	h.Type("p")
	waitContains(t, h, "Note to the reviewer")
	h.Type("first pass")
	h.Send(enter())

	waitContains(t, h, "Proposal #9 submitted")
	waitContains(t, h, "publishes it to the team for the first time")

	frame := stripANSI(string(finalFrame(t, h)))
	if strings.Contains(frame, "the team's current version") {
		t.Errorf("a draft has no team version to be based on:\n%s", frame)
	}
}

// Its creator can edit the same draft in the browser, so a draft can carry
// a stale base too — and that warning must not claim a team copy moved.
func TestPublishVerdictForAStaleDraftStillSaysDraft(t *testing.T) {
	deps := &fakeDeps{
		configured: true, serverURL: "http://srv", snapshot: draftSnapshot(),
		localDocs: map[string][]byte{"skill-mine": []byte("# mine\n\nWritten out.\n")},
		baseDocs:  map[string][]byte{"skill-mine": []byte("# mine\n\nTemplate.\n")},
		publishReceipt: &api.ProposalReceipt{
			ID: 10, Status: "pending", BasedOnCurrent: false,
			ReviewURL: "/knowledge_proposals/10",
		},
	}
	h := newTestModel(t, deps)
	waitContains(t, h, "skill-mine.md")
	h.Type("p")
	waitContains(t, h, "Note to the reviewer")
	h.Type("second pass")
	h.Send(enter())

	waitContains(t, h, "Your draft moved on the server")
	frame := stripANSI(string(finalFrame(t, h)))
	if strings.Contains(frame, "the team's copy moved") {
		t.Errorf("nobody else has a copy of a draft:\n%s", frame)
	}
}

// draftSnapshot: one edited, unpublished draft — the row a developer
// publishes to hand a new skill to the team.
func draftSnapshot() *Snapshot {
	return &Snapshot{
		OrgName: "Corpus Primary Organization", Reachable: true, SkillProposals: true,
		Rows: []Row{{
			Slug: "skill-mine", Filename: "skill-mine.md", Format: "markdown",
			ProposalSlug: "skill-mine", RemoteDigest: "aaaabbbbccccdddd",
			BaseDigest: "aaaabbbbccccdddd", Classification: state.Drifted, Draft: true,
		}},
	}
}
