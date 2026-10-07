package mcp

import (
	"os"
	"strings"
	"testing"

	"github.com/asaraog/mcp-cricket/internal/history"
	"github.com/asaraog/mcp-cricket/internal/history/histtest"
)

// The archive opens once a process, so every test in this package reads
// one copy of the checked-in fixture, extended with the matches histtest
// adds: the 2024 World Cup games, India's women's T20I and men's ODI, a
// men's and a women's Big Bash game and a Syed Mushtaq Ali Trophy game. A
// file that is there is opened as it is, and nothing is downloaded.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "histfixture")
	if err != nil {
		panic(err)
	}
	path, err := histtest.Extend("../history/testdata/fixture.db", dir)
	if err != nil {
		panic(err)
	}
	os.Setenv("HISTORY_DB", path)
	history.Enabled()
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

// India's five games in the fixture, as cricket_team_form lists them.
const (
	formNov   = "2024-11-15  WON vs South Africa (India tour of South Africa)\n"
	formFinal = "2024-06-29  WON vs South Africa (ICC Men's T20 World Cup)\n"
	formSemi  = "2024-06-27  WON vs England (ICC Men's T20 World Cup)\n"
	formWomen = "2024-04-28  WON vs Bangladesh (India Women tour of Bangladesh)\n"
	formODI   = "2022-12-10  WON vs Bangladesh (India tour of Bangladesh)\n"
)

// cricket_team_form with no format or gender reads every game, as it
// always has: India's five in the fixture, the women's T20I and the men's
// ODI among them, since the archive calls both of India's sides "India".
// With them it reads one side's games of one format, and says which.
func TestTeamFormToolKeepsOneFormatAndGender(t *testing.T) {
	for _, c := range []struct {
		args map[string]any
		want string
	}{
		{map[string]any{"team": "India"}, "India — last 5 archived matches\n" + formNov + formFinal + formSemi + formWomen + formODI},
		{map[string]any{"team": "India", "format": "t20", "gender": "male"}, "India — last 3 archived men's T20s\n" + formNov + formFinal + formSemi},
		{map[string]any{"team": "India", "gender": "women"}, "India — last 1 archived women's game\n" + formWomen},
		{map[string]any{"team": "India", "format": "ODI"}, "India — last 1 archived ODI\n" + formODI},
	} {
		if out, isErr := callTool(t, "cricket_team_form", c.args); isErr || out != c.want {
			t.Errorf("%v:\n got: %q\nwant: %q", c.args, out, c.want)
		}
	}
	for _, args := range []map[string]any{
		{"team": "India", "format": "hundred"},
		{"team": "India", "gender": "mixed"},
		{"team": "India", "format": "test"},
	} {
		if out, isErr := callTool(t, "cricket_team_form", args); !isErr {
			t.Errorf("%v answered:\n%s", args, out)
		}
	}
	// A format the fixture holds none of is named in the answer.
	if out, _ := callTool(t, "cricket_team_form", map[string]any{"team": "India", "format": "test"}); out != `no archived Tests for "India"` {
		t.Errorf("India's Tests: %q", out)
	}
}

// cricket_leaders read every country's T20Is together under "t20", and a
// 2024 list put S Mandhana's 80 third among the men. t20, odi and test
// read the men's unless gender says otherwise, and the header says so;
// every other league reads every game under the header it always had.
// "t20" holds the domestic T20s histgen.py knows no league for too: V
// Solanki's 78 is a Syed Mushtaq Ali Trophy innings.
func TestLeadersToolReadsTheMensOfTheInternationalCodes(t *testing.T) {
	const men2024 = "T20 men's leaders — batting, 2024\n" +
		" 1. N Tilak Varma — 120 runs off 47 balls (strike rate 255.3)\n" +
		" 2. SV Samson — 109 runs off 56 balls (strike rate 194.6)\n" +
		" 3. V Solanki — 78 runs off 40 balls (strike rate 195.0)\n" +
		" 4. V Kohli — 76 runs off 59 balls (strike rate 128.8)\n" +
		" 5. T Stubbs — 74 runs off 50 balls (strike rate 148.0)\n" +
		" 6. RG Sharma — 66 runs off 44 balls (strike rate 150.0)\n" +
		" 7. H Klaasen — 60 runs off 34 balls (strike rate 176.5)\n" +
		" 8. SA Yadav — 50 runs off 40 balls (strike rate 125.0)\n" +
		" 9. AR Patel — 47 runs off 31 balls (strike rate 151.6)\n"
	if out, isErr := callTool(t, "cricket_leaders", map[string]any{"league": "t20", "year": "2024", "limit": 9}); isErr || out != men2024 {
		t.Errorf("t20 2024 with no gender:\n got: %q\nwant: %q", out, men2024)
	}
	out, isErr := callTool(t, "cricket_leaders", map[string]any{"league": "t20", "year": "2024", "gender": "female"})
	if isErr || !strings.HasPrefix(out, "T20 women's leaders — batting, 2024\n 1. S Mandhana — 80 runs off 50 balls (strike rate 160.0)\n") || strings.Contains(out, "V Kohli") {
		t.Errorf("t20 2024 women's:\n%s", out)
	}
	if out, isErr := callTool(t, "cricket_leaders", map[string]any{"league": "t20", "gender": "mixed"}); !isErr {
		t.Errorf("an unknown gender answered:\n%s", out)
	}
	// A league that is not t20, odi or test, with no gender: the header it
	// always had. With gender male: the same players, under a header that
	// says so. The fixture files its two MLC games under "fixture.zip",
	// the zip it was built from.
	all, isErr := callTool(t, "cricket_leaders", map[string]any{"league": "fixture.zip", "limit": 50})
	men, isErr2 := callTool(t, "cricket_leaders", map[string]any{"league": "fixture.zip", "limit": 50, "gender": "male"})
	if isErr || isErr2 || !strings.HasPrefix(all, "FIXTURE.ZIP leaders — batting, all seasons\n") ||
		!strings.HasPrefix(men, "FIXTURE.ZIP men's leaders — batting, all seasons\n") ||
		strings.Count(all, "\n") != strings.Count(men, "\n") {
		t.Errorf("fixture:\n%s\nfixture men's:\n%s", all, men)
	}
}

// Staging, 2026-10-07, "who has the most runs in the big bash": "BL Mooney
// leads the Big Bash with 5,000 runs ... EA Perry sits second". The
// Women's Big Bash League is filed under "bbl" with the men's. The tool
// reads the men's unless asked, and says so; gender female reads the
// women's.
func TestBigBashLeadersAreTheMensUnlessAsked(t *testing.T) {
	const bblMen = "Big Bash men's leaders — batting, all seasons\n" +
		" 1. CA Lynn — 40 runs off 30 balls (strike rate 133.3)\n" +
		" 2. AJ Finch — 30 runs off 25 balls (strike rate 120.0)\n"
	const bblWomen = "Big Bash women's leaders — batting, all seasons\n" +
		" 1. BL Mooney — 60 runs off 45 balls (strike rate 133.3)\n" +
		" 2. HK Matthews — 45 runs off 35 balls (strike rate 128.6)\n"
	for _, c := range []struct {
		args map[string]any
		want string
	}{
		{map[string]any{"league": "bbl"}, bblMen},
		{map[string]any{"league": "BBL", "gender": "male"}, bblMen},
		{map[string]any{"league": "bbl", "gender": "female"}, bblWomen},
	} {
		if out, isErr := callTool(t, "cricket_leaders", c.args); isErr || out != c.want {
			t.Errorf("%v:\n got: %q\nwant: %q", c.args, out, c.want)
		}
	}
	// The wickets are the men's too: Richardson's of Lynn and Swepson's of
	// Finch, and not Matthews's of Mooney.
	out, isErr := callTool(t, "cricket_leaders", map[string]any{"league": "bbl", "kind": "bowling"})
	if isErr || !strings.HasPrefix(out, "Big Bash men's leaders — bowling, all seasons\n") ||
		!strings.Contains(out, "KW Richardson — 1 wickets, economy 8.00\n") || !strings.Contains(out, "MJ Swepson — 1 wickets, economy 7.20\n") ||
		strings.Contains(out, "HK Matthews") {
		t.Errorf("Big Bash bowling:\n%s", out)
	}
}

// Prod and staging, 2026-10-06, "who has the most wickets in T20
// internationals in 2025" had no list to read, and "t20" holds the
// domestic T20s histgen.py knows no league for with the T20Is. t20i is
// the T20Is alone, the men's unless gender says otherwise: neither the
// Syed Mushtaq Ali Trophy's V Solanki and AA Sheth nor the women's S
// Mandhana and SS Vastrakar. odi reads the men's as well, and test, which
// the fixture holds none of, says it has no men's list.
func TestLeadersToolReadsTheInternationalCodes(t *testing.T) {
	const t20iMen2024 = "T20I men's leaders — batting, 2024\n" +
		" 1. N Tilak Varma — 120 runs off 47 balls (strike rate 255.3)\n" +
		" 2. SV Samson — 109 runs off 56 balls (strike rate 194.6)\n" +
		" 3. V Kohli — 76 runs off 59 balls (strike rate 128.8)\n" +
		" 4. T Stubbs — 74 runs off 50 balls (strike rate 148.0)\n" +
		" 5. RG Sharma — 66 runs off 44 balls (strike rate 150.0)\n" +
		" 6. H Klaasen — 60 runs off 34 balls (strike rate 176.5)\n" +
		" 7. SA Yadav — 50 runs off 40 balls (strike rate 125.0)\n" +
		" 8. AR Patel — 47 runs off 31 balls (strike rate 151.6)\n" +
		" 9. Q de Kock — 39 runs off 31 balls (strike rate 125.8)\n"
	if out, isErr := callTool(t, "cricket_leaders", map[string]any{"league": "t20i", "year": "2024", "limit": 9}); isErr || out != t20iMen2024 {
		t.Errorf("t20i 2024:\n got: %q\nwant: %q", out, t20iMen2024)
	}
	out, isErr := callTool(t, "cricket_leaders", map[string]any{"league": "t20i", "year": "2024", "kind": "bowling"})
	if isErr || !strings.HasPrefix(out, "T20I men's leaders — bowling, 2024\n") ||
		!strings.Contains(out, "AR Patel — 3 wickets, economy 7.75\n") || !strings.Contains(out, "K Maharaj — 3 wickets, economy 9.69\n") ||
		strings.Contains(out, "AA Sheth") || strings.Contains(out, "SS Vastrakar") {
		t.Errorf("t20i 2024 bowling:\n%s", out)
	}
	const odiMen = "ODI men's leaders — batting, all seasons\n" +
		" 1. Ishan Kishan — 210 runs off 131 balls (strike rate 160.3)\n" +
		" 2. Shakib Al Hasan — 43 runs off 50 balls (strike rate 86.0)\n"
	if out, isErr := callTool(t, "cricket_leaders", map[string]any{"league": "odi"}); isErr || !strings.HasPrefix(out, odiMen) {
		t.Errorf("odi:\n%s", out)
	}
	if out, isErr := callTool(t, "cricket_leaders", map[string]any{"league": "test"}); !isErr || out != `no archived men's data for league "test"` {
		t.Errorf("test, which the fixture holds none of: %q", out)
	}
}
