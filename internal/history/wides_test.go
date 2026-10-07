package history

import (
	"strings"
	"testing"

	"github.com/asaraog/mcp-cricket/internal/history/histtest"
)

// Staging, 2026-10-06, "who top scored in the 2024 T20 world cup final":
// "Virat Kohli 76 off 62" and "Klaasen 52 off 29", for 76 off 59 and 52
// off 27. Balls faced were a row count, and a wide is a row. The fixture
// gives Kohli three wides and Klaasen two.
func TestWidesAreNotBallsFaced(t *testing.T) {
	if !Enabled() || !hasWide {
		t.Fatal("precondition: the extended fixture carries the wide column")
	}
	m, ok := FindMatch("who top scored in the 2024 T20 world cup final", nil)
	if !ok {
		t.Fatal("final should resolve")
	}
	card := Scorecard(m)
	for _, want := range []string{"  bat: V Kohli 76 off 59\n", "  bat: H Klaasen 52 off 27\n"} {
		if !strings.Contains(card, want) {
			t.Errorf("scorecard lacks %q:\n%s", want, card)
		}
	}
	for _, c := range []struct{ msg, want string }{
		{"Virat Kohli in the 2024 T20 World Cup final", "  bat: V Kohli 76 off 59, out caught\n"},
		{"Heinrich Klaasen in the 2024 T20 World Cup final", "  bat: H Klaasen 52 off 27, out caught\n"},
	} {
		if got := PlayerLine(m, c.msg); got != c.want {
			t.Errorf("%q:\n got: %q\nwant: %q", c.msg, got, c.want)
		}
	}
}

// A bowler's balls, the ones his overs and economy are over, are neither
// wides nor no-balls; a batter's are everything but the wides. Bumrah
// bowled Klaasen 27 and Markram 5 with two wides: 32 legal balls, 34
// rows. One of Stubbs's 21 balls off Pandya is a no-ball: 21 faced, and
// with Miller's 17 Pandya's 37 legal balls over 38 rows.
func TestBowlersBallsAreLegalOnes(t *testing.T) {
	balls := func(kind string) map[string]int {
		ls, ok := Leaders("t20", "2024", kind, "", 50)
		if !ok {
			t.Fatalf("no %s leaders", kind)
		}
		out := map[string]int{}
		for _, l := range ls {
			out[l.Name] = l.Balls
		}
		return out
	}
	bowl, bat := balls("bowling"), balls("batting")
	for _, c := range []struct {
		name string
		got  int
		want int
	}{
		{"JJ Bumrah bowling", bowl["JJ Bumrah"], 32},
		{"HH Pandya bowling", bowl["HH Pandya"], 37},
		{"V Kohli batting", bat["V Kohli"], 59},
		{"T Stubbs batting", bat["T Stubbs"], 21 + 29},
	} {
		if c.got != c.want {
			t.Errorf("%s: %d balls, want %d", c.name, c.got, c.want)
		}
	}
	if d, ok := DisciplineStats("JJ Bumrah", "bowling", 20); !ok || d.Balls != 32 {
		t.Errorf("Bumrah discipline: %+v, %v; want 32 balls", d, ok)
	}
	if d, ok := DisciplineStats("V Kohli", "batting", 20); !ok || d.Balls != 59 {
		t.Errorf("Kohli discipline: %+v, %v; want 59 balls", d, ok)
	}
}

// The v1 archive, which staging and prod run today, has no wide column:
// there every row is a ball, as it was, and the same innings reads 76
// off 62. The server must keep working on it.
func TestOldArchiveWithoutTheColumnCountsEveryRow(t *testing.T) {
	path, err := histtest.ExtendOld("testdata/fixture.db", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	old, cols, err := openFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if cols.wide {
		t.Fatal("the old-shape fixture reports a wide column")
	}
	savedDB, savedWide, savedGender := db, hasWide, hasGender
	db, hasWide, hasGender = old, cols.wide, cols.gender
	t.Cleanup(func() {
		db, hasWide, hasGender = savedDB, savedWide, savedGender
		old.Close()
	})
	m, ok := FindMatch("who top scored in the 2024 T20 world cup final", nil)
	if !ok {
		t.Fatal("final should resolve")
	}
	card := Scorecard(m)
	for _, want := range []string{"  bat: V Kohli 76 off 62\n", "  bat: H Klaasen 52 off 29\n"} {
		if !strings.Contains(card, want) {
			t.Errorf("scorecard lacks %q:\n%s", want, card)
		}
	}
	if got, want := PlayerLine(m, "Virat Kohli in the final"), "  bat: V Kohli 76 off 62, out caught\n"; got != want {
		t.Errorf("PlayerLine:\n got: %q\nwant: %q", got, want)
	}
	if ls, ok := Leaders("t20", "2024", "bowling", "", 50); !ok {
		t.Error("no bowling leaders on the old shape")
	} else {
		for _, l := range ls {
			if l.Name == "JJ Bumrah" && l.Balls != 34 {
				t.Errorf("Bumrah on the old shape: %d balls, want every row, 34", l.Balls)
			}
		}
	}
}
