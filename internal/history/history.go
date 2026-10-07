// Package history answers questions about past matches from a per-delivery
// SQLite database built out of Cricsheet archives (scripts/histgen.py,
// whose canonical copy is asaraog/cricket-history-data/scripts/histgen.py,
// commit fbb4344, where the release is built; the copy here stays
// byte-identical to it).
// The DB is not in the binary; when the file is absent the archive tools
// say so rather than inventing an answer.
package history

import (
	"compress/gzip"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
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
	// hasWide says the deliveries table carries the wide and noball
	// columns histgen.py writes since 2026-10-06. The v1 release has
	// neither, and on it every row counts as a ball.
	hasWide bool
	// hasGender says the matches table carries the gender column
	// histgen.py writes since 2026-10-07, "male" or "female" from
	// Cricsheet. Files built before it have none, and a gender is read
	// off the event name there (formatGender).
	hasGender bool
)

// faced is 1 on a row the batter faced and 0 on a wide, for the deliveries
// alias d ("d." or "" for none). A wide is not a ball faced; a no-ball is.
//
// Staging, 2026-10-06, "who top scored in the 2024 T20 world cup final":
// "Virat Kohli 76 off 62" and "Klaasen 52 off 29", for 76 off 59 and 52
// off 27. Balls faced were COUNT(*) over a batter's rows, and three of
// Kohli's were wides. The v1 archive has no wide column, so there every
// row is still a ball, as it was.
func faced(d string) string {
	if !hasWide {
		return "1"
	}
	return "CASE WHEN " + d + "wide=0 THEN 1 ELSE 0 END"
}

// ballsFaced is the SQL aggregate for the balls a batter faced.
func ballsFaced(d string) string {
	if !hasWide {
		return "COUNT(*)"
	}
	return "COALESCE(SUM(" + faced(d) + "),0)"
}

// legalBalls is the SQL aggregate for the balls a bowler bowled that count
// in his overs and economy: neither a wide nor a no-ball.
func legalBalls(d string) string {
	if !hasWide {
		return "COUNT(*)"
	}
	return "COALESCE(SUM(CASE WHEN " + d + "wide=0 AND " + d + "noball=0 THEN 1 ELSE 0 END),0)"
}

// The archive is built by the asaraog/cricket-history-data workflow and
// published as a release asset there, history-full.db.gz (206 MB). The
// release is private: with no HISTORY_DB_TOKEN that can read it, the
// download fails and the archive tools report the archive as missing;
// scripts/histgen.py builds the same file from public Cricsheet data.
//
// The archive rebuilt with the wide and noball columns (2026-10-06,
// release history-20261007-37551550048) is a new release with a new asset
// id, and a server that downloaded one id would have gone on with the v1
// file. A first run asks the data repo for its latest release and
// downloads the history-full.db.gz on it; the id below is where it goes
// when that lookup fails for any reason. It is the archive rebuilt with
// the gender column as well (2026-10-07, release
// history-20261007-37566870453, built by histgen.py at fbb4344): pinned
// to the wide-only rebuild, 617060111, a first run whose lookup failed
// would have had no gender column, and cricket_team_form and
// cricket_leaders would have told a side's men's games from its women's
// by the event name alone. Variables, so a test can point them at its
// own server.
var (
	defaultAssetURL  = "https://api.github.com/repos/asaraog/cricket-history-data/releases/assets/617455143"
	latestReleaseURL = "https://api.github.com/repos/asaraog/cricket-history-data/releases/latest"
)

// defaultAssetID is the asset defaultAssetURL serves, for the sidecar.
const defaultAssetID = 617455143

// assetName is the full archive's file name on every release.
const assetName = "history-full.db.gz"

// releaseAsset is one file on a GitHub release, as the API lists it: its
// id, its name and the url that serves its bytes with Accept:
// application/octet-stream.
type releaseAsset struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	URL  string `json:"url"`
}

// latestAsset resolves the full archive on the data repo's latest release.
func latestAsset() (releaseAsset, error) {
	req, err := http.NewRequest(http.MethodGet, latestReleaseURL, nil)
	if err != nil {
		return releaseAsset{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	// The release is private; HISTORY_DB_TOKEN is a token that can read it.
	if tok := os.Getenv("HISTORY_DB_TOKEN"); tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return releaseAsset{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return releaseAsset{}, fmt.Errorf("release lookup %s", resp.Status)
	}
	var rel struct {
		Assets []releaseAsset
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&rel); err != nil {
		return releaseAsset{}, err
	}
	for _, a := range rel.Assets {
		if a.Name == assetName && a.URL != "" {
			return a, nil
		}
	}
	return releaseAsset{}, fmt.Errorf("latest release has no %s", assetName)
}

// resolveArchive is where this boot's archive comes from: HISTORY_DB_URL
// when set (no asset id, so a file on disk is never replaced by it), else
// the latest release's full archive, else the id defaultAssetURL pins.
func resolveArchive() releaseAsset {
	if url := os.Getenv("HISTORY_DB_URL"); url != "" {
		log.Printf("history: archive is HISTORY_DB_URL %s", url)
		return releaseAsset{Name: assetName, URL: url}
	}
	a, err := latestAsset()
	if err != nil {
		log.Printf("history: latest release lookup failed (%v); archive is asset %d", err, defaultAssetID)
		return releaseAsset{ID: defaultAssetID, Name: assetName, URL: defaultAssetURL}
	}
	log.Printf("history: archive is %s on the latest release, asset %d", a.Name, a.ID)
	return a
}

// sidecar is the file beside the archive that holds the id of the release
// asset it was downloaded from.
func sidecar(path string) string { return path + ".asset" }

// sidecarID is the asset id the archive at path came from, 0 when the
// sidecar is missing or unreadable.
func sidecarID(path string) int64 {
	raw, err := os.ReadFile(sidecar(path))
	if err != nil {
		return 0
	}
	id, err := strconv.ParseInt(strings.TrimSpace(string(raw)), 10, 64)
	if err != nil {
		return 0
	}
	return id
}

// fetchDB downloads the archive src to dst, through a temp path and a
// rename, so a reader of the file at dst sees the old file or the new
// one and never a part of either, and records the asset id in the
// sidecar.
func fetchDB(dst string, src releaseAsset) bool {
	url := src.URL
	log.Printf("history: downloading %s", url)
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return false
	}
	req.Header.Set("Accept", "application/octet-stream")
	if tok := os.Getenv("HISTORY_DB_TOKEN"); tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	// 206 MB over a home connection: a generous budget.
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
	if src.ID == 0 {
		// HISTORY_DB_URL: the file is no release asset.
		_ = os.Remove(sidecar(dst))
	} else if err := os.WriteFile(sidecar(dst), []byte(strconv.FormatInt(src.ID, 10)+"\n"), 0o644); err != nil {
		log.Printf("history: sidecar %s not written (%v); the archive is downloaded again next boot", sidecar(dst), err)
	}
	log.Printf("history: db downloaded to %s", dst)
	return true
}

// ensureArchive puts the archive this run should open at path, or in the
// temp dir when path is not writable, and returns where it is.
//
// The archive is downloaded once into the cache directory and reused from
// then on. The sidecar beside it holds the asset id it came from; when the
// latest release's asset is another, the archive is downloaded again and
// replaces the file in one rename, and when that download fails the file
// on disk is served as before. A file with no sidecar is one the user
// built or supplied (HISTORY_DB at their own histgen.py output): it is
// opened as it is, and the release is not looked up at all.
func ensureArchive(path string) (string, bool) {
	if _, err := os.Stat(path); err != nil {
		src := resolveArchive()
		// Prefer fetching into the configured path; fall back to temp
		// when that location is not writable.
		if !fetchDB(path, src) {
			alt := filepath.Join(os.TempDir(), "history.db")
			if _, err2 := os.Stat(alt); err2 != nil && !fetchDB(alt, src) {
				return "", false
			}
			path = alt
		}
		return path, true
	}
	have := sidecarID(path)
	if have == 0 {
		return path, true
	}
	if src := resolveArchive(); src.ID != 0 && have != src.ID {
		log.Printf("history: %s is asset %d, the latest is %d; replacing it", path, have, src.ID)
		if !fetchDB(path, src) {
			log.Printf("history: keeping %s (asset %d); the download failed", path, have)
		}
	}
	return path, true
}

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

func open() {
	path := os.Getenv("HISTORY_DB")
	if path == "" {
		// Default to a durable per-user location so the one-time
		// download survives reboots — a temp directory would make the
		// user pay for it again after every cleanup.
		path = defaultArchivePath()
	}
	path, ok := ensureArchive(path)
	if !ok {
		return
	}
	d, cols, err := openFile(path)
	if err != nil {
		return
	}
	if cols.wide {
		log.Printf("history: %s has the wide column; wides are not balls faced", path)
	} else {
		log.Printf("history: %s has no wide column (v1); every row counts as a ball", path)
	}
	if cols.gender {
		log.Printf("history: %s has the gender column", path)
	} else {
		log.Printf("history: %s has no gender column; gender is read off the event name", path)
	}
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
	hasWide, hasGender = cols.wide, cols.gender
	db = d
}

// columns says which of the columns histgen.py added after the v1
// release an archive carries.
type columns struct {
	wide   bool // deliveries.wide and deliveries.noball, since 2026-10-06
	gender bool // matches.gender, since 2026-10-07
}

// openFile opens an archive read-only and reports whether its deliveries
// table carries the wide column and its matches table the gender column.
func openFile(path string) (*sql.DB, columns, error) {
	d, err := sql.Open("sqlite", path+"?mode=ro")
	if err != nil {
		return nil, columns{}, err
	}
	// Bounded pool: each connection carries its own page cache, so
	// unbounded readers would be a memory hazard on a small instance.
	// Archive queries are millisecond-scale, so a small pool is plenty.
	d.SetMaxOpenConns(8)
	d.SetMaxIdleConns(4)
	d.SetConnMaxIdleTime(5 * time.Minute)
	return d, columns{wide: hasColumn(d, "deliveries", "wide"), gender: hasColumn(d, "matches", "gender")}, nil
}

// hasColumn reports whether a table of the archive has the column, from
// PRAGMA table_info. The table name is a constant of this package.
func hasColumn(d *sql.DB, table, column string) bool {
	rows, err := d.Query(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		return false
	}
	defer rows.Close()
	found := false
	for rows.Next() {
		var cid, notnull, pk int
		var name, typ string
		var dflt sql.NullString
		if rows.Scan(&cid, &name, &typ, &notnull, &dflt, &pk) == nil && name == column {
			found = true
		}
	}
	return found
}

// Enabled reports whether the history DB is present. The first call blocks
// on the download.
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

// analyticsDeadline is the budget for the archive analytics in analytics.go
// (phase splits, leaders, dismissals, discipline, situational and
// partnerships), HISTORY_ANALYTICS_TIMEOUT. On the hosted server
// cricket_partnerships sat at 3.19s against the 3s cap and failed on every
// call: a career partnership query aggregates every delivery of every
// innings a batter has appeared in, and is slow because of what it is.
// Match lookups and scorecards stay on the 3s budget.
func analyticsDeadline() time.Duration {
	if v := os.Getenv("HISTORY_ANALYTICS_TIMEOUT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			return d
		}
	}
	return 15 * time.Second
}

// aqctx bounds an analytics query. Same shape as qctx, longer budget.
func aqctx() context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), analyticsDeadline())
	_ = cancel
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

// ArchiveTeam is the archive's own name for a side as a feed or a reader
// names it: the same name in any case, else the one archive team whose
// name holds it ("United States" for "United States of America"), else the
// first team TeamsIn finds in it.
func ArchiveTeam(name string) (string, bool) {
	if !Enabled() || strings.TrimSpace(name) == "" {
		return "", false
	}
	low := strings.ToLower(strings.TrimSpace(name))
	var within []string
	for _, t := range teams {
		tl := strings.ToLower(t)
		if tl == low {
			return t, true
		}
		if strings.Contains(tl, low) {
			within = append(within, t)
		}
	}
	if len(within) == 1 {
		return within[0], true
	}
	if hits := TeamsIn(name); len(hits) > 0 {
		return hits[0], true
	}
	return "", false
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

// A tournament is how a reader names an ICC or Asian event, and the
// Cricsheet event names the archive holds it under (histgen.py stores
// info.event.name: "ICC Men's T20 World Cup", "ICC Women's T20 World Cup",
// "ICC Cricket World Cup", "ICC Women's World Cup", "ICC Champions Trophy",
// "Asia Cup"). The event column never says "final": a World Cup final is
// the last match of its event and year, the same way a league season's is.
//
// Prod, 2026-10-06, "who won the 2024 T20 world cup final": with no team
// in the question only a league could resolve a match, so nothing did and
// the model answered with the women's final. "India v South Africa 2024
// T20 World Cup final" named the teams and the year, and the year alone
// picked the November T20I of India's tour (Tilak Varma 120), because
// nothing read "T20 World Cup". Order matters: "t20 world cup" is checked
// before "world cup".
type tournament struct {
	phrase *regexp.Regexp
	// men and women are the LIKE patterns on LOWER(m.event) for each
	// edition; the older event names ("ICC World Twenty20") sit beside
	// the current ones.
	men, women []string
}

var tournaments = []tournament{
	{
		regexp.MustCompile(`\b(t20 world cup|t20 wc|t20wc|world t20|world twenty20)\b`),
		[]string{"%t20 world cup%", "%world twenty20%"},
		[]string{"%t20 world cup%", "%world twenty20%"},
	},
	{
		regexp.MustCompile(`\bworld cup\b`),
		[]string{"%cricket world cup%"},
		[]string{"%women's world cup%"},
	},
	{
		regexp.MustCompile(`\bchampions trophy\b`),
		[]string{"%champions trophy%"},
		[]string{"%champions trophy%"},
	},
	{
		regexp.MustCompile(`\basia cup\b`),
		[]string{"%asia cup%"},
		[]string{"%asia cup%"},
	},
	{
		regexp.MustCompile(`\b(world test championship|wtc)\b`),
		[]string{"%world test championship%"},
		[]string{"%world test championship%"},
	},
}

var womenRe = regexp.MustCompile(`\bwomen`)

// decidedRe is a question about who took a tournament: it resolves to the
// final even without the word "final" in it.
var decidedRe = regexp.MustCompile(`\b(final|won|winner|winners|champion|champions)\b`)

// tournamentFilter is the SQL that pins a match to the tournament the
// message names, with its arguments, or "" when the message names none.
// "Women" in the message picks the women's edition; otherwise the men's,
// which is what "T20 World Cup" means with nothing said. Qualifiers and
// warm-ups carry the event's name too and are kept out: the last 2024
// match with "T20 World Cup" in its event is a regional qualifier for
// 2026, not the final.
func tournamentFilter(msg string) (string, []any) {
	low := strings.ToLower(msg)
	for _, t := range tournaments {
		if !t.phrase.MatchString(low) {
			continue
		}
		patterns := t.men
		gender := ` AND LOWER(m.event) NOT LIKE '%women%'`
		if womenRe.MatchString(low) {
			patterns = t.women
			gender = ` AND LOWER(m.event) LIKE '%women%'`
		}
		var likes []string
		var args []any
		for _, p := range patterns {
			likes = append(likes, `LOWER(m.event) LIKE ?`)
			args = append(args, p)
		}
		q := ` AND (` + strings.Join(likes, " OR ") + `)` + gender +
			` AND LOWER(m.event) NOT LIKE '%qualifier%' AND LOWER(m.event) NOT LIKE '%warm%'`
		return q, args
	}
	return "", nil
}

func FindMatch(msg string, names []string) (Match, bool) {
	if !Enabled() {
		return Match{}, false
	}
	if len(names) == 0 {
		// No team named: "the 2025 IPL final" still resolves by league —
		// latest game of that league (and year), which for a season file
		// is the final. "The 2024 T20 World Cup final" resolves the same
		// way by tournament, and so does "who won the 2024 T20 World Cup":
		// the last match of the event is the final. A tournament named
		// with nothing asked about its outcome ("best bowling at the 2024
		// T20 World Cup") is no one match and resolves nothing.
		lg := leagueIn(msg)
		tq, targs := tournamentFilter(msg)
		if lg == "" && (tq == "" || !decidedRe.MatchString(strings.ToLower(msg))) {
			return Match{}, false
		}
		year := yearRe.FindString(msg)
		q := `SELECT m.id, m.date, m.league, m.event, m.venue, m.overs,
		             t1.name, t2.name, COALESCE(w.name, m.result)
		      FROM matches m
		      JOIN names t1 ON t1.id = m.team1
		      JOIN names t2 ON t2.id = m.team2
		      LEFT JOIN names w ON w.id = m.winner
		      WHERE 1 = 1`
		var args []any
		if lg != "" {
			q += ` AND m.league = ?`
			args = append(args, lg)
		} else {
			q += tq
			args = append(args, targs...)
		}
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
	// The tournament named keeps the teams' other meetings that year out:
	// "India v South Africa 2024 T20 World Cup final" is the June final,
	// not the November T20I.
	if tq, targs := tournamentFilter(msg); tq != "" {
		q += tq
		args = append(args, targs...)
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
		// nrows is the innings' row count, wides and all: it only says
		// whether the innings was played.
		var runs, wkts, nrows int
		err := db.QueryRowContext(qctx(), `SELECT COALESCE(SUM(runs_batter+runs_extras),0),
		    COALESCE(SUM(CASE WHEN wicket_kind != '' THEN 1 ELSE 0 END),0), COUNT(*)
		    FROM deliveries WHERE match_id = ? AND innings = ?`, m.ID, inn).Scan(&runs, &wkts, &nrows)
		if err != nil || nrows == 0 {
			continue
		}
		team := m.Team1
		if inn == 2 {
			team = m.Team2
		}
		fmt.Fprintf(&b, "Innings %d (%s): %d/%d\n", inn, team, runs, wkts)
		rows, err := db.QueryContext(qctx(), `SELECT n.name, SUM(d.runs_batter) r, `+ballsFaced("d.")+` FROM deliveries d
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

// PlayerLine is the line of a player the question names in an archived
// match: what he made and how he was out, and what he took when he
// bowled. Scorecard lists three batters an innings, so a player below
// them is not in the block at all.
//
// Prod, 2026-10-06, "how many runs did Rohit Sharma score in the 2024 T20
// world cup final", three runs: "9 off 5" (the model's memory, right),
// "I don't have the per-match scorecard", and "Rohit Sharma scored 76 off
// 59 balls", which is Kohli's innings. Rohit's 9 off 5 was in the
// archive and never in the block.
//
// The archive holds Cricsheet names, "RG Sharma", and the question holds
// "Rohit Sharma": a player matches when the name after his initials
// ("Sharma", "du Plessis") is in the question as whole words and the word
// before it starts with the letter his initials start with, or is his
// initials typed. The name after the initials alone is enough when only
// one player in the match carries it. Returns "" when the question names
// no one who batted or bowled.
func PlayerLine(m Match, msg string) string {
	if !Enabled() {
		return ""
	}
	rows, err := db.QueryContext(qctx(), `SELECT DISTINCT n.id, n.name FROM deliveries d
	    JOIN names n ON n.id IN (d.batter, d.bowler) WHERE d.match_id = ?`, m.ID)
	if err != nil {
		return ""
	}
	type player struct {
		id   int64
		name string
	}
	var players []player
	for rows.Next() {
		var p player
		if rows.Scan(&p.id, &p.name) == nil && p.name != "" {
			players = append(players, p)
		}
	}
	rows.Close()
	tails := map[string]int{}
	for _, p := range players {
		tails[strings.ToLower(nameTail(p.name))]++
	}
	words := strings.Fields(strings.NewReplacer("?", " ", ",", " ", ".", " ", "'s", "").Replace(msg))
	var b strings.Builder
	for _, p := range players {
		if !namedIn(words, p.name, tails[strings.ToLower(nameTail(p.name))] == 1) {
			continue
		}
		brows, err := db.QueryContext(qctx(), `SELECT d.innings, SUM(d.runs_batter), `+ballsFaced("d.")+`,
		    COALESCE((SELECT o.wicket_kind FROM deliveries o WHERE o.match_id = d.match_id
		              AND o.innings = d.innings AND o.player_out = ? LIMIT 1), '')
		    FROM deliveries d WHERE d.match_id = ? AND d.batter = ?
		    GROUP BY d.innings ORDER BY d.innings`, p.id, m.ID, p.id)
		if err == nil {
			for brows.Next() {
				var inn, r, bl int
				var how string
				if brows.Scan(&inn, &r, &bl, &how) == nil {
					out := "not out"
					if how != "" {
						out = "out " + how
					}
					fmt.Fprintf(&b, "  bat: %s %d off %d, %s", p.name, r, bl, out)
					if m.Overs == 0 {
						fmt.Fprintf(&b, " (innings %d)", inn)
					}
					b.WriteString("\n")
				}
			}
			brows.Close()
		}
		wrows, err := db.QueryContext(qctx(), `SELECT d.innings,
		    SUM(CASE WHEN d.wicket_kind NOT IN ('', 'run out') THEN 1 ELSE 0 END),
		    SUM(d.runs_batter+d.runs_extras)
		    FROM deliveries d WHERE d.match_id = ? AND d.bowler = ?
		    GROUP BY d.innings ORDER BY d.innings`, m.ID, p.id)
		if err == nil {
			for wrows.Next() {
				var inn, w, c int
				if wrows.Scan(&inn, &w, &c) == nil {
					fmt.Fprintf(&b, "  bowl: %s %d wickets for %d", p.name, w, c)
					if m.Overs == 0 {
						fmt.Fprintf(&b, " (innings %d)", inn)
					}
					b.WriteString("\n")
				}
			}
			wrows.Close()
		}
	}
	return b.String()
}

// nameTail is the archive name after its initials: "Sharma" of "RG
// Sharma", "du Plessis" of "F du Plessis". A one-word name is its own
// tail.
func nameTail(name string) string {
	f := strings.Fields(name)
	if len(f) < 2 {
		return name
	}
	return strings.Join(f[1:], " ")
}

// namedIn reports whether the words of a question name the archive
// player: his tail as whole words, led by a word that starts with his
// first initial or is his initials typed. The tail alone does when unique
// says no one else in the match carries it, unless the word before it is
// another capitalised name ("Abhishek Sharma" is not RG Sharma).
func namedIn(words []string, name string, unique bool) bool {
	f := strings.Fields(strings.ToLower(name))
	if len(f) == 0 {
		return false
	}
	initials, tail := "", f
	if len(f) > 1 {
		initials, tail = f[0], f[1:]
	}
	for i := 0; i+len(tail) <= len(words); i++ {
		hit := true
		for j, w := range tail {
			if !strings.EqualFold(words[i+j], w) {
				hit = false
				break
			}
		}
		if !hit {
			continue
		}
		if initials == "" {
			return true
		}
		if i == 0 {
			if unique {
				return true
			}
			continue
		}
		before := strings.ToLower(words[i-1])
		if before == initials || before[0] == initials[0] {
			return true
		}
		if unique && before == words[i-1] {
			return true
		}
	}
	return false
}
