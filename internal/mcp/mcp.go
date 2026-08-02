// Package mcp implements the Model Context Protocol surface for Cricket
// for Noobs: the tool set (win model, archive, matchups, live scores)
// plus JSON-RPC handling, shared by the stdio binary and the HTTP
// endpoint the website serves. Keeping tools here means the hosted
// server can stay private while remaining usable from any MCP client.
package mcp

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/asaraog/cricket-mcp/internal/cricinfo"
	"github.com/asaraog/cricket-mcp/internal/explainer"
	"github.com/asaraog/cricket-mcp/internal/glossary"
	"github.com/asaraog/cricket-mcp/internal/history"
	"github.com/asaraog/cricket-mcp/internal/kalshi"
	"github.com/asaraog/cricket-mcp/internal/matchup"
	"github.com/asaraog/cricket-mcp/internal/rag"
)

const (
	ServerName      = "cricket-for-noobs"
	ServerVersion   = "0.1.0"
	defaultProtocol = "2025-06-18"
)

// ---------------------------------------------------------------- protocol

type Request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcErr struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type Response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcErr         `json:"error,omitempty"`
}

// Tool is one callable capability plus its JSON Schema.
type Tool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
	// Annotations mark these as read-only, which MCP clients surface and
	// Anthropic's directory review requires.
	Annotations map[string]any `json:"annotations,omitempty"`
	handler     func(args map[string]any) (string, error)
}

func obj(props map[string]any, required ...string) map[string]any {
	s := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		s["required"] = required
	}
	return s
}

func str(desc string) map[string]any  { return map[string]any{"type": "string", "description": desc} }
func num(desc string) map[string]any  { return map[string]any{"type": "number", "description": desc} }
func inte(desc string) map[string]any { return map[string]any{"type": "integer", "description": desc} }

func Handle(req Request, tools []Tool, byName map[string]Tool) (Response, bool) {
	res := Response{JSONRPC: "2.0", ID: req.ID}
	switch req.Method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(req.Params, &p)
		version := p.ProtocolVersion
		if version == "" {
			version = defaultProtocol
		}
		res.Result = map[string]any{
			"protocolVersion": version,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": ServerName, "version": ServerVersion},
		}
	case "notifications/initialized", "notifications/cancelled":
		return res, true
	case "ping":
		res.Result = map[string]any{}
	case "tools/list":
		res.Result = map[string]any{"tools": tools}
	case "tools/call":
		var p struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			res.Error = &rpcErr{Code: -32602, Message: "bad params"}
			break
		}
		t, ok := byName[p.Name]
		if !ok {
			res.Error = &rpcErr{Code: -32601, Message: "unknown tool: " + p.Name}
			break
		}
		text, err := t.handler(p.Arguments)
		if err != nil {
			// Tool failures ride in the result with isError so the model
			// can react, per MCP convention.
			res.Result = map[string]any{
				"content": []any{map[string]any{"type": "text", "text": err.Error()}},
				"isError": true,
			}
			break
		}
		res.Result = map[string]any{
			"content": []any{map[string]any{"type": "text", "text": text}},
		}
	default:
		res.Error = &rpcErr{Code: -32601, Message: "method not found: " + req.Method}
	}
	return res, false
}

// ------------------------------------------------------------------- tools

func argStr(args map[string]any, key string) string {
	if v, ok := args[key].(string); ok {
		return strings.TrimSpace(v)
	}
	return ""
}

func argInt(args map[string]any, key string) (int, bool) {
	switch v := args[key].(type) {
	case float64:
		return int(v), true
	case int:
		return v, true
	}
	return 0, false
}

func argFloat(args map[string]any, key string) (float64, bool) {
	if v, ok := args[key].(float64); ok {
		return v, true
	}
	return 0, false
}

// ByName indexes tools for dispatch.
func ByName(ts []Tool) map[string]Tool {
	m := map[string]Tool{}
	for _, t := range ts {
		m[t.Name] = t
	}
	return m
}

func BuildTools() []Tool {
	ts := buildTools()
	for i := range ts {
		// Every tool here only reads cricket data — no writes, nothing
		// destructive. Clients surface this, and directory review
		// requires it.
		ts[i].Annotations = map[string]any{
			"readOnlyHint":    true,
			"destructiveHint": false,
			"openWorldHint":   true,
			"title":           ts[i].Name,
		}
	}
	return ts
}

func buildTools() []Tool {
	return []Tool{
		{
			Name:        "cricket_win_probability",
			Description: "Win probability for a live or hypothetical limited-overs match state, from a logistic model fitted on 8,000+ archived matches (per format and innings, with pre-match Elo). Returns the batting side's probability and who is favored. Use for 'who is winning' or 'what are the odds at X/Y'.",
			InputSchema: obj(map[string]any{
				"runs":         inte("runs scored so far by the batting side"),
				"wickets":      inte("wickets lost so far (0-10)"),
				"overs":        num("overs bowled in cricket notation, e.g. 15.3 = 15 overs 3 balls"),
				"total_overs":  inte("overs per side: 20 for T20, 50 for ODI"),
				"innings":      inte("1 for the side setting a target, 2 for the chase"),
				"target":       inte("runs needed to win (second innings only)"),
				"batting_team": str("optional team name, improves the estimate via Elo"),
				"bowling_team": str("optional team name, improves the estimate via Elo"),
			}, "runs", "wickets", "overs", "total_overs", "innings"),
			handler: winProbTool,
		},
		{
			Name:        "cricket_head_to_head",
			Description: "Career batter-vs-bowler record from ball-by-ball archives: balls faced, runs scored, dismissals, strike rate. Cricket tracks these like baseball's batter-vs-pitcher splits.",
			InputSchema: obj(map[string]any{
				"batter": str("batter name, e.g. 'Virat Kohli' or 'V Kohli'"),
				"bowler": str("bowler name, e.g. 'Jasprit Bumrah'"),
				"format": str("'t20' or 'odi'; omit to try T20 then ODI"),
			}, "batter", "bowler"),
			handler: headToHeadTool,
		},
		{
			Name:        "cricket_player_career",
			Description: "Career aggregate statistics for a player (men's and women's cricket) from Cricsheet ball-by-ball archives: innings, runs, strike rate, average, high score, wickets, economy — per format.",
			InputSchema: obj(map[string]any{
				"name": str("player name, e.g. 'Rachin Ravindra'"),
			}, "name"),
			handler: playerCareerTool,
		},
		{
			Name:        "cricket_match_archive",
			Description: "Look up an archived limited-overs match and return its scorecard: innings totals, top scorers, leading wicket-takers. Search by team names, league (mlc, ipl, bbl, psl, cpl), and/or year.",
			InputSchema: obj(map[string]any{
				"query": str("free text, e.g. 'the 2025 IPL final' or 'Washington Freedom vs San Francisco Unicorns 2026'"),
			}, "query"),
			handler: matchArchiveTool,
		},
		{
			Name:        "cricket_phase_stats",
			Description: "How a player performs by phase of the innings — powerplay, middle overs, and the death — for batting and bowling. This is the split that separates a strike-rate merchant from a genuine finisher, computed from ball-by-ball archives.",
			InputSchema: obj(map[string]any{
				"name":        str("player name, e.g. 'Nicholas Pooran'"),
				"total_overs": inte("20 for T20 (default), 50 for ODI/List-A"),
			}, "name"),
			handler: phaseStatsTool,
		},
		{
			Name:        "cricket_venue_stats",
			Description: "How a ground plays: matches recorded, average first-innings score, highest first-innings total, and how often the chasing side wins there. Useful for toss decisions and pre-match reads.",
			InputSchema: obj(map[string]any{
				"venue":       str("ground name or fragment, e.g. 'Grand Prairie' or 'Eden Gardens'"),
				"total_overs": inte("20 for T20 (default), 50 for ODI/List-A"),
			}, "venue"),
			handler: venueStatsTool,
		},
		{
			Name:        "cricket_leaders",
			Description: "Leaderboards for a league and optional season: most runs or most wickets, from ball-by-ball archives. Leagues include mlc, ipl, bbl, psl, cpl and international cricket.",
			InputSchema: obj(map[string]any{
				"league": str("league code, e.g. 'mlc', 'ipl', 'bbl'"),
				"year":   str("optional season year, e.g. '2026'"),
				"kind":   str("'batting' (default) or 'bowling'"),
				"limit":  inte("how many players to return (default 10)"),
			}, "league"),
			handler: leadersTool,
		},
		{
			Name:        "cricket_team_form",
			Description: "A team's most recent archived results — opponent, outcome, and match event — for reading current form.",
			InputSchema: obj(map[string]any{
				"team":  str("team name, e.g. 'San Francisco Unicorns'"),
				"limit": inte("how many recent matches (default 8)"),
			}, "team"),
			handler: teamFormTool,
		},
		{
			Name:        "cricket_market_odds",
			Description: "Live prediction-market prices for a cricket match from Kalshi (a CFTC-regulated US exchange), shown beside this server's own win probability so the two can be compared. Prices are cents that equal implied probability: 42 means the market prices a 42% chance. Informational only — not betting advice, and event contracts are legal only in some jurisdictions.",
			InputSchema: obj(map[string]any{
				"team_a": str("one team, e.g. 'San Francisco Unicorns'"),
				"team_b": str("the other team, e.g. 'Guyana Amazon Warriors'"),
			}, "team_a", "team_b"),
			handler: marketOddsTool,
		},
		{
			Name:        "cricket_live_matches",
			Description: "Currently live and upcoming cricket matches with scores where available (ESPNcricinfo public feeds).",
			InputSchema: obj(map[string]any{}),
			handler:     liveMatchesTool,
		},
		{
			Name:        "cricket_explain_term",
			Description: "Explain a cricket term in plain English with its closest baseball equivalent (wicket, yorker, googly, powerplay, DLS, and ~60 more).",
			InputSchema: obj(map[string]any{
				"term": str("cricket term, e.g. 'googly'"),
			}, "term"),
			handler: explainTermTool,
		},
	}
}

func winProbTool(args map[string]any) (string, error) {
	runs, ok1 := argInt(args, "runs")
	wkts, ok2 := argInt(args, "wickets")
	overs, ok3 := argFloat(args, "overs")
	total, ok4 := argInt(args, "total_overs")
	inn, ok5 := argInt(args, "innings")
	if !ok1 || !ok2 || !ok3 || !ok4 || !ok5 {
		return "", fmt.Errorf("need runs, wickets, overs, total_overs, innings")
	}
	st := explainer.MatchState{
		Runs: runs, Wickets: wkts, Overs: overs,
		TotalOvers: total, Innings: inn,
		BattingTeam: argStr(args, "batting_team"),
		BowlingTeam: argStr(args, "bowling_team"),
	}
	if st.BattingTeam == "" {
		st.BattingTeam = "batting side"
	}
	if st.BowlingTeam == "" {
		st.BowlingTeam = "bowling side"
	}
	if t, ok := argInt(args, "target"); ok && t > 0 {
		st.Target = &t
	}
	wp := explainer.WinProbability(st)
	var b strings.Builder
	fmt.Fprintf(&b, "%s: %.0f%% win probability (%s: %.0f%%).\n",
		st.BattingTeam, wp.BattingTeamWinProb*100, st.BowlingTeam, (1-wp.BattingTeamWinProb)*100)
	fmt.Fprintf(&b, "Favored: %s.\n", wp.Leader)
	fmt.Fprintf(&b, "State: %d/%d after %.1f of %d overs", runs, wkts, overs, total)
	if st.Target != nil {
		need := *st.Target - runs
		balls := st.BallsLeft()
		fmt.Fprintf(&b, ", chasing %d — %d needed from %d balls", *st.Target, need, balls)
		if balls > 0 {
			fmt.Fprintf(&b, " (%.2f per over required)", float64(need)*6/float64(balls))
		}
	}
	b.WriteString(".\nModel: logistic fit per format and innings on 8,000+ Cricsheet matches, with pre-match Elo. Held-out log loss 0.40 (T20) / 0.37 (ODI); well calibrated above 5 wickets in hand, slightly pessimistic below.")
	return b.String(), nil
}

func headToHeadTool(args map[string]any) (string, error) {
	batter, bowler := argStr(args, "batter"), argStr(args, "bowler")
	if batter == "" || bowler == "" {
		return "", fmt.Errorf("need batter and bowler")
	}
	format := strings.ToLower(argStr(args, "format"))
	r, ok := matchup.Lookup(format, batter, bowler)
	if !ok {
		return "", fmt.Errorf("no archived head-to-head for %s vs %s (needs 12+ legal balls faced in the ball-by-ball archive)", batter, bowler)
	}
	return fmt.Sprintf("%s (%s cricket)\n%d runs off %d balls, out %d times, strike rate %.1f.\nSource: Cricsheet ball-by-ball archives, pairs with 12+ legal balls.",
		r.Line(batter, bowler), strings.ToUpper(r.Format), r.Runs, r.Balls, r.Outs, r.StrikeRate), nil
}

func playerCareerTool(args map[string]any) (string, error) {
	name := argStr(args, "name")
	if name == "" {
		return "", fmt.Errorf("need a player name")
	}
	for _, h := range rag.Search(name, 3) {
		if strings.HasPrefix(h.Doc.ID, "player-") {
			return h.Doc.Title + "\n" + h.Doc.Text, nil
		}
	}
	return "", fmt.Errorf("no career record found for %q in the archive", name)
}

func matchArchiveTool(args map[string]any) (string, error) {
	query := argStr(args, "query")
	if query == "" {
		return "", fmt.Errorf("need a query")
	}
	if !history.Enabled() {
		return "", fmt.Errorf("archive unavailable: set HISTORY_DB to the ball-by-ball SQLite file")
	}
	m, ok := history.FindMatch(query, history.TeamsIn(query))
	if !ok {
		return "", fmt.Errorf("no archived match matched %q", query)
	}
	return history.Scorecard(m), nil
}

func liveMatchesTool(_ map[string]any) (string, error) {
	ms := cricinfo.LiveMatches()
	if len(ms) == 0 {
		return "No matches listed right now.", nil
	}
	var b strings.Builder
	for i, m := range ms {
		if i >= 15 {
			break
		}
		state := map[string]string{"in": "LIVE", "pre": "upcoming", "post": "finished"}[m.State]
		fmt.Fprintf(&b, "%s — %s", m.Title, state)
		if m.Summary != "" {
			fmt.Fprintf(&b, " (%s)", m.Summary)
		}
		if m.Date != "" && m.State == "pre" {
			fmt.Fprintf(&b, " starts %s", m.Date)
		}
		b.WriteString("\n")
	}
	return b.String(), nil
}

func totalOversArg(args map[string]any) int {
	if v, ok := argInt(args, "total_overs"); ok && (v == 20 || v == 50) {
		return v
	}
	return 20
}

func phaseStatsTool(args map[string]any) (string, error) {
	name := argStr(args, "name")
	if name == "" {
		return "", fmt.Errorf("need a player name")
	}
	if !history.Enabled() {
		return "", fmt.Errorf("archive unavailable: set HISTORY_DB")
	}
	out, ok := history.PhaseReport(name, totalOversArg(args))
	if !ok {
		return "", fmt.Errorf("no archived deliveries found for %q", name)
	}
	return out, nil
}

func venueStatsTool(args map[string]any) (string, error) {
	venue := argStr(args, "venue")
	if venue == "" {
		return "", fmt.Errorf("need a venue")
	}
	if !history.Enabled() {
		return "", fmt.Errorf("archive unavailable: set HISTORY_DB")
	}
	r, ok := history.VenueStats(venue, totalOversArg(args))
	if !ok {
		return "", fmt.Errorf("no archived matches at a venue matching %q", venue)
	}
	chasePct := 0.0
	if r.Decided > 0 {
		chasePct = float64(r.ChaseWins) * 100 / float64(r.Decided)
	}
	return fmt.Sprintf("%s\n%d archived matches. Average first-innings score %.0f (highest %d).\nChasing side wins %.0f%% of decided matches (%d of %d).",
		r.Venue, r.Matches, r.AvgFirst, r.HighestFirst, chasePct, r.ChaseWins, r.Decided), nil
}

func leadersTool(args map[string]any) (string, error) {
	league := argStr(args, "league")
	if league == "" {
		return "", fmt.Errorf("need a league")
	}
	kind := strings.ToLower(argStr(args, "kind"))
	if kind != "bowling" {
		kind = "batting"
	}
	limit, ok := argInt(args, "limit")
	if !ok || limit <= 0 || limit > 50 {
		limit = 10
	}
	rows, ok := history.Leaders(league, argStr(args, "year"), kind, limit)
	if !ok {
		return "", fmt.Errorf("no archived data for league %q", league)
	}
	var b strings.Builder
	season := argStr(args, "year")
	if season == "" {
		season = "all seasons"
	}
	fmt.Fprintf(&b, "%s leaders — %s, %s\n", strings.ToUpper(league), kind, season)
	for i, l := range rows {
		if kind == "bowling" {
			fmt.Fprintf(&b, "%2d. %s — %d wickets, economy %.2f\n", i+1, l.Name, l.Wkts, l.Econ)
			continue
		}
		sr := 0.0
		if l.Balls > 0 {
			sr = float64(l.Runs) * 100 / float64(l.Balls)
		}
		fmt.Fprintf(&b, "%2d. %s — %d runs off %d balls (strike rate %.1f)\n", i+1, l.Name, l.Runs, l.Balls, sr)
	}
	return b.String(), nil
}

func teamFormTool(args map[string]any) (string, error) {
	team := argStr(args, "team")
	if team == "" {
		return "", fmt.Errorf("need a team")
	}
	limit, ok := argInt(args, "limit")
	if !ok || limit <= 0 || limit > 30 {
		limit = 8
	}
	rows, ok := history.TeamForm(team, limit)
	if !ok {
		return "", fmt.Errorf("no archived matches for %q", team)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s — last %d archived matches\n", team, len(rows))
	for _, f := range rows {
		fmt.Fprintf(&b, "%s  %s vs %s", f.Date, strings.ToUpper(f.Result), f.Opponent)
		if f.Event != "" {
			fmt.Fprintf(&b, " (%s)", f.Event)
		}
		b.WriteString("\n")
	}
	return b.String(), nil
}

func marketOddsTool(args map[string]any) (string, error) {
	a, b := argStr(args, "team_a"), argStr(args, "team_b")
	if a == "" || b == "" {
		return "", fmt.Errorf("need team_a and team_b")
	}
	// The exchange scan is a background crawl; on a freshly started server
	// the first lookup would otherwise fail while it warms. Wait briefly
	// rather than reporting a market that does exist as missing.
	set, _, _, ok := kalshi.FindEventForTeams(a, b)
	for i := 0; !ok && i < 12; i++ {
		time.Sleep(5 * time.Second)
		set, _, _, ok = kalshi.FindEventForTeams(a, b)
	}
	if !ok || len(set.Markets) == 0 {
		return "", fmt.Errorf("no open market found for %s v %s — the exchange lists only some fixtures, mostly current T20 leagues", a, b)
	}
	var out strings.Builder
	fmt.Fprintf(&out, "Prediction-market prices for %s v %s:\n", a, b)
	for _, mk := range set.Markets {
		m := kalshi.WithQuotes(mk)
		if m.HasQuotes() {
			fmt.Fprintf(&out, "  %s — %.0f%% implied (%.0f cents)\n", m.Title, m.ImpliedProb*100, m.ImpliedProb*100)
			continue
		}
		fmt.Fprintf(&out, "  %s — listed, no trades yet\n", m.Title)
	}
	out.WriteString("\nA price is the crowd's implied probability. To judge whether it looks rich or cheap, compare it with cricket_win_probability for the current match state: the difference between the two is the edge a trader would be claiming. Remember the market can see injuries, weather and team news that a state-based model cannot.\n")
	out.WriteString("Informational only, not advice. Event contracts are legal in some US states and not others.")
	return out.String(), nil
}

func explainTermTool(args map[string]any) (string, error) {
	term := argStr(args, "term")
	if term == "" {
		return "", fmt.Errorf("need a term")
	}
	e := glossary.Lookup(term)
	if e == nil {
		return "", fmt.Errorf("no glossary entry for %q", term)
	}
	return fmt.Sprintf("%s\nBaseball equivalent: %s\n%s", e.Term, e.Baseball, e.Plain), nil
}
