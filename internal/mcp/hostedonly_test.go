package mcp

import (
	"strings"
	"testing"
)

// cricket_minor_league_info carries the hosted server's name and arguments,
// and in this build answers every call with one line naming where the data
// is. A client switching between the two must see the same schema.
func TestMinorLeagueInfoPointsAtTheHostedServer(t *testing.T) {
	tool, ok := ByName(BuildTools())["cricket_minor_league_info"]
	if !ok {
		t.Fatal("cricket_minor_league_info is not in tools/list")
	}
	if tool.Annotations["readOnlyHint"] != true || tool.Annotations["destructiveHint"] != false {
		t.Errorf("annotations: %v", tool.Annotations)
	}
	if req, ok := tool.InputSchema["required"]; ok {
		t.Errorf("every argument must be optional, schema requires %v", req)
	}
	props, _ := tool.InputSchema["properties"].(map[string]any)
	want := map[string]string{
		"player": "string", "team": "string", "season": "integer",
		"ground": "string", "leaders": "string", "topic": "string",
	}
	if len(props) != len(want) {
		t.Errorf("%d arguments, want %d: %v", len(props), len(want), props)
	}
	for name, typ := range want {
		p, _ := props[name].(map[string]any)
		if p["type"] != typ {
			t.Errorf("%s: type %v, want %s", name, p["type"], typ)
		}
	}
	if !strings.Contains(tool.Description, hostedURL) {
		t.Errorf("description does not say the data is hosted-only: %q", tool.Description)
	}

	for _, args := range []map[string]any{
		{},
		{"player": "Unmukt Chand"},
		{"team": "Seattle Thunderbolts", "season": 2025},
		{"ground": "Grand Prairie", "leaders": "runs", "topic": "who won in 2024"},
	} {
		out, isErr := callTool(t, "cricket_minor_league_info", args)
		if isErr || out != minorLeagueInfoHosted {
			t.Errorf("%v: %q (error %v)", args, out, isErr)
		}
	}
	if strings.Contains(minorLeagueInfoHosted, "\n") || !strings.Contains(minorLeagueInfoHosted, hostedURL) {
		t.Errorf("want one line naming %s: %q", hostedURL, minorLeagueInfoHosted)
	}
}
