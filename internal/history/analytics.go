// Analytics over the ball-by-ball archive: the questions people actually
// ask about cricket that raw scorecards can't answer — how a player
// performs in the powerplay versus at the death, whether a ground
// favours chasing, who leads a season. All computed with SQL over the
// deliveries table, bounded by the same query deadline as the rest of
// this package.
package history

import (
	"database/sql"
	"fmt"
	"strings"
)

// PhaseSplit is one phase of an innings for one player.
type PhaseSplit struct {
	Phase   string
	Balls   int
	Runs    int
	Outs    int
	Wickets int // when the player is bowling
}

// phases carve a limited-overs innings the way commentators do.
//
// The archive stores over as the over's number, 1 to 20: histgen.py
// writes Cricsheet's 0-based over plus one, and "who bowled the 4th over"
// reads d.over = 4 (OverDetail). The bounds here were 0-based, so the
// powerplay split was overs 1 to 5 plus an over 0 no row has, the middle
// split began an over early, and the 20th over, the one a death bowler
// is asked about, was in no phase at all. The bounds are the over numbers
// the names say.
func phaseBounds(totalOvers int) []struct {
	name     string
	from, to int
} {
	if totalOvers >= 50 {
		return []struct {
			name     string
			from, to int
		}{{"powerplay (1-10)", 1, 10}, {"middle (11-40)", 11, 40}, {"death (41-50)", 41, 50}}
	}
	return []struct {
		name     string
		from, to int
	}{{"powerplay (1-6)", 1, 6}, {"middle (7-15)", 7, 15}, {"death (16-20)", 16, 20}}
}

// PhaseStats splits a player's batting and bowling by innings phase.
func PhaseStats(name string, totalOvers int) (batting, bowling []PhaseSplit, ok bool) {
	if !Enabled() {
		return nil, nil, false
	}
	var id int
	if err := db.QueryRowContext(aqctx(), `SELECT id FROM names WHERE LOWER(name)=LOWER(?)`, name).Scan(&id); err != nil {
		// fall back to a suffix match ("Kohli" -> "V Kohli")
		if err := db.QueryRowContext(aqctx(), `SELECT id FROM names WHERE LOWER(name) LIKE LOWER(?) ORDER BY LENGTH(name) LIMIT 1`, "%"+name).Scan(&id); err != nil {
			return nil, nil, false
		}
	}
	for _, p := range phaseBounds(totalOvers) {
		var b PhaseSplit
		b.Phase = p.name
		err := db.QueryRowContext(aqctx(), `
			SELECT `+ballsFaced("d.")+`, COALESCE(SUM(d.runs_batter),0),
			       COALESCE(SUM(CASE WHEN d.player_out=? THEN 1 ELSE 0 END),0)
			FROM deliveries d JOIN matches m ON m.id=d.match_id
			WHERE d.batter=? AND m.overs=? AND d.over BETWEEN ? AND ?`,
			id, id, totalOvers, p.from, p.to).Scan(&b.Balls, &b.Runs, &b.Outs)
		if err == nil && b.Balls > 0 {
			batting = append(batting, b)
		}
		var w PhaseSplit
		w.Phase = p.name
		err = db.QueryRowContext(aqctx(), `
			SELECT `+legalBalls("d.")+`, COALESCE(SUM(d.runs_batter+d.runs_extras),0),
			       COALESCE(SUM(CASE WHEN d.wicket_kind NOT IN ('','run out') THEN 1 ELSE 0 END),0)
			FROM deliveries d JOIN matches m ON m.id=d.match_id
			WHERE d.bowler=? AND m.overs=? AND d.over BETWEEN ? AND ?`,
			id, totalOvers, p.from, p.to).Scan(&w.Balls, &w.Runs, &w.Wickets)
		if err == nil && w.Balls > 0 {
			bowling = append(bowling, w)
		}
	}
	return batting, bowling, len(batting) > 0 || len(bowling) > 0
}

// VenueReport describes how a ground plays.
type VenueReport struct {
	Venue        string
	Matches      int
	AvgFirst     float64
	ChaseWins    int
	Decided      int
	HighestFirst int
}

// VenueStats reports scoring and chase success at a ground.
func VenueStats(venue string, totalOvers int) (VenueReport, bool) {
	if !Enabled() {
		return VenueReport{}, false
	}
	var full string
	if err := db.QueryRowContext(aqctx(),
		`SELECT venue FROM matches WHERE LOWER(venue) LIKE LOWER(?) AND overs=? GROUP BY venue ORDER BY COUNT(*) DESC LIMIT 1`,
		"%"+venue+"%", totalOvers).Scan(&full); err != nil {
		return VenueReport{}, false
	}
	r := VenueReport{Venue: full}
	rows, err := db.QueryContext(aqctx(), `
		SELECT m.id, COALESCE(w.name,''), t1.name, t2.name,
		       (SELECT COALESCE(SUM(runs_batter+runs_extras),0) FROM deliveries d WHERE d.match_id=m.id AND d.innings=1)
		FROM matches m
		JOIN names t1 ON t1.id=m.team1 JOIN names t2 ON t2.id=m.team2
		LEFT JOIN names w ON w.id=m.winner
		WHERE m.venue=? AND m.overs=?`, full, totalOvers)
	if err != nil {
		return VenueReport{}, false
	}
	defer rows.Close()
	var total int
	for rows.Next() {
		var id, winner, t1, t2 string
		var first int
		if err := rows.Scan(&id, &winner, &t1, &t2, &first); err != nil || first == 0 {
			continue
		}
		r.Matches++
		total += first
		if first > r.HighestFirst {
			r.HighestFirst = first
		}
		if winner == "" {
			continue
		}
		r.Decided++
		// The side batting second is whichever team did not bat first;
		// innings 1 batting side is inferred from the delivery table.
		var batFirst sql.NullString
		_ = db.QueryRowContext(aqctx(), `
			SELECT n.name FROM deliveries d
			JOIN names n ON n.id=d.batter
			WHERE d.match_id=? AND d.innings=1 LIMIT 1`, id).Scan(&batFirst)
		_ = batFirst // batting-side name is a player; use winner vs first-innings runs instead
		var secondRuns int
		_ = db.QueryRowContext(aqctx(),
			`SELECT COALESCE(SUM(runs_batter+runs_extras),0) FROM deliveries WHERE match_id=? AND innings=2`, id).Scan(&secondRuns)
		if secondRuns > first {
			r.ChaseWins++
		}
	}
	if r.Matches > 0 {
		r.AvgFirst = float64(total) / float64(r.Matches)
	}
	return r, r.Matches > 0
}

// Leader is one row of a season/league leaderboard.
type Leader struct {
	Name  string
	Runs  int
	Balls int
	Wkts  int
	Econ  float64
}

// Leaders ranks batters by runs or bowlers by wickets for a league and
// optional season year, of one gender when it is set (formatGender). ""
// reads both.
//
// histgen.py files a game it knows no league for under its match type, so
// "t20" holds every country's men's and women's T20Is together, and a
// women's innings ranks among the men's runs. A league's code is only as
// single-gender as its event names: the Women's Big Bash League is filed
// under "bbl", and staging answered "who has the most runs in the big
// bash" with BL Mooney and EA Perry on 2026-10-07.
//
// "t20" holds every domestic T20 histgen.py knows no league for as well:
// Cricsheet types the Vitality Blast and the Syed Mushtaq Ali Trophy
// "T20", as it types a T20I, and staging's form for Surrey on 2026-10-06
// was eight Vitality Blast Men games. "t20i" is the T20Is alone: the
// games of "t20" between two international sides (bothInternational).
// Prod and staging, 2026-10-06, "who has the most wickets in T20
// internationals in 2025" had no list to read, and "t20" would have
// given the counties' and the states' bowlers as the T20I leaders. "odi"
// and "test" need no such filter: Cricsheet types a domestic one-day game
// "ODM" and a first-class one "MDM".
func Leaders(league, year, kind, gender string, limit int) ([]Leader, bool) {
	if !Enabled() || limit <= 0 {
		return nil, false
	}
	where := "m.league = ?"
	args := []any{strings.ToLower(league)}
	if strings.EqualFold(league, "t20i") {
		cond, cargs := bothInternational("m.")
		where = "m.league = 't20'" + cond
		args = cargs
	}
	if year != "" {
		where += " AND m.date LIKE ?"
		args = append(args, year+"%")
	}
	filter, fargs := formatGender("m.", "", gender)
	where += filter
	args = append(args, fargs...)
	var q string
	if kind == "bowling" {
		q = `SELECT n.name,
		            SUM(CASE WHEN d.wicket_kind NOT IN ('','run out') THEN 1 ELSE 0 END) w,
		            ` + legalBalls("d.") + ` balls, SUM(d.runs_batter+d.runs_extras) conceded
		     FROM deliveries d JOIN matches m ON m.id=d.match_id
		     JOIN names n ON n.id=d.bowler
		     WHERE ` + where + ` GROUP BY n.name ORDER BY w DESC LIMIT ?`
	} else {
		q = `SELECT n.name, SUM(d.runs_batter) r, ` + ballsFaced("d.") + ` balls
		     FROM deliveries d JOIN matches m ON m.id=d.match_id
		     JOIN names n ON n.id=d.batter
		     WHERE ` + where + ` GROUP BY n.name ORDER BY r DESC LIMIT ?`
	}
	args = append(args, limit)
	rows, err := db.QueryContext(aqctx(), q, args...)
	if err != nil {
		return nil, false
	}
	defer rows.Close()
	var out []Leader
	for rows.Next() {
		var l Leader
		if kind == "bowling" {
			var conceded int
			if err := rows.Scan(&l.Name, &l.Wkts, &l.Balls, &conceded); err != nil {
				continue
			}
			if l.Balls > 0 {
				l.Econ = float64(conceded) * 6 / float64(l.Balls)
			}
		} else if err := rows.Scan(&l.Name, &l.Runs, &l.Balls); err != nil {
			continue
		}
		out = append(out, l)
	}
	return out, len(out) > 0
}

// internationalSides are the sides Cricsheet's international games are
// played by, as it names them: the country column of
// internal/rag/data/playerstats.json (cmd/statsgen, Cricsheet through
// 2026-09-17), which is the side each player has played the most
// team_type "international" games for, composite World XIs left out.
// histgen.py does not store the team type, so a game is an international
// when both its sides are on this list. Barbados is on it for its women's
// Commonwealth Games T20Is, and its domestic opponents, Jamaica or
// Guyana, are not. Cricsheet publishes no Afghanistan men's matches
// (cmd/statsgen), and Afghanistan is on the list so that its games count
// the day they are added.
var internationalSides = []string{
	"Afghanistan", "Argentina", "Australia", "Austria", "Bahamas", "Bahrain",
	"Bangladesh", "Barbados", "Belgium", "Belize", "Bermuda", "Bhutan",
	"Botswana", "Brazil", "Bulgaria", "Cambodia", "Cameroon", "Canada",
	"Cayman Islands", "Chile", "China", "Cook Islands", "Costa Rica", "Croatia",
	"Cyprus", "Czech Republic", "Denmark", "England", "Estonia", "Eswatini",
	"Fiji", "Finland", "France", "Gambia", "Germany", "Ghana",
	"Gibraltar", "Greece", "Guernsey", "Hong Kong", "Hungary", "India",
	"Indonesia", "Ireland", "Isle of Man", "Israel", "Italy", "Ivory Coast",
	"Japan", "Jersey", "Kenya", "Kuwait", "Lesotho", "Luxembourg",
	"Malawi", "Malaysia", "Maldives", "Mali", "Malta", "Mexico",
	"Mongolia", "Mozambique", "Myanmar", "Namibia", "Nepal", "Netherlands",
	"New Zealand", "Nigeria", "Norway", "Oman", "Pakistan", "Panama",
	"Papua New Guinea", "Philippines", "Portugal", "Qatar", "Romania", "Rwanda",
	"Samoa", "Saudi Arabia", "Scotland", "Serbia", "Seychelles", "Sierra Leone",
	"Singapore", "Slovenia", "South Africa", "South Korea", "Spain", "Sri Lanka",
	"St Helena", "Suriname", "Swaziland", "Sweden", "Switzerland", "Tanzania",
	"Thailand", "Timor-Leste", "Turkey", "Turks and Caicos Island", "Uganda", "United Arab Emirates",
	"United States of America", "Uzbekistan", "Vanuatu", "West Indies", "Zambia", "Zimbabwe",
}

// bothInternational is the SQL condition, " AND ..." on the matches alias
// m ("m."), that keeps a game between two internationalSides, and its
// arguments. A game with one innings has no team2, since histgen.py reads
// the sides off the innings, and its innings counts.
func bothInternational(m string) (string, []any) {
	marks := strings.TrimSuffix(strings.Repeat("?,", len(internationalSides)), ",")
	sides := "(SELECT id FROM names WHERE name IN (" + marks + "))"
	args := make([]any, 0, 2*len(internationalSides))
	for i := 0; i < 2; i++ {
		for _, s := range internationalSides {
			args = append(args, s)
		}
	}
	return " AND " + m + "team1 IN " + sides + " AND (" + m + "team2 IS NULL OR " + m + "team2 IN " + sides + ")", args
}

// FormLine is one recent result for a team.
type FormLine struct {
	Date, Opponent, Result, Event string
}

// NormFormat is "t20", "odi" or "test" for the ways a caller names a
// format, and "" for any other.
func NormFormat(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "t20", "t20s", "t20i", "t20is", "twenty20":
		return "t20"
	case "odi", "odis", "one-day", "one day", "list a", "list-a":
		return "odi"
	case "test", "tests", "first-class", "first class", "multi-day":
		return "test"
	}
	return ""
}

// NormGender is "male" or "female", as Cricsheet writes them, for the
// ways a caller names one, and "" for any other.
func NormGender(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "male", "men", "men's", "mens", "man":
		return "male"
	case "female", "women", "women's", "womens", "woman":
		return "female"
	}
	return ""
}

// ScopeLabel names the games a format and a gender keep, n of them:
// "men's T20s", "women's ODI", "Tests". "" when neither is set.
func ScopeLabel(format, gender string, n int) string {
	what := map[string]string{"t20": "T20", "odi": "ODI", "test": "Test"}[NormFormat(format)]
	who := map[string]string{"male": "men's", "female": "women's"}[NormGender(gender)]
	switch {
	case what == "" && who == "":
		return ""
	case what == "":
		what = "game"
	}
	if n != 1 {
		what += "s"
	}
	if who == "" {
		return what
	}
	return who + " " + what
}

// formatGender is the SQL condition, " AND ..." on the matches alias m
// ("m." or "m2."), that keeps one format and one gender, and its
// arguments. "" for either keeps every one.
//
// Cricsheet names India's men and India's women "India", and a side's
// last ten games were every format as well, so the in-form list for a
// men's T20I could be topped by a Test 150 or a women's 80. Format is the
// overs histgen.py writes: 20 for a T20, 50 for a one-day game, 0 for a
// multi-day one. Gender is the matches column histgen.py writes since
// 2026-10-07. A file built before it has no such column, and the event
// name stands in there. That is imperfect: a women's game whose event
// does not say "Women" (a blank event, a series named for the venue)
// reads as a men's.
func formatGender(m, format, gender string) (string, []any) {
	var conds []string
	var args []any
	switch NormFormat(format) {
	case "t20":
		conds = append(conds, m+"overs = 20")
	case "odi":
		conds = append(conds, m+"overs = 50")
	case "test":
		conds = append(conds, m+"overs = 0")
	}
	switch g := NormGender(gender); {
	case g == "":
	case hasGender:
		conds = append(conds, m+"gender = ?")
		args = append(args, g)
	case g == "female":
		conds = append(conds, "LOWER(COALESCE("+m+"event,'')) LIKE '%women%'")
	default:
		conds = append(conds, "LOWER(COALESCE("+m+"event,'')) NOT LIKE '%women%'")
	}
	if len(conds) == 0 {
		return "", nil
	}
	return " AND " + strings.Join(conds, " AND "), args
}

// TeamForm returns a team's most recent archived results, of one format
// and one gender when they are set (formatGender). With neither set it
// reads every game, as it always has.
func TeamForm(team, format, gender string, limit int) ([]FormLine, bool) {
	if !Enabled() || limit <= 0 {
		return nil, false
	}
	filter, fargs := formatGender("m.", format, gender)
	args := append([]any{team, team}, fargs...)
	args = append(args, limit)
	rows, err := db.QueryContext(aqctx(), `
		SELECT m.date, t1.name, t2.name, COALESCE(w.name,''), COALESCE(m.event,'')
		FROM matches m
		JOIN names t1 ON t1.id=m.team1 JOIN names t2 ON t2.id=m.team2
		LEFT JOIN names w ON w.id=m.winner
		WHERE (LOWER(t1.name)=LOWER(?) OR LOWER(t2.name)=LOWER(?))`+filter+`
		ORDER BY m.date DESC LIMIT ?`, args...)
	if err != nil {
		return nil, false
	}
	defer rows.Close()
	var out []FormLine
	for rows.Next() {
		var date, t1, t2, winner, event string
		if err := rows.Scan(&date, &t1, &t2, &winner, &event); err != nil {
			continue
		}
		opp := t2
		if !strings.EqualFold(t1, team) {
			opp = t1
		}
		res := "no result"
		switch {
		case strings.EqualFold(winner, team):
			res = "won"
		case winner != "":
			res = "lost"
		}
		out = append(out, FormLine{Date: date, Opponent: opp, Result: res, Event: event})
	}
	return out, len(out) > 0
}

// KeyPlayer is one of a side's leading batters or bowlers over its recent
// archived games: runs, balls faced and strike rate for a batter; wickets,
// legal balls and economy for a bowler. Innings is how many he batted or
// bowled in.
type KeyPlayer struct {
	Name    string
	Innings int
	Runs    int
	Balls   int
	SR      float64
	Wkts    int
	Econ    float64
}

// TeamKeyPlayers is a side's top five run scorers and top five wicket
// takers over its last `matches` archived games of one format and one
// gender, the games TeamForm reads with the same three, by Cricsheet
// name. The side batting an innings is team1 in the odd innings and team2
// in the even ones, as histgen.py writes them.
//
// Prod, 2026-10-05, USA v UAE selected, "can you create dream11 teams":
// "that's not something this service does", then "I don't pull live
// match lineups or team sheets" for "but you can fetch lineup and stats
// right". The reader wanted each side's in-form names, and the archive
// held them. Read over every game a side played, India's in-form list for
// a men's T20I could be topped by a Test 150 or a women's 80: Cricsheet
// calls both of India's sides "India".
func TeamKeyPlayers(team, format, gender string, matches int) (batters, bowlers []KeyPlayer, ok bool) {
	if !Enabled() || matches <= 0 {
		return nil, nil, false
	}
	filter, fargs := formatGender("m2.", format, gender)
	recent := `d.match_id IN (SELECT m2.id FROM matches m2
	        JOIN names t1 ON t1.id=m2.team1 JOIN names t2 ON t2.id=m2.team2
	        WHERE (LOWER(t1.name)=LOWER(?) OR LOWER(t2.name)=LOWER(?))` + filter + `
	        ORDER BY m2.date DESC LIMIT ?)`
	const side = `(SELECT id FROM names WHERE LOWER(name)=LOWER(?))`
	args := append([]any{team, team}, fargs...)
	args = append(args, matches, team)
	rows, err := db.QueryContext(aqctx(), `
		SELECT n.name, SUM(d.runs_batter) r, `+ballsFaced("d.")+` balls,
		       COUNT(DISTINCT d.match_id || '/' || d.innings)
		FROM deliveries d JOIN matches m ON m.id=d.match_id
		JOIN names n ON n.id=d.batter
		WHERE `+recent+`
		  AND (CASE WHEN d.innings % 2 = 1 THEN m.team1 ELSE m.team2 END) = `+side+`
		GROUP BY n.name ORDER BY r DESC, balls ASC LIMIT 5`, args...)
	if err != nil {
		return nil, nil, false
	}
	for rows.Next() {
		var k KeyPlayer
		if err := rows.Scan(&k.Name, &k.Runs, &k.Balls, &k.Innings); err != nil {
			continue
		}
		if k.Balls > 0 {
			k.SR = float64(k.Runs) * 100 / float64(k.Balls)
		}
		batters = append(batters, k)
	}
	rows.Close()
	rows, err = db.QueryContext(aqctx(), `
		SELECT n.name, SUM(CASE WHEN d.wicket_kind NOT IN ('','run out') THEN 1 ELSE 0 END) w,
		       `+legalBalls("d.")+` balls, SUM(d.runs_batter+d.runs_extras) conceded,
		       COUNT(DISTINCT d.match_id || '/' || d.innings)
		FROM deliveries d JOIN matches m ON m.id=d.match_id
		JOIN names n ON n.id=d.bowler
		WHERE `+recent+`
		  AND (CASE WHEN d.innings % 2 = 1 THEN m.team2 ELSE m.team1 END) = `+side+`
		GROUP BY n.name ORDER BY w DESC, conceded ASC LIMIT 5`, args...)
	if err != nil {
		return batters, nil, len(batters) > 0
	}
	defer rows.Close()
	for rows.Next() {
		var k KeyPlayer
		var conceded int
		if err := rows.Scan(&k.Name, &k.Wkts, &k.Balls, &conceded, &k.Innings); err != nil {
			continue
		}
		if k.Balls > 0 {
			k.Econ = float64(conceded) * 6 / float64(k.Balls)
		}
		bowlers = append(bowlers, k)
	}
	return batters, bowlers, len(batters) > 0 || len(bowlers) > 0
}

// describeSplits renders phase splits as readable lines.
func describeSplits(title string, splits []PhaseSplit, bowling bool) string {
	if len(splits) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(title + "\n")
	for _, s := range splits {
		if bowling {
			econ := 0.0
			if s.Balls > 0 {
				econ = float64(s.Runs) * 6 / float64(s.Balls)
			}
			fmt.Fprintf(&b, "  %s: %d wickets, economy %.2f (%d balls, %d runs)\n", s.Phase, s.Wickets, econ, s.Balls, s.Runs)
			continue
		}
		sr, avg := 0.0, 0.0
		if s.Balls > 0 {
			sr = float64(s.Runs) * 100 / float64(s.Balls)
		}
		if s.Outs > 0 {
			avg = float64(s.Runs) / float64(s.Outs)
		}
		fmt.Fprintf(&b, "  %s: %d runs off %d balls, strike rate %.1f, out %d times (average %.1f)\n",
			s.Phase, s.Runs, s.Balls, sr, s.Outs, avg)
	}
	return b.String()
}

// PhaseReport renders both sides of a player's phase profile.
func PhaseReport(name string, totalOvers int) (string, bool) {
	bat, bowl, ok := PhaseStats(name, totalOvers)
	if !ok {
		return "", false
	}
	format := "T20"
	if totalOvers >= 50 {
		format = "ODI/List-A"
	}
	out := fmt.Sprintf("%s — %s phase profile (all archived %s cricket)\n", name, format, format)
	out += describeSplits("Batting:", bat, false)
	out += describeSplits("Bowling:", bowl, true)
	return out, true
}

// nameID resolves a player name to its id, falling back to a suffix match
// so "Kohli" finds "V Kohli". PhaseStats grew this inline; the tools below
// all need it, so it lives here rather than four more times.
func nameID(name string) (int, bool) {
	var id int
	if err := db.QueryRowContext(aqctx(),
		`SELECT id FROM names WHERE LOWER(name)=LOWER(?)`, name).Scan(&id); err == nil {
		return id, true
	}
	if err := db.QueryRowContext(aqctx(),
		`SELECT id FROM names WHERE LOWER(name) LIKE LOWER(?) ORDER BY LENGTH(name) LIMIT 1`,
		"%"+name).Scan(&id); err != nil {
		return 0, false
	}
	return id, true
}

// Dismissal is one way a batter has been out, or one way a bowler gets
// batters out, with how often.
type Dismissal struct {
	Kind  string
	Count int
}

// Dismissals breaks down how a player gets out, or how a bowler takes
// wickets. Run outs are excluded from the bowling side because they are
// not credited to the bowler, which is the same rule Leaders uses.
func Dismissals(name, perspective string) ([]Dismissal, int, bool) {
	if !Enabled() {
		return nil, 0, false
	}
	id, ok := nameID(name)
	if !ok {
		return nil, 0, false
	}
	// deliveries is indexed on batter and bowler but NOT on player_out, so
	// filtering on player_out alone scans all 11.4M rows and exceeds the
	// query deadline. Anchoring on the indexed column first turns it into an
	// index seek. The cost is non-striker run outs, which are recorded
	// against a batter who was not on strike: rare, and named in the output
	// rather than quietly dropped.
	q := `SELECT d.wicket_kind, COUNT(*) c FROM deliveries d
	      WHERE d.batter=? AND d.player_out=? AND d.wicket_kind <> ''
	      GROUP BY d.wicket_kind ORDER BY c DESC`
	args := []any{id, id}
	if perspective == "bowling" {
		q = `SELECT d.wicket_kind, COUNT(*) c FROM deliveries d
		     WHERE d.bowler=? AND d.wicket_kind NOT IN ('','run out')
		     GROUP BY d.wicket_kind ORDER BY c DESC`
		args = []any{id}
	}
	rows, err := db.QueryContext(aqctx(), q, args...)
	if err != nil {
		return nil, 0, false
	}
	defer rows.Close()
	var out []Dismissal
	total := 0
	for rows.Next() {
		var d Dismissal
		if err := rows.Scan(&d.Kind, &d.Count); err != nil {
			continue
		}
		total += d.Count
		out = append(out, d)
	}
	return out, total, len(out) > 0
}

// Discipline is the unglamorous side of limited-overs cricket: the dot
// balls and the boundaries conceded that decide close games long before
// the wickets column does.
type Discipline struct {
	Balls, Dots, Fours, Sixes, Extras, Runs int
	DotPct, BoundaryPct, Econ               float64
}

// DisciplineStats computes dot-ball and boundary rates for a bowler, or the
// same rates faced by a batter. Optionally restricted to one format by
// innings length.
func DisciplineStats(name, perspective string, totalOvers int) (Discipline, bool) {
	var d Discipline
	if !Enabled() {
		return d, false
	}
	id, ok := nameID(name)
	if !ok {
		return d, false
	}
	// A bowler's balls are his legal ones, the ones his economy is over;
	// a batter's are the balls he faced, no-balls among them.
	col, balls := "d.bowler", legalBalls("d.")
	if perspective == "batting" {
		col, balls = "d.batter", ballsFaced("d.")
	}
	where := col + "=?"
	args := []any{id}
	if totalOvers > 0 {
		where += " AND m.overs=?"
		args = append(args, totalOvers)
	}
	err := db.QueryRowContext(aqctx(), `
		SELECT `+balls+`,
		       SUM(CASE WHEN d.runs_batter+d.runs_extras=0 THEN 1 ELSE 0 END),
		       SUM(CASE WHEN d.runs_batter=4 THEN 1 ELSE 0 END),
		       SUM(CASE WHEN d.runs_batter=6 THEN 1 ELSE 0 END),
		       COALESCE(SUM(d.runs_extras),0),
		       COALESCE(SUM(d.runs_batter+d.runs_extras),0)
		FROM deliveries d JOIN matches m ON m.id=d.match_id
		WHERE `+where, args...).
		Scan(&d.Balls, &d.Dots, &d.Fours, &d.Sixes, &d.Extras, &d.Runs)
	if err != nil || d.Balls == 0 {
		return d, false
	}
	d.DotPct = float64(d.Dots) * 100 / float64(d.Balls)
	d.BoundaryPct = float64(d.Fours+d.Sixes) * 100 / float64(d.Balls)
	d.Econ = float64(d.Runs) * 6 / float64(d.Balls)
	return d, true
}

// Situation is a player's record in one match situation.
type Situation struct {
	Label       string
	Balls, Runs int
	Outs        int
	Avg, SR     float64
}

// SituationalStats splits a batter's record by whether their side batted
// first or chased. The archive stores the innings number, and in
// limited-overs cricket innings 2 IS the chase, which is the whole split.
// Multi-day cricket is excluded rather than guessed at: there the fourth
// innings is the chase and the first three are not comparable.
func SituationalStats(name string, totalOvers int) ([]Situation, bool) {
	if !Enabled() {
		return nil, false
	}
	id, ok := nameID(name)
	if !ok {
		return nil, false
	}
	var out []Situation
	for _, c := range []struct {
		label  string
		inning int
	}{{"batting first", 1}, {"chasing", 2}} {
		var s Situation
		s.Label = c.label
		where := "d.batter=? AND d.innings=?"
		args := []any{id, c.inning}
		if totalOvers > 0 {
			where += " AND m.overs=?"
			args = append(args, totalOvers)
		}
		err := db.QueryRowContext(aqctx(), `
			SELECT `+ballsFaced("d.")+`, COALESCE(SUM(d.runs_batter),0),
			       COALESCE(SUM(CASE WHEN d.player_out=? THEN 1 ELSE 0 END),0)
			FROM deliveries d JOIN matches m ON m.id=d.match_id
			WHERE `+where, append([]any{id}, args...)...).
			Scan(&s.Balls, &s.Runs, &s.Outs)
		if err != nil || s.Balls == 0 {
			continue
		}
		s.SR = float64(s.Runs) * 100 / float64(s.Balls)
		if s.Outs > 0 {
			s.Avg = float64(s.Runs) / float64(s.Outs)
		}
		out = append(out, s)
	}
	return out, len(out) > 0
}

// Partnership is one pair's combined record at the crease.
type Partnership struct {
	A, B        string
	Runs, Balls int
	Innings     int
	Best        int
}

// Partnerships returns a batter's most productive partners: runs added while
// the two were at the crease together, not runs they happened to score in the
// same innings.
//
// The archive records the striker but not the non-striker, so a stand is
// reconstructed from the wickets around it. Deliveries in an innings are
// ordered by over and ball, a running count of wickets already fallen labels
// each ball with the stand it belongs to, and the pair is the distinct
// strikers inside that stand. That is exact whenever both batters face at
// least one ball, which is almost always; a partner who is run out without
// facing does not appear.
//
// Scoped to the innings this player batted in, via idx_del_batter. The
// obvious formulation instead sums a partner's runs across every shared
// innings, which for two team-mates who open together is simply each
// player's career total and has nothing to do with partnerships.
func Partnerships(name string, limit int) ([]Partnership, bool) {
	if !Enabled() || limit <= 0 {
		return nil, false
	}
	id, ok := nameID(name)
	if !ok {
		return nil, false
	}
	rows, err := db.QueryContext(aqctx(), `
		WITH mine AS (
		  SELECT DISTINCT match_id, innings FROM deliveries WHERE batter=?
		),
		seg AS (
		  SELECT d.match_id, d.innings, d.batter,
		         d.runs_batter + d.runs_extras AS runs,
		         `+faced("d.")+` AS faced,
		         SUM(CASE WHEN d.wicket_kind <> '' THEN 1 ELSE 0 END) OVER (
		           PARTITION BY d.match_id, d.innings
		           ORDER BY d.over, d.ball
		           ROWS BETWEEN UNBOUNDED PRECEDING AND 1 PRECEDING) AS stand
		  FROM deliveries d
		  JOIN mine ON mine.match_id = d.match_id AND mine.innings = d.innings
		),
		totals AS (
		  SELECT match_id, innings, stand,
		         SUM(runs) runs, SUM(faced) balls
		  FROM seg GROUP BY match_id, innings, stand
		),
		ours AS (
		  SELECT DISTINCT match_id, innings, stand FROM seg WHERE batter = ?
		),
		partner AS (
		  SELECT DISTINCT s.match_id, s.innings, s.stand, s.batter
		  FROM seg s JOIN ours o
		    ON o.match_id = s.match_id AND o.innings = s.innings AND o.stand = s.stand
		  WHERE s.batter <> ?
		)
		SELECT p.name, SUM(t.runs) runs, SUM(t.balls) balls,
		       COUNT(*) stands, MAX(t.runs) best
		FROM partner pa
		JOIN totals t ON t.match_id = pa.match_id AND t.innings = pa.innings
		             AND t.stand = pa.stand
		JOIN names p ON p.id = pa.batter
		GROUP BY p.name ORDER BY runs DESC LIMIT ?`, id, id, id, limit)
	if err != nil {
		return nil, false
	}
	defer rows.Close()
	var out []Partnership
	for rows.Next() {
		var p Partnership
		p.A = name
		if err := rows.Scan(&p.B, &p.Runs, &p.Balls, &p.Innings, &p.Best); err != nil {
			continue
		}
		out = append(out, p)
	}
	return out, len(out) > 0
}
