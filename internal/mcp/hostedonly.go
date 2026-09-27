package mcp

// cricket_minor_league_info in the local build.
//
// The hosted server answers questions about Minor League Cricket's past:
// seasons, teams, grounds and player records. That data is not published
// with this repository, so the local build cannot answer them. The tool is
// still listed, under the hosted server's name, description and schema
// exactly, so a client moving between the two sees one tool set, and an
// assistant that reaches for it is told where the data is instead of
// finding no tool at all. testdata/minor_league_info.hosted.json is the
// hosted tools/list entry this is checked against.

// hostedURL is the hosted MCP endpoint, the one the README leads with.
const hostedURL = "https://cricketfornoobs.com/mcp"

// minorLeagueInfoHosted is the local build's whole answer. One line, the
// same for every call: nothing here depends on the arguments.
const minorLeagueInfoHosted = "Minor League history, teams, grounds and player records are served by the hosted server at " + hostedURL +
	" (add it as a connector); this local build does not hold them."

func minorLeagueInfoToolDef() Tool {
	return Tool{
		Name: "cricket_minor_league_info",
		Description: "Player records and league history for Minor League Cricket (the US domestic T20 league), from data this server holds: " +
			"CricClubs scorecards for 2026 and results with grounds for 2023-2026, CricClubs' 2025 and 2026 batting tables, " +
			"Wikipedia's 2021-2024 squads and leader tables (CC BY-SA 4.0) and a research record that sources every fact. " +
			"Every argument is optional and they combine: player for a season-by-season card (add team when two players share a name); " +
			"team for where they are based and play, their finals and a season's captain, wicketkeeper, players and results; " +
			"team with opponent, date or both for one fixture's ground, result and 2026 top performers; ground; leaders with season and team; " +
			"season alone for its champion, final and awards; topic for league facts. With no arguments it lists what it can answer. " +
			"Each answer gives exact figures, its source and how current it is, and says what the data lacks. " +
			"Names are matched whole: a surname several players share gets a question back, and a name the data lacks is said to be missing, never answered with another player's figures. " +
			"Live scores and win chances are cricket_minor_league's.",
		InputSchema: obj(map[string]any{
			"player": str("a player's full name, e.g. 'Andries Gous'. A surname alone works when only one player has it"),
			"team": str("a team's name, e.g. 'Chicago Kingsmen', 'All Stars' or 'Kingsmen'; 'Atlanta Fire v Atlanta Lightning' is read as a fixture. " +
				"With player, it picks between players who share a name"),
			"opponent": str("the other team, for a fixture with team"),
			"date":     str("a day, YYYY-MM-DD, e.g. '2026-09-26': with team (and opponent) for that day's game, or alone for every game that day"),
			"season": map[string]any{
				"type": "integer", "minimum": 2020, "maximum": 2026,
				"description": "a season, 2020-2026. Alone, that season's champion, final and awards; with other arguments, it narrows them. Default 2026 where a season is needed",
			},
			"ground": str("a ground's name, e.g. 'Church Street Park', 'Grand Prairie' or 'NY Ovals'"),
			"leaders": map[string]any{
				"type":        "string",
				"enum":        []string{"runs", "wickets", "sixes", "fours", "fifties", "hundreds", "economy", "strike_rate", "batting_average", "bowling_average"},
				"description": "a season's leaders: runs or wickets for 2021-2026, the rest for 2026. Narrow with season and team",
			},
			"topic": map[string]any{
				"type":        "string",
				"enum":        []string{"overview", "format", "champions", "draft", "watch", "history", "mlc", "teams"},
				"description": "a league-level fact: overview, format (season and playoffs), champions (every final), draft (roster rules), watch (tickets and streaming), history (founding and renamed teams), mlc (the pathway to Major League Cricket), teams (conferences and divisions)",
			},
		}),
		handler: minorLeagueInfoTool,
	}
}

func minorLeagueInfoTool(_ map[string]any) (string, error) {
	return minorLeagueInfoHosted, nil
}
