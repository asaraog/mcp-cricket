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
func phaseBounds(totalOvers int) []struct {
	name     string
	from, to int
} {
	if totalOvers >= 50 {
		return []struct {
			name     string
			from, to int
		}{{"powerplay (1-10)", 0, 9}, {"middle (11-40)", 10, 39}, {"death (41-50)", 40, 49}}
	}
	return []struct {
		name     string
		from, to int
	}{{"powerplay (1-6)", 0, 5}, {"middle (7-15)", 6, 14}, {"death (16-20)", 15, 19}}
}

// PhaseStats splits a player's batting and bowling by innings phase.
func PhaseStats(name string, totalOvers int) (batting, bowling []PhaseSplit, ok bool) {
	if !Enabled() {
		return nil, nil, false
	}
	var id int
	if err := db.QueryRowContext(qctx(), `SELECT id FROM names WHERE LOWER(name)=LOWER(?)`, name).Scan(&id); err != nil {
		// fall back to a suffix match ("Kohli" -> "V Kohli")
		if err := db.QueryRowContext(qctx(), `SELECT id FROM names WHERE LOWER(name) LIKE LOWER(?) ORDER BY LENGTH(name) LIMIT 1`, "%"+name).Scan(&id); err != nil {
			return nil, nil, false
		}
	}
	for _, p := range phaseBounds(totalOvers) {
		var b PhaseSplit
		b.Phase = p.name
		err := db.QueryRowContext(qctx(), `
			SELECT COUNT(*), COALESCE(SUM(d.runs_batter),0),
			       COALESCE(SUM(CASE WHEN d.player_out=? THEN 1 ELSE 0 END),0)
			FROM deliveries d JOIN matches m ON m.id=d.match_id
			WHERE d.batter=? AND m.overs=? AND d.over BETWEEN ? AND ?`,
			id, id, totalOvers, p.from, p.to).Scan(&b.Balls, &b.Runs, &b.Outs)
		if err == nil && b.Balls > 0 {
			batting = append(batting, b)
		}
		var w PhaseSplit
		w.Phase = p.name
		err = db.QueryRowContext(qctx(), `
			SELECT COUNT(*), COALESCE(SUM(d.runs_batter+d.runs_extras),0),
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
	Venue       string
	Matches     int
	AvgFirst    float64
	ChaseWins   int
	Decided     int
	HighestFirst int
}

// VenueStats reports scoring and chase success at a ground.
func VenueStats(venue string, totalOvers int) (VenueReport, bool) {
	if !Enabled() {
		return VenueReport{}, false
	}
	var full string
	if err := db.QueryRowContext(qctx(),
		`SELECT venue FROM matches WHERE LOWER(venue) LIKE LOWER(?) AND overs=? GROUP BY venue ORDER BY COUNT(*) DESC LIMIT 1`,
		"%"+venue+"%", totalOvers).Scan(&full); err != nil {
		return VenueReport{}, false
	}
	r := VenueReport{Venue: full}
	rows, err := db.QueryContext(qctx(), `
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
		_ = db.QueryRowContext(qctx(), `
			SELECT n.name FROM deliveries d
			JOIN names n ON n.id=d.batter
			WHERE d.match_id=? AND d.innings=1 LIMIT 1`, id).Scan(&batFirst)
		_ = batFirst // batting-side name is a player; use winner vs first-innings runs instead
		var secondRuns int
		_ = db.QueryRowContext(qctx(),
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
// optional season year.
func Leaders(league, year, kind string, limit int) ([]Leader, bool) {
	if !Enabled() || limit <= 0 {
		return nil, false
	}
	where := "m.league = ?"
	args := []any{strings.ToLower(league)}
	if year != "" {
		where += " AND m.date LIKE ?"
		args = append(args, year+"%")
	}
	var q string
	if kind == "bowling" {
		q = `SELECT n.name,
		            SUM(CASE WHEN d.wicket_kind NOT IN ('','run out') THEN 1 ELSE 0 END) w,
		            COUNT(*) balls, SUM(d.runs_batter+d.runs_extras) conceded
		     FROM deliveries d JOIN matches m ON m.id=d.match_id
		     JOIN names n ON n.id=d.bowler
		     WHERE ` + where + ` GROUP BY n.name ORDER BY w DESC LIMIT ?`
	} else {
		q = `SELECT n.name, SUM(d.runs_batter) r, COUNT(*) balls
		     FROM deliveries d JOIN matches m ON m.id=d.match_id
		     JOIN names n ON n.id=d.batter
		     WHERE ` + where + ` GROUP BY n.name ORDER BY r DESC LIMIT ?`
	}
	args = append(args, limit)
	rows, err := db.QueryContext(qctx(), q, args...)
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

// FormLine is one recent result for a team.
type FormLine struct {
	Date, Opponent, Result, Event string
}

// TeamForm returns a team's most recent archived results.
func TeamForm(team string, limit int) ([]FormLine, bool) {
	if !Enabled() || limit <= 0 {
		return nil, false
	}
	rows, err := db.QueryContext(qctx(), `
		SELECT m.date, t1.name, t2.name, COALESCE(w.name,''), COALESCE(m.event,'')
		FROM matches m
		JOIN names t1 ON t1.id=m.team1 JOIN names t2 ON t2.id=m.team2
		LEFT JOIN names w ON w.id=m.winner
		WHERE LOWER(t1.name)=LOWER(?) OR LOWER(t2.name)=LOWER(?)
		ORDER BY m.date DESC LIMIT ?`, team, team, limit)
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
