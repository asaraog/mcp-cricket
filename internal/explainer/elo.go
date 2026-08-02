package explainer

// Team strength for the win model: pre-computed Elo ratings from 8,614
// Cricsheet matches (T20 and ODI pools kept separate; K=24, chronological
// so each match only sees earlier results). Unknown teams — exhibition
// sides, new franchises — fall back to a neutral rating with zero games,
// which zeroes the Elo feature and degrades gracefully to the pure
// match-state model.

import (
	_ "embed"
	"encoding/json"
	"strings"
	"sync"
)

//go:embed data/elo.json
var eloJSON []byte

// pool -> lowercased team -> [rating, games played]
var (
	eloOnce sync.Once
	eloPool map[string]map[string][2]float64
)

func eloLoad() {
	var raw map[string]map[string][2]float64
	if err := json.Unmarshal(eloJSON, &raw); err != nil {
		eloPool = map[string]map[string][2]float64{}
		return
	}
	eloPool = make(map[string]map[string][2]float64, len(raw))
	for pool, teams := range raw {
		m := make(map[string][2]float64, len(teams))
		for t, rg := range teams {
			m[strings.ToLower(strings.TrimSpace(t))] = rg
		}
		eloPool[pool] = m
	}
}

// eloFor returns (rating, gamesPlayed) for a team in the format's pool.
func eloFor(totalOvers int, team string) (float64, float64) {
	eloOnce.Do(eloLoad)
	pool := "50"
	if totalOvers <= 20 {
		pool = "20"
	}
	if rg, ok := eloPool[pool][strings.ToLower(strings.TrimSpace(team))]; ok {
		return rg[0], rg[1]
	}
	return 1500, 0
}

// eloDiffFeature is the fitted model's team-strength input: rating gap
// scaled to ~[-1,1] and shrunk toward 0 for teams with little history.
func eloDiffFeature(s MatchState) float64 {
	rBat, gBat := eloFor(s.TotalOvers, s.BattingTeam)
	rBowl, gBowl := eloFor(s.TotalOvers, s.BowlingTeam)
	g := gBat
	if gBowl < g {
		g = gBowl
	}
	conf := g / (g + 20.0)
	return (rBat - rBowl) / 400.0 * conf
}
