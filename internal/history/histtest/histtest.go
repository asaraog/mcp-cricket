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
// Two more India games are here for the format and gender filters, and
// their lines are made up apart from Ishan Kishan's 210 off 131, his real
// double hundred: a women's T20I against Bangladesh, where S Mandhana's 80
// would top India's men's T20 batting if gender were ignored, and a men's
// ODI against Bangladesh, where Kishan's 210 would top it if format were.
// Cricsheet names both of India's sides "India".
//
// Two Big Bash games, a men's and a women's, both under league "bbl" as
// histgen.py files them, are for the leaders' gender default. Their lines
// are made up.
//
// A men's Syed Mushtaq Ali Trophy game, India's domestic T20, is filed
// under "t20" with the T20Is, as histgen.py files it, for the T20I
// leaders. Its lines are made up.
//
// Extend writes the wide and noball columns the rebuilt archive carries
// (histgen.py since 2026-10-06) and the gender column (since 2026-10-07);
// ExtendOld writes the same matches in the v1 file's shape, without any
// of them, where a wide faced is one more row and Kohli reads 76 off 62,
// as staging said on 2026-10-06, and a gender is only in the event name.
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
// venue, event, winner and gender ("male" or "female", as Cricsheet
// writes it), then each innings in batting order.
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
	gender  string
	innings [][]bat
}

var matches = []match{
	{
		"t20wc-2024-sf2", "2024-06-27", "t20", 20,
		"India", "England", "Providence Stadium", "ICC Men's T20 World Cup", "India", "male",
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
		"India", "South Africa", "Kensington Oval", "ICC Men's T20 World Cup", "India", "male",
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
		"India", "South Africa", "The Wanderers Stadium", "India tour of South Africa", "India", "male",
		[][]bat{
			{{"N Tilak Varma", 120, 47, 0, 0, "M Jansen", ""}, {"SV Samson", 109, 56, 0, 0, "K Maharaj", ""}},
			{{"T Stubbs", 43, 29, 0, 0, "Arshdeep Singh", "caught"}, {"H Klaasen", 8, 7, 0, 0, "Arshdeep Singh", "bowled"}},
		},
	},
	{
		"wt20wc-2024-final", "2024-10-20", "t20", 20,
		"New Zealand", "South Africa", "Dubai International Cricket Stadium", "ICC Women's T20 World Cup", "New Zealand", "female",
		[][]bat{
			{{"AC Kerr", 43, 38, 0, 0, "N de Klerk", "caught"}, {"BM Halliday", 38, 28, 0, 0, "N de Klerk", ""}},
			{{"L Wolvaardt", 33, 27, 0, 0, "AC Kerr", "caught"}, {"T Brits", 17, 18, 0, 0, "AC Kerr", "caught"}},
		},
	},
	{
		// India's women. Mandhana's 80 would top India's men's T20
		// batting and Vastrakar's four wickets its bowling, read without
		// the gender. The event says "Women", as Cricsheet's do, so a file
		// without the gender column can still tell it apart.
		"ban-ind-w-2024-04-28", "2024-04-28", "t20", 20,
		"India", "Bangladesh", "Sylhet International Cricket Stadium", "India Women tour of Bangladesh", "India", "female",
		[][]bat{
			{{"S Mandhana", 80, 50, 0, 0, "Nahida Akter", "caught"}},
			{
				{"Nigar Sultana", 40, 36, 0, 0, "SS Vastrakar", "caught"},
				{"Murshida Khatun", 20, 22, 0, 0, "SS Vastrakar", "bowled"},
				{"Shorna Akter", 12, 10, 0, 0, "SS Vastrakar", "caught"},
				{"Sobhana Mostary", 8, 9, 0, 0, "SS Vastrakar", "lbw"},
			},
		},
	},
	{
		// India's men in a fifty-over game. Kishan's 210 would top the
		// men's T20 batting and Thakur's three wickets sit level with
		// Patel's, read without the format.
		"ban-ind-2022-12-10", "2022-12-10", "odi", 50,
		"India", "Bangladesh", "Zahur Ahmed Chowdhury Stadium", "India tour of Bangladesh", "India", "male",
		[][]bat{
			{{"Ishan Kishan", 210, 131, 0, 0, "Taskin Ahmed", "caught"}},
			{
				{"Liton Das", 29, 26, 0, 0, "SN Thakur", "caught"},
				{"Shakib Al Hasan", 43, 50, 0, 0, "SN Thakur", "caught"},
				{"Mahmudullah", 25, 21, 0, 0, "SN Thakur", "bowled"},
			},
		},
	},
	{
		// A men's Big Bash game, filed under "bbl" as histgen.py files
		// any event named "Big Bash".
		"bbl-2022-01-05", "2022-01-05", "bbl", 20,
		"Brisbane Heat", "Melbourne Renegades", "Brisbane Cricket Ground", "Big Bash League", "Brisbane Heat", "male",
		[][]bat{
			{{"CA Lynn", 40, 30, 0, 0, "KW Richardson", "caught"}},
			{{"AJ Finch", 30, 25, 0, 0, "MJ Swepson", "caught"}},
		},
	},
	{
		// A women's Big Bash game, filed under "bbl" too: the Women's Big
		// Bash League's name holds "Big Bash". Mooney's 60 and Matthews's
		// 45 top a list read without the gender, as BL Mooney and EA Perry
		// topped staging's "who has the most runs in the big bash" on
		// 2026-10-07.
		"wbbl-2023-11-04", "2023-11-04", "bbl", 20,
		"Perth Scorchers", "Melbourne Renegades", "W.A.C.A. Ground", "Women's Big Bash League", "Perth Scorchers", "female",
		[][]bat{
			{{"BL Mooney", 60, 45, 0, 0, "HK Matthews", "caught"}},
			{{"HK Matthews", 45, 35, 0, 0, "SFM Devine", "caught"}},
		},
	},
	{
		// A men's Syed Mushtaq Ali Trophy game. histgen.py knows no
		// league for its event, so it goes under its Cricsheet match
		// type, "T20", with the T20Is. In a T20I list read off "t20",
		// V Solanki's 78 sits third among the 2024 batters, between
		// Samson's 109 and Kohli's 76, and AA Sheth's four wickets top
		// the bowling.
		"smat-2024-11-23", "2024-11-23", "t20", 20,
		"Baroda", "Sikkim", "Emerald High School Ground", "Syed Mushtaq Ali Trophy", "Baroda", "male",
		[][]bat{
			{{"V Solanki", 78, 40, 0, 0, "Palzor Tamang", ""}},
			{
				{"Ankur Malik", 20, 18, 0, 0, "AA Sheth", "caught"},
				{"Lee Yong Lepcha", 10, 12, 0, 0, "AA Sheth", "bowled"},
				{"Ashish Thapa", 6, 8, 0, 0, "AA Sheth", "lbw"},
				{"Palzor Tamang", 2, 4, 0, 0, "AA Sheth", "caught"},
			},
		},
	},
}

// Extend copies the fixture at src into dir, adds the wide, noball and
// gender columns and the matches above, and returns the copy's path.
func Extend(src, dir string) (string, error) {
	return extend(src, dir, true)
}

// ExtendOld is Extend in the v1 file's shape: the same matches with no
// wide, noball or gender column, a wide faced being one more row.
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
	// The fixture was built before histgen.py wrote the three columns; the
	// MLC rows it holds get 0 in wide and noball, and "male" in gender,
	// which is what they are. gender is the trailing column of matches,
	// as histgen.py writes it.
	insert := `INSERT INTO deliveries (match_id, innings, over, ball, batter, bowler,
	    runs_batter, runs_extras, wicket_kind, player_out) VALUES (?,?,?,?,?,?,?,?,?,?)`
	insertMatch := `INSERT OR REPLACE INTO matches VALUES (?,?,?,?,?,?,?,?,?,?)`
	if columns {
		for _, col := range []string{"wide", "noball"} {
			if _, err := tx.Exec(`ALTER TABLE deliveries ADD COLUMN ` + col + ` INTEGER DEFAULT 0`); err != nil {
				return "", err
			}
		}
		insert = `INSERT INTO deliveries (match_id, innings, over, ball, batter, bowler,
		    runs_batter, runs_extras, wicket_kind, player_out, wide, noball) VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`
		if _, err := tx.Exec(`ALTER TABLE matches ADD COLUMN gender TEXT`); err != nil {
			return "", err
		}
		if _, err := tx.Exec(`UPDATE matches SET gender = 'male'`); err != nil {
			return "", err
		}
		insertMatch = `INSERT OR REPLACE INTO matches VALUES (?,?,?,?,?,?,?,?,?,?,?)`
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
		args := []any{m.id, m.date, m.league, m.overs, t1, t2, m.venue, m.event, w, "won"}
		if columns {
			args = append(args, m.gender)
		}
		if _, err := tx.Exec(insertMatch, args...); err != nil {
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
