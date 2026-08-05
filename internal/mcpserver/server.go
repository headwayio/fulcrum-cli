// Package mcpserver is the client's third face: a Model Context Protocol
// server that lets a coding harness — Claude Code, Codex, Kimi Code — pull
// Fulcrum context mid-session, rather than being handed one snapshot at
// launch and going stale from there.
//
// It is a BRIDGE, not a second implementation. The tool catalogue is fetched
// from the server and forwarded back to it, so a tool added in Rails reaches
// every developer on the next call rather than the next release of this
// binary. Nothing here restates a tool's name, description or schema.
//
// What it DOES add is the half a hosted server structurally cannot: this
// process runs inside the developer's checkout, so it can answer "which
// project is this" from the .fulcrum directory rather than making the model
// ask.
//
// Like internal/cli it sits over the kernel packages and never imports the
// TUI. Unlike internal/cli it must NEVER write to stdout: under the stdio
// transport that pipe is the JSON-RPC channel itself, and a single stray
// write corrupts the session for the rest of its life. Diagnostics go to
// stderr, which every harness captures as server logs.
package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/headwayio/fulcrum-cli/internal/api"
	"github.com/headwayio/fulcrum-cli/internal/projectctx"
)

// Deps is the kernel surface the bridge reaches through. An interface rather
// than a *api.Client so the tools are testable without a server, matching the
// seam internal/tui already uses.
type Deps interface {
	McpTools(ctx context.Context) ([]api.ToolDefinition, error)
	McpCall(ctx context.Context, name string, arguments map[string]any) (*api.ToolResult, error)
}

// Server carries what the tools need beyond the protocol itself.
type Server struct {
	deps  Deps
	local *projectctx.Local
}

// New fetches the catalogue and builds a server exposing it, plus the local
// tools that only a process in the checkout can answer.
//
// The catalogue is fetched ONCE, at startup, because MCP clients read
// tools/list when they connect and a harness session is short-lived relative
// to a deploy. A tool added server-side reaches an already-running session on
// its next restart, which is the same cadence as any other config change.
func New(ctx context.Context, version string, deps Deps, workingDir string) (*mcp.Server, error) {
	definitions, err := deps.McpTools(ctx)
	if err != nil {
		return nil, err
	}

	// A checkout with no .fulcrum is an ordinary state, not a failure: the
	// remote tools all still work, they just have to be told the project.
	local, _ := projectctx.Resolve(workingDir)

	server := mcp.NewServer(&mcp.Implementation{Name: "fulcrum", Version: version}, nil)
	bridge := &Server{deps: deps, local: local}

	for _, definition := range definitions {
		bridge.addRemote(server, definition)
	}
	bridge.addWhereAmI(server, workingDir)

	return server, nil
}

// addRemote exposes one server-defined tool, forwarding every call.
func (s *Server) addRemote(server *mcp.Server, definition api.ToolDefinition) {
	tool := &mcp.Tool{
		Name:        definition.Name,
		Description: definition.Description,
		InputSchema: schemaFor(definition),
	}

	needsProject := requiresProject(definition)

	server.AddTool(tool, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		arguments := decodeArguments(req)

		// Rescue a call that would otherwise hard-fail for want of something
		// this process already knows. Deliberately ONLY when the tool
		// requires a project: filling in an OPTIONAL project would silently
		// narrow a search the caller left open on purpose — "has anyone built
		// this before" is a question about the organization, not this repo.
		if needsProject && s.local != nil && !hasArgument(arguments, "project") {
			arguments["project"] = strconv.FormatInt(s.local.ProjectID, 10)
		}

		result, err := s.deps.McpCall(ctx, definition.Name, arguments)
		if err != nil {
			return failure("%s", describeCallError(definition.Name, err)), nil
		}
		return &mcp.CallToolResult{
			IsError: result.IsError,
			Content: []mcp.Content{&mcp.TextContent{Text: result.Text()}},
		}, nil
	})
}

// addWhereAmI is the one tool implemented locally, because it is the one
// question a hosted server cannot answer: the model can see the repository,
// but not which Fulcrum project it corresponds to.
func (s *Server) addWhereAmI(server *mcp.Server, workingDir string) {
	tool := &mcp.Tool{
		Name: "where_am_i",
		Description: "Report which Fulcrum project this checkout belongs to. Call it first " +
			"when you are working in a repository and need to act on the board, so you " +
			"do not have to guess the project or ask the user for it. If this checkout " +
			"has no Fulcrum context yet it says so, and the other tools will need a " +
			"project named explicitly.",
		InputSchema: emptySchema(),
	}

	server.AddTool(tool, func(_ context.Context, _ *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if s.local == nil {
			return text(fmt.Sprintf(
				"This checkout (%s) has no Fulcrum project context. Run `fulcrum context "+
					"--project <name>` in it to link one, or name the project explicitly when "+
					"calling the other tools.", workingDir)), nil
		}

		var b strings.Builder
		fmt.Fprintf(&b, "This checkout is Fulcrum project **%s** (id %d).\n\n",
			s.local.ProjectName, s.local.ProjectID)
		fmt.Fprintf(&b, "- Checkout root: %s\n", s.local.Root)
		if s.local.Digest != "" {
			fmt.Fprintf(&b, "- Local estimation context digest: %.12s…\n", s.local.Digest)
		}

		// The pin, when `fulcrum work` set one. Saying which card this
		// checkout is about is the difference between the model guessing and
		// the model knowing.
		if work := projectctx.ReadCurrentWork(s.local.Root); work != nil {
			fmt.Fprintf(&b, "\nCurrently working **%s**", work.Feature)
			if work.Name != "" {
				fmt.Fprintf(&b, " — %s", work.Name)
			}
			if work.Role != "" {
				fmt.Fprintf(&b, " (as %s)", work.Role)
			}
			b.WriteString(".\nCall get_feature on it if you have not read the brief yet.")
		}

		b.WriteString("\n\nTools that need a project use this one unless you name another.")
		return text(b.String()), nil
	})
}

// schemaFor hands the server's schema through untouched, falling back to a
// permissive object when a tool declares none.
func schemaFor(definition api.ToolDefinition) map[string]any {
	if len(definition.InputSchema) == 0 {
		return emptySchema()
	}
	return definition.InputSchema
}

func emptySchema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}

func requiresProject(definition api.ToolDefinition) bool {
	required, ok := definition.InputSchema["required"].([]any)
	if !ok {
		return false
	}
	for _, name := range required {
		if value, isString := name.(string); isString && value == "project" {
			return true
		}
	}
	return false
}

// Arguments arrive as raw JSON because Server.AddTool does no validation of
// its own — the schema came from the server and the server revalidates, so
// there is nothing here worth a second opinion.
func decodeArguments(req *mcp.CallToolRequest) map[string]any {
	arguments := map[string]any{}
	if req == nil || req.Params == nil || len(req.Params.Arguments) == 0 {
		return arguments
	}
	_ = json.Unmarshal(req.Params.Arguments, &arguments)
	return arguments
}

func hasArgument(arguments map[string]any, key string) bool {
	value, present := arguments[key]
	if !present || value == nil {
		return false
	}
	text, isString := value.(string)
	return !isString || strings.TrimSpace(text) != ""
}

// describeCallError turns a transport or contract failure into something the
// model can act on. An expired token and an unreachable server call for
// different next moves, and "call failed" tells it neither.
func describeCallError(name string, err error) string {
	var apiErr *api.Error
	if !errors.As(err, &apiErr) {
		return fmt.Sprintf("could not reach Fulcrum to call %s: %v", name, err)
	}

	switch apiErr.Code {
	case "unauthorized":
		return "this Fulcrum token is not valid any more — the user needs to mint a new one " +
			"at /settings/developer and re-register the server."
	case "insufficient_scope":
		return fmt.Sprintf("this Fulcrum token may not call %s: %s", name, apiErr.ServerMessage)
	case "organization_required":
		return "this token belongs to several Fulcrum organizations and none was chosen; " +
			"the user needs to set FULCRUM_ORG_ID."
	case "rate_limited":
		return "Fulcrum is rate limiting this token; wait a moment before trying again."
	}
	return fmt.Sprintf("Fulcrum refused the %s call: %s", name, apiErr.Error())
}

// text is the ordinary success shape: one markdown block.
func text(body string) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: body}}}
}

// failure reports a problem the MODEL should see and can act on. Returning a
// Go error instead would surface it as a transport fault, which the model
// cannot recover from and the user sees as "the server is broken".
func failure(format string, args ...any) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		IsError: true,
		Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf(format, args...)}},
	}
}
