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
| `cricket_market_odds` | Live prediction-market prices (Kalshi) beside this model's number |
| `cricket_live_matches` | Matches live and upcoming right now |
| `cricket_explain_term` | Any cricket term, with its baseball equivalent |

All tools are read-only.

## 🚀 Quick start

### 1. Install

One static binary, no runtime, no interpreter, no dependencies.

```bash
go install github.com/asaraog/cricket-mcp/cmd/cricket-mcp@latest
```

Or download a prebuilt binary for macOS (Apple silicon or Intel), Linux
(x86-64 or arm64) or Windows from
[Releases](https://github.com/asaraog/cricket-mcp/releases).

Register it with Claude Code:

```bash
claude mcp add cricket -- ~/go/bin/cricket-mcp
```

### 2. Or configure a desktop client

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
- *"What does the market think versus your model for Welsh Fire vs Southern Brave?"*

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

## 📈 Markets

`cricket_market_odds` reads public prices from [Kalshi](https://kalshi.com), a
CFTC-regulated US exchange where contracts settle at $1 and a price in cents
*is* the implied probability. Put beside `cricket_win_probability`, the gap
between the two is the edge a trader would be claiming.

This is informational only — read-only market data, no account, no orders, no
advice. The model cannot see injuries, weather or team news, which is often
exactly why it disagrees with the market. Event contracts are legal in some
jurisdictions and not others.

## 🙏 Data

Ball-by-ball data from [Cricsheet](https://cricsheet.org), licensed
[CC BY-SA 4.0](https://creativecommons.org/licenses/by-sa/4.0/). Live scores
from public ESPNcricinfo endpoints; market prices from Kalshi's public API.
This project is unaffiliated with any of them.

## 📝 License

BSD 3-Clause. See [LICENSE](LICENSE).
