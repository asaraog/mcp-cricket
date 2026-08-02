// Package rag is a lexical (BM25) retrieval layer over a cricket knowledge
// corpus. Retrieval is per-question: only the chunks relevant to what the
// user asked ride into the LLM prompt, replacing the old ship-the-whole-
// glossary-every-call approach (smaller prompts, lower latency and token
// spend). BM25 over authored chunks beats embeddings here: zero deps, no
// embedding API, deterministic, and the corpus is small and curated.
package rag

import (
	"fmt"

	"github.com/asaraog/cricket-mcp/internal/commentary"
	"github.com/asaraog/cricket-mcp/internal/glossary"
)

// Doc is one retrievable knowledge chunk.
type Doc struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Text  string `json:"text"`
	// Weight breaks ranking ties between same-scoring docs (surname
	// collisions among ~10k players): career volume, not alphabet.
	Weight float64 `json:"-"`
}

// authored covers what the glossary's one-liners can't: rules, formats,
// betting mechanics, and strategy — the questions confused bettors actually
// ask (several taken verbatim from Kalshi's cricket social feed).
var authored = []Doc{
	{ID: "formats", Title: "Cricket formats: T20, ODI, Test",
		Text: "Cricket has three main formats. T20: 20 overs (120 balls) per side, about 3 hours — what Major League Cricket and The Hundred-style leagues play. ODI (One Day International): 50 overs per side, a full day. Test: up to 5 days, each team bats twice (two innings each); the match can end with no winner (a draw). Shorter formats always produce a winner or a tie."},
	{ID: "test-draw", Title: "How a Test match can end in a draw",
		Text: "A Test is drawn when time runs out (5 days) before both sides complete their innings and neither wins. A draw is NOT a tie: a tie means scores finished exactly level (extremely rare); a draw means the game simply wasn't finished. Rain and bad light eat playing time and make draws more likely — that's why weather forecasts move Test draw prices on prediction markets."},
	{ID: "test-length", Title: "How long does a Test match last?",
		Text: "Up to 5 days, roughly 90 overs per day. Each team can bat twice. Many finish in 3-4 days. If neither side is bowled out twice and no target is chased by the end of day 5, it's a draw. Common bettor mistake: expecting a same-day result like T20."},
	{ID: "follow-on", Title: "The follow-on",
		Text: "In a Test, if the team batting second trails by 200+ runs after first innings, the leading captain may make them bat again immediately (the follow-on). It saves time and increases the chance of forcing a result instead of a draw."},
	{ID: "declaration", Title: "Declaring an innings",
		Text: "A Test captain can declare — voluntarily end their innings — to leave time to bowl the opponent out. Declaring trades runs for time. It's like intentionally ending your at-bats because you're far enough ahead and need outs before the clock kills the game."},
	{ID: "innings-structure", Title: "Innings and how a match progresses",
		Text: "In limited-overs cricket each team bats once: the first team sets a total; the second team chases target = total + 1. The chase ends when the target is passed, the overs run out, or 10 wickets fall. In Tests each team bats twice and the fourth innings is the chase."},
	{ID: "score-notation", Title: "Reading a cricket score like 145/3",
		Text: "145/3 means 145 runs scored and 3 wickets (outs) lost — each team has 10. '(16.2 ov)' means 16 overs and 2 balls bowled — the decimal counts balls (0-5), not tenths. Australia reverses it (3/145). A score with '&' (311 & 96/1) is a Test showing both innings."},
	{ID: "rrr", Title: "Required run rate and reading a chase",
		Text: "Required run rate (RRR) = runs still needed divided by overs left. Compare it with the current run rate: chasing side ahead if RRR is below what they've been scoring. Wickets in hand matter as much: 8 wickets left justifies risk; 2 left means the chase usually dies. RRR above ~12 in a T20 is desperate territory."},
	{ID: "dls", Title: "DLS: rain-shortened matches",
		Text: "When rain shortens a limited-overs match, the Duckworth-Lewis-Stern method recalculates a fair target from the resources each side had — balls remaining AND wickets in hand, via official tables. The chasing side wins if ahead of the DLS par score when play stops, loses if behind; 60/0 and 60/5 at the same over are very different DLS positions. Bets and markets settle on the DLS result. If too few overs are possible (usually under 5 per side in T20), the match is abandoned — check each market's rules for what happens then. Scoreboards publish the official par during delays; this app doesn't compute its own. Baseball has no DLS equivalent — shortened MLB games revert to the last completed inning."},
	{ID: "powerplay", Title: "Powerplay and fielding restrictions",
		Text: "In a T20's first 6 overs only 2 fielders may stand outside the inner circle, so batters attack — expect 45-60 runs in a good powerplay. After it, up to 5 boundary riders make big hitting harder. Scoring pace usually dips mid-innings and spikes again at the death (overs 17-20)."},
	{ID: "death-overs", Title: "Death overs",
		Text: "The final overs (17-20 in T20) when batting sides go all-out — often 10-15 runs per over. Teams save their best yorker bowlers for this phase. A chase that looks behind can flip fast here, which is why in-play prices swing hardest at the death."},
	{ID: "dismissals", Title: "The main ways a batter gets out",
		Text: "Bowled (ball hits the stumps), caught (any hit caught on the fly — no foul territory), LBW (leg blocks a ball that would hit the stumps), run out (stumps broken mid-run), stumped (keeper breaks the stumps when the batter leaves the crease). Ten outs end an innings — each wicket is a big win-probability event."},
	{ID: "extras", Title: "Wides, no-balls, byes: free runs",
		Text: "A wide (unreachable ball) or no-ball (illegal delivery, e.g. overstepping) gifts a run and must be re-bowled; in T20 a no-ball also gives a free hit, where the batter can't be out except run out. Byes and leg-byes are runs taken when the ball misses the bat. Extras add up — 10+ extras is sloppy bowling."},
	{ID: "drs", Title: "DRS reviews",
		Text: "The Decision Review System lets each side challenge umpire calls using ball-tracking and edge-detection tech. Teams get limited reviews per innings (lost if the challenge fails). Like a replay challenge in the NFL — a successful LBW review can flip a match moment."},
	{ID: "super-over", Title: "Ties and the Super Over",
		Text: "If a T20 ends with scores level, a Super Over decides it: one over each, best score wins; if that ties too, they repeat. Like extra innings but one-shot. Test matches don't do this — level scores in a Test is a tie (vanishingly rare), unfinished is a draw."},
	{ID: "toss-pitch", Title: "The toss and pitch conditions",
		Text: "The coin-toss winner chooses to bat or bowl first. Pitches change: fresh pitches favor pace bowling, wearing pitches favor spin later. Dew in evening games makes bowling second harder, so T20 toss winners often chase. Toss and conditions shift pre-match prices a few points."},
	{ID: "spin-pace", Title: "Pace bowling vs spin bowling",
		Text: "Pace bowlers (like power pitchers) rely on speed (80-95 mph), bounce, and swing. Spinners bowl slow (45-60 mph) and break the ball off the pitch like a big curveball. Spin dominates the middle overs and on worn pitches; pace rules with the new ball and at the death."},
	{ID: "mlc", Title: "Major League Cricket (MLC)",
		Text: "MLC is the professional T20 league in the United States, playing summer seasons with franchise teams like the Washington Freedom, San Francisco Unicorns, LA Knight Riders, MI New York, Texas Super Kings, and Seattle Orcas. Minor League Cricket (MiLC) is the development tier. Standard T20 rules: 20 overs, powerplay, super over for ties."},
	{ID: "moneyline", Title: "Moneyline odds and implied probability",
		Text: "American moneyline: -150 means bet 150 to win 100 (implied probability 60%); +200 means bet 100 to win 200 (implied 33%). Convert: negative odds → odds/(odds+100); positive → 100/(odds+100). Sportsbooks build in vig, so implied probabilities across all outcomes sum to over 100%."},
	{ID: "kalshi-mechanics", Title: "How Kalshi event contracts work",
		Text: "Kalshi is a CFTC-regulated exchange where you trade yes/no contracts priced 1-99 cents; each pays $1 if it resolves yes. The price IS the market's implied probability — 39 cents means the crowd prices a 39% chance. Unlike a sportsbook, you trade against other people and can sell before the match resolves. A cricket match event lists a market per team, plus Draw/Tie for Tests."},
	{ID: "in-play", Title: "In-play (live) betting basics",
		Text: "Live prices move with every ball — a wicket or a big over can move win probability 5-15 points. The key live numbers: required run rate vs current rate, wickets in hand, and balls remaining. Markets often overreact to single boundaries and underreact to wickets. Prices always lag the ground by seconds — never assume you're first."},
	{ID: "totals", Title: "Totals and prop markets in cricket",
		Text: "Beyond match winner: over/under total runs, individual batter runs, top scorer, and per-over props. A par first-innings T20 score is ~160-175; ~285 in ODIs — but ground size and pitch shift par massively, so judge totals against that venue's history, not a universal number."},
	{ID: "positions-map", Title: "Fielding positions, quickly",
		Text: "Off side = the batter's bat side; leg side = behind their legs. Slips wait beside the keeper for edges. Point and cover patrol square and forward of the batter on the off side; midwicket and square leg mirror them on the leg side; third man and fine leg guard behind; long-on and long-off patrol the straight boundary. 'Deep' before any name = pushed to the boundary."},
	{ID: "the-hundred", Title: "The Hundred: the 100-ball format",
		Text: "The Hundred is England's 100-ball format: each side faces exactly 100 legal balls, delivered in 20 sets of FIVE balls each. A set is FIVE balls — never ten. The confusion: a single bowler may bowl TWO consecutive five-ball sets (10 balls in a row), but the set itself is always five. Bowlers max out at 20 balls each; a 25-ball powerplay starts each innings. The math: 20 sets x 5 balls = 100. Otherwise it plays like T20."},
	{ID: "dead-ball-law", Title: "When the ball is dead (and when it is not)",
		Text: "The ball becomes dead when: it settles finally with the keeper or bowler, a boundary is scored, a batter is dismissed, it lodges in a player's equipment, or the umpire calls over/time/dead ball (e.g. serious injury, or the batter not ready). The ball is NOT dead just because it hits the stumps without a dismissal — overthrows off the stumps stay live and batters may keep running. A keeper catching a missed ball does not instantly kill it either; byes can be attempted until the ball is settled. A ball that accidentally hits an UMPIRE stays live (runs can still be run) — it is only dead if it lodges in the umpire's clothing or gear."},
	{ID: "equipment-facts", Title: "Equipment rules: bats, balls, gloves",
		Text: "Bat (Law 5): limits on length (38in), width (4.25in), depth and edge thickness — but NO weight limit; players choose their own weight. Ball: 156-163g in men's cricket, 140-151g in women's. Only the wicketkeeper may wear gloves in the field. Stumps are wooden, 28 inches tall, topped by two bails. Since 2017 a delivery bouncing more than ONCE before the popping crease is a no-ball (it used to be twice). Balls per innings: Tests offer a new ball after 80 overs; ODIs use TWO new balls, one from each end, since 2011; T20s use one ball throughout."},
	{ID: "dismissals-list", Title: "All the ways out (current laws)",
		Text: "Since 2017 there are NINE main ways out (handled-the-ball was merged into obstructing the field): bowled, caught, LBW, run out, stumped, hit wicket, obstructing the field, hit the ball twice, and timed out. Mankad run-outs at the non-striker's end are a form of run out and fully legal."},
	{ID: "first-t20i", Title: "The first T20 internationals",
		Text: "The WOMEN played the first-ever T20 international: England v New Zealand in August 2004. The first men's T20I came about six months LATER: Australia v New Zealand at Eden Park in February 2005 (played in retro kits and beige). The women's game was first — never say the men's match came first."},
	{ID: "trivia-bank", Title: "Real cricket trivia (verified)",
		Text: "Safe, TRUE trivia to draw from: the 1939 'Timeless Test' (England v South Africa) was abandoned after NINE days because England's ship home was leaving. Don Bradman needed just 4 runs in his last innings for a career average of 100 and was bowled for a duck — 99.94. Two players have hit six sixes in an over in internationals: Herschelle Gibbs (2007 World Cup) and Yuvraj Singh (2007 T20 World Cup). The first World Cup was 1975 (women's was 1973, earlier!). A Test over in 1932 once took no runs off 8 balls (Australia used 8-ball overs until 1979). Use ONLY these or other facts you are certain of — never invent trivia."},
	{ID: "baseball-bridges", Title: "Canonical cricket-to-baseball mappings",
		Text: "Verified bridges — use these, do not improvise new ones: bowler=pitcher (straight arm), keeper=catcher, over=a pitcher's fixed 6-pitch turn, innings=one giant half-inning, run=each end-swap, six=home run, four=ground-rule-double energy, wide=ball way outside the zone, no-ball=balk/illegal pitch, yorker=unhittable low fastball at the shoe tops, bouncer=high-and-tight brushback, slower ball=changeup, googly=screwball, DRS=replay challenge, crease=the safe ground like a base, pavilion=clubhouse/dugout area. Fielders' hands: only the KEEPER wears gloves — every other cricket fielder catches BARE-HANDED, the opposite of baseball where all fielders wear a glove. There is NO true bullpen equivalent: all bowlers are already fielding on the field between spells — like position players who take scheduled turns pitching. Slips have no baseball twin (imagine shortstops placed for foul tips that count). Do not claim baseball adjusts targets for shortened games — it reverts to the last completed inning."},
	{ID: "test-facts", Title: "Test cricket: follow-on, ties, bad light, over rates",
		Text: "Follow-on (Law 14): the side batting first can make the other side bat again immediately if leading by 200+ in a 5-day match (150 for 3-4 days, 100 for 2 days, 75 for 1 day). A TIE is different from a draw and needs the final innings COMPLETED with scores level — it has happened twice ever in Tests; scores level when time runs out with wickets in hand is a DRAW. Bad light: since October 2010 the umpires alone decide when light is unsafe — batters are no longer 'offered the light'. Over-rate penalties: fines and World Test Championship point deductions for bowling overs too slowly; white-ball cricket adds an in-match penalty (an extra fielder inside the circle at the death)."},
	{ID: "market-mechanics", Title: "In-play price movers and the bid-ask spread",
		Text: "What moves in-play cricket prices: WICKETS crash the batting side's price hardest; boundaries push it up; and quiet, dry overs slowly SINK the batting side too, because the required rate climbs while resources stay flat — a scoreless over is bad news for the chasing team, not neutral. The bid-ask spread is the gap between the best buy (ask) and best sell (bid) price on the book — a definitional fact needing no live data: tighter spread = more liquid market; crossing it immediately costs you the spread."},
	{ID: "match-durations", Title: "How long each format takes",
		Text: "Typical durations: a T20 runs about 3 hours (The Hundred about 2.5). An ODI is a FULL-DAY event — roughly 7-8 hours with the innings break. A Test is up to five days of ~6 hours each. Never describe an ODI as a 3-4 hour game."},
	{ID: "famous-matches", Title: "Famous matches: exact facts",
		Text: "The '438 game' (Johannesburg, March 2006): Australia posted 434/4, South Africa chased 438/9 with one ball to spare — still the greatest ODI chase — 872 combined runs, the winning run coming off the 49.5th over (one ball to spare). Exactly TWO centuries were scored in it: Ricky Ponting's 164 and Herschelle Gibbs's 175 (not four). The 2023 ODI World Cup final: Australia beat India in Ahmedabad, Travis Head 137. Brathwaite 2016 T20 WC final: needing 19 off the last over, Carlos Brathwaite hit FOUR CONSECUTIVE SIXES off Ben Stokes' first four balls — West Indies won with two balls to spare."},
	{ID: "mlc-stars", Title: "MLC leaders and champions (league-only numbers)",
		Text: "MLC-ONLY career leaders from this archive (all seasons through 2026): most runs — Nicholas Pooran (MI New York, 1,352), Faf du Plessis (Texas Super Kings, 1,226), Matthew Short (1,195), Andries Gous (USA/Unicorns, 1,058), Quinton de Kock (1,007). Most wickets — Trent Boult (MI New York, 54), Saurabh Netravalkar (USA, 49), Haris Rauf (SF Unicorns, 39), Marcus Stoinis (35). Champions: LA Knight Riders won the 2026 final (beat Washington Freedom); MI New York won 2025; the six franchises are in Dallas, San Francisco/Bay Area, Los Angeles, Washington DC, New York, and SEATTLE (Orcas) — there is NO Houston franchise. Season leading run-scorers: 2023 Nicholas Pooran (388), 2024 Faf du Plessis (420), 2025 Monank Patel (478), 2026 Andries Gous (547 — the single-season record). For 'best in MLC by the numbers', quote ONLY these MLC-only figures; any other 'MLC record' you remember is unreliable."},
	{ID: "ipl-titles", Title: "IPL champions by year",
		Text: "IPL champions: 2020 Mumbai Indians, 2021 Chennai Super Kings, 2022 Gujarat Titans, 2023 Chennai Super Kings, 2024 Kolkata Knight Riders, 2025 Royal Challengers Bengaluru — RCB's FIRST title after 18 years, beating Punjab Kings in the final (KKR won 2024, NOT RCB). Most titles: Mumbai Indians and Chennai Super Kings with five each. The IPL auction is an OPEN ASCENDING live auction with an auctioneer — franchises bid up from a base price using a salary purse. It is NOT a draft of any kind (no snake order, no picks). For 2026 onward, archived match data is authoritative."},
	{ID: "batting-basics", Title: "Strike rotation, blocking, and the bunt question",
		Text: "Two batters are in at once; the one facing is 'on strike'. The strike rotates when they run an ODD number of runs (1 or 3) and automatically at the end of each over — so the same batter does NOT face every ball, and picking who to face which bowler is real strategy. Batters are NEVER required to run after hitting. Can you bunt in cricket? Effectively YES, and it is routine, not a special play: defensive blocks, soft-hands dabs, and tap-and-run singles are core technique — a batter may deaden the ball at their feet and sprint a single any time. Never say bunting is impossible in cricket."},
	{ID: "t20-worldcup", Title: "T20 World Cup winners and facts",
		Text: "Men's T20 World Cup champions: 2007 India, 2009 Pakistan, 2010 England, 2012 West Indies, 2014 Sri Lanka, 2016 West Indies, 2021 Australia, 2022 England (beat Pakistan at the MCG — AUSTRALIA hosted that edition, not England), 2024 India (beat South Africa in Barbados; 20 teams; co-hosted by the USA and West Indies; the USA famously beat Pakistan in the group stage). No country has three men's titles — West Indies, England, and India have two each; Pakistan, Sri Lanka, and Australia one each. Now played roughly every two years; the 2026 edition was hosted by India and Sri Lanka. The 50-over ODI World Cup is a separate event: 2019 England, 2023 Australia — there was NO ODI World Cup in 2022."},
	{ID: "drs-reviews", Title: "DRS reviews: counts and umpire's call",
		Text: "Each team gets 2 unsuccessful reviews per innings in T20s and ODIs, and 3 per innings in Tests. Crucially, on UMPIRE'S CALL the on-field decision stands AND the reviewing team KEEPS its review — you only lose a review when the challenge clearly fails. The third umpire uses ball-tracking, UltraEdge/Snicko audio, and Hot Spot. LBW review logic: an OUT decision is overturned by an INSIDE EDGE (ball touched bat first) or tracking showing the ball missing or umpire's-call-margin on the stumps; a NOT OUT stands unless tracking shows all three red (pitching, impact, and hitting). 'Missed the bat' can never overturn an LBW out — LBW assumes the bat missed it."},
	{ID: "extras-law", Title: "Extras, overthrows, and who bats",
		Text: "Byes: runs taken when the ball passes the batter touching NEITHER bat NOR body. Leg byes: ball hits the batter's body (not the bat) and they run — allowed ONLY if the batter attempted a stroke or was avoiding the ball (Law 23); there are no other conditions. Wide: +1 run and the ball is re-bowled. No-ball: +1, re-bowled, and a free hit in white-ball cricket. Overthrows: when the hit came off the bat, overthrow runs ARE credited to the batter (Ben Stokes' deflected six in the 2019 World Cup final); otherwise they count as extras. And there is no designated hitter: ALL 11 players must bat, but only a few ever bowl — the specialization runs opposite to baseball's."},
	{ID: "t20-records", Title: "T20 records: biggest chases and totals",
		Text: "As of mid-2026, the biggest successful chase in ANY T20 is Punjab Kings hunting down 262 against Kolkata in IPL 2024. In T20 internationals the record chase is South Africa's 259/4 against West Indies in 2023 (Bulgaria's 246 in 2022 was a briefly-held record). Highest T20I total: Nepal's 314/3 v Mongolia (2023). These records move — treat anything newer in match data as authoritative."},
	{ID: "womens-rules", Title: "How women's cricket differs",
		Text: "Women's cricket uses the same laws and formats as men's with two physical differences: a slightly smaller, lighter ball (about 140-151g vs 156-163g) and shorter boundaries (women about 55-64 meters vs men roughly 59-82 — around 10-20 meters shorter). Overs, wickets, scoring, and dismissals are identical. The Women's Hundred, WPL (India), and WBBL (Australia) are the top T20-style leagues."},
	{ID: "womens-results", Title: "Women's cricket: recent champions and records",
		Text: "Recent women's world champions: India won the 2025 ODI World Cup (their first, beating South Africa in the final); New Zealand won the 2024 T20 World Cup; Australia won the 2023 T20 World Cup and the 2022 ODI World Cup, and their 2020 T20 World Cup final at the MCG drew 86,000+. Record: Amelia (Melie) Kerr's 232* for New Zealand v Ireland in 2018 is the highest individual score in women's ODIs — it predates our ball-by-ball archive, so archive aggregates for her understate career highs."},
	{ID: "mlc-venues", Title: "MLC teams and home venues",
		Text: "Major League Cricket venues: Grand Prairie Stadium near Dallas (the league's flagship ground, home of Texas Super Kings), Church Street Park in Morrisville NC, and the Oakland Coliseum, which has hosted San Francisco Unicorns home games. The six MLC cities are exactly: Dallas, San Francisco/Bay Area, Los Angeles, Washington DC, New York, and Seattle — Houston has NO MLC team; never list Houston."},
	{ID: "responsible", Title: "Responsible betting",
		Text: "Bet only where legal and only what you can afford to lose. Prediction-market prices are crowd opinion, not certainty; a 90-cent favorite still loses 1 time in 10. Set limits before the match starts."},
}

// Corpus returns every retrievable doc: the authored chunks, docs
// generated from the glossary and commentary-jargon maps (definitions
// stay in one source of truth), and per-player career lines derived from
// Cricsheet archives.
func Corpus() []Doc {
	docs := make([]Doc, 0, len(authored)+len(glossary.Terms)+8)
	docs = append(docs, authored...)
	docs = append(docs, playerDocs()...)
	for term, e := range glossary.Terms {
		docs = append(docs, Doc{
			ID:    "gloss-" + term,
			Title: term,
			Text:  fmt.Sprintf("%s — baseball parallel: %s. %s", term, e.Baseball, e.Plain),
		})
	}
	for term, plain := range commentary.Terms {
		docs = append(docs, Doc{
			ID:    "comm-" + term,
			Title: "commentary term: " + term,
			Text:  fmt.Sprintf("In ball-by-ball commentary, %q means: %s.", term, plain),
		})
	}
	return docs
}
