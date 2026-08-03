// Command cricket-mcp serves cricket data over the Model Context
// Protocol on stdin/stdout, for MCP clients that launch a local server
// (Claude Desktop, Claude Code, Cursor).
//
//	go build ./cmd/cricket-mcp && ./cricket-mcp
package main

import (
	"bufio"
	"encoding/json"
	"io"
	"os"

	"github.com/asaraog/mcp-cricket/internal/mcp"
)

func main() {
	tools := mcp.BuildTools()
	byName := mcp.ByName(tools)
	dec := json.NewDecoder(bufio.NewReader(os.Stdin))
	out := json.NewEncoder(os.Stdout)
	for {
		var req mcp.Request
		if err := dec.Decode(&req); err != nil {
			if err == io.EOF {
				return
			}
			continue
		}
		resp, notify := mcp.Handle(req, tools, byName)
		if notify {
			continue
		}
		_ = out.Encode(resp)
	}
}
