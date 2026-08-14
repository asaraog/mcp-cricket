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

// callWinProb returns the text of a cricket_win_probability call.
func callWinProb(t *testing.T, args string) string {
	t.Helper()
	res := rpc(t, "tools/call", `{"name":"cricket_win_probability","arguments":`+args+`}`)
	m, _ := res.Result.(map[string]any)
	if m["isError"] == true {
		t.Fatalf("unexpected error: %v", m)
	}
	content, _ := m["content"].([]any)
	return content[0].(map[string]any)["text"].(string)
}

// Two of the seven innings-one features are measured against par, and par
// swings from 153.7 to 172.5 by league alone. Without a venue the model uses
// one global constant for every T20 ever played: 0.6047 held-out log loss
// against 0.5988 with the real ground. So the venue has to reach the model,
// and the only proof of that is the answer moving.
func TestVenueReachesTheModel(t *testing.T) {
	const state = `"runs":80,"wickets":2,"overs":10.0,"total_overs":20,"innings":1`
	none := callWinProb(t, `{`+state+`}`)
	high := callWinProb(t, `{`+state+`,"venue":"M Chinnaswamy Stadium, Bengaluru","league":"IPL"}`)
	if none == high {
		t.Errorf("venue changed nothing; par is not reaching the model:\n%s", none)
	}
	if !strings.Contains(high, "Par here:") {
		t.Errorf("par not surfaced with a venue:\n%s", high)
	}
	// A high-scoring ground raises par, so the SAME score is worth less.
	if !strings.Contains(none, "Par: using the global average") {
		t.Errorf("no-venue call should say it fell back:\n%s", none)
	}
}

// An unrecognised ground must fall back to the league rather than fail:
// ESPN and Cricsheet disagree on plenty of spellings, and a hard miss here
// would be worse than the global constant it replaces.
func TestUnknownGroundFallsBackToLeague(t *testing.T) {
	text := callWinProb(t, `{"runs":80,"wickets":2,"overs":10.0,"total_overs":20,"innings":1,`+
		`"venue":"Not A Real Ground At All","league":"IPL"}`)
	if !strings.Contains(text, "Par here:") {
		t.Errorf("unknown ground should still resolve a par via the league:\n%s", text)
	}
}

// The Hundred bowls five-ball sets and has its own par anchor; a venue must
// not drag it onto the six-ball table.
func TestHundredKeepsItsOwnPar(t *testing.T) {
	text := callWinProb(t, `{"runs":80,"wickets":2,"overs":10.0,"total_overs":20,"innings":1,`+
		`"balls_per_over":5,"venue":"Lord's","league":"The Hundred"}`)
	if !strings.Contains(text, "Par here:") {
		t.Errorf("Hundred par not surfaced:\n%s", text)
	}
}
