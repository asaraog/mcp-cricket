// Command cricket-mcp serves cricket data over the Model Context
// Protocol on stdin/stdout, for MCP clients that launch a local server
// (Claude Desktop, Claude Code, Cursor).
//
//	go build ./cmd/cricket-mcp && ./cricket-mcp
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/asaraog/mcp-cricket/internal/mcp"
)

func main() {
	tools := mcp.BuildTools()
	serve(os.Stdin, os.Stdout, tools, mcp.ByName(tools))
}

// serve answers JSON-RPC messages one line at a time, the framing MCP's
// stdio transport specifies, until in ends.
//
// It used to decode in as one JSON stream and "continue" past any decode
// error. A json.Decoder that hits a syntax error stays parked on the bad
// bytes and returns the same error on every later call, so one malformed
// message pinned a core at 100% and nothing after it was ever read. A line
// is its own unit: a bad one gets a JSON-RPC error back and the next line
// is read as usual.
func serve(in io.Reader, w io.Writer, tools []mcp.Tool, byName map[string]mcp.Tool) {
	r := bufio.NewReader(in)
	out := json.NewEncoder(w)
	for {
		line, err := r.ReadBytes('\n')
		if msg := bytes.TrimSpace(line); len(msg) > 0 {
			var req mcp.Request
			if derr := json.Unmarshal(msg, &req); derr != nil {
				_ = out.Encode(mcp.Unreadable(req.ID, derr))
			} else if resp, notify := mcp.Handle(req, tools, byName); !notify {
				_ = out.Encode(resp)
			}
		}
		if err != nil {
			// io.EOF is the client closing stdin; the last line may have
			// had no newline, and was answered above.
			if err != io.EOF {
				fmt.Fprintf(os.Stderr, "cricket-mcp: reading stdin: %v\n", err)
			}
			return
		}
	}
}
