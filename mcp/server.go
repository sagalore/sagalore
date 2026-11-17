// Package mcp is a stdio MCP server for Sagalore.
// Tools register themselves from init. The hosted Smia listing is separate;
// this server speaks the Sagalore HTTP API directly.
package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"

	"github.com/sagalore/sagalore/internal/version"
)

// Tool is one MCP tool. Description is what the assistant reads before calling.
type Tool struct {
	Name        string
	Description string
	Schema      map[string]any
	ReadOnly    bool
	Call        func(ctx context.Context, args json.RawMessage) (string, error)
}

var (
	regMu sync.Mutex
	reg   []Tool
)

// Register adds a tool. Call it from init.
func Register(t Tool) {
	if t.Name == "" || t.Call == nil {
		panic("mcp tool needs a name and Call")
	}
	if t.Schema == nil {
		t.Schema = map[string]any{"type": "object", "properties": map[string]any{}}
	}
	regMu.Lock()
	reg = append(reg, t)
	regMu.Unlock()
}

func tools() []Tool {
	regMu.Lock()
	defer regMu.Unlock()
	out := make([]Tool, len(reg))
	copy(out, reg)
	return out
}

func findTool(name string) (Tool, bool) {
	for _, t := range tools() {
		if t.Name == name {
			return t, true
		}
	}
	return Tool{}, false
}

type rpcReq struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

// Serve reads MCP JSON-RPC from r until EOF and writes framed responses to w.
// Messages may be Content-Length frames or a single JSON object per line.
func Serve(r io.Reader, w io.Writer) error {
	br := bufio.NewReader(r)
	for {
		msg, err := readMessage(br)
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		var req rpcReq
		if err := json.Unmarshal(msg, &req); err != nil {
			if err := writeMessage(w, rpcError(nil, -32700, "parse error")); err != nil {
				return err
			}
			continue
		}
		if len(req.ID) == 0 || string(req.ID) == "null" {
			continue
		}
		resp := dispatch(req)
		if err := writeMessage(w, resp); err != nil {
			return err
		}
	}
}

func dispatch(req rpcReq) any {
	switch req.Method {
	case "initialize":
		return rpcResult(req.ID, map[string]any{
			"protocolVersion": "2024-11-05",
			"capabilities":    map[string]any{"tools": map[string]any{"listChanged": false}},
			"serverInfo":      map[string]any{"name": "sagalore", "version": version.Version},
			"instructions":    "Call how_it_works before the first saga. Characters come from Battle.net sync. After create_saga, the player types /reload in game.",
		})
	case "ping":
		return rpcResult(req.ID, map[string]any{})
	case "tools/list":
		list := make([]map[string]any, 0)
		for _, t := range tools() {
			list = append(list, map[string]any{
				"name":        t.Name,
				"description": t.Description,
				"inputSchema": t.Schema,
				"annotations": map[string]any{"readOnlyHint": t.ReadOnly},
			})
		}
		return rpcResult(req.ID, map[string]any{"tools": list})
	case "tools/call":
		return callTool(req)
	default:
		return rpcError(req.ID, -32601, "method not found")
	}
}

func callTool(req rpcReq) any {
	var params struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if len(req.Params) > 0 {
		if err := json.Unmarshal(req.Params, &params); err != nil {
			return rpcResult(req.ID, toolError("arguments must be an object"))
		}
	}
	t, ok := findTool(params.Name)
	if !ok {
		return rpcResult(req.ID, toolError("unknown tool "+params.Name))
	}
	args := params.Arguments
	if len(args) == 0 {
		args = []byte(`{}`)
	}
	text, err := t.Call(context.Background(), args)
	if err != nil {
		return rpcResult(req.ID, toolError(err.Error()))
	}
	return rpcResult(req.ID, map[string]any{
		"content": []map[string]string{{"type": "text", "text": text}},
		"isError": false,
	})
}

func toolError(msg string) map[string]any {
	return map[string]any{
		"content": []map[string]string{{"type": "text", "text": msg}},
		"isError": true,
	}
}

func rpcResult(id json.RawMessage, result any) map[string]any {
	return map[string]any{"jsonrpc": "2.0", "id": json.RawMessage(id), "result": result}
}

func rpcError(id json.RawMessage, code int, msg string) map[string]any {
	out := map[string]any{
		"jsonrpc": "2.0",
		"error":   map[string]any{"code": code, "message": msg},
	}
	if len(id) > 0 {
		out["id"] = json.RawMessage(id)
	} else {
		out["id"] = nil
	}
	return out
}

func writeMessage(w io.Writer, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "Content-Length: %d\r\n\r\n%s", len(b), b)
	return err
}

func readMessage(r *bufio.Reader) ([]byte, error) {
	for {
		line, err := r.ReadString('\n')
		trim := strings.TrimSpace(line)
		if trim == "" {
			if err != nil {
				return nil, err
			}
			continue
		}
		if err != nil && !errors.Is(err, io.EOF) {
			return nil, err
		}
		if strings.HasPrefix(trim, "{") {
			return []byte(trim), nil
		}
		if err != nil {
			return nil, err
		}
		if !strings.HasPrefix(strings.ToLower(trim), "content-length:") {
			continue
		}
		n, err := strconv.Atoi(strings.TrimSpace(trim[len("content-length:"):]))
		if err != nil || n < 0 {
			return nil, fmt.Errorf("bad content-length")
		}
		for {
			h, err := r.ReadString('\n')
			if err != nil {
				return nil, err
			}
			if strings.TrimSpace(h) == "" {
				break
			}
		}
		buf := make([]byte, n)
		if _, err := io.ReadFull(r, buf); err != nil {
			return nil, err
		}
		return bytes.TrimSpace(buf), nil
	}
}
