package mcp

import (
	"encoding/json"
	"strings"
	"testing"
)

func rpc(t *testing.T, method, params string) Response {
	t.Helper()
	tools := BuildTools()
	byName := map[string]Tool{}
	for _, tl := range tools {
		byName[tl.Name] = tl
	}
	req := Request{JSONRPC: "2.0", ID: json.RawMessage(`1`), Method: method}
	if params != "" {
		req.Params = json.RawMessage(params)
	}
	res, _ := Handle(req, tools, byName)
	return res
}

func TestInitializeAndList(t *testing.T) {
	res := rpc(t, "initialize", `{"protocolVersion":"2025-06-18"}`)
	m, _ := res.Result.(map[string]any)
	if m["protocolVersion"] != "2025-06-18" {
		t.Errorf("protocol echo failed: %v", m["protocolVersion"])
	}
	res = rpc(t, "tools/list", `{}`)
	m, _ = res.Result.(map[string]any)
	tools, _ := m["tools"].([]Tool)
	if len(tools) < 6 {
		t.Errorf("expected the full tool set, got %d", len(tools))
	}
	for _, tl := range tools {
		if tl.Description == "" || tl.InputSchema == nil {
			t.Errorf("tool %s missing description or schema", tl.Name)
		}
	}
}

func TestWinProbabilityTool(t *testing.T) {
	res := rpc(t, "tools/call", `{"name":"cricket_win_probability","arguments":{"runs":149,"wickets":7,"overs":17.0,"total_overs":20,"innings":2,"target":178}}`)
	m, _ := res.Result.(map[string]any)
	if m["isError"] == true {
		t.Fatalf("unexpected error: %v", m)
	}
	content, _ := m["content"].([]any)
	text := content[0].(map[string]any)["text"].(string)
	if !strings.Contains(text, "win probability") || !strings.Contains(text, "29 needed from 18 balls") {
		t.Errorf("unexpected output: %s", text)
	}
}

func TestUnknownToolIsAnError(t *testing.T) {
	res := rpc(t, "tools/call", `{"name":"nope","arguments":{}}`)
	if res.Error == nil {
		t.Error("unknown tool should return a JSON-RPC error")
	}
}

func TestMissingArgsReportedToModel(t *testing.T) {
	res := rpc(t, "tools/call", `{"name":"cricket_head_to_head","arguments":{"batter":"V Kohli"}}`)
	m, _ := res.Result.(map[string]any)
	if m["isError"] != true {
		t.Error("missing bowler should surface as a tool error, not a protocol error")
	}
}
