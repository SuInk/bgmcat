package app

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

type Tunnel struct {
	mu         sync.Mutex
	cancel     context.CancelFunc
	done       chan struct{}
	state, url string
	err        string
	restarts   int
}

var quickURL = regexp.MustCompile(`https://[a-z0-9-]+\.trycloudflare\.com`)

func (t *Tunnel) stop() {
	t.mu.Lock()
	cancel, done := t.cancel, t.done
	t.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if done != nil {
		<-done
	}
}
func (a *App) tunnelStatus() Obj {
	a.tunnel.mu.Lock()
	defer a.tunnel.mu.Unlock()
	s := a.Settings()
	state := a.tunnel.state
	if state == "" {
		state = "stopped"
	}
	return Obj{"state": state, "enabled": flag(s["tunnelEnabled"]), "mode": s["tunnelMode"], "publicUrl": a.tunnel.url, "lastError": a.tunnel.err, "readyConnections": 0, "restartCount": a.tunnel.restarts, "binaryAvailable": a.tunnelBinary() != "", "binaryPath": a.tunnelBinary(), "binarySource": "path", "hostname": s["tunnelHostname"], "connectorId": nil, "metricsPort": nil, "pid": nil, "startedAt": nil, "nextRetryAt": nil}
}
func (a *App) tunnelBinary() string {
	path := str(a.Settings()["tunnelBinaryPath"])
	if path == "" {
		path = "cloudflared"
	}
	resolved, _ := exec.LookPath(path)
	return resolved
}
func (a *App) startTunnel() error {
	a.tunnel.stop()
	path := a.tunnelBinary()
	if path == "" {
		return errors.New("未找到可选的 Cloudflare Tunnel 组件；本地与云盘功能不需要它")
	}
	s := a.Settings()
	_, port, _ := net.SplitHostPort(a.cfg.Addr)
	origin := "http://127.0.0.1:" + port
	args := []string{"--no-autoupdate", "tunnel"}
	if str(s["tunnelMode"]) == "named" {
		if str(s["tunnelCredentialMode"]) == "credentials-file" {
			if str(s["tunnelId"]) == "" || str(s["tunnelCredentialsFile"]) == "" {
				return errors.New("请填写隧道 ID 和凭据文件")
			}
			config := fmt.Sprintf("tunnel: %q\ncredentials-file: %q\n", str(s["tunnelId"]), str(s["tunnelCredentialsFile"]))
			if host := str(s["tunnelHostname"]); host != "" {
				config += fmt.Sprintf("ingress:\n  - hostname: %q\n    service: %q\n  - service: http_status:404\n", host, origin)
			} else {
				config += fmt.Sprintf("url: %q\n", origin)
			}
			file := filepath.Join(a.cfg.DataDir, "tunnel.yml")
			if e := os.WriteFile(file, []byte(config), 0600); e != nil {
				return e
			}
			args = []string{"--no-autoupdate", "--config", file, "tunnel", "run", str(s["tunnelId"])}
		} else {
			if str(s["tunnelToken"]) == "" {
				return errors.New("请填写隧道 token")
			}
			file := filepath.Join(a.cfg.DataDir, "tunnel-token")
			if e := os.WriteFile(file, []byte(str(s["tunnelToken"])), 0600); e != nil {
				return e
			}
			args = append(args, "run", "--token-file", file)
		}
	} else {
		args = append(args, "--url", origin)
	}
	ctx, cancel := context.WithCancel(a.ctx)
	a.tunnel.mu.Lock()
	a.tunnel.cancel = cancel
	a.tunnel.done = make(chan struct{})
	done := a.tunnel.done
	a.tunnel.state = "starting"
	a.tunnel.err = ""
	a.tunnel.restarts = 0
	a.tunnel.url = ""
	if host := str(s["tunnelHostname"]); str(s["tunnelMode"]) == "named" && host != "" {
		a.tunnel.url = "https://" + host
	}
	a.tunnel.mu.Unlock()
	go func() {
		defer close(done)
		defer cancel()
		for attempt := 0; ; attempt++ {
			cmd := exec.CommandContext(ctx, path, args...)
			pipe, e := cmd.StderrPipe()
			cmd.Stdout = io.Discard
			if e == nil {
				e = cmd.Start()
			}
			if e == nil {
				scanner := bufio.NewScanner(pipe)
				for scanner.Scan() {
					line := scanner.Text()
					a.tunnel.mu.Lock()
					if u := quickURL.FindString(line); u != "" {
						a.tunnel.url = u
						a.tunnel.state = "online"
					}
					if strings.Contains(line, "Registered tunnel connection") {
						a.tunnel.state = "online"
					}
					a.tunnel.mu.Unlock()
				}
				e = cmd.Wait()
			}
			a.tunnel.mu.Lock()
			a.tunnel.state = "stopped"
			if ctx.Err() == nil {
				a.tunnel.state = "error"
				a.tunnel.err = "隧道进程退出，请检查凭据及网络"
			}
			a.tunnel.mu.Unlock()
			if ctx.Err() != nil || !flag(a.Settings()["tunnelAutoRestart"]) || attempt >= 8 {
				break
			}
			delay := time.Second * time.Duration(1<<min(attempt, 5))
			if delay > 30*time.Second {
				delay = 30 * time.Second
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(delay):
			}
			a.tunnel.mu.Lock()
			a.tunnel.restarts = attempt + 1
			a.tunnel.state = "starting"
			a.tunnel.mu.Unlock()
		}
		a.tunnel.mu.Lock()
		a.tunnel.cancel = nil
		a.tunnel.mu.Unlock()
	}()

	return nil
}
func (a *App) tunnelRoutes() {
	a.handle("GET /api/tunnel/status", func(w http.ResponseWriter, r *http.Request) error { ok(w, a.tunnelStatus()); return nil })
	a.handle("GET /api/tunnel/preflight", func(w http.ResponseWriter, r *http.Request) error {
		issues := []Obj{}
		if a.tunnelBinary() == "" {
			issues = append(issues, Obj{"level": "block", "code": "MISSING_BINARY", "message": "未安装可选 cloudflared 组件", "autoFixable": false})
		}
		ok(w, issues)
		return nil
	})
	a.handle("GET /api/tunnel/logs", func(w http.ResponseWriter, r *http.Request) error { ok(w, []any{}); return nil })
	a.handle("GET /api/tunnel/discovered", func(w http.ResponseWriter, r *http.Request) error {
		items := []Obj{}
		home, _ := os.UserHomeDir()
		paths, _ := filepath.Glob(filepath.Join(home, ".cloudflared", "*.json"))
		for _, path := range paths {
			raw, e := os.ReadFile(path)
			if e != nil {
				continue
			}
			var v Obj
			if json.Unmarshal(raw, &v) != nil || str(v["TunnelID"]) == "" {
				continue
			}
			items = append(items, Obj{"uuid": v["TunnelID"], "hostname": nil, "credentialsFile": path, "fromConfig": false})
		}
		ok(w, items)
		return nil
	})
	for _, action := range []string{"start", "restart", "stop"} {
		a.handle("POST /api/tunnel/"+action, func(w http.ResponseWriter, r *http.Request) error {
			if strings.HasSuffix(r.URL.Path, "/stop") {
				a.tunnel.stop()
			} else if e := a.startTunnel(); e != nil {
				return e
			}
			ok(w, a.tunnelStatus())
			return nil
		})
	}
}
