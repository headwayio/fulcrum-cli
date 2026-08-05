package mcpserver_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/headwayio/fulcrum-cli/internal/api"
	"github.com/headwayio/fulcrum-cli/internal/mcpserver"
	"github.com/headwayio/fulcrum-cli/internal/projectctx"
)

// fakeDeps stands in for the server, recording what the bridge forwarded.
type fakeDeps struct {
	tools []api.ToolDefinition
	calls []recordedCall
	err   error
}

type recordedCall struct {
	name      string
	arguments map[string]any
}

func (f *fakeDeps) McpTools(context.Context) ([]api.ToolDefinition, error) {
	return f.tools, nil
}

func (f *fakeDeps) McpCall(_ context.Context, name string, arguments map[string]any) (*api.ToolResult, error) {
	f.calls = append(f.calls, recordedCall{name: name, arguments: arguments})
	if f.err != nil {
		return nil, f.err
	}
	return &api.ToolResult{Content: []api.ToolContent{{Type: "text", Text: "served " + name}}}, nil
}

func catalogue() []api.ToolDefinition {
	return []api.ToolDefinition{
		{
			Name:        "get_project_prompt",
			Description: "needs a project",
			InputSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{"project": map[string]any{"type": "string"}},
				"required":   []any{"project"},
			},
		},
		{
			Name:        "find_features",
			Description: "project is optional",
			InputSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{"project": map[string]any{"type": "string"}},
			},
		},
	}
}

func linkedCheckout(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, projectctx.Dir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "---\nname: project-context\nproject: Embr - MVP\nproject_id: 24\ndigest: abc123\n---\n\nbody\n"
	if err := os.WriteFile(filepath.Join(dir, projectctx.ContextFile), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

// connect wires a real client to the bridge over in-memory transports, so the
// assertions run through actual protocol traffic rather than direct calls.
func connect(t *testing.T, deps mcpserver.Deps, workingDir string) *mcp.ClientSession {
	t.Helper()
	ctx := context.Background()

	server, err := mcpserver.New(ctx, "test", deps, workingDir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	serverSession, err := server.Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatalf("server connect: %v", err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	clientSession, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	t.Cleanup(func() {
		_ = clientSession.Close()
		_ = serverSession.Wait()
	})
	return clientSession
}

func callText(t *testing.T, session *mcp.ClientSession, name string, arguments map[string]any) (string, bool) {
	t.Helper()
	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: arguments})
	if err != nil {
		t.Fatalf("CallTool %s: %v", name, err)
	}
	var text strings.Builder
	for _, block := range result.Content {
		if content, ok := block.(*mcp.TextContent); ok {
			text.WriteString(content.Text)
		}
	}
	return text.String(), result.IsError
}

// The whole point of the registry living in Rails: the binary exposes what
// the server said, and nothing it decided for itself.
func TestToolsComeFromTheServer(t *testing.T) {
	deps := &fakeDeps{tools: catalogue()}
	session := connect(t, deps, t.TempDir())

	listed, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}

	names := map[string]bool{}
	for _, tool := range listed.Tools {
		names[tool.Name] = true
	}
	for _, want := range []string{"get_project_prompt", "find_features", "where_am_i"} {
		if !names[want] {
			t.Errorf("missing tool %q; got %v", want, names)
		}
	}
	if len(listed.Tools) != 3 {
		t.Errorf("expected exactly the served tools plus where_am_i, got %d", len(listed.Tools))
	}
}

func TestProjectIsFilledInFromTheCheckoutWhenRequired(t *testing.T) {
	deps := &fakeDeps{tools: catalogue()}
	session := connect(t, deps, linkedCheckout(t))

	if _, isError := callText(t, session, "get_project_prompt", map[string]any{}); isError {
		t.Fatal("expected the call to succeed")
	}

	if len(deps.calls) != 1 {
		t.Fatalf("expected one forwarded call, got %d", len(deps.calls))
	}
	if got := deps.calls[0].arguments["project"]; got != "24" {
		t.Errorf("project = %v, want the checkout's project id 24", got)
	}
}

// Filling in an optional project would silently narrow a search the caller
// left open on purpose — "has anyone built this before" is a question about
// the organization, not this repository.
func TestOptionalProjectIsNeverFilledIn(t *testing.T) {
	deps := &fakeDeps{tools: catalogue()}
	session := connect(t, deps, linkedCheckout(t))

	callText(t, session, "find_features", map[string]any{"query": "webhooks"})

	if _, present := deps.calls[0].arguments["project"]; present {
		t.Errorf("project was injected into an open search: %v", deps.calls[0].arguments)
	}
}

func TestAnExplicitProjectIsNeverOverridden(t *testing.T) {
	deps := &fakeDeps{tools: catalogue()}
	session := connect(t, deps, linkedCheckout(t))

	callText(t, session, "get_project_prompt", map[string]any{"project": "99"})

	if got := deps.calls[0].arguments["project"]; got != "99" {
		t.Errorf("project = %v, want the caller's 99", got)
	}
}

func TestUnlinkedCheckoutForwardsWithoutAProject(t *testing.T) {
	deps := &fakeDeps{tools: catalogue()}
	session := connect(t, deps, t.TempDir())

	callText(t, session, "get_project_prompt", map[string]any{})

	if _, present := deps.calls[0].arguments["project"]; present {
		t.Error("invented a project for a checkout that has none")
	}
}

func TestWhereAmIReportsTheLinkedProject(t *testing.T) {
	session := connect(t, &fakeDeps{tools: catalogue()}, linkedCheckout(t))

	text, isError := callText(t, session, "where_am_i", map[string]any{})
	if isError {
		t.Fatal("where_am_i should not error on a linked checkout")
	}
	if !strings.Contains(text, "Embr - MVP") || !strings.Contains(text, "24") {
		t.Errorf("where_am_i did not name the project: %s", text)
	}
}

func TestWhereAmISaysSoWhenNothingIsLinked(t *testing.T) {
	session := connect(t, &fakeDeps{tools: catalogue()}, t.TempDir())

	text, _ := callText(t, session, "where_am_i", map[string]any{})
	if !strings.Contains(text, "fulcrum context") {
		t.Errorf("expected the fix to be named: %s", text)
	}
}

// An expired token and an unreachable server call for different next moves,
// and a transport fault the model cannot read tells it neither.
func TestATokenFailureReachesTheModelAsAReadableResult(t *testing.T) {
	deps := &fakeDeps{
		tools: catalogue(),
		err:   &api.Error{Status: 401, Code: "unauthorized", ServerMessage: "nope"},
	}
	session := connect(t, deps, linkedCheckout(t))

	text, isError := callText(t, session, "get_project_prompt", map[string]any{})
	if !isError {
		t.Fatal("expected isError so the model can see it")
	}
	if !strings.Contains(text, "/settings/developer") {
		t.Errorf("expected the remedy to be named: %s", text)
	}
}

func TestAScopeFailureNamesTheTool(t *testing.T) {
	deps := &fakeDeps{
		tools: catalogue(),
		err: &api.Error{Status: 403, Code: "insufficient_scope",
			ServerMessage: "mint one with the execution permission"},
	}
	session := connect(t, deps, linkedCheckout(t))

	text, isError := callText(t, session, "get_project_prompt", map[string]any{})
	if !isError {
		t.Fatal("expected isError")
	}
	if !strings.Contains(text, "get_project_prompt") || !strings.Contains(text, "execution") {
		t.Errorf("expected the tool and the missing scope: %s", text)
	}
}

// The pin is how the model learns which card this checkout is about without
// being told — and how the telemetry hooks, which cannot ask anybody
// anything, will learn it too.
func TestWhereAmIReportsThePinnedCard(t *testing.T) {
	root := linkedCheckout(t)
	if err := projectctx.WriteCurrentWork(root, &projectctx.CurrentWork{
		Feature: "EM-19", Name: "Brand Identity System", ProjectID: 24, Role: "Design",
	}); err != nil {
		t.Fatal(err)
	}

	session := connect(t, &fakeDeps{tools: catalogue()}, root)

	text, isError := callText(t, session, "where_am_i", map[string]any{})
	if isError {
		t.Fatal("where_am_i should not error")
	}
	for _, want := range []string{"EM-19", "Brand Identity System", "Design", "get_feature"} {
		if !strings.Contains(text, want) {
			t.Errorf("where_am_i did not mention %q: %s", want, text)
		}
	}
}

func TestWhereAmIIsQuietWhenNothingIsPinned(t *testing.T) {
	session := connect(t, &fakeDeps{tools: catalogue()}, linkedCheckout(t))

	text, _ := callText(t, session, "where_am_i", map[string]any{})
	if strings.Contains(text, "Currently working") {
		t.Errorf("claimed a pin that does not exist: %s", text)
	}
}
