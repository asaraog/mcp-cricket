package mcp

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
)

// hostedToolNames is the hosted server's tools/list at 0.3.0, in its order,
// as https://cricketfornoobs.com/mcp served it on 2026-09-27.
var hostedToolNames = []string{
	"cricket_win_probability", "cricket_head_to_head", "cricket_player_career",
	"cricket_match_archive", "cricket_phase_stats", "cricket_venue_stats",
	"cricket_leaders", "cricket_team_form", "cricket_market_odds",
	"cricket_dismissals", "cricket_discipline", "cricket_situational",
	"cricket_partnerships", "cricket_live_matches", "cricket_minor_league",
	"cricket_minor_league_info", "cricket_explain_term",
}

// A client moving between the hosted server and this binary sees one tool
// set, under one version.
func TestToolListMatchesTheHostedServer(t *testing.T) {
	res := rpc(t, "tools/list", `{}`)
	m, _ := res.Result.(map[string]any)
	tools, _ := m["tools"].([]Tool)
	var got []string
	for _, tl := range tools {
		got = append(got, tl.Name)
	}
	if !reflect.DeepEqual(got, hostedToolNames) {
		t.Errorf("tools/list:\n got %d %v\nwant %d %v", len(got), got, len(hostedToolNames), hostedToolNames)
	}

	if ServerVersion != "0.3.0" {
		t.Errorf("ServerVersion = %q, want 0.3.0", ServerVersion)
	}
	hello := rpc(t, "initialize", `{"protocolVersion":"2025-06-18"}`)
	hm, _ := hello.Result.(map[string]any)
	if info, _ := hm["serverInfo"].(map[string]any); info["version"] != "0.3.0" {
		t.Errorf("serverInfo = %v; a client keeps 0.2.0's tool list until the version moves", info)
	}
}

// The tool's name, description, schema and annotations are the hosted
// server's exactly: testdata holds its tools/list entry.
func TestMinorLeagueInfoSchemaIsTheHostedOne(t *testing.T) {
	raw, err := os.ReadFile("testdata/minor_league_info.hosted.json")
	if err != nil {
		t.Fatal(err)
	}
	var want map[string]any
	if err := json.Unmarshal(raw, &want); err != nil {
		t.Fatal(err)
	}
	tool, ok := ByName(BuildTools())["cricket_minor_league_info"]
	if !ok {
		t.Fatal("cricket_minor_league_info is not in tools/list")
	}
	b, err := json.Marshal(tool)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if reflect.DeepEqual(got, want) {
		return
	}
	for _, key := range []string{"name", "description", "annotations"} {
		if !reflect.DeepEqual(got[key], want[key]) {
			t.Errorf("%s:\n got %v\nwant %v", key, got[key], want[key])
		}
	}
	gs, _ := got["inputSchema"].(map[string]any)
	ws, _ := want["inputSchema"].(map[string]any)
	gp, _ := gs["properties"].(map[string]any)
	wp, _ := ws["properties"].(map[string]any)
	for name := range wp {
		if !reflect.DeepEqual(gp[name], wp[name]) {
			t.Errorf("argument %s:\n got %v\nwant %v", name, gp[name], wp[name])
		}
	}
	for name := range gp {
		if _, ok := wp[name]; !ok {
			t.Errorf("argument %s is not in the hosted schema", name)
		}
	}
	t.Errorf("tool differs from testdata/minor_league_info.hosted.json:\n got %s", b)
}

// Every call, whatever its arguments, gets the same one line naming where
// the data is, and never an error.
func TestMinorLeagueInfoPointsAtTheHostedServer(t *testing.T) {
	if strings.Contains(minorLeagueInfoHosted, "\n") || !strings.Contains(minorLeagueInfoHosted, hostedURL) {
		t.Errorf("want one line naming %s: %q", hostedURL, minorLeagueInfoHosted)
	}
	for _, args := range []map[string]any{
		nil,
		{},
		{"player": "Andries Gous"},
		{"team": "Chicago Kingsmen", "season": 2025},
		{"team": "Atlanta Fire", "opponent": "Atlanta Lightning", "date": "2026-09-26"},
		{"ground": "Grand Prairie", "leaders": "runs", "topic": "champions"},
		{"player": []any{"Andries Gous"}, "season": "not a year"},
	} {
		out, isErr := callTool(t, "cricket_minor_league_info", args)
		if isErr || out != minorLeagueInfoHosted {
			t.Errorf("%v: %q (error %v)", args, out, isErr)
		}
	}
}
