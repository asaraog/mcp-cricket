package history

import (
	"os"
	"strings"
	"testing"
)

// Fixture: two real 2026 MLC matches — Freedom v Unicorns (Jul 16) and
// Knight Riders v Freedom (Jul 18, the season's last game).
func TestMain(m *testing.M) {
	os.Setenv("HISTORY_DB", "testdata/fixture.db")
	os.Exit(m.Run())
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
