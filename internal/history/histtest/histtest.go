// Package histtest extends the checked-in fixture archive for tests.
//
// testdata/fixture.db is a binary built by scripts/histgen.py from two
// 2026 MLC matches, and is not rebuilt by hand. The World Cup matches the
// tests need are added here, in Go, to a copy of it: the 2024 men's T20
// World Cup final and the semi-final before it, the November 2024 India v
// South Africa T20I that the year alone used to pick for "India v South
// Africa 2024 T20 World Cup final", and the 2024 women's T20 World Cup
// final the model gave for the men's. Innings totals are the batters'
// runs and the wides here, not the real scores; the lines the tests read
// (RG Sharma 9 off 5, V Kohli 76 off 59, N Tilak Varma 120 off 47) are
// the real ones.
//
// Extend writes the wide and noball columns the rebuilt archive carries
// (histgen.py since 2026-10-06); ExtendOld writes the same matches in the
// v1 file's shape, without them, where a wide faced is one more row and
// Kohli reads 76 off 62, as staging said on 2026-10-06.
package histtest

import (
	"database/sql"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

// A bat is one batter's innings: what he made, off how many balls, the
// wides he faced on top of them (one row and one extra each, not a ball
// faced), how many of his balls were no-balls (a ball faced, one extra,
// not a legal ball bowled), who bowled every ball of it, and how he was
// out ("" for not out).
type bat struct {
	name    string
	runs    int
	balls   int
	wides   int
	noballs int
	bowler  string
	out     string
}

// A match is one row of matches: id, date, league, overs, the two sides,
// venue, event and winner, then each innings in batting order.
type match struct {
	id      string
	date    string
	league  string
	overs   int
	team1   string
	team2   string
	venue   string
	event   string
	winner  string
	innings [][]bat
}

var matches = []match{
	{
		"t20wc-2024-sf2", "2024-06-27", "t20", 20,
		"India", "England", "Providence Stadium", "ICC Men's T20 World Cup", "India",
		[][]bat{
			{{"RG Sharma", 57, 39, 0, 0, "JC Archer", "caught"}, {"SA Yadav", 47, 36, 0, 0, "AU Rashid", "caught"}},
			{{"JC Buttler", 23, 15, 0, 0, "AR Patel", "stumped"}, {"JM Bairstow", 0, 2, 0, 0, "AR Patel", "bowled"}},
		},
	},
	{
		// Kohli faced three wides and Klaasen two, so a row count reads
		// 76 off 62 and 52 off 29, as staging did on 2026-10-06. One of
		// Stubbs's 21 balls is a no-ball: a ball he faced, not one of
		// Pandya's legal 37.
		"t20wc-2024-final", "2024-06-29", "t20", 20,
		"India", "South Africa", "Kensington Oval", "ICC Men's T20 World Cup", "India",
		[][]bat{
			{
				{"V Kohli", 76, 59, 3, 0, "K Maharaj", "caught"},
				{"AR Patel", 47, 31, 0, 0, "A Nortje", "run out"},
				{"S Dube", 27, 16, 0, 0, "M Jansen", "caught"},
				{"RG Sharma", 9, 5, 0, 0, "K Maharaj", "caught"},
				{"SA Yadav", 3, 4, 0, 0, "A Nortje", "caught"},
				{"RR Pant", 0, 2, 0, 0, "K Maharaj", "caught"},
			},
			{
				{"H Klaasen", 52, 27, 2, 0, "JJ Bumrah", "caught"},
				{"Q de Kock", 39, 31, 0, 0, "AR Patel", "caught"},
				{"T Stubbs", 31, 21, 0, 1, "HH Pandya", "bowled"},
				{"DA Miller", 21, 17, 0, 0, "HH Pandya", "caught"},
				{"A Markram", 4, 5, 0, 0, "JJ Bumrah", "caught"},
			},
		},
	},
	{
		"sa-ind-2024-11-15", "2024-11-15", "t20", 20,
		"India", "South Africa", "The Wanderers Stadium", "India tour of South Africa", "India",
		[][]bat{
			{{"N Tilak Varma", 120, 47, 0, 0, "M Jansen", ""}, {"SV Samson", 109, 56, 0, 0, "K Maharaj", ""}},
			{{"T Stubbs", 43, 29, 0, 0, "Arshdeep Singh", "caught"}, {"H Klaasen", 8, 7, 0, 0, "Arshdeep Singh", "bowled"}},
		},
	},
	{
		"wt20wc-2024-final", "2024-10-20", "t20", 20,
		"New Zealand", "South Africa", "Dubai International Cricket Stadium", "ICC Women's T20 World Cup", "New Zealand",
		[][]bat{
			{{"AC Kerr", 43, 38, 0, 0, "N de Klerk", "caught"}, {"BM Halliday", 38, 28, 0, 0, "N de Klerk", ""}},
			{{"L Wolvaardt", 33, 27, 0, 0, "AC Kerr", "caught"}, {"T Brits", 17, 18, 0, 0, "AC Kerr", "caught"}},
		},
	},
}

// Extend copies the fixture at src into dir, adds the wide and noball
// columns and the World Cup matches, and returns the copy's path.
func Extend(src, dir string) (string, error) {
	return extend(src, dir, true)
}

// ExtendOld is Extend in the v1 file's shape: the same matches with no
// wide or noball column, a wide faced being one more row.
func ExtendOld(src, dir string) (string, error) {
	return extend(src, dir, false)
}

func extend(src, dir string, columns bool) (string, error) {
	raw, err := os.ReadFile(src)
	if err != nil {
		return "", err
	}
	dst := filepath.Join(dir, "fixture.db")
	if err := os.WriteFile(dst, raw, 0o644); err != nil {
		return "", err
	}
	db, err := sql.Open("sqlite", dst)
	if err != nil {
		return "", err
	}
	defer db.Close()
	tx, err := db.Begin()
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	// The fixture was built before histgen.py wrote the two columns; the
	// MLC rows it holds get 0 in both, which is what they are.
	insert := `INSERT INTO deliveries (match_id, innings, over, ball, batter, bowler,
	    runs_batter, runs_extras, wicket_kind, player_out) VALUES (?,?,?,?,?,?,?,?,?,?)`
	if columns {
		for _, col := range []string{"wide", "noball"} {
			if _, err := tx.Exec(`ALTER TABLE deliveries ADD COLUMN ` + col + ` INTEGER DEFAULT 0`); err != nil {
				return "", err
			}
		}
		insert = `INSERT INTO deliveries (match_id, innings, over, ball, batter, bowler,
		    runs_batter, runs_extras, wicket_kind, player_out, wide, noball) VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`
	}
	ids := map[string]int64{}
	nid := func(name string) (int64, error) {
		if id, ok := ids[name]; ok {
			return id, nil
		}
		if _, err := tx.Exec(`INSERT OR IGNORE INTO names(name) VALUES (?)`, name); err != nil {
			return 0, err
		}
		var id int64
		if err := tx.QueryRow(`SELECT id FROM names WHERE name = ?`, name).Scan(&id); err != nil {
			return 0, err
		}
		ids[name] = id
		return id, nil
	}
	for _, m := range matches {
		t1, err := nid(m.team1)
		if err != nil {
			return "", err
		}
		t2, err := nid(m.team2)
		if err != nil {
			return "", err
		}
		w, err := nid(m.winner)
		if err != nil {
			return "", err
		}
		if _, err := tx.Exec(`INSERT OR REPLACE INTO matches VALUES (?,?,?,?,?,?,?,?,?,?)`,
			m.id, m.date, m.league, m.overs, t1, t2, m.venue, m.event, w, "won"); err != nil {
			return "", err
		}
		for inn, bats := range m.innings {
			seq := 0
			for _, b := range bats {
				batter, err := nid(b.name)
				if err != nil {
					return "", err
				}
				bowler, err := nid(b.bowler)
				if err != nil {
					return "", err
				}
				// One row a delivery; wide and noball are written only
				// when the file has the columns.
				row := func(runs, extras int, kind string, out any, wide, noball int) error {
					args := []any{m.id, inn + 1, seq/6 + 1, seq%6 + 1, batter, bowler, runs, extras, kind, out}
					if columns {
						args = append(args, wide, noball)
					}
					_, err := tx.Exec(insert, args...)
					seq++
					return err
				}
				// The wides first, each one extra and no ball faced.
				for j := 0; j < b.wides; j++ {
					if err := row(0, 1, "", nil, 1, 0); err != nil {
						return "", err
					}
				}
				// Runs spread evenly over the balls, the remainder one
				// each on the first balls; the no-balls, one extra each,
				// on the first balls too; the dismissal on the last.
				base, rem := b.runs/b.balls, b.runs%b.balls
				for j := 0; j < b.balls; j++ {
					runs := base
					if j < rem {
						runs++
					}
					extras, noball := 0, 0
					if j < b.noballs {
						extras, noball = 1, 1
					}
					kind, out := "", any(nil)
					if b.out != "" && j == b.balls-1 {
						kind, out = b.out, batter
					}
					if err := row(runs, extras, kind, out, 0, noball); err != nil {
						return "", err
					}
				}
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return "", err
	}
	return dst, nil
}
