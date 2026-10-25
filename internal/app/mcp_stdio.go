package app

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

// RunMCPStdio bridges newline-delimited MCP JSON-RPC to the running server.
// Stdout is reserved exclusively for protocol messages.
func RunMCPStdio(ctx context.Context, input io.Reader, output io.Writer, endpoint, token string) error {
	if !httpURL(endpoint) || token == "" {
		return errors.New("ANIME_RSS_MCP_URL 与 ANIME_RSS_MCP_TOKEN 必须配置")
	}
	client := &http.Client{Timeout: 120 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	scan := bufio.NewScanner(input)
	scan.Buffer(make([]byte, 4096), 2<<20)
	for scan.Scan() {
		line := append([]byte(nil), scan.Bytes()...)
		var message Obj
		if e := json.Unmarshal(line, &message); e != nil {
			return fmt.Errorf("invalid MCP JSON: %w", e)
		}
		req, e := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewReader(line))
		if e != nil {
			return e
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		res, e := client.Do(req)
		var response []byte
		if e == nil {
			response, e = io.ReadAll(io.LimitReader(res.Body, 16<<20))
			res.Body.Close()
			if e == nil && res.StatusCode >= 300 {
				e = fmt.Errorf("MCP 服务 HTTP %d", res.StatusCode)
			}
		}
		if message["id"] == nil {
			continue
		}
		if e != nil {
			response, _ = json.Marshal(Obj{"jsonrpc": "2.0", "id": message["id"], "error": Obj{"code": -32603, "message": "无法连接 Bangumi Cat MCP 服务，请检查服务地址、口令和运行状态"}})
		} else {
			var rpc Obj
			if json.Unmarshal(response, &rpc) != nil || str(rpc["jsonrpc"]) != "2.0" {
				return errors.New("MCP 服务返回无效响应")
			}
			response, _ = json.Marshal(rpc)
		}
		if _, e = fmt.Fprintf(output, "%s\n", response); e != nil {
			return e
		}
	}
	return scan.Err()
}
