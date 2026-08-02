// Command mcp serves Cricket for Noobs' cricket data over the Model
// Context Protocol on stdin/stdout, for MCP clients that launch a local
// server (Claude Desktop's local config). The hosted HTTP endpoint on
// the website serves the same tools without users installing anything.
//
//	go build ./cmd/mcp && HISTORY_DB=/path/history.db ./mcp
package main

import (
	"bufio"
	"encoding/json"
	"io"
	"os"

	"github.com/asaraog/cricket-mcp/internal/mcp"
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
