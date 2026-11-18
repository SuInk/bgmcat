package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

func (a *App) agentPath(host string) (string, error) {
	home, e := os.UserHomeDir()
	if e != nil {
		return "", e
	}
	switch host {
	case "diana":
		return a.dianaPath()
	case "codex":
		return filepath.Join(home, ".codex", "config.toml"), nil
	case "claude":
		return filepath.Join(home, ".claude.json"), nil
	case "cursor":
		return filepath.Join(home, ".cursor", "mcp.json"), nil
	case "openclaw":
		return filepath.Join(home, ".openclaw", "mcp.json"), nil
	case "cherry-studio":
		return "", errors.New("Cherry Studio 请使用界面中的 MCP URL 手动添加")
	}
	return "", errors.New("未知客户端")
}
func (a *App) agentRoutes() {
	a.handle("GET /api/agents/detect", func(w http.ResponseWriter, r *http.Request) error {
		items := []Obj{}
		for _, host := range []string{"diana", "codex", "claude", "cursor", "openclaw", "cherry-studio"} {
			path, pathErr := a.agentPath(host)
			data, e := os.ReadFile(path)
			textData := string(data)
			installed := strings.Contains(textData, `"bgmcat"`) || strings.Contains(textData, "[mcp_servers.bgmcat]") || strings.Contains(textData, "anime-rss-go")
			name := host
			details := ""
			detected := e == nil
			if host == "diana" {
				name = "Diana"
				detected = pathErr == nil
				details = "本机 stdio 接入；Bangumi Cat 服务需保持运行。修改后在 Diana 重新加载 MCP 或重启。"
				if pathErr != nil {
					details = pathErr.Error()
				}
				var config Obj
				if json.Unmarshal(data, &config) == nil {
					servers := object(config["mcpServers"])
					_, okCur := servers["bgmcat"]
					_, okLegacy := servers["anime-rss-go"]
					installed = okCur || okLegacy
				} else {
					installed = false
				}
			}
			items = append(items, Obj{"host": host, "displayName": name, "detected": detected, "details": details, "installed": installed, "managed": installed, "basePath": filepath.Dir(path), "configPath": path, "version": nil})
		}
		ok(w, items)
		return nil
	})
	for _, action := range []string{"install", "uninstall"} {
		a.handle("POST /api/agents/"+action, func(w http.ResponseWriter, r *http.Request) error {
			o, e := body(w, r)
			if e != nil {
				return e
			}
			host := str(o["host"])
			path, e := a.agentPath(host)
			if e != nil {
				return e
			}
			data, e := os.ReadFile(path)
			if e != nil && !os.IsNotExist(e) {
				return e
			}
			if len(data) > 0 {
				if e = os.WriteFile(path+".bgmcat-backup", data, 0600); e != nil {
					return e
				}
			}
			if e = os.MkdirAll(filepath.Dir(path), 0700); e != nil {
				return e
			}
			install := strings.HasSuffix(r.URL.Path, "/install")
			var next []byte
			if host == "codex" {
				text := string(data)
				for _, markers := range [][2]string{
					{"# bgmcat managed begin", "# bgmcat managed end"},
					{"# anime-rss-go managed begin", "# anime-rss-go managed end"},
				} {
					if pos := strings.Index(text, markers[0]); pos >= 0 {
						endPos := strings.Index(text[pos:], markers[1])
						if endPos < 0 {
							return errors.New("已有配置区块不完整")
						}
						text = text[:pos] + text[pos+endPos+len(markers[1]):]
					}
				}
				if install {
					begin := "# bgmcat managed begin"
					end := "# bgmcat managed end"
					text += fmt.Sprintf("\n%s\n[mcp_servers.bgmcat]\nurl = %q\nhttp_headers = { Authorization = %q }\n%s\n", begin, a.cfg.PublicURL+"/mcp", "Bearer "+str(a.Settings()["token"]), end)
				}
				next = []byte(text)
			} else {
				config := Obj{}
				if len(data) > 0 {
					if e = json.Unmarshal(data, &config); e != nil {
						return errors.New("客户端配置不是有效 JSON，未修改")
					}
				}
				if value, exists := config["mcpServers"]; exists {
					if _, ok := value.(map[string]any); !ok {
						return errors.New("mcpServers 必须是对象，未修改配置")
					}
				}
				servers := object(config["mcpServers"])
				delete(servers, "anime-rss-go")
				if install {
					if host == "diana" {
						entry, err := a.dianaServerConfig()
						if err != nil {
							return err
						}
						servers["bgmcat"] = entry
					} else {
						servers["bgmcat"] = Obj{"type": "http", "url": a.cfg.PublicURL + "/mcp", "headers": Obj{"Authorization": "Bearer " + str(a.Settings()["token"])}}
					}
				} else {
					delete(servers, "bgmcat")
				}
				config["mcpServers"] = servers
				next, e = json.MarshalIndent(config, "", "  ")
				if e != nil {
					return e
				}
			}
			if e = atomicAgentWrite(path, next); e != nil {
				return e
			}
			ok(w, Obj{"host": host, "installed": install, "configPath": path, "filesWritten": []string{path}, "message": "配置已更新；重启客户端加载。"})
			return nil
		})
	}
	a.handle("GET /api/agents/doctor", func(w http.ResponseWriter, r *http.Request) error {
		host := r.URL.Query().Get("host")
		path, e := a.agentPath(host)
		if e != nil {
			return e
		}
		if host == "diana" {
			raw, err := os.ReadFile(path)
			var config Obj
			issues := []string{}
			if err != nil {
				issues = append(issues, "MCP 配置文件不存在或不可读取")
			} else if json.Unmarshal(raw, &config) != nil {
				issues = append(issues, "MCP 配置不是有效 JSON")
			}
			servers := object(config["mcpServers"])
			entry := object(servers["bgmcat"])
			if len(entry) == 0 {
				entry = object(servers["anime-rss-go"])
			}
			env := object(entry["env"])
			cfgToken := firstNonEmpty(str(env["BGMCAT_MCP_TOKEN"]), str(env["ANIME_RSS_MCP_TOKEN"]))
			cfgURL := firstNonEmpty(str(env["BGMCAT_MCP_URL"]), str(env["ANIME_RSS_MCP_URL"]))
			tokenConfigured := cfgToken != ""
			if len(entry) == 0 {
				issues = append(issues, "尚未接入 Bangumi Cat")
			}
			if tokenConfigured && cfgToken != str(a.Settings()["token"]) {
				issues = append(issues, "访问口令已改变，请重新接入")
			}
			if cfgURL != a.cfg.PublicURL+"/mcp" {
				issues = append(issues, "服务地址不匹配，请重新接入")
			}
			if info, e := os.Stat(str(entry["command"])); e != nil || info.IsDir() {
				issues = append(issues, "Go 可执行文件不存在")
			}
			ok(w, Obj{"host": host, "configPath": path, "configExists": err == nil, "appRunning": true, "endpointReachable": len(issues) == 0, "tokenConfigured": tokenConfigured, "message": "Diana 本机 stdio 配置检查；实际加载请在 Diana 中重新加载 MCP。", "issues": issues})
			return nil
		}
		_, e = os.Stat(path)
		ok(w, Obj{"host": host, "configPath": path, "configExists": e == nil, "appRunning": true, "endpointReachable": true, "tokenConfigured": true, "message": "Go MCP 服务运行中", "issues": []any{}})
		return nil
	})
}

func atomicAgentWrite(path string, data []byte) error {
	f, e := os.CreateTemp(filepath.Dir(path), ".bgmcat-config-*")
	if e != nil {
		return e
	}
	defer os.Remove(f.Name())
	if _, e = f.Write(data); e != nil {
		f.Close()
		return e
	}
	if e = f.Sync(); e != nil {
		f.Close()
		return e
	}
	if e = f.Close(); e != nil {
		return e
	}
	return os.Rename(f.Name(), path)
}
