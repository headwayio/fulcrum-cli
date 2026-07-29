package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// The trail is the stack, so it answers "how deep am I and how did I get
// here" — and a file that is already named is not named twice.
func TestBreadcrumbTrailsTheStack(t *testing.T) {
	deps := &fakeDeps{configured: true, serverURL: "http://srv", snapshot: allStatesSnapshot()}
	h := newTestModel(t, deps)
	waitContains(t, h, "drifted.json")

	h.Type("j") // the drifted row
	h.Type("p") // publish, straight from the list
	waitPlain(t, h, "documents › drifted.json › publish")

	h.Send(tea.KeyPressMsg{Code: tea.KeyEscape})
	h.Send(enter()) // diff first this time (enter opens by state)…
	waitPlain(t, h, "documents › drifted.json › diff")
	h.Type("p") // …then publish from inside it
	waitPlain(t, h, "documents › drifted.json › diff › publish")

	styled := strings.SplitN(string(finalFrame(t, h)), "\n", 2)[0]
	if trail := stripANSI(styled); strings.Count(trail, "drifted.json") != 1 {
		t.Errorf("the filename should appear once in the trail: %q", trail)
	}
	// Emphasis goes to where you are, not to the crumb that never changes.
	if !strings.Contains(styled, "\x1b[1mpublish") {
		t.Errorf("the current crumb should be the bright one: %q", styled)
	}
	if strings.Contains(styled, "\x1b[1mfulcrum") {
		t.Errorf("the app name is an ancestor and should recede: %q", styled)
	}
}

func TestBreadcrumbElidesTheMiddleRatherThanWrapping(t *testing.T) {
	long := []string{"fulcrum", "An Organization With A Very Long Name Indeed", "documents",
		"some-quite-long-document-name.md", "publish"}
	got := fitCrumbs(long, 80)
	// The long org name goes; what you are working on stays.
	want := []string{"fulcrum", "…", "documents", "some-quite-long-document-name.md", "publish"}
	if strings.Join(got, " › ") != strings.Join(want, " › ") {
		t.Errorf("fitCrumbs = %v, want %v", got, want)
	}
	if fit := fitCrumbs([]string{"fulcrum", "Headway", "documents"}, 80); len(fit) != 3 {
		t.Errorf("a trail that fits should be left alone, got %v", fit)
	}
}
