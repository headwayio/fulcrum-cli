package agenthook

import (
	"regexp"
	"strings"
)

// SessionRef is the one name a piece of agent work goes by, from the moment
// start_work is called to the moment its transcript is read. A main session
// is its harness session id. A Claude Code subagent runs INSIDE its parent's
// session — the hooks it fires carry the parent's session_id plus an
// agent_id, and its transcript is a separate file — so it is named
// "<session_id>/<agent_id>", which keeps it apart from the parent and from
// its siblings. OMP gives each subagent a session of its own, so no agent
// id is involved there.
func SessionRef(sessionID, agentID string) string {
	sessionID, agentID = strings.TrimSpace(sessionID), strings.TrimSpace(agentID)
	if sessionID == "" {
		return ""
	}
	if agentID == "" {
		return sessionID
	}
	return sessionID + "/" + agentID
}

// workTool matches the Fulcrum tools whose calls name a session's card.
// Claude Code exposes an MCP tool as mcp__<server>__<tool>; the bare name is
// accepted too so the rule is the same wherever it runs.
var workTool = regexp.MustCompile(`^(?:mcp__fulcrum__)?(start_work|finish_work)$`)

// InjectSessionRef fills session_ref into a start_work / finish_work call
// that left it blank, returning the amended input and true. Any other tool,
// or a call that already names a session, returns false: a model that set
// session_ref on purpose — an orchestrator pinning another session — is
// never overridden.
func InjectSessionRef(toolName string, input map[string]any, sessionRef string) (map[string]any, bool) {
	if sessionRef == "" || !workTool.MatchString(toolName) {
		return nil, false
	}
	if existing, _ := input["session_ref"].(string); strings.TrimSpace(existing) != "" {
		return nil, false
	}
	amended := make(map[string]any, len(input)+1)
	for key, value := range input {
		amended[key] = value
	}
	amended["session_ref"] = sessionRef
	return amended, true
}
