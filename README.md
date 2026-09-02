# 🏏 Cricket MCP Server

[![MCP](https://img.shields.io/badge/MCP-server-blue)](https://modelcontextprotocol.io)
[![Go](https://img.shields.io/badge/go-1.25%2B-00ADD8)](https://go.dev)
[![License](https://img.shields.io/badge/license-BSD--3--Clause-green)](LICENSE)

> Cricket analytics for Claude Desktop, Claude Code, Cursor and any other MCP
> client — a calibrated win-probability model, 22,479 archived matches, and
> live prediction-market prices.

A [Model Context Protocol](https://modelcontextprotocol.io) server that gives
AI assistants real cricket knowledge: a **calibrated win-probability model**, a
**ball-by-ball archive of 22,000+ matches**, live prediction-market prices,
career records, and live scores.

Ask cricket questions in plain language and get answers computed from data
rather than recalled from training.

## ⚡ Add it in one step

It is hosted. There is nothing to install, download, build or configure.

```
https://cricketfornoobs.com/mcp
```

Paste that into your client's connector settings and you are done.

| Client | Where |
|---|---|
| **Claude** (free plan included) | Customize → Connectors → Add custom connector |
| **ChatGPT** | Settings → Apps → Developer mode → add server |
| **Claude Code** | `claude mcp add --transport http cricket https://cricketfornoobs.com/mcp` |
| **Cursor / VS Code** | one-click buttons at [cricketfornoobs.com/mcp](https://cricketfornoobs.com/mcp) |
| **Gemini CLI** | add `{"cricket": {"httpUrl": "https://cricketfornoobs.com/mcp"}}` to `~/.gemini/settings.json` |

No account, no API key, no data pipeline. Read-only.

Prefer to run it on your own machine? See [Quick start](#-quick-start) below:
same tools, one static binary.

## 💬 Things to ask it

- *"Who's winning the India match right now, and what does the model say?"*
- *"Who is favoured at 149 for 7 chasing 178 with three overs left?"*
- *"How does Kohli bat against Bumrah in T20s?"*
- *"What does the market think versus your model for Welsh Fire vs Southern Brave?"*
- *"How does Bumrah get his wickets — bowled, caught, lbw?"*
- *"What's Rashid Khan's dot-ball percentage in T20s?"*
- *"Is Kohli better batting first or chasing?"*
- *"Who does Rohit Sharma score most of his runs with?"*
- *"Does Grand Prairie Stadium favour chasing?"*
- *"Show me Pooran's death-overs record."*
- *"What actually is a googly?"*

The same win model runs in production at
**[cricketfornoobs.com](https://cricketfornoobs.com)**, a live cricket explainer
for American sports fans. This server exposes the analytics side of it to any
MCP client.

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
| `cricket_dismissals` | How a batter gets out, or how a bowler takes wickets |
| `cricket_discipline` | Dot-ball and boundary percentage, the numbers no scorecard shows |
| `cricket_situational` | A batter's record batting first versus chasing |
| `cricket_partnerships` | Runs added with each partner at the crease, and the best stand |
| `cricket_market_odds` | Live prediction-market prices (Kalshi) beside this model's number |
| `cricket_live_matches` | Matches live and upcoming right now |
| `cricket_explain_term` | Any cricket term, with its baseball equivalent |

All tools are read-only.

## 🚀 Quick start

### 1. Install

One static binary, no runtime, no interpreter, no dependencies.

```bash
go install github.com/asaraog/mcp-cricket/cmd/cricket-mcp@latest
```

Or download a prebuilt binary for macOS (Apple silicon or Intel), Linux
(x86-64 or arm64) or Windows from
[Releases](https://github.com/asaraog/mcp-cricket/releases).

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

**Par is per ground.** A first-innings score only means something relative to
what the ground usually yields, so the innings-one segments are fitted against
a table of 371 grounds and 7 leagues rather than one global constant. Real pars
run from 153.7 to 172.5 by league alone, and further by ground. Pass `venue`
(and `league`) to `cricket_win_probability` and the same 80/2 at ten overs is
47% at Chinnaswamy and 60% at Newlands. Without a venue it falls back
ground → league → global, which costs about 0.006 of held-out log loss.

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
