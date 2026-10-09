package mcpserver_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/headwayio/fulcrum-cli/internal/agenthook"
	"github.com/headwayio/fulcrum-cli/internal/api"
	"github.com/headwayio/fulcrum-cli/internal/mcpserver"
	"github.com/headwayio/fulcrum-cli/internal/projectctx"
)

// fakeDeps stands in for the server, recording what the bridge forwarded.
type fakeDeps struct {
	tools  []api.ToolDefinition
	calls  []recordedCall
	result *api.ToolResult
	err    error
	// replies, when set, answers a tool by name instead of the generic text.
	replies map[string]string
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
	if f.result != nil {
		return f.result, nil
	}
	if reply, ok := f.replies[name]; ok {
		return &api.ToolResult{Content: []api.ToolContent{{Type: "text", Text: reply}}}, nil
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
	return connectWithPins(t, deps, workingDir, "")
}

// connectWithPins also hands the bridge a config dir, where start_work pins
// land.
func connectWithPins(t *testing.T, deps mcpserver.Deps, workingDir, pinDir string) *mcp.ClientSession {
	t.Helper()
	ctx := context.Background()

	server, err := mcpserver.New(ctx, "test", deps, workingDir, pinDir)
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

// POST /mcp reports a missing scope as a tool failure rather than an HTTP
// refusal, so the server's own text — which names the remedy — must reach the
// model unchanged and still flagged as an error.
func TestAScopeFailureResultPassesThroughVerbatim(t *testing.T) {
	refusal := "this token is not permitted to update the execution board; " +
		"mint one with the execution permission at /settings/developer"
	deps := &fakeDeps{
		tools:  catalogue(),
		result: &api.ToolResult{IsError: true, Content: []api.ToolContent{{Type: "text", Text: refusal}}},
	}
	session := connect(t, deps, linkedCheckout(t))

	text, isError := callText(t, session, "get_project_prompt", map[string]any{})
	if !isError {
		t.Fatal("expected isError")
	}
	if text != refusal {
		t.Errorf("text = %q, want the server's own", text)
	}
}

func TestAProtocolErrorNamesTheToolAndCode(t *testing.T) {
	deps := &fakeDeps{
		tools: catalogue(),
		err:   &api.RPCError{Method: "tools/call", Code: -32602, Message: "invalid params"},
	}
	session := connect(t, deps, linkedCheckout(t))

	text, isError := callText(t, session, "get_project_prompt", map[string]any{})
	if !isError {
		t.Fatal("expected isError so the model can see it")
	}
	for _, want := range []string{"get_project_prompt", "-32602", "invalid params"} {
		if !strings.Contains(text, want) {
			t.Errorf("expected %q in: %s", want, text)
		}
	}
	if strings.Contains(text, "could not reach") {
		t.Errorf("a protocol error is not a connectivity failure: %s", text)
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

func workCatalogue() []api.ToolDefinition {
	return append(catalogue(),
		api.ToolDefinition{Name: "start_work", Description: "opens an episode", InputSchema: map[string]any{
			"type": "object", "properties": map[string]any{"feature": map[string]any{"type": "string"}}, "required": []any{"feature"},
		}},
		api.ToolDefinition{Name: "get_feature", Description: "brief", InputSchema: map[string]any{
			"type": "object", "properties": map[string]any{"feature": map[string]any{"type": "string"}}, "required": []any{"feature"},
		}},
	)
}

const briefForPin = "---\nname: feature-brief\nproject: Embr - MVP\nproject_id: 24\nfeature: FUL-17\nfeature_id: 1994\n---\n\n# FUL-17 — Dynamic field mapping\n"

// A session that names itself when it starts work is pinned by that name, so
// the telemetry hook can put its turns on this card even when the checkout is
// pinned to another one — which is how parallel subagents stay apart.
func TestStartWorkWithASessionRefPinsTheSession(t *testing.T) {
	deps := &fakeDeps{tools: workCatalogue(), replies: map[string]string{
		"start_work":  "Started episode 3 on feature 1994 as Development. Call finish_work when you stop.",
		"get_feature": briefForPin,
	}}
	pinDir := t.TempDir()
	session := connectWithPins(t, deps, linkedCheckout(t), pinDir)

	text, isError := callText(t, session, "start_work", map[string]any{"feature": "FUL-17", "session_ref": "sess-a"})
	if isError || !strings.Contains(text, "Started episode") {
		t.Fatalf("start_work reply lost: %q (error=%v)", text, isError)
	}

	pin := agenthook.ReadSessionPin(pinDir, "sess-a")
	if pin == nil {
		t.Fatal("no session pin was written")
	}
	if pin.FeatureID != 1994 || pin.ProjectID != 24 || pin.Feature != "FUL-17" {
		t.Errorf("pin carries the wrong ids: %+v", pin)
	}
	if pin.Role != "Development" {
		t.Errorf("role should come from the server's reply when the call left it out: %q", pin.Role)
	}
}

func TestStartWorkWithoutASessionRefLeavesTheCheckoutPinInCharge(t *testing.T) {
	deps := &fakeDeps{tools: workCatalogue(), replies: map[string]string{"start_work": "Started episode 1 on feature 1994 as Development.", "get_feature": briefForPin}}
	pinDir := t.TempDir()
	session := connectWithPins(t, deps, linkedCheckout(t), pinDir)

	callText(t, session, "start_work", map[string]any{"feature": "FUL-17"})

	if entries, _ := os.ReadDir(filepath.Join(pinDir, agenthook.PinsDir)); len(entries) != 0 {
		t.Errorf("a pin was written with nothing to key it on: %v", entries)
	}
	for _, call := range deps.calls {
		if call.name == "get_feature" {
			t.Error("the brief was fetched although no pin could be written")
		}
	}
}

func TestAFailedStartWorkPinsNothing(t *testing.T) {
	deps := &fakeDeps{tools: workCatalogue(), err: &api.Error{Code: "insufficient_scope", ServerMessage: "no"}}
	pinDir := t.TempDir()
	session := connectWithPins(t, deps, linkedCheckout(t), pinDir)

	callText(t, session, "start_work", map[string]any{"feature": "FUL-17", "session_ref": "sess-b"})

	if agenthook.ReadSessionPin(pinDir, "sess-b") != nil {
		t.Error("a session was pinned to work that never started")
	}
}
