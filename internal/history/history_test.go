package history

import (
	"os"
	"strings"
	"testing"

	"github.com/asaraog/mcp-cricket/internal/history/histtest"
)

// Fixture: two real 2026 MLC matches — Freedom v Unicorns (Jul 16) and
// Knight Riders v Freedom (Jul 18, the season's last game) — plus the
// 2024 World Cup matches histtest adds to a copy of it.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "histfixture")
	if err != nil {
		panic(err)
	}
	path, err := histtest.Extend("testdata/fixture.db", dir)
	if err != nil {
		panic(err)
	}
	os.Setenv("HISTORY_DB", path)
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

func TestEnabledAndTeams(t *testing.T) {
	if !Enabled() {
		t.Fatal("fixture db should enable history")
	}
	got := TeamsIn("how did the Washington Freedom do against the San Francisco Unicorns?")
	if len(got) != 2 {
		t.Fatalf("TeamsIn = %v", got)
	}
}

func TestFindMatchAndScorecard(t *testing.T) {
	m, ok := FindMatch("what happened in the Freedom Unicorns game in 2026",
		[]string{"Washington Freedom", "San Francisco Unicorns"})
	if !ok {
		t.Fatal("match should resolve")
	}
	if m.Date != "2026-07-16" {
		t.Fatalf("wrong match: %+v", m)
	}
	card := Scorecard(m)
	if !strings.Contains(card, "Innings 1") || !strings.Contains(card, "bat:") {
		t.Fatalf("thin scorecard:\n%s", card)
	}
}

func TestFinalHeuristicPicksLatest(t *testing.T) {
	m, ok := FindMatch("who won the 2026 final", []string{"Washington Freedom"})
	if !ok {
		t.Fatal("final should resolve")
	}
	if m.Date != "2026-07-18" {
		t.Fatalf("final heuristic picked %s", m.Date)
	}
}

func TestOverDetail(t *testing.T) {
	m, _ := FindMatch("freedom unicorns 2026", []string{"Washington Freedom", "San Francisco Unicorns"})
	od := OverDetail(m, "who bowled the 4th over?")
	if !strings.Contains(od, "Over 4 of innings 1: bowled by") {
		t.Fatalf("over detail:\n%q", od)
	}
}

func TestUnknownTeamFailsQuiet(t *testing.T) {
	if _, ok := FindMatch("anything", []string{"Fake Team FC"}); ok {
		t.Fatal("unknown team must not resolve")
	}
}

// Prod, 2026-10-06, "who won the 2024 T20 world cup final": "New Zealand
// won the 2024 Women's T20 World Cup final." No team was named, and only
// a league could resolve a match without one. The tournament resolves it
// now: the last match of the event and year is the final, the men's
// unless the question says women's, and a question about who took it
// resolves without the word "final".
func TestTournamentFinalResolvesWithoutATeam(t *testing.T) {
	for _, c := range []struct{ msg, date, event string }{
		{"who won the 2024 T20 world cup final", "2024-06-29", "ICC Men's T20 World Cup"},
		{"who won the 2024 T20 world cup", "2024-06-29", "ICC Men's T20 World Cup"},
		{"how many runs did Rohit Sharma score in the 2024 T20 world cup final", "2024-06-29", "ICC Men's T20 World Cup"},
		{"who won the 2024 women's T20 world cup final", "2024-10-20", "ICC Women's T20 World Cup"},
		{"2024 womens t20 wc winner", "2024-10-20", "ICC Women's T20 World Cup"},
	} {
		m, ok := FindMatch(c.msg, nil)
		if !ok {
			t.Errorf("%q resolved nothing", c.msg)
			continue
		}
		if m.Date != c.date || m.Event != c.event {
			t.Errorf("%q: %s %q, want %s %q", c.msg, m.Date, m.Event, c.date, c.event)
		}
	}
	// A tournament with nothing asked about its outcome is no one match,
	// and a tournament the archive does not hold resolves nothing.
	for _, msg := range []string{
		"best bowling figures at the 2024 T20 world cup",
		"who won the 2024 champions trophy final",
		"who won the 2024 world cup final",
	} {
		if m, ok := FindMatch(msg, nil); ok {
			t.Errorf("%q resolved %s %q", msg, m.Date, m.Event)
		}
	}
}

// The hosted archive tool for "India v South Africa 2024 T20 World Cup
// final" returned the November T20I of India's tour (Tilak Varma 120):
// with teams named, only the year filtered. The tournament named keeps
// the teams' other meetings out; the year alone still picks the latest.
func TestTournamentKeepsTheTeamsOtherMeetingsOut(t *testing.T) {
	names := TeamsIn("India v South Africa 2024 T20 World Cup final")
	if len(names) != 2 {
		t.Fatalf("TeamsIn = %v", names)
	}
	for _, c := range []struct {
		msg   string
		names []string
		date  string
	}{
		{"India v South Africa 2024 T20 World Cup final", names, "2024-06-29"},
		{"India 2024 T20 World Cup final", []string{"India"}, "2024-06-29"},
		{"India v South Africa 2024", names, "2024-11-15"},
		{"India v South Africa 2024 final", names, "2024-11-15"},
	} {
		m, ok := FindMatch(c.msg, c.names)
		if !ok {
			t.Errorf("%q resolved nothing", c.msg)
			continue
		}
		if m.Date != c.date {
			t.Errorf("%q: %s %q, want %s", c.msg, m.Date, m.Event, c.date)
		}
	}
}

// Prod, 2026-10-06, "how many runs did Rohit Sharma score in the 2024 T20
// world cup final" was answered "9 off 5", then "I don't have the
// per-match scorecard", then "76 off 59" (Kohli's). Scorecard lists three
// batters an innings and Rohit was fourth, so his line was never in the
// block. PlayerLine finds the player the question names, under the
// archive's name.
func TestPlayerLineNamesTheQuestionsPlayer(t *testing.T) {
	m, ok := FindMatch("how many runs did Rohit Sharma score in the 2024 T20 world cup final", nil)
	if !ok {
		t.Fatal("final should resolve")
	}
	if card := Scorecard(m); strings.Contains(card, "RG Sharma") {
		t.Fatalf("precondition: the top three hold Rohit:\n%s", card)
	}
	rohit := "  bat: RG Sharma 9 off 5, out caught\n"
	for _, c := range []struct{ msg, want string }{
		{"how many runs did Rohit Sharma score in the 2024 T20 world cup final", rohit},
		{"RG Sharma in the 2024 t20 world cup final?", rohit},
		{"what did Sharma do in the final", rohit},
		{"Rohit Sharma's score in the final", rohit},
		{"Virat Kohli in the 2024 T20 World Cup final", "  bat: V Kohli 76 off 59, out caught\n"},
		{"Quinton de Kock's score in the 2024 T20 World Cup final", "  bat: Q de Kock 39 off 31, out caught\n"},
		// Klaasen's two wides are Bumrah's: 52 and 4 and two extras.
		{"how did Bumrah bowl in the 2024 T20 World Cup final", "  bowl: JJ Bumrah 2 wickets for 58\n"},
		{"Axar Patel in the 2024 T20 World Cup final", "  bat: AR Patel 47 off 31, out run out\n  bowl: AR Patel 1 wickets for 39\n"},
		// No one named, and a Sharma who was not in the match.
		{"who won the 2024 T20 world cup final", ""},
		{"how many did Abhishek Sharma score in the 2024 T20 world cup final", ""},
	} {
		if got := PlayerLine(m, c.msg); got != c.want {
			t.Errorf("%q:\n got: %q\nwant: %q", c.msg, got, c.want)
		}
	}
}
