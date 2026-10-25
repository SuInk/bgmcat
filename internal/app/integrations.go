package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"runtime"
	"strings"
	"time"
)

var toolNames = []string{"system_status", "list_subscriptions", "create_subscription", "update_subscription", "delete_subscription", "refresh_subscription", "list_feed_items", "create_download", "list_downloads", "pause_download", "resume_download", "delete_download", "recent_logs"}

func (a *App) mcp(ctx context.Context, o Obj) Obj {
	response := Obj{"jsonrpc": "2.0", "id": o["id"]}
	var result any
	var e error
	params := object(o["params"])
	switch str(o["method"]) {
	case "initialize":
		result = Obj{"protocolVersion": "2024-11-05", "capabilities": Obj{"tools": Obj{}}, "serverInfo": Obj{"name": "anime-rss-go", "version": Version}}
	case "ping":
		result = Obj{}
	case "tools/list":
		tools := []Obj{}
		for _, name := range toolNames {
			properties := Obj{}
			required := []string{}
			switch name {
			case "create_download":
				properties = Obj{"url": Obj{"type": "string"}, "title": Obj{"type": "string"}, "feedItemId": Obj{"type": "integer"}}
			case "create_subscription":
				properties = Obj{"name": Obj{"type": "string"}, "url": Obj{"type": "string"}, "rule": Obj{"type": "object"}}
				required = []string{"name", "url"}
			case "update_subscription":
				properties = Obj{"id": Obj{"type": "integer"}, "name": Obj{"type": "string"}, "url": Obj{"type": "string"}, "enabled": Obj{"type": "boolean"}, "rule": Obj{"type": "object"}}
				required = []string{"id"}
			case "delete_subscription", "refresh_subscription":
				properties = Obj{"id": Obj{"type": "integer"}}
				required = []string{"id"}
			case "pause_download", "resume_download", "delete_download":
				properties = Obj{"gid": Obj{"type": "string"}}
				required = []string{"gid"}
			}
			tools = append(tools, Obj{"name": name, "description": strings.ReplaceAll(name, "_", " "), "inputSchema": Obj{"type": "object", "properties": properties, "required": required}})
		}
		result = Obj{"tools": tools}
	case "tools/call":
		var data any
		arg := object(params["arguments"])
		switch str(params["name"]) {
		case "system_status":
			data = a.status()
		case "list_subscriptions":
			data, e = a.Store.List("subscriptions")
		case "create_subscription":
			data, e = a.newSubscription(arg)
		case "update_subscription":
			id := str(arg["id"])
			delete(arg, "id")
			data, e = a.Store.Patch("subscriptions", id, arg)
		case "delete_subscription":
			e = a.Store.Delete("subscriptions", str(arg["id"]))
			data = Obj{"deleted": e == nil}
		case "refresh_subscription":
			var sub Obj
			sub, e = a.Store.Get("subscriptions", str(arg["id"]))
			if sub == nil {
				e = errors.New("订阅不存在")
			} else {
				data, e = a.refreshSubscription(ctx, sub)
			}
		case "list_feed_items":
			data, e = a.Store.List("feed")
		case "create_download":
			data, e = a.enqueue(arg)
			data = publicDownloads(data)
		case "list_downloads":
			data, e = a.Store.List("downloads")
			data = publicDownloads(data)
		case "pause_download":
			e = a.stopJob(str(arg["gid"]))
			if e != nil {
				break
			}
			data, e = a.Store.Get("downloads", str(arg["gid"]))
		case "resume_download":
			a.startJob(str(arg["gid"]))
			data, e = a.Store.Get("downloads", str(arg["gid"]))
		case "delete_download":
			a.driveMu.Lock()
			defer a.driveMu.Unlock()
			e = a.stopJob(str(arg["gid"]))
			if e == nil {
				e = a.Store.Delete("downloads", str(arg["gid"]))
			}
			data = Obj{"deleted": e == nil}
		case "recent_logs":
			data, e = a.Store.List("logs")
		default:
			e = errors.New("未知工具")
		}
		if e != nil {
			result = Obj{"content": []Obj{{"type": "text", "text": e.Error()}}, "isError": true}
			e = nil
		} else {
			raw, _ := json.Marshal(Obj{"ok": true, "data": data})
			result = Obj{"content": []Obj{{"type": "text", "text": string(raw)}}}
		}
	default:
		e = errors.New("不支持的方法")
	}
	if e != nil {
		response["error"] = Obj{"code": -32601, "message": e.Error()}
	} else {
		response["result"] = result
	}
	return response
}
func (a *App) integrationRoutes() {
	a.handle("POST /mcp", func(w http.ResponseWriter, r *http.Request) error {
		o, e := body(w, r)
		if e != nil {
			return e
		}
		if o["id"] == nil {
			w.WriteHeader(202)
			return nil
		}
		w.Header().Set("Content-Type", "application/json")
		return json.NewEncoder(w).Encode(a.mcp(r.Context(), o))
	})
	a.handle("GET /mcp/sse", func(w http.ResponseWriter, r *http.Request) error {
		f, yes := w.(http.Flusher)
		if !yes {
			return errors.New("不支持 SSE")
		}
		id := randomID(24)
		ch := make(chan Obj, 16)
		a.mcpMu.Lock()
		a.mcpSessions[id] = ch
		a.mcpMu.Unlock()
		defer func() { a.mcpMu.Lock(); delete(a.mcpSessions, id); a.mcpMu.Unlock() }()
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = fmt.Fprintf(w, "event: endpoint\ndata: /mcp/messages?sessionId=%s\n\n", id)
		f.Flush()
		ticker := time.NewTicker(20 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-r.Context().Done():
				return nil
			case <-a.ctx.Done():
				return nil
			case msg := <-ch:
				b, _ := json.Marshal(msg)
				_, _ = fmt.Fprintf(w, "event: message\ndata: %s\n\n", b)
				f.Flush()
			case <-ticker.C:
				_, _ = fmt.Fprint(w, ": keepalive\n\n")
				f.Flush()
			}
		}
	})
	a.handle("POST /mcp/messages", func(w http.ResponseWriter, r *http.Request) error {
		o, e := body(w, r)
		if e != nil {
			return e
		}
		a.mcpMu.Lock()
		ch := a.mcpSessions[r.URL.Query().Get("sessionId")]
		a.mcpMu.Unlock()
		if ch == nil {
			return errors.New("MCP 会话不存在")
		}
		if o["id"] != nil {
			select {
			case ch <- a.mcp(r.Context(), o):
			case <-r.Context().Done():
				return r.Context().Err()
			}
		}
		w.WriteHeader(202)
		return nil
	})
	a.handle("GET /api/local-ai/status", func(w http.ResponseWriter, r *http.Request) error {
		s := a.Settings()
		reachable := false
		models := []string{}
		var last any
		if flag(s["localAiEnabled"]) {
			o, e := a.requestJSON(r.Context(), "GET", strings.TrimRight(str(s["localAiBaseUrl"]), "/")+"/api/tags", nil, nil)
			if e != nil {
				last = e.Error()
			} else {
				reachable = true
				for _, v := range list(o["models"]) {
					models = append(models, str(object(v)["name"]))
				}
			}
		}
		ok(w, Obj{"enabled": s["localAiEnabled"], "baseUrl": s["localAiBaseUrl"], "model": s["localAiModel"], "reachable": reachable, "availableModels": models, "lastError": last})
		return nil
	})
	a.handle("POST /api/local-ai/chat", func(w http.ResponseWriter, r *http.Request) error {
		s := a.Settings()
		if !flag(s["localAiEnabled"]) {
			return errors.New("本地 AI 未启用")
		}
		o, e := body(w, r)
		if e != nil {
			return e
		}
		result, e := a.requestJSON(r.Context(), "POST", strings.TrimRight(str(s["localAiBaseUrl"]), "/")+"/api/chat", Obj{"model": s["localAiModel"], "messages": o["messages"], "stream": false}, nil)
		if e == nil {
			ok(w, Obj{"message": result["message"], "model": s["localAiModel"]})
		}
		return e
	})
	a.handle("GET /api/system/version", func(w http.ResponseWriter, r *http.Request) error {
		ok(w, Obj{"version": Version, "appVersion": Version, "platform": runtime.GOOS, "arch": runtime.GOARCH, "buildType": "source", "gitDescribe": nil, "packaged": true, "channel": "stable", "source": "go"})
		return nil
	})
	status := func(w http.ResponseWriter, r *http.Request) error {
		ok(w, Obj{"kind": "unsupported", "supported": false, "updateAvailable": false, "currentVersion": Version, "buildType": "source", "platform": runtime.GOOS, "arch": runtime.GOARCH, "unsupportedReason": "Go 版使用 scripts/build.sh 构建，再替换可执行文件；尚无在线发布源。", "latestVersion": "", "latestPublishedAt": nil, "checkedAt": nil, "downloadedVersion": "", "progress": Obj{"phase": "idle", "done": 0, "total": 0, "percent": 0}, "busy": false, "lastError": "", "failureCount": 0, "circuitBroken": false, "source": nil})
		return nil
	}
	a.handle("GET /api/system/update", status)
	a.handle("POST /api/system/update/check", status)
	a.handle("GET /api/system/update/policy", func(w http.ResponseWriter, r *http.Request) error {
		s := a.Settings()
		ok(w, Obj{"autoDownload": s["updateAutoDownload"], "autoInstall": s["updateAutoInstall"], "mirror": s["updateMirror"], "channel": "stable"})
		return nil
	})
	a.handle("PUT /api/system/update/policy", func(w http.ResponseWriter, r *http.Request) error {
		o, e := body(w, r)
		if e != nil {
			return e
		}
		_, e = a.Store.Patch("config", "settings", Obj{"updateAutoDownload": o["autoDownload"], "updateAutoInstall": o["autoInstall"], "updateMirror": o["mirror"]})
		if e == nil {
			ok(w, o)
		}
		return e
	})
	a.handle("GET /api/system/update/changelog", func(w http.ResponseWriter, r *http.Request) error { ok(w, []any{}); return nil })
	for _, path := range []string{"download", "install", "rollback", "service"} {
		a.handle("POST /api/system/update/"+path, func(w http.ResponseWriter, r *http.Request) error {
			return errors.New("Go 版本尚未发布，请使用本项目的构建与安装脚本")
		})
	}
	a.agentRoutes()
	a.tunnelRoutes()
}
