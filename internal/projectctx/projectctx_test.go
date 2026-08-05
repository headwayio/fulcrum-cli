package projectctx_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/headwayio/fulcrum-cli/internal/projectctx"
)

const bundle = `---
name: project-context
organization: Headway
project: Embr - MVP
project_id: 24
digest: 1f4d82937aba7aec824811b068e8682d0ba8be92f5fc0491dc72fd3a1ed85202
---

You are an expert software project estimator.
`

func linked(t *testing.T, content string) string {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, projectctx.Dir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, projectctx.ContextFile), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestResolveReadsTheProjectOutOfFrontmatter(t *testing.T) {
	root := linked(t, bundle)

	local, err := projectctx.Resolve(root)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if local == nil {
		t.Fatal("expected a resolved project")
	}
	if local.ProjectID != 24 {
		t.Errorf("ProjectID = %d, want 24", local.ProjectID)
	}
	if local.ProjectName != "Embr - MVP" {
		t.Errorf("ProjectName = %q, want %q", local.ProjectName, "Embr - MVP")
	}
	if local.Digest == "" {
		t.Error("expected the bundle digest to be carried through")
	}
}

// A harness is usually launched from somewhere inside the repository, not at
// its root. Answering "no project here" when it is two levels up would be
// both wrong and hard to diagnose.
func TestResolveWalksUpFromASubdirectory(t *testing.T) {
	root := linked(t, bundle)
	deep := filepath.Join(root, "src", "deep", "nested")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}

	local, err := projectctx.Resolve(deep)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if local == nil || local.ProjectID != 24 {
		t.Fatalf("expected project 24 from a subdirectory, got %+v", local)
	}
	if local.Root != root {
		t.Errorf("Root = %q, want the checkout root %q", local.Root, root)
	}
}

// An un-provisioned checkout is an ordinary state. Returning an error would
// make every caller treat "not linked yet" as a failure.
func TestResolveReturnsNothingWhenThereIsNoContext(t *testing.T) {
	local, err := projectctx.Resolve(t.TempDir())
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if local != nil {
		t.Errorf("expected no project, got %+v", local)
	}
}

func TestResolveIgnoresAContextItCannotUnderstand(t *testing.T) {
	for name, content := range map[string]string{
		"no frontmatter": "just a body with no fences\n",
		"no project_id":  "---\nname: project-context\nproject: Embr\n---\nbody\n",
		"unparsable id":  "---\nproject_id: not-a-number\n---\nbody\n",
		"empty":          "",
	} {
		t.Run(name, func(t *testing.T) {
			local, err := projectctx.Resolve(linked(t, content))
			if err != nil {
				t.Fatalf("Resolve: %v", err)
			}
			if local != nil {
				t.Errorf("expected no project from %s, got %+v", name, local)
			}
		})
	}
}

func TestCurrentWorkRoundTrips(t *testing.T) {
	root := linked(t, bundle)

	if projectctx.ReadCurrentWork(root) != nil {
		t.Fatal("a fresh checkout should have no pin")
	}

	want := &projectctx.CurrentWork{Feature: "FUL-17", Name: "Mapping engine", ProjectID: 24, Role: "Development"}
	if err := projectctx.WriteCurrentWork(root, want); err != nil {
		t.Fatalf("WriteCurrentWork: %v", err)
	}

	got := projectctx.ReadCurrentWork(root)
	if got == nil || got.Feature != "FUL-17" || got.Name != "Mapping engine" || got.Role != "Development" {
		t.Fatalf("pin did not round trip: %+v", got)
	}

	if err := projectctx.ClearCurrentWork(root); err != nil {
		t.Fatalf("ClearCurrentWork: %v", err)
	}
	if projectctx.ReadCurrentWork(root) != nil {
		t.Error("pin survived being cleared")
	}
	// Absent is the same as cleared.
	if err := projectctx.ClearCurrentWork(root); err != nil {
		t.Errorf("clearing twice should be a no-op: %v", err)
	}
}

func TestCurrentWorkIgnoresAPinItCannotUnderstand(t *testing.T) {
	root := linked(t, bundle)
	path := filepath.Join(root, projectctx.Dir, projectctx.CurrentWorkFile)

	for name, content := range map[string]string{
		"not json":      "{{{",
		"no feature":    `{"project_id":24}`,
		"blank feature": `{"feature":"   ","project_id":24}`,
	} {
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		if got := projectctx.ReadCurrentWork(root); got != nil {
			t.Errorf("%s should read as no pin, got %+v", name, got)
		}
	}
}
