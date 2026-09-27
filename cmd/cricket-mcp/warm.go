package main

import (
	"time"

	"github.com/asaraog/mcp-cricket/internal/kalshi"
	"github.com/asaraog/mcp-cricket/internal/milclive"
)

// The stdio server warms what the hosted server warms at boot, for the same
// reason.
//
// Kalshi's market scan is served from cache and filled in the background,
// so a process that has never read it answers from an empty one. The
// hosted server starts the fill at boot; this binary never did, so a local
// client's first question about a priced upcoming Minor League game would
// wait on a direct event read, and cricket_market_odds on the scan itself.
// The Minor League listing is the same story: its first read waits at most
// 1.5 seconds for a cold fill.
//
// A client launches this server when it starts, well before the first tool
// call, so a fill started at launch is done before anything asks. It is in
// init, in its own file, so main.go stays the protocol loop and nothing
// else; MILC_LIVE=off still turns the Minor League fill into a no-op.
func init() {
	go kalshi.WarmScan()
	go milclive.Warm(5 * time.Second)
}
