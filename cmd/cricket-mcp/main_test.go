package main

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/asaraog/mcp-cricket/internal/mcp"
)

// A malformed line used to leave the stdin decoder parked on it, and the
// loop spun on the same error at 100% CPU without reading another byte.
// Each bad line now gets a JSON-RPC error, and the requests after it are
// answered.
func TestMalformedLineThenValidRequest(t *testing.T) {
	in := strings.Join([]string{
		`this is not json`,
		`{"jsonrpc":"2.0","id":1,"method":"ping"`,
		``,
		`{"jsonrpc":"2.0","id":2,"method":"ping"}`,
		`{"jsonrpc":"2.0","id":3,"method":7}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":4,"method":"tools/list"}`, // no trailing newline
	}, "\n")

	var out bytes.Buffer
	done := make(chan struct{})
	go func() {
		defer close(done)
		tools := mcp.BuildTools()
		serve(strings.NewReader(in), &out, tools, mcp.ByName(tools))
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("serve did not return: stuck on the malformed line")
	}

	type reply struct {
		ID     json.RawMessage `json:"id"`
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Code int `json:"code"`
		} `json:"error"`
	}
	var got []reply
	dec := json.NewDecoder(&out)
	for {
		var r reply
		if err := dec.Decode(&r); err == io.EOF {
			break
		} else if err != nil {
			t.Fatalf("output is not JSON: %v\n%s", err, out.String())
		}
		got = append(got, r)
	}

	// The blank line and the notification get nothing back.
	want := []struct {
		id   string
		code int // 0 for a result
	}{
		{"null", -32700},
		{"null", -32700},
		{"2", 0},
		{"3", -32600},
		{"4", 0},
	}
	if len(got) != len(want) {
		t.Fatalf("%d replies, want %d", len(got), len(want))
	}
	for i, w := range want {
		g := got[i]
		code := 0
		if g.Error != nil {
			code = g.Error.Code
		}
		if string(g.ID) != w.id || code != w.code {
			t.Errorf("reply %d: id %s code %d, want id %s code %d", i, g.ID, code, w.id, w.code)
		}
		if w.code == 0 && len(g.Result) == 0 {
			t.Errorf("reply %d: no result", i)
		}
	}
	if !strings.Contains(string(got[4].Result), `"cricket_minor_league_info"`) {
		t.Errorf("tools/list after the bad lines: %s", got[4].Result)
	}
}
