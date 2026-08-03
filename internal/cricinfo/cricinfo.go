// Package cricinfo fetches live cricket data from ESPN's public JSON APIs
// (the same ESPNcricinfo data; cricinfo's own hs-consumer-api is
// Akamai-blocked to non-browser clients). Fallback: the cricinfo RSS
// livescores feed, whose match IDs share ESPN's event ID space.
//
// MLC 2026 league id = 1528556 (per-season; rediscover each June).
// Minor League Cricket is not on ESPNcricinfo at all (CricClubs only).
package cricinfo

import (
	"encoding/json"
	"html"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/asaraog/mcp-cricket/internal/explainer"
)

const (
	discoveryURL = "https://site.web.api.espn.com/apis/personalized/v2/scoreboard/header?sport=cricket&lang=en&region=us"
	siteAPI      = "https://site.api.espn.com/apis/site/v2/sports/cricket"
	rssLiveURL   = "https://static.cricinfo.com/rss/livescores.xml"

	MLCLeague2026 = "1528556"
)

var client = &http.Client{Timeout: 10 * time.Second, Transport: tunedTransport()}

// ---------------------------------------------------------------- caching
//
// The ESPN endpoints are unofficial and unmetered but not unlimited — the
// scalable shape is: users hit US, we hit ESPN on a fixed clock. All users
// share one cached fetch per endpoint, so backend load on ESPN is constant
// (a few requests/minute) regardless of user count.

type flight struct {
	done chan struct{}
	val  any
	err  error
}

var cacheFlights = map[string]*flight{}

type cacheEntry struct {
	val        any
	exp        time.Time
	refreshing bool
}

var (
	cacheMu  sync.Mutex
	cacheMap = map[string]cacheEntry{}
)

// fromCache is a TTL cache with two prod-hardening behaviors:
//   - stale-while-revalidate: an expired entry is served immediately and
//     refreshed in the background, so no request ever waits on upstream
//     when we have anything at all to show (staleness is bounded by one
//     extra TTL);
//   - singleflight: when there is no value to serve, concurrent misses
//     collapse into ONE upstream fetch — N users arriving on an expired
//     entry must not turn into N identical ESPN calls.
func fromCache[T any](key string, ttl time.Duration, fetch func() (T, error)) (T, error) {
	now := time.Now()
	cacheMu.Lock()
	e, ok := cacheMap[key]
	if ok && now.Before(e.exp) {
		cacheMu.Unlock()
		return e.val.(T), nil
	}
	if ok && !e.refreshing {
		// Serve stale, refresh behind the scenes.
		e.refreshing = true
		cacheMap[key] = e
		cacheMu.Unlock()
		go func() {
			if v, err := fetch(); err == nil {
				cacheMu.Lock()
				cacheMap[key] = cacheEntry{val: v, exp: time.Now().Add(ttl)}
				cacheMu.Unlock()
			} else {
				cacheMu.Lock()
				if cur, still := cacheMap[key]; still {
					cur.refreshing = false
					cacheMap[key] = cur
				}
				cacheMu.Unlock()
			}
		}()
		return e.val.(T), nil
	}
	if ok {
		// Stale but a refresh is already in flight: keep serving stale.
		cacheMu.Unlock()
		return e.val.(T), nil
	}
	// Nothing cached: singleflight the first fill.
	fl, exists := cacheFlights[key]
	if !exists {
		fl = &flight{done: make(chan struct{})}
		cacheFlights[key] = fl
		cacheMu.Unlock()
		v, err := fetch()
		cacheMu.Lock()
		if err == nil {
			cacheMap[key] = cacheEntry{val: v, exp: time.Now().Add(ttl)}
		}
		fl.val, fl.err = v, err
		close(fl.done)
		delete(cacheFlights, key)
		cacheMu.Unlock()
		return v, err
	}
	cacheMu.Unlock()
	<-fl.done
	if fl.err != nil {
		var zero T
		return zero, fl.err
	}
	return fl.val.(T), nil
}

func getJSON(url string, out any) error {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "cricket-mcp/0.1 (+https://github.com/asaraog/mcp-cricket)")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// ---------------------------------------------------------------- discovery

// ListedMatch is one row in match discovery.
type ListedMatch struct {
	LeagueID string `json:"league_id"`
	League   string `json:"league,omitempty"`
	EventID  string `json:"event_id"`
	Title    string `json:"title"`
	State    string `json:"state"` // pre | in | post
	Summary  string `json:"summary,omitempty"`
	Link     string `json:"link,omitempty"`
	// HasScores marks live matches whose feed actually carries score
	// strings; ESPN lists minor fixtures as "in" with empty scoreboards.
	HasScores *bool  `json:"has_scores,omitempty"`
	// Date is the scheduled start (ISO, UTC) straight from the feed.
	Date string `json:"date,omitempty"`
	Source   string `json:"source"` // espn | rss
	// Phase is the discovery feed's status word ("Stumps", "Lunch") — a
	// Test can be state "in" while play is paused for the day.
	Phase string `json:"phase,omitempty"`
}

type headerResp struct {
	Sports []struct {
		Leagues []struct {
			ID        string `json:"id"`
			Name      string `json:"name"`
			ShortName string `json:"shortName"`
			Events    []struct {
				ID      string `json:"id"`
				Name    string `json:"name"`
				Summary string `json:"summary"`
				Status  string `json:"status"`
				Date    string `json:"date"`
			} `json:"events"`
		} `json:"leagues"`
	} `json:"sports"`
}

// LiveMatches returns ESPNcricinfo's curated live-match list (its RSS feed
// decides WHICH matches matter — no obscure scheduled fixtures) joined with
// ESPN discovery for the league/event IDs and clean titles. Falls back to
// the full ESPN list if RSS is down, or the raw RSS if discovery is.
// Cached 15s: every user shares one fetch.
func LiveMatches() []ListedMatch {
	v, _ := fromCache("live", 15*time.Second, func() ([]ListedMatch, error) {
		return joinedLiveMatches(), nil
	})
	return v
}

func joinedLiveMatches() []ListedMatch {
	espn := fetchLiveMatches()
	rss := rssLiveMatches()
	if len(rss) == 0 {
		return espn // cricinfo feed down: full ESPN list is better than none
	}
	byEvent := map[string]ListedMatch{}
	for _, m := range espn {
		if m.EventID != "" {
			byEvent[m.EventID] = m
		}
	}
	out := make([]ListedMatch, 0, len(rss))
	for _, r := range rss {
		if em, ok := byEvent[r.EventID]; ok {
			em.Summary = r.Title // cricinfo's score line, e.g. "WI 311 v PAK 199/3 *"
			out = append(out, em)
		} else {
			out = append(out, r) // no ESPN ids; still listable
		}
	}
	return out
}

func fetchLiveMatches() []ListedMatch {
	var hdr headerResp
	if err := getJSON(discoveryURL, &hdr); err == nil {
		var out []ListedMatch
		for _, sport := range hdr.Sports {
			for _, lg := range sport.Leagues {
				name := lg.Name
				if name == "" {
					name = lg.ShortName
				}
				for _, ev := range lg.Events {
					out = append(out, ListedMatch{
						LeagueID: lg.ID, League: name,
						EventID: ev.ID, Title: ev.Name,
						State: ev.Status, Summary: ev.Summary, Source: "espn",
						Phase: ev.Summary, Date: ev.Date,
					})
				}
			}
		}
		if len(out) > 0 {
			return out
		}
	}
	return rssLiveMatches()
}

type rssFeed struct {
	Items []struct {
		Title string `xml:"title"`
		GUID  string `xml:"guid"`
		Link  string `xml:"link"`
	} `xml:"channel>item"`
}

var rssMatchIDRe = regexp.MustCompile(`/match/(\d+)`)

func rssLiveMatches() []ListedMatch {
	req, _ := http.NewRequest(http.MethodGet, rssLiveURL, nil)
	resp, err := client.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil
	}
	var feed rssFeed
	if err := xml.Unmarshal(body, &feed); err != nil {
		return nil
	}
	var out []ListedMatch
	for _, item := range feed.Items {
		title := strings.TrimSpace(item.Title)
		if title == "" {
			continue
		}
		link := item.GUID
		if link == "" {
			link = item.Link
		}
		eventID := ""
		if m := rssMatchIDRe.FindStringSubmatch(link); m != nil {
			eventID = m[1]
		}
		out = append(out, ListedMatch{
			EventID: eventID, Title: title, Summary: title,
			State: "in", Link: link, Source: "rss",
		})
	}
	return out
}

// ------------------------------------------------------------------ detail

// TeamScore is one side's line in a simplified match.
type TeamScore struct {
	Name   string `json:"name"`
	Score  string `json:"score,omitempty"` // "311" or "96/1 (19 ov)"
	Winner bool   `json:"winner,omitempty"`
}

// Match is the simplified match state served to clients and the LLM.
type Match struct {
	LeagueID    string      `json:"league_id"`
	EventID     string      `json:"event_id"`
	Title       string      `json:"title,omitempty"`
	State       string      `json:"state,omitempty"` // pre | in | post
	StatusText  string      `json:"status_text,omitempty"`
	Note        string      `json:"note,omitempty"`
	EventType   string      `json:"event_type,omitempty"` // "Test", "ODI", "T20" (authoritative)
	Description string      `json:"description,omitempty"`
	Teams       []TeamScore `json:"teams"`
	Source      string      `json:"source"`
}

type statusT struct {
	Type struct {
		State       string `json:"state"`
		Detail      string `json:"detail"`
		Description string `json:"description"`
	} `json:"type"`
}

// flexBool tolerates ESPN sending booleans as true/false OR "true"/"false"
// (finished matches use the string form for competitor winner flags).
type flexBool bool

func (b *flexBool) UnmarshalJSON(data []byte) error {
	s := strings.Trim(strings.TrimSpace(string(data)), `"`)
	*b = flexBool(s == "true" || s == "1")
	return nil
}

// flexFloat tolerates ESPN sending numbers as strings ("6", "-", "").
type flexFloat float64

func (f *flexFloat) UnmarshalJSON(data []byte) error {
	s := strings.Trim(strings.TrimSpace(string(data)), `"`)
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		*f = 0 // "-", "", null: best-effort stats, never a parse failure
		return nil
	}
	*f = flexFloat(v)
	return nil
}

type competitionT struct {
	Description string  `json:"description"`
	Status      statusT `json:"status"`
	Class       struct {
		EventType        string `json:"eventType"`
		GeneralClassCard string `json:"generalClassCard"`
	} `json:"class"`
	Competitors []struct {
		Winner flexBool        `json:"winner"`
		Score  json.RawMessage `json:"score"`
		Team   struct {
			DisplayName string `json:"displayName"`
			Name        string `json:"name"`
		} `json:"team"`
	} `json:"competitors"`
	Notes []struct {
		Headline string `json:"headline"`
	} `json:"notes"`
}

type eventT struct {
	ID           string         `json:"id"`
	Name         string         `json:"name"`
	Description  string         `json:"description"`
	Status       statusT        `json:"status"`
	Competitions []competitionT `json:"competitions"`
}

type scoreboardResp struct {
	Events []eventT `json:"events"`
}

// Scoreboard returns simplified matches for one league. Cached 10s.
func Scoreboard(leagueID string) ([]Match, error) {
	return fromCache("sb:"+leagueID, 10*time.Second, func() ([]Match, error) {
		var data scoreboardResp
		if err := getJSON(fmt.Sprintf("%s/%s/scoreboard", siteAPI, leagueID), &data); err != nil {
			return nil, err
		}
		out := make([]Match, 0, len(data.Events))
		for _, ev := range data.Events {
			out = append(out, simplifyEvent(ev, leagueID))
		}
		return out, nil
	})
}

// SimplifyScoreboardJSON parses a raw ESPN scoreboard response that a CLIENT
// fetched (fan-side polling: each browser hits ESPN from its own IP; the
// backend only parses). Returns the named event.
func SimplifyScoreboardJSON(body []byte, leagueID, eventID string) (Match, error) {
	var data scoreboardResp
	if err := json.Unmarshal(body, &data); err != nil {
		return Match{}, fmt.Errorf("client scoreboard: %w", err)
	}
	for _, ev := range data.Events {
		if ev.ID == eventID {
			return simplifyEvent(ev, leagueID), nil
		}
	}
	return Match{}, fmt.Errorf("event %s not in client scoreboard", eventID)
}

// MatchDetail returns match state for one game.
func MatchDetail(leagueID, eventID string) (Match, error) {
	events, err := Scoreboard(leagueID)
	if err == nil {
		for _, m := range events {
			if m.EventID == eventID {
				return m, nil
			}
		}
	}
	// Not on today's scoreboard — fall back to the (much larger) summary.
	var data struct {
		Header eventT `json:"header"`
	}
	url := fmt.Sprintf("%s/%s/summary?event=%s", siteAPI, leagueID, eventID)
	if err := getJSON(url, &data); err != nil {
		return Match{}, err
	}
	if len(data.Header.Competitions) == 0 {
		return Match{}, fmt.Errorf("event %s not found in league %s", eventID, leagueID)
	}
	if data.Header.ID == "" {
		data.Header.ID = eventID
	}
	return simplifyEvent(data.Header, leagueID), nil
}

func simplifyEvent(ev eventT, leagueID string) Match {
	m := Match{
		LeagueID: leagueID, EventID: ev.ID,
		Title: ev.Name, Description: ev.Description, Source: "espn",
	}
	st := ev.Status
	var comp competitionT
	if len(ev.Competitions) > 0 {
		comp = ev.Competitions[0]
		if st.Type.State == "" {
			st = comp.Status
		}
	}
	m.State = st.Type.State
	m.StatusText = st.Type.Detail
	if m.StatusText == "" {
		m.StatusText = st.Type.Description
	}
	// ESPN keeps detail="Live" through multi-day pauses while description
	// carries the real phase ("Stumps", "Lunch", "Rain Delay") — a Test at
	// stumps is NOT live play, so prefer the specific phase.
	if desc := st.Type.Description; desc != "" &&
		strings.EqualFold(m.StatusText, "Live") &&
		!strings.EqualFold(desc, "Live") && !strings.EqualFold(desc, "In Progress") {
		m.StatusText = desc
	}
	if m.Description == "" {
		m.Description = comp.Description
	}
	m.EventType = comp.Class.EventType
	if m.EventType == "" {
		m.EventType = comp.Class.GeneralClassCard
	}
	if len(comp.Notes) > 0 {
		m.Note = comp.Notes[0].Headline
	}
	for _, c := range comp.Competitors {
		name := c.Team.DisplayName
		if name == "" {
			name = c.Team.Name
		}
		m.Teams = append(m.Teams, TeamScore{
			Name: name, Score: rawToString(c.Score), Winner: bool(c.Winner),
		})
	}
	return m
}

// rawToString tolerates ESPN sending scores as either strings or numbers.
func rawToString(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	var n float64
	if err := json.Unmarshal(raw, &n); err == nil {
		return strconv.FormatFloat(n, 'f', -1, 64)
	}
	return ""
}

// Ball is one delivery from the play-by-play feed.
type Ball struct {
	Text       string  `json:"text"`
	Over       float64 `json:"over,omitempty"`
	ScoreValue int     `json:"score_value"`
	Innings    int     `json:"innings,omitempty"`
	// Sequence is ESPN's per-item counter — the only safe ball identity:
	// over numbers repeat (a wide doesn't advance the count) and text
	// gets edited after the fact.
	Sequence int64 `json:"sequence,omitempty"`
	// Cumulative innings position as of this ball — lets the client post
	// an over-end score without extra fetches.
	InningsRuns    int `json:"innings_runs,omitempty"`
	InningsWickets int `json:"innings_wickets,omitempty"`
	BallsRemaining int `json:"balls_remaining,omitempty"`
}

// BallByBall returns latest commentary (newest first, 25 per page). Cached 5s.
func BallByBall(leagueID, eventID string, page int) ([]Ball, error) {
	if page < 1 {
		page = 1
	}
	key := fmt.Sprintf("bbb:%s:%s:%d", leagueID, eventID, page)
	return fromCache(key, 5*time.Second, func() ([]Ball, error) {
		return fetchBallByBall(leagueID, eventID, page)
	})
}

type pbpResp struct {
	Commentary struct {
		PageCount int `json:"pageCount"`
		Items     []struct {
			Text      string `json:"text"`
			ShortText string `json:"shortText"`
			Over      struct {
				Actual float64 `json:"actual"`
			} `json:"over"`
			ScoreValue int   `json:"scoreValue"`
			Sequence   int64 `json:"sequence"`
			Innings    struct {
				Number         int `json:"number"`
				Runs           int `json:"runs"`
				Wickets        int `json:"wickets"`
				RemainingBalls int `json:"remainingBalls"`
			} `json:"innings"`
		} `json:"items"`
	} `json:"commentary"`
}

// htmlTagRe: ESPN sometimes embeds markup in commentary ("<b>Baker gets
// Marsh!</b>") — strip it, then decode entities.
var htmlTagRe = regexp.MustCompile(`<[^>]+>`)

// balls converts a page to NEWEST-FIRST order. ESPN pages run oldest->newest
// (page 1 = the first over of the match, last page = the latest balls), and
// items within a page are chronological — so we reverse, and skip the
// occasional empty commentary line.
func (d pbpResp) balls() []Ball {
	out := make([]Ball, 0, len(d.Commentary.Items))
	for i := len(d.Commentary.Items) - 1; i >= 0; i-- {
		it := d.Commentary.Items[i]
		text := strings.TrimSpace(it.Text)
		if text == "" {
			// Smaller tournaments (GSL, MLC) ship only the terse
			// shortText ("Lawes to Robinson, FOUR"); big matches
			// get full prose in text.
			text = strings.TrimSpace(it.ShortText)
		}
		text = strings.TrimSpace(html.UnescapeString(htmlTagRe.ReplaceAllString(text, "")))
		if text == "" {
			continue
		}
		out = append(out, Ball{
			Text: text, Over: it.Over.Actual,
			ScoreValue: it.ScoreValue, Innings: it.Innings.Number,
			Sequence:    it.Sequence,
			InningsRuns: it.Innings.Runs, InningsWickets: it.Innings.Wickets,
			BallsRemaining: it.Innings.RemainingBalls,
		})
	}
	return out
}

func fetchBallPage(leagueID, eventID string, page int) (pbpResp, error) {
	var data pbpResp
	url := fmt.Sprintf("%s/%s/playbyplay?event=%s&page=%d", siteAPI, leagueID, eventID, page)
	err := getJSON(url, &data)
	return data, err
}

func fetchBallByBall(leagueID, eventID string, page int) ([]Ball, error) {
	data, err := fetchBallPage(leagueID, eventID, page)
	if err != nil {
		return nil, err
	}
	return data.balls(), nil
}

// LatestBalls returns the newest n deliveries (newest first) by walking to
// the LAST page of the play-by-play feed, pulling the prior page too when
// the last one is short.
func LatestBalls(leagueID, eventID string, n int) ([]Ball, error) {
	key := fmt.Sprintf("latest:%s:%s:%d", leagueID, eventID, n)
	return fromCache(key, 5*time.Second, func() ([]Ball, error) {
		first, err := fetchBallPage(leagueID, eventID, 1)
		if err != nil {
			return nil, err
		}
		pc := first.Commentary.PageCount
		balls := first.balls()
		if pc > 1 {
			last, err := fetchBallPage(leagueID, eventID, pc)
			if err != nil {
				return nil, err
			}
			balls = last.balls()
			if len(balls) < n && pc > 2 {
				if prev, err := fetchBallPage(leagueID, eventID, pc-1); err == nil {
					balls = append(balls, prev.balls()...)
				}
			}
		}
		if len(balls) > n {
			balls = balls[:n]
		}
		return balls, nil
	})
}

// PlayerLine is one player's live scorecard line from the summary rosters.
type PlayerLine struct {
	Name, Team string
	Active     bool // on the field right now (at the crease / bowling)
	Out        string // dismissal, cricket-style: "c Robinson b Gore (over 8.3)"
	Batted     bool
	Runs       int
	BallsFaced int
	Fours      int
	Sixes      int
	StrikeRate string
	Bowled     bool
	Overs      string
	Conceded   int
	Wickets    int
	Economy    string
}

type rostersResp struct {
	Rosters []struct {
		Team struct {
			DisplayName string `json:"displayName"`
		} `json:"team"`
		Roster []struct {
			Active  bool `json:"active"`
			Athlete struct {
				DisplayName string `json:"displayName"`
			} `json:"athlete"`
			Linescores []struct {
				Linescores []struct {
					Statistics struct {
						Categories []struct {
							Stats []struct {
								Name         string    `json:"name"`
								Value        flexFloat `json:"value"`
								DisplayValue string    `json:"displayValue"`
							} `json:"stats"`
						} `json:"categories"`
						Batting struct {
							OutDetails struct {
								DismissalCard string `json:"dismissalCard"`
								Bowler        struct {
									DisplayName string `json:"displayName"`
								} `json:"bowler"`
								Fielders []struct {
									Athlete struct {
										DisplayName string `json:"displayName"`
									} `json:"athlete"`
								} `json:"fielders"`
								Details struct {
									Over struct {
										Overs flexFloat `json:"overs"`
									} `json:"over"`
								} `json:"details"`
							} `json:"outDetails"`
						} `json:"batting"`
					} `json:"statistics"`
				} `json:"linescores"`
			} `json:"linescores"`
		} `json:"roster"`
	} `json:"rosters"`
}

// PlayersFromSummaryJSON parses the summary rosters into scorecard lines
// plus the names on the batting side still to bat. Batting-side players
// carry a ballsFaced stat; bowling-side players carry balls (bowled) —
// that key difference is the role discriminator.
func PlayersFromSummaryJSON(body []byte) ([]PlayerLine, []string, error) {
	var data rostersResp
	if err := json.Unmarshal(body, &data); err != nil {
		return nil, nil, fmt.Errorf("summary rosters: %w", err)
	}
	lines, ytb := playersFromRosters(data)
	return lines, ytb, nil
}

func playersFromRosters(data rostersResp) ([]PlayerLine, []string) {
	var lines []PlayerLine
	var yetToBat []string
	for _, r := range data.Rosters {
		for _, p := range r.Roster {
			vals := map[string]float64{}
			disp := map[string]string{}
			outDesc := ""
			for _, per := range p.Linescores {
				for _, line := range per.Linescores {
					for _, c := range line.Statistics.Categories {
						for _, s := range c.Stats {
							vals[s.Name] = float64(s.Value)
							disp[s.Name] = s.DisplayValue
						}
					}
					if od := line.Statistics.Batting.OutDetails; od.Bowler.DisplayName != "" || od.DismissalCard != "" {
						outDesc = dismissalDesc(od.DismissalCard, od.Bowler.DisplayName,
							firstFielder(od.Fielders), float64(od.Details.Over.Overs))
					}
				}
			}
			if len(vals) == 0 {
				continue
			}
			pl := PlayerLine{Name: p.Athlete.DisplayName, Team: r.Team.DisplayName, Active: p.Active, Out: outDesc}
			if vals["ballsFaced"] > 0 {
				pl.Batted = true
				pl.Runs = int(vals["runs"])
				pl.BallsFaced = int(vals["ballsFaced"])
				pl.Fours = int(vals["fours"])
				pl.Sixes = int(vals["sixes"])
				pl.StrikeRate = disp["strikeRate"]
			}
			if vals["balls"] > 0 { // balls bowled
				pl.Bowled = true
				pl.Overs = disp["overs"]
				pl.Conceded = int(vals["conceded"])
				pl.Wickets = int(vals["wickets"])
				pl.Economy = disp["economyRate"]
			}
			if pl.Batted || pl.Bowled {
				lines = append(lines, pl)
			} else if _, onBattingSide := vals["ballsFaced"]; onBattingSide {
				yetToBat = append(yetToBat, p.Athlete.DisplayName)
			}
		}
	}
	return lines, yetToBat
}

// dismissalDesc renders scorecard shorthand: "c Robinson b Gore (over 8.3)".
func dismissalDesc(card, bowler, fielder string, over float64) string {
	var b strings.Builder
	switch card {
	case "not out", "":
		return ""
	case "c", "caught":
		if fielder != "" {
			fmt.Fprintf(&b, "c %s ", fielder)
		} else {
			b.WriteString("caught ")
		}
		if bowler != "" {
			fmt.Fprintf(&b, "b %s", bowler)
		}
	case "b", "bowled":
		fmt.Fprintf(&b, "b %s", bowler)
	case "lbw":
		fmt.Fprintf(&b, "lbw b %s", bowler)
	case "st", "stumped":
		fmt.Fprintf(&b, "st %s b %s", fielder, bowler)
	case "runout", "ro", "run out":
		b.WriteString("run out")
		if fielder != "" {
			fmt.Fprintf(&b, " (%s)", fielder)
		}
	default:
		if bowler != "" {
			fmt.Fprintf(&b, "out, b %s", bowler)
		} else {
			b.WriteString("out")
		}
	}
	if over > 0 {
		fmt.Fprintf(&b, " (over %.1f)", over)
	}
	return strings.TrimSpace(b.String())
}

func firstFielder(fs []struct {
	Athlete struct {
		DisplayName string `json:"displayName"`
	} `json:"athlete"`
}) string {
	if len(fs) == 0 {
		return ""
	}
	return fs[0].Athlete.DisplayName
}

type playerStatsResult struct {
	Lines    []PlayerLine
	YetToBat []string
}

// PlayerStats fetches live per-player scorecard lines (cached 15s).
func PlayerStats(leagueID, eventID string) ([]PlayerLine, []string, error) {
	key := "players:" + leagueID + ":" + eventID
	res, err := fromCache(key, 15*time.Second, func() (playerStatsResult, error) {
		var data rostersResp
		url := fmt.Sprintf("%s/%s/summary?event=%s", siteAPI, leagueID, eventID)
		if err := getJSON(url, &data); err != nil {
			return playerStatsResult{}, err
		}
		lines, ytb := playersFromRosters(data)
		return playerStatsResult{Lines: lines, YetToBat: ytb}, nil
	})
	return res.Lines, res.YetToBat, err
}

// BallsFromPlayByPlayJSON parses a raw ESPN playbyplay response fetched by a
// client (fan-side polling), newest first.
func BallsFromPlayByPlayJSON(body []byte) ([]Ball, error) {
	var data pbpResp
	if err := json.Unmarshal(body, &data); err != nil {
		return nil, fmt.Errorf("client playbyplay: %w", err)
	}
	return data.balls(), nil
}

// --------------------------------------------------------------- to state

var (
	parenRe      = regexp.MustCompile(`\(([^)]*)\)`)
	oversRe      = regexp.MustCompile(`([\d.]+)(?:/[\d.]+)?\s*ov`)
	ballsRe      = regexp.MustCompile(`(\d+)\s*balls?`)
	testTextRe   = regexp.MustCompile(`(?i)\bday\s*\d|\bstumps\b|\bsession\b|\btest\b`)
	totalOversRe = regexp.MustCompile(`[\d.]+/(\d+)\s*ov`)
	targetRe     = regexp.MustCompile(`(?i)target\s*(\d+)`)
)

// IsTestMatch reports whether this is multi-day cricket, where the T20
// chase math doesn't apply.
func IsTestMatch(m Match) bool {
	et := strings.ToLower(m.EventType)
	if et != "" {
		return et == "test" || et == "fc" || et == "first class" || et == "first-class"
	}
	for _, t := range m.Teams {
		if strings.Contains(t.Score, "&") {
			return true
		}
	}
	text := strings.Join([]string{m.StatusText, m.Title, m.Note, m.Description}, " ")
	return testTextRe.MatchString(text)
}

// ParseScoreString parses ESPN cricket score strings.
//
//	"311"                        -> (311, 10, nil)   completed innings
//	"96/1 (19 ov)"               -> (96, 1, 19.0)
//	"67/1 (8.5/20 ov, target 160)" -> (67, 1, 8.5)
//	"159/9 (96 balls)"           -> (159, 9, 16.0)
//	"311 & 96/1 (19 ov)"         -> last innings segment (Tests)
func ParseScoreString(score string) (runs, wickets int, overs *float64, ok bool) {
	if strings.TrimSpace(score) == "" {
		return 0, 0, nil, false
	}
	parts := strings.Split(score, "&")
	segment := strings.TrimSpace(parts[len(parts)-1])

	if loc := parenRe.FindStringSubmatchIndex(segment); loc != nil {
		inner := segment[loc[2]:loc[3]]
		if m := oversRe.FindStringSubmatch(inner); m != nil {
			if v, err := strconv.ParseFloat(m[1], 64); err == nil {
				overs = &v
			}
		} else if m := ballsRe.FindStringSubmatch(inner); m != nil {
			if balls, err := strconv.Atoi(m[1]); err == nil {
				v := float64(balls/6) + float64(balls%6)/10
				overs = &v
			}
		}
		segment = strings.TrimSpace(segment[:loc[0]])
	}
	segment = strings.TrimSpace(strings.TrimRight(segment, "d "))

	if strings.Contains(segment, "/") {
		bits := strings.SplitN(segment, "/", 2)
		r, err1 := strconv.Atoi(strings.TrimSpace(bits[0]))
		w, err2 := strconv.Atoi(strings.TrimSpace(bits[1]))
		if err1 != nil || err2 != nil {
			return 0, 0, nil, false
		}
		return r, w, overs, true
	}
	r, err := strconv.Atoi(segment)
	if err != nil {
		return 0, 0, nil, false
	}
	return r, 10, overs, true
}

// ToMatchState converts a simplified match into an explainer.MatchState.
// Tuned for limited-overs cricket; returns nil for multi-day Tests rather
// than mis-reading them as a T20 chase.
func ToMatchState(m Match) *explainer.MatchState {
	if len(m.Teams) != 2 || IsTestMatch(m) {
		return nil
	}
	totalOvers := 20
	if strings.Contains(strings.ToLower(m.EventType), "odi") {
		totalOvers = 50
	}

	type parsed struct {
		runs, wickets int
		overs         *float64
		ok            bool
	}
	var p [2]parsed
	for i, t := range m.Teams {
		p[i].runs, p[i].wickets, p[i].overs, p[i].ok = ParseScoreString(t.Score)
	}
	if !p[0].ok && !p[1].ok {
		return nil
	}

	// Batting side: the score string carrying an overs annotation.
	battingIdx := -1
	for i := range p {
		if p[i].ok && p[i].overs != nil {
			battingIdx = i
		}
	}
	if battingIdx == -1 {
		for i := range p {
			if p[i].ok {
				battingIdx = i
				break
			}
		}
	}
	bowlingIdx := 1 - battingIdx

	state := explainer.MatchState{
		BattingTeam: m.Teams[battingIdx].Name,
		BowlingTeam: m.Teams[bowlingIdx].Name,
		Runs:        p[battingIdx].runs,
		Wickets:     p[battingIdx].wickets,
		TotalOvers:  totalOvers,
		Innings:     1,
		Notes:       m.StatusText,
	}
	if p[battingIdx].overs != nil {
		state.Overs = *p[battingIdx].overs
	}
	if p[bowlingIdx].ok && p[bowlingIdx].overs == nil {
		// Other side has a completed innings on the board -> a chase.
		state.Innings = 2
		target := p[bowlingIdx].runs + 1
		state.Target = &target
	}

	// Some score strings state it outright: "(8.5/20 ov, target 160)".
	rawBat := m.Teams[battingIdx].Score
	if mt := totalOversRe.FindStringSubmatch(rawBat); mt != nil {
		if v, err := strconv.Atoi(mt[1]); err == nil {
			state.TotalOvers = v
		}
	}
	if mt := targetRe.FindStringSubmatch(rawBat); mt != nil {
		if v, err := strconv.Atoi(mt[1]); err == nil {
			state.Innings = 2
			state.Target = &v
		}
	}
	return &state
}

// tunedTransport keeps enough idle connections for burst traffic. Go's
// default (MaxIdleConnsPerHost: 2) forces a fresh TLS handshake on nearly
// every request once concurrency rises, which shows up as latency spikes
// exactly when the site is busiest.
func tunedTransport() *http.Transport {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.MaxIdleConns = 200
	t.MaxIdleConnsPerHost = 100
	t.MaxConnsPerHost = 0 // unlimited in-flight; idle pool is what matters
	t.IdleConnTimeout = 90 * time.Second
	return t
}
