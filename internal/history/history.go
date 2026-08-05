// Package history answers questions about past matches from a per-delivery
// SQLite database built out of Cricsheet archives (scripts/histgen.py).
// The DB ships in the Docker image, not the binary; when the file is
// absent (local dev without it) the feature quietly disables and the LLM
// falls back to its famous-facts-only behavior.
package history

import (
	"compress/gzip"
	"context"
	"database/sql"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

var (
	once      sync.Once
	db        *sql.DB
	teams     []string          // distinct team names, for entity spotting
	teamWords map[string]string // distinctive single word -> unique team
)

// The DB is a private GitHub release asset; a fresh instance without the
// file (Render's disk is wiped on every deploy/spin-down) downloads it
// once at startup using the same token the log mirror uses.
// defaultAssetURL is the prebuilt archive published with this project's
// releases. Nothing to build and no account to create: on first run the
// server downloads it once (~200 MB compressed) and reuses it forever
// after. Override with HISTORY_DB_URL, or skip the download entirely by
// pointing HISTORY_DB at an archive you generated yourself.
const defaultAssetURL = "https://github.com/asaraog/mcp-cricket/releases/latest/download/cricket-archive.db.gz"

// defaultArchivePath returns ~/.cache/cricket-mcp/history.db (or the OS
// equivalent), creating the directory when needed.
func defaultArchivePath() string {
	dir, err := os.UserCacheDir()
	if err != nil {
		return "history.db"
	}
	dir = filepath.Join(dir, "cricket-mcp")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "history.db"
	}
	return filepath.Join(dir, "history.db")
}

func fetchDB(dst string) bool {
	url := os.Getenv("HISTORY_DB_URL")
	if url == "" {
		url = defaultAssetURL
	}
	if url == "" {
		return false // nothing to fetch; the user supplies HISTORY_DB
	}
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return false
	}
	req.Header.Set("Accept", "application/octet-stream")
	// Private archives can require auth; set HISTORY_DB_TOKEN for those.
	if tok := os.Getenv("HISTORY_DB_TOKEN"); tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	log.Printf("history: first run — downloading the cricket archive (~200 MB, once) from %s", url)
	client := &http.Client{Timeout: 30 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		log.Printf("history: asset download %s", resp.Status)
		return false
	}
	gz, err := gzip.NewReader(resp.Body)
	if err != nil {
		return false
	}
	tmp := dst + ".part"
	f, err := os.Create(tmp)
	if err != nil {
		return false
	}
	if _, err := io.Copy(f, gz); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return false
	}
	_ = f.Close()
	if err := os.Rename(tmp, dst); err != nil {
		return false
	}
	log.Printf("history: db downloaded to %s", dst)
	return true
}

func open() {
	path := os.Getenv("HISTORY_DB")
	if path == "" {
		// Default to a durable per-user location so the one-time
		// download survives reboots — a temp directory would make the
		// user pay for it again after every cleanup.
		path = defaultArchivePath()
	}
	if _, err := os.Stat(path); err != nil {
		// Prefer fetching into the configured path (a mounted disk keeps
		// it); fall back to temp when that location is not writable.
		if fetchDB(path) {
			// downloaded to the intended location
		} else {
			alt := filepath.Join(os.TempDir(), "history.db")
			if _, err2 := os.Stat(alt); err2 != nil && !fetchDB(alt) {
				return
			}
			path = alt
		}
	}
	d, err := sql.Open("sqlite", path+"?mode=ro")
	if err != nil {
		return
	}
	// Bounded pool: each connection carries its own page cache, so
	// unbounded readers would be a memory hazard on a small instance.
	// Archive queries are millisecond-scale, so a small pool is plenty.
	d.SetMaxOpenConns(8)
	d.SetMaxIdleConns(4)
	d.SetConnMaxIdleTime(5 * time.Minute)
	rows, err := d.Query(`SELECT DISTINCT n.name FROM matches m JOIN names n ON n.id IN (m.team1, m.team2)`)
	if err != nil {
		_ = d.Close()
		return
	}
	defer rows.Close()
	for rows.Next() {
		var t string
		if rows.Scan(&t) == nil && t != "" {
			teams = append(teams, t)
		}
	}
	// Fans say "the Unicorns", not "San Francisco Unicorns": index each
	// distinctive word (5+ letters) that maps to exactly ONE team.
	counts := map[string][]string{}
	for _, t := range teams {
		for _, w := range strings.Fields(strings.ToLower(t)) {
			if len(w) >= 5 {
				counts[w] = append(counts[w], t)
			}
		}
	}
	// Words that appear constantly in NON-team phrases ("World Cup",
	// "cricket board") must never nominate a team by shorthand.
	generic := map[string]bool{"world": true, "cricket": true, "national": true, "united": true}
	teamWords = map[string]string{}
	for w, ts := range counts {
		if len(ts) == 1 && !generic[w] {
			teamWords[w] = ts[0]
		}
	}
	db = d
}

// Enabled reports whether the history DB is present.
func Enabled() bool {
	once.Do(open)
	return db != nil
}

// TeamsIn returns team names mentioned in free text, longest first so
// "New York" style overlaps resolve to the fuller name.
// qctx bounds every history query: a slow plan degrades to "no archive
// answer" (the LLM answers from knowledge) instead of hanging a handler.
// queryDeadline bounds every archive query. The web path wants a tight
// budget (a slow plan must never hang a chat turn), but analytics tools
// legitimately aggregate millions of deliveries — HISTORY_QUERY_TIMEOUT
// lets the MCP server raise it without loosening the site.
func queryDeadline() time.Duration {
	if v := os.Getenv("HISTORY_QUERY_TIMEOUT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			return d
		}
	}
	return 3 * time.Second
}

func qctx() context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), queryDeadline())
	_ = cancel // query completion releases resources; 3s cap is the point
	return ctx
}

func TeamsIn(msg string) []string {
	if !Enabled() {
		return nil
	}
	low := strings.ToLower(msg)
	var hits []string
	for _, t := range teams {
		if strings.Contains(low, strings.ToLower(t)) {
			hits = append(hits, t)
		}
	}
	for i := range hits {
		for j := i + 1; j < len(hits); j++ {
			if len(hits[j]) > len(hits[i]) {
				hits[i], hits[j] = hits[j], hits[i]
			}
		}
	}
	// drop names contained in an already-kept longer name
	kept := hits[:0]
	for _, h := range hits {
		sub := false
		for _, k := range kept {
			if strings.Contains(strings.ToLower(k), strings.ToLower(h)) {
				sub = true
				break
			}
		}
		if !sub {
			kept = append(kept, h)
		}
	}
	// Shorthand pass: "the Unicorns beat the Freedom" — words that map to
	// exactly one team count as that team.
	for _, w := range strings.Fields(strings.ToLower(strings.NewReplacer("?", " ", ",", " ", ".", " ", "'s", "").Replace(msg))) {
		if t, ok := teamWords[w]; ok {
			dup := false
			for _, k := range kept {
				if k == t {
					dup = true
					break
				}
			}
			if !dup {
				kept = append(kept, t)
			}
		}
	}
	return kept
}

// Match is one archived game.
type Match struct {
	ID, Date, League, Event, Venue string
	Overs                          int
	Team1, Team2, Result           string
}

var yearRe = regexp.MustCompile(`\b(20[0-2]\d)\b`)

// FindMatch locates the best archived game for the teams (1 or 2 names)
// and an optional year pulled from the question; latest match wins ties.
var leagueWords = map[string]string{
	"mlc": "mlc", "major league cricket": "mlc",
	"ipl": "ipl", "indian premier league": "ipl",
	"bbl": "bbl", "big bash": "bbl",
	"psl": "psl", "pakistan super league": "psl",
	"cpl": "cpl", "caribbean premier league": "cpl",
}

// leagueIn spots a league mention ("the 2025 IPL final") in free text.
func leagueIn(msg string) string {
	low := strings.ToLower(msg)
	for k, v := range leagueWords {
		if regexp.MustCompile(`\b` + k + `\b`).MatchString(low) {
			return v
		}
	}
	return ""
}

func FindMatch(msg string, names []string) (Match, bool) {
	if !Enabled() {
		return Match{}, false
	}
	if len(names) == 0 {
		// No team named: "the 2025 IPL final" still resolves by league —
		// latest game of that league (and year), which for a season file
		// is the final.
		lg := leagueIn(msg)
		if lg == "" {
			return Match{}, false
		}
		year := yearRe.FindString(msg)
		q := `SELECT m.id, m.date, m.league, m.event, m.venue, m.overs,
		             t1.name, t2.name, COALESCE(w.name, m.result)
		      FROM matches m
		      JOIN names t1 ON t1.id = m.team1
		      JOIN names t2 ON t2.id = m.team2
		      LEFT JOIN names w ON w.id = m.winner
		      WHERE m.league = ?`
		args := []any{lg}
		if year != "" {
			q += ` AND m.date LIKE ?`
			args = append(args, year+"%")
		}
		q += ` ORDER BY m.date DESC LIMIT 1`
		var m Match
		err := db.QueryRowContext(qctx(), q, args...).Scan(&m.ID, &m.Date, &m.League, &m.Event,
			&m.Venue, &m.Overs, &m.Team1, &m.Team2, &m.Result)
		return m, err == nil
	}
	year := ""
	if y := yearRe.FindString(msg); y != "" {
		year = y
	}
	q := `SELECT m.id, m.date, m.league, m.event, m.venue, m.overs,
	             t1.name, t2.name,
	             COALESCE(w.name, m.result)
	      FROM matches m
	      JOIN names t1 ON t1.id = m.team1
	      JOIN names t2 ON t2.id = m.team2
	      LEFT JOIN names w ON w.id = m.winner
	      WHERE (t1.name = ? OR t2.name = ?)`
	args := []any{names[0], names[0]}
	if len(names) > 1 {
		q += ` AND (t1.name = ? OR t2.name = ?)`
		args = append(args, names[1], names[1])
	}
	if year != "" {
		q += ` AND m.date LIKE ?`
		args = append(args, year+"%")
	}
	// "final": prefer an explicitly-named final, else the latest match of
	// the season (which for a season file IS the final). ORDER BY does
	// this in one indexed pass — the old correlated subquery re-scanned
	// matches-squared per row and could hang for minutes.
	if strings.Contains(strings.ToLower(msg), "final") {
		q += ` ORDER BY (CASE WHEN LOWER(m.event) LIKE '%final%' THEN 0 ELSE 1 END), m.date DESC LIMIT 1`
	} else {
		q += ` ORDER BY m.date DESC LIMIT 1`
	}
	var m Match
	err := db.QueryRowContext(qctx(), q, args...).Scan(&m.ID, &m.Date, &m.League, &m.Event,
		&m.Venue, &m.Overs, &m.Team1, &m.Team2, &m.Result)
	return m, err == nil
}

// Scorecard renders a compact factual block for the LLM: innings totals,
// top scorers, top wicket-takers.
func Scorecard(m Match) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s v %s, %s (%s, %s)\n", m.Team1, m.Team2, m.Date, m.Event, m.Venue)
	fmt.Fprintf(&b, "Result: %s\n", m.Result)
	for inn := 1; inn <= 2; inn++ {
		var runs, wkts, balls int
		err := db.QueryRowContext(qctx(), `SELECT COALESCE(SUM(runs_batter+runs_extras),0),
		    COALESCE(SUM(CASE WHEN wicket_kind != '' THEN 1 ELSE 0 END),0), COUNT(*)
		    FROM deliveries WHERE match_id = ? AND innings = ?`, m.ID, inn).Scan(&runs, &wkts, &balls)
		if err != nil || balls == 0 {
			continue
		}
		team := m.Team1
		if inn == 2 {
			team = m.Team2
		}
		fmt.Fprintf(&b, "Innings %d (%s): %d/%d\n", inn, team, runs, wkts)
		rows, err := db.QueryContext(qctx(), `SELECT n.name, SUM(d.runs_batter) r, COUNT(*) FROM deliveries d
		    JOIN names n ON n.id = d.batter WHERE d.match_id = ? AND d.innings = ?
		    GROUP BY d.batter ORDER BY r DESC LIMIT 3`, m.ID, inn)
		if err == nil {
			for rows.Next() {
				var name string
				var r, bl int
				if rows.Scan(&name, &r, &bl) == nil {
					fmt.Fprintf(&b, "  bat: %s %d off %d\n", name, r, bl)
				}
			}
			rows.Close()
		}
		rows, err = db.QueryContext(qctx(), `SELECT n.name, SUM(CASE WHEN d.wicket_kind NOT IN ('', 'run out') THEN 1 ELSE 0 END) w,
		    SUM(d.runs_batter+d.runs_extras) c FROM deliveries d
		    JOIN names n ON n.id = d.bowler WHERE d.match_id = ? AND d.innings = ?
		    GROUP BY d.bowler ORDER BY w DESC, c ASC LIMIT 2`, m.ID, inn)
		if err == nil {
			for rows.Next() {
				var name string
				var w, c int
				if rows.Scan(&name, &w, &c) == nil {
					fmt.Fprintf(&b, "  bowl: %s %d wickets for %d\n", name, w, c)
				}
			}
			rows.Close()
		}
	}
	return b.String()
}

var overRe = regexp.MustCompile(`(?i)\b(\d{1,2})(?:st|nd|rd|th)? over\b|\bover (\d{1,2})\b`)

// OverDetail answers "who bowled the Nth over": per-innings bowler and
// what the over cost. Returns "" when the question names no over.
func OverDetail(m Match, msg string) string {
	g := overRe.FindStringSubmatch(msg)
	if g == nil {
		return ""
	}
	num := g[1]
	if num == "" {
		num = g[2]
	}
	var b strings.Builder
	for inn := 1; inn <= 2; inn++ {
		var bowler string
		var runs, wkts int
		err := db.QueryRowContext(qctx(), `SELECT n.name, SUM(d.runs_batter+d.runs_extras),
		    SUM(CASE WHEN d.wicket_kind != '' THEN 1 ELSE 0 END)
		    FROM deliveries d JOIN names n ON n.id = d.bowler
		    WHERE d.match_id = ? AND d.innings = ? AND d.over = ?
		    GROUP BY d.bowler LIMIT 1`, m.ID, inn, num).Scan(&bowler, &runs, &wkts)
		if err == nil {
			fmt.Fprintf(&b, "Over %s of innings %d: bowled by %s, %d runs, %d wickets\n",
				num, inn, bowler, runs, wkts)
		}
	}
	return b.String()
}
