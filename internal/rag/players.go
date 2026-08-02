package rag

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"
)

// Aggregated per-player career lines derived from Cricsheet ball-by-ball
// archives (all formats, men's and women's) — the open, structured
// stand-in for "the cricinfo database". Careers are split per format:
// a strike rate blended across T20 and Tests would mean nothing.
// Regenerate with cmd/statsgen from a fresh all_json.zip.
//
//go:embed data/playerstats.json
var playerStatsJSON []byte

type formatStat struct {
	Format     string  `json:"format"`
	BatInnings int     `json:"bat_innings"`
	Runs       int     `json:"runs"`
	Balls      int     `json:"balls"`
	Fours      int     `json:"fours"`
	Sixes      int     `json:"sixes"`
	High       int     `json:"high"`
	SR         float64 `json:"sr"`
	Avg        float64 `json:"avg"`
	Wickets    int     `json:"wickets"`
	BallsBowl  int     `json:"balls_bowled"`
	Econ       float64 `json:"econ"`
	Season     string  `json:"season"`
	SeasonRuns int     `json:"season_runs"`
	SeasonSR   float64 `json:"season_sr"`
}

type playerStat struct {
	Name    string       `json:"name"`
	Gender  string       `json:"gender"`
	Team    string       `json:"team"`
	Through string       `json:"through"`
	Formats []formatStat `json:"formats"`
}

// famousFeats: curated identity/feat lines merged into generated player
// docs — the archive can't know world records set before its window or
// what a player is famous FOR.
var famousFeats = map[string]string{
	"AC Kerr":         "Known as Melie Kerr, New Zealand's star leg-spinning all-rounder; her 232* v Ireland (2018) is the women's ODI world-record individual score and predates this archive's window — her real career high beats the archive number.",
	"NR Sciver-Brunt": "England's premier all-rounder and captain, famous for the 'Natmeg' — her trademark flick between her own legs.",
	"AJ Healy":        "Australia's wicketkeeper and captain, player of the final in the 2020 T20 World Cup and the 2022 ODI World Cup.",
	"Haris Rauf":      "Pakistan's express fast bowler, a death-overs specialist clocked among the fastest in the world.",
	"R Ravindra":      "Rachin Ravindra, New Zealand's left-handed top-order batting all-rounder, breakout star of the 2023 World Cup.",
	"SR Tendulkar":    "The real career: 34,357 international runs and 100 international centuries (1989-2013), the most ever — the archive window below captures only his final years.",
	"CM Edwards":      "England's legendary BATTER and long-time captain (2005-2016) — she was never a bowler.",
	"HC Knight":       "England's long-serving captain until 2025, when Nat Sciver-Brunt took over the captaincy.",
	"EA Perry":        "Australia's greatest all-rounder — note her T20I-only numbers are ~2,100 runs; bigger figures below span every T20 league she has played.",
	"V Kohli":         "India's modern batting great — the standard against whom T20/ODI chasing is measured.",
	"JJ Bumrah":       "India's generational fast bowler, the best death bowler of his era.",
}

func playerDocs() []Doc {
	var stats []playerStat
	if err := json.Unmarshal(playerStatsJSON, &stats); err != nil {
		return nil
	}
	docs := make([]Doc, 0, len(stats))
	for _, p := range stats {
		side := "men's"
		if p.Gender == "female" {
			side = "women's"
		}
		var b strings.Builder
		fmt.Fprintf(&b, "%s plays %s cricket, most recently for %s.", p.Name, side, p.Team)
		for _, f := range p.Formats {
			if f.Balls > 0 {
				fmt.Fprintf(&b, " All %s-format cricket combined (internationals AND franchise/domestic — NOT international-only numbers): %d innings, %d runs at strike rate %.1f", f.Format, f.BatInnings, f.Runs, f.SR)
				if f.Avg > 0 {
					fmt.Fprintf(&b, ", average %.1f", f.Avg)
				}
				fmt.Fprintf(&b, ", high score %d", f.High)
				if f.Season != "" && f.SeasonRuns > 0 {
					fmt.Fprintf(&b, "; %s season: %d runs at strike rate %.1f", f.Season, f.SeasonRuns, f.SeasonSR)
				}
				b.WriteString(".")
			}
			if f.BallsBowl > 0 {
				fmt.Fprintf(&b, " %s bowling, all competitions: %d wickets at economy %.1f.", f.Format, f.Wickets, f.Econ)
			}
		}
		fmt.Fprintf(&b, " (Career aggregates via Cricsheet through %s — totals only, not per-delivery or per-match records, not live.)", p.Through)
		id := "player-" + strings.ToLower(strings.ReplaceAll(p.Name, " ", "-"))
		if p.Gender == "female" {
			id += "-w"
		}
		weight := 0.0
		for _, f := range p.Formats {
			weight += float64(f.Balls + f.BallsBowl)
		}
		if extra, ok := famousFeats[p.Name]; ok {
			b.WriteString(" " + extra)
		}
		docs = append(docs, Doc{
			ID:     id,
			Title:  fmt.Sprintf("%s (%s)", p.Name, p.Team),
			Text:   b.String(),
			Weight: weight,
		})
	}
	return docs
}
