// Package explainer turns raw cricket match state into plain English with
// baseball framing. Win probabilities are interpretable logistic models
// calibrated on Cricsheet ball-by-ball archives — transparent and
// reproducible, not a betting model.
package explainer

import (
	"fmt"
	"math"
	"strings"
)

const BallsPerOver = 6

// OversToBalls converts cricket overs notation (16.2 = 16 overs + 2 balls,
// NOT 16.2 decimal) to a ball count.
func OversToBalls(overs float64) (int, error) { return OversToBallsN(overs, BallsPerOver) }

// OversToBallsN is OversToBalls for formats whose over is not six balls —
// The Hundred bowls five-ball sets, and 13.5 there means 70 balls, not 83.
func OversToBallsN(overs float64, bpo int) (int, error) {
	whole := int(overs)
	balls := int(math.Round((overs - float64(whole)) * 10))
	// ESPN writes a just-completed over as x.6 — that's x+1 whole overs.
	if balls == bpo {
		whole, balls = whole+1, 0
	}
	if balls < 0 || balls >= bpo {
		return 0, fmt.Errorf("invalid overs notation: %v (digit after the point must be 0-5)", overs)
	}
	return whole*bpo + balls, nil
}

// BallsToOversStr renders a ball count in cricket notation.
func BallsToOversStr(balls int) string {
	return fmt.Sprintf("%d.%d", balls/BallsPerOver, balls%BallsPerOver)
}

// MatchState is a snapshot of a limited-overs innings in progress.
type MatchState struct {
	BattingTeam string  `json:"batting_team"`
	BowlingTeam string  `json:"bowling_team"`
	Runs        int     `json:"runs"`
	Wickets     int     `json:"wickets"`
	Overs       float64 `json:"overs"` // cricket notation
	TotalOvers  int     `json:"total_overs"`
	Innings     int     `json:"innings"`
	Target      *int    `json:"target,omitempty"` // runs needed to win (chase)
	Notes       string  `json:"notes,omitempty"`
	// BPO is balls per over: zero means the standard six. The Hundred is
	// five, and every balls-left and run-rate figure below flows from it —
	// with six assumed, a Hundred innings was priced and narrated as 120
	// deliveries when only 100 exist.
	BPO int `json:"balls_per_over,omitempty"`
}

func (s MatchState) bpo() int {
	if s.BPO > 0 {
		return s.BPO
	}
	return BallsPerOver
}

func (s MatchState) BallsBowled() int {
	b, err := OversToBallsN(s.Overs, s.bpo())
	if err != nil {
		return 0
	}
	return b
}

func (s MatchState) BallsLeft() int {
	left := s.TotalOvers*s.bpo() - s.BallsBowled()
	if left < 0 {
		return 0
	}
	return left
}

func (s MatchState) WicketsInHand() int { return 10 - s.Wickets }

func (s MatchState) CurrentRunRate() float64 {
	if s.BallsBowled() == 0 {
		return 0
	}
	return float64(s.Runs) / float64(s.BallsBowled()) * float64(s.bpo())
}

// RequiredRunRate returns (rate, true) during a chase with balls remaining.
func (s MatchState) RequiredRunRate() (float64, bool) {
	if s.Target == nil || s.BallsLeft() == 0 {
		return 0, false
	}
	needed := float64(*s.Target - s.Runs)
	if needed < 0 {
		needed = 0
	}
	return needed / float64(s.BallsLeft()) * float64(s.bpo()), true
}

// ParScore is a very rough par first-innings total by format.
func ParScore(totalOvers int) int {
	// Shares parFor's scaling so the par a user reads matches the par
	// the win model anchors on (165 at 20 overs, 285 at 50).
	return int(math.Round(parFor(totalOvers)))
}

// ProjectedScore projects a first innings: current rate, damped by wickets lost.
func ProjectedScore(s MatchState) int {
	if s.BallsBowled() == 0 {
		return ParScore(s.TotalOvers)
	}
	damping := 0.6 + 0.04*float64(s.WicketsInHand())
	rest := s.CurrentRunRate() * damping * (float64(s.BallsLeft()) / float64(s.bpo()))
	return int(math.Round(float64(s.Runs) + rest))
}

// winSeg is one fitted logistic segment: p = sigmoid(b + c·x).
//
// Coefficients were fitted on 8,184 completed Cricsheet matches (2.65M
// per-ball states across T20I, IPL, MLC, BBL, PSL, CPL, and ODIs; ties
// count as half credit, rain-adjusted results excluded), with pre-match
// Elo team ratings (see elo.go) as the strength signal. Variants were
// selected on the training split only; the 20% by-match held-out split
// was scored once. Held-out log-loss vs the original hand-tuned
// constants:
//
//	T20 innings 1: 0.603 (was 0.759)    T20 chase: 0.402 (was 0.516)
//	ODI innings 1: 0.546 (was 1.074)    ODI chase: 0.365 (was 0.967)
//
// Ball-state accuracy 75%, ~92% when the model is >80/20 confident; the
// estimated information ceiling for all-state accuracy is ~74-76%, so
// richer models buy little. Reduced-overs games are extrapolation (the
// training set is exactly-20 and exactly-50 over matches only).
type winSeg struct {
	// c[7] is the wickets x required-rate interaction, used by the chase
	// segments only: thin batting hurts far more when the ask is steep,
	// and without this term the model was measurably too pessimistic
	// with few wickets left. Innings-1 segments leave it zero.
	c [8]float64
	b float64
}

var winSegs = map[string]winSeg{
	// innings 1 features:
	// [proj-par, crr, wicketsInHand, progress, runs/par, elo, elo*remain]
	"t20-1": {c: [8]float64{0.003338036328069468, -0.01798275646119048, 0.27245776388269016, -1.9983790833924384, 3.915425351161525, 2.138784538198616, 0.48269187452768897, 0}, b: -2.541006187391477},
	"odi-1": {c: [8]float64{0.00375725184140388, -0.12900193157949036, 0.33321913583729357, -1.1465588513129303, 4.093939838211865, 2.0422874764820356, 0.35867166932611116, 0}, b: -2.5931749579036794},
	// chase features:
	// [ln-lb, ln-lw, rrr, lw*lb/10, progress, elo, elo*remain] where
	// ln/lb/lw = log1p(needed / ballsLeft / wicketsInHand)
	"t20-2": {c: [8]float64{0.7980191665882098, -2.5274714647102594, 3.4784165113743314e-05, 8.082712914324674, 2.5032795396640974, 0.28501287071395276, 2.2777580433475544, -1.4926209122934888}, b: -0.0811036881629809},
	"odi-2": {c: [8]float64{1.1972917441813171, -2.6696321543185575, -0.052917886727885804, 7.862527944904587, 3.76815232570906, 0.6624026154192848, 1.4661187027734173, -2.5009970091086413}, b: 1.0933351639881634},
}

func segFor(totalOvers, innings int) winSeg {
	k := "odi"
	if totalOvers <= 20 {
		k = "t20"
	}
	if innings >= 2 {
		return winSegs[k+"-2"]
	}
	return winSegs[k+"-1"]
}

// parFor anchors the fitted par (165 T20 / 285 ODI) and scales it for
// nonstandard overs counts, where the model extrapolates.
func parFor(totalOvers int) float64 {
	if totalOvers <= 20 {
		return 165.0 * float64(totalOvers) / 20.0
	}
	return 285.0 * float64(totalOvers) / 50.0
}

func sigmoid(z float64) float64 { return 1.0 / (1.0 + math.Exp(-z)) }

func clampProb(p float64) float64 { return math.Min(0.99, math.Max(0.01, p)) }

// ChaseWinProbability is the fitted P(batting side wins) during a chase.
func ChaseWinProbability(s MatchState) float64 {
	if s.Target == nil {
		return 0
	}
	needed := float64(*s.Target - s.Runs)
	if needed <= 0 {
		return 1
	}
	ballsLeft := float64(s.BallsLeft())
	wih := math.Max(float64(s.WicketsInHand()), 0)
	if ballsLeft == 0 || wih == 0 {
		return 0
	}
	total := float64(s.TotalOvers * BallsPerOver)
	rrr := math.Min(needed*6.0/ballsLeft, 36.0)
	ln, lb, lw := math.Log1p(needed), math.Log1p(ballsLeft), math.Log1p(wih)
	elo := eloDiffFeature(s)
	sg := segFor(s.TotalOvers, 2)
	z := sg.b + sg.c[0]*(ln-lb) + sg.c[1]*(ln-lw) + sg.c[2]*rrr +
		sg.c[3]*lw*lb/10.0 + sg.c[4]*((total-ballsLeft)/total) +
		sg.c[5]*elo + sg.c[6]*elo*(ballsLeft/total) +
		sg.c[7]*lw*rrr/10.0
	return clampProb(sigmoid(z))
}

// WinProb summarizes who's ahead.
type WinProb struct {
	BattingTeamWinProb float64 `json:"batting_team_win_prob"`
	Leader             string  `json:"leader"`
	Confidence         float64 `json:"confidence"`
}

// WinProbability estimates P(batting side wins) for either innings using
// the fitted segments above.
func WinProbability(s MatchState) WinProb {
	var p float64
	switch {
	case s.TotalOvers <= 0:
		p = 0.5 // malformed state: no basis for a lean either way
	case s.Innings >= 2 && s.Target != nil:
		p = ChaseWinProbability(s)
	default:
		par := parFor(s.TotalOvers)
		total := float64(s.TotalOvers * BallsPerOver)
		balls := float64(s.BallsBowled())
		wih := math.Max(float64(s.WicketsInHand()), 0)
		crr, proj := 0.0, par
		if balls > 0 {
			crr = float64(s.Runs) * 6.0 / balls
			proj = float64(s.Runs) + crr*(0.6+0.04*wih)*(math.Max(total-balls, 0)/6.0)
		}
		elo := eloDiffFeature(s)
		sg := segFor(s.TotalOvers, 1)
		z := sg.b + sg.c[0]*(proj-par) + sg.c[1]*crr + sg.c[2]*wih +
			sg.c[3]*(balls/total) + sg.c[4]*float64(s.Runs)/par +
			sg.c[5]*elo + sg.c[6]*elo*(math.Max(total-balls, 0)/total)
		p = clampProb(sigmoid(z))
	}
	leader := s.BattingTeam
	if p < 0.5 {
		leader = s.BowlingTeam
	}
	return WinProb{
		BattingTeamWinProb: round3(p),
		Leader:             leader,
		Confidence:         round3(math.Max(p, 1-p)),
	}
}

func round3(f float64) float64 { return math.Round(f*1000) / 1000 }

func ordinal(n int) string {
	switch {
	case n%10 == 1 && n%100 != 11:
		return fmt.Sprintf("%dst", n)
	case n%10 == 2 && n%100 != 12:
		return fmt.Sprintf("%dnd", n)
	case n%10 == 3 && n%100 != 13:
		return fmt.Sprintf("%drd", n)
	default:
		return fmt.Sprintf("%dth", n)
	}
}

func baseballInningEquivalent(s MatchState) string {
	total := s.TotalOvers * BallsPerOver
	if total == 0 {
		return ""
	}
	frac := float64(s.BallsBowled()) / float64(total)
	inning := int(math.Ceil(frac * 9))
	if inning < 1 {
		inning = 1
	}
	if inning > 9 {
		inning = 9
	}
	return fmt.Sprintf("on a baseball clock, this is roughly the %s inning", ordinal(inning))
}

// ExplainScore is the 'who is winning and what does the score mean' answer.
func ExplainScore(s MatchState) string {
	var lines []string
	lines = append(lines,
		fmt.Sprintf("%s are %d/%d after %v overs against %s. (An over = 6 pitched balls; this innings is %d overs total.)",
			s.BattingTeam, s.Runs, s.Wickets, s.Overs, s.BowlingTeam, s.TotalOvers),
		fmt.Sprintf("Read %d/%d as: %d runs scored, %d of their 10 outs used (%d wickets in hand).",
			s.Runs, s.Wickets, s.Runs, s.Wickets, s.WicketsInHand()),
		fmt.Sprintf("They've faced %d of %d deliveries — %s.",
			s.BallsBowled(), s.TotalOvers*BallsPerOver, baseballInningEquivalent(s)),
		fmt.Sprintf("Scoring pace: %.1f runs per over.", s.CurrentRunRate()),
	)

	if s.Innings == 2 && s.Target != nil {
		needed := *s.Target - s.Runs
		if needed < 0 {
			needed = 0
		}
		if needed == 0 {
			lines = append(lines, fmt.Sprintf("%s have won — target chased down.", s.BattingTeam))
		} else {
			rrr, ok := s.RequiredRunRate()
			chase := fmt.Sprintf("Chasing %d: they need %d more off %d balls", *s.Target, needed, s.BallsLeft())
			if ok {
				chase += fmt.Sprintf(" (required pace %.1f/over vs %.1f so far).", rrr, s.CurrentRunRate())
			} else {
				chase += "."
			}
			lines = append(lines, chase)
			if ok {
				gap := rrr - s.CurrentRunRate()
				switch {
				case gap <= 0:
					lines = append(lines, "They're ahead of the ask — the chasing side is on top.")
				case gap < 2:
					lines = append(lines, "It's tight — one big over or one wicket swings it.")
				default:
					lines = append(lines, "They're behind the required pace — like needing a big rally in the late innings.")
				}
			}
		}
	} else {
		lines = append(lines, fmt.Sprintf(
			"Projection: about %d by the end of this innings (par is ~%d).",
			ProjectedScore(s), ParScore(s.TotalOvers)))
	}

	wp := WinProbability(s)
	lines = append(lines, fmt.Sprintf("Heuristic win probability: %s favored (%d%%).",
		wp.Leader, int(wp.Confidence*100)))
	return strings.Join(lines, "\n")
}
