# 🏏 Cricket MCP Server

A [Model Context Protocol](https://modelcontextprotocol.io) server that gives
AI assistants real cricket knowledge: a **calibrated win-probability model**, a
**ball-by-ball archive of 22,000+ matches**, career records, and live scores.

Built for MCP clients such as Claude Desktop and Cursor, so you can ask cricket
questions in plain language and get answers computed from data instead of
recalled from training.

**No API key required.** The archive is built from the freely available
[Cricsheet](https://cricsheet.org) dataset, and live scores come from public
endpoints.

## ✨ What makes this different

Most sports MCP servers wrap a scores API. This one ships analysis:

- **Win probability from a fitted model** — logistic regression per format and
  innings over 17,907 matches (5.6M ball states), with pre-match Elo ratings.
  Held-out log loss 0.42 (T20 chases) / 0.40 (ODI chases); ~91% accurate on
  confident calls. It knows that 149/7 chasing 178 is not the same story as
  149/2.
- **Ball-by-ball archive** — 22,479 matches and 11.4M deliveries across T20,
  ODI/List-A, Tests and domestic multi-day cricket.
- **Career and matchup records** — batter-vs-bowler head-to-heads, phase
  splits (powerplay / middle / death), venue reports, league leaderboards.
- **Baseball translations** — every cricket term explained through its closest
  baseball equivalent, for newcomers to the sport.

## 🛠️ Tools

| Tool | What it does |
|------|-------------|
| `cricket_win_probability` | Win probability for any live or hypothetical match state |
| `cricket_head_to_head` | Career batter-vs-bowler record (balls, runs, dismissals, strike rate) |
| `cricket_player_career` | Career aggregates per format, men's and women's cricket |
| `cricket_match_archive` | Scorecard for an archived match, searched by teams / league / year |
| `cricket_phase_stats` | Batting and bowling split by powerplay, middle overs and death |
| `cricket_venue_stats` | Ground report: average first-innings score, chase win rate |
| `cricket_leaders` | League and season leaderboards for runs or wickets |
| `cricket_team_form` | A team's recent archived results |
| `cricket_live_matches` | Matches live and upcoming right now |
| `cricket_explain_term` | Any cricket term, with its baseball equivalent |

All tools are read-only.

## 🚀 Quick start

### 1. Get the binary

Prebuilt binaries are published for macOS (Apple silicon and Intel), Linux
(x86-64 and arm64) and Windows — a single static file with no runtime
dependencies. Or build from source:

```bash
go install github.com/asaraog/cricket-mcp/cmd/cricket-mcp@latest
```

### 2. Configure your MCP client

Add the server to your client's config — for Claude Desktop:

| OS | Config file |
|----|-------------|
| macOS | `~/Library/Application Support/Claude/claude_desktop_config.json` |
| Windows | `%APPDATA%\Claude\claude_desktop_config.json` |
| Linux | `~/.config/Claude/claude_desktop_config.json` |


```json
{
  "mcpServers": {
    "cricket": {
      "command": "/ABSOLUTE/PATH/TO/cricket-mcp"
    }
  }
}
```

Restart the client and the cricket tools appear. **That's the whole setup** —
on first use the server downloads the prebuilt archive once (~200 MB) into
your OS cache directory (`~/Library/Caches` on macOS, `~/.cache` on Linux,
`%LocalAppData%` on Windows) and reuses it from then on. No account, no API key, no
data pipeline to run.

<details>
<summary>Building the archive yourself instead</summary>

The archive is generated from public [Cricsheet](https://cricsheet.org) data,
so you can build your own rather than downloading ours:

```bash
curl -O https://cricsheet.org/downloads/all_json.zip
python3 scripts/histgen.py all_json.zip history.db
```

Then point `HISTORY_DB` at the result. Limited-overs-only archives work too —
tools degrade gracefully when a format is absent.
</details>

## 💬 Example prompts

- *"Who is favoured at 149 for 7 chasing 178 with three overs left?"*
- *"How does Kohli do against Bumrah in T20s?"*
- *"Does Grand Prairie Stadium favour chasing?"*
- *"Show me Pooran's death-overs record."*
- *"Who led the IPL run charts?"*
- *"What actually is a googly?"*

## ⚙️ Configuration

| Variable | Purpose |
|----------|---------|
| `HISTORY_DB` | Where the archive lives (default: your OS cache directory) |
| `HISTORY_DB_URL` | Override the archive download URL |
| `HISTORY_DB_TOKEN` | Bearer token, if that URL needs auth |
| `HISTORY_QUERY_TIMEOUT` | Query deadline, default `3s`; raise for heavy leaderboards |

Live-score tools work without any archive; archive tools report clearly when
the database is missing rather than inventing an answer.

## 📊 About the model

The win model is fitted offline, not guessed at runtime. Features are match
state (runs, wickets, balls remaining, required rate), pre-match Elo, and a
wickets × required-rate interaction — because thin batting hurts far more when
the asking rate is steep. Calibration is measured by wickets in hand: within
about one point across most of the range.

It cannot see injuries, weather, pitch reports or team news.

## 🙏 Data

Ball-by-ball data from [Cricsheet](https://cricsheet.org), licensed
[CC BY-SA 4.0](https://creativecommons.org/licenses/by-sa/4.0/). Live scores
from public ESPNcricinfo endpoints. This project is unaffiliated with either.

## 📝 License

BSD 3-Clause. See [LICENSE](LICENSE).
