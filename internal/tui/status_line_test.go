package tui

import (
	"strings"
	"testing"
)

// A report describes the action you just took. Take another one — even
// moving the cursor — and it is history.
func TestStatusClearsOnTheNextKeypress(t *testing.T) {
	deps := &fakeDeps{
		configured: true, serverURL: "http://srv", snapshot: allStatesSnapshot(),
		syncSummary: SyncSummary{Synced: 1, Fresh: 6},
	}
	h := newTestModel(t, deps)
	waitContains(t, h, "synced.md")

	h.Type("s")
	waitContains(t, h, "synced 1")

	h.Type("j") // just moving the cursor
	view := finalView(t, h)
	if strings.Contains(stripANSI(view.Content), "synced 1") {
		t.Errorf("the sync report outlived the action it described:\n%s", stripANSI(view.Content))
	}
}

// Offline is a condition, not an event: it stays until the server is back.
func TestOfflineNoticeSurvivesKeypresses(t *testing.T) {
	snapshot := allStatesSnapshot()
	snapshot.Reachable = false
	snapshot.NetErr = "dial tcp: connection refused"
	deps := &fakeDeps{configured: true, serverURL: "http://srv", snapshot: snapshot}
	h := newTestModel(t, deps)
	waitContains(t, h, "showing last-known state")

	h.Type("jjk")
	view := finalView(t, h)
	if !strings.Contains(stripANSI(view.Content), "showing last-known state") {
		t.Errorf("the offline notice should persist while it is still true:\n%s", stripANSI(view.Content))
	}
}
