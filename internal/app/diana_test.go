package app

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDianaPathDiscovery(t *testing.T) {
	root := t.TempDir()
	work := filepath.Join(root, "workspace")
	os.MkdirAll(filepath.Join(work, ".diana"), 0700)
	path, e := dianaConfigPath("", []string{root})
	if e != nil || path != filepath.Join(root, ".mcp.json") {
		t.Fatal(path, e)
	}
	custom := filepath.Join(root, "custom.json")
	raw, _ := json.Marshal(Obj{"mcp_config_path": custom})
	os.WriteFile(filepath.Join(work, ".diana", "extension-paths.json"), raw, 0600)
	path, e = dianaConfigPath("", []string{root})
	if e != nil || path != custom {
		t.Fatal(path, e)
	}
	other := t.TempDir()
	os.MkdirAll(filepath.Join(other, "workspace"), 0700)
	if _, e = dianaConfigPath("", []string{root, other}); e == nil {
		t.Fatal("ambiguous roots accepted")
	}
	if _, e = dianaConfigPath("relative.json", nil); e == nil {
		t.Fatal("relative override accepted")
	}
}
func TestDianaInstallAndUninstallPreservesConfig(t *testing.T) {
	a := testApp(t)
	path := filepath.Join(t.TempDir(), ".mcp.json")
	original := []byte(`{"custom":"keep","mcpServers":{"existing":{"command":"unchanged","env":{"VALUE":"preserve"}}}}`)
	os.WriteFile(path, original, 0600)
	a.Store.Patch("config", "settings", Obj{"dianaMcpConfigPath": path})
	w := apiTest(a, "POST", "/api/agents/install", Obj{"host": "diana"}, true)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	raw, _ := os.ReadFile(path)
	var config Obj
	json.Unmarshal(raw, &config)
	servers := object(config["mcpServers"])
	entry := object(servers["bgmcat"])
	if str(config["custom"]) != "keep" || str(object(servers["existing"])["command"]) != "unchanged" || str(entry["command"]) == "" || str(list(entry["args"])[0]) != "--mcp-stdio" {
		t.Fatal("invalid merged configuration")
	}
	if str(object(entry["env"])["BGMCAT_MCP_TOKEN"]) != str(a.Settings()["token"]) {
		t.Fatal("missing token")
	}
	backup, _ := os.ReadFile(path + ".bgmcat-backup")
	if !bytes.Equal(backup, original) {
		t.Fatal("backup differs")
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0600 {
		t.Fatal(info.Mode())
	}
	w = apiTest(a, "GET", "/api/agents/detect", nil, true)
	if !strings.Contains(w.Body.String(), `"displayName":"Diana"`) {
		t.Fatal("Diana missing")
	}
	w = apiTest(a, "POST", "/api/agents/uninstall", Obj{"host": "diana"}, true)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	raw, _ = os.ReadFile(path)
	json.Unmarshal(raw, &config)
	if _, exists := object(config["mcpServers"])["bgmcat"]; exists {
		t.Fatal("entry remains")
	}
	if str(object(object(config["mcpServers"])["existing"])["command"]) != "unchanged" {
		t.Fatal("other entry lost")
	}
}
func TestDianaRejectsInvalidConfig(t *testing.T) {
	a := testApp(t)
	path := filepath.Join(t.TempDir(), ".mcp.json")
	for _, raw := range []string{`{"mcpServers":[]}`, `not json`} {
		os.WriteFile(path, []byte(raw), 0600)
		a.Store.Patch("config", "settings", Obj{"dianaMcpConfigPath": path})
		w := apiTest(a, "POST", "/api/agents/install", Obj{"host": "diana"}, true)
		if w.Code != 400 {
			t.Fatal(w.Code)
		}
		after, _ := os.ReadFile(path)
		if string(after) != raw {
			t.Fatal("invalid config overwritten")
		}
	}
}
func TestStdioBridgeHandshakeAndTools(t *testing.T) {
	a := testApp(t)
	server := httptest.NewServer(a.Handler())
	defer server.Close()
	input := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05"}}
{"jsonrpc":"2.0","method":"notifications/initialized"}
{"jsonrpc":"2.0","id":2,"method":"tools/list"}
`
	var out bytes.Buffer
	if e := RunMCPStdio(context.Background(), strings.NewReader(input), &out, server.URL+"/mcp", str(a.Settings()["token"])); e != nil {
		t.Fatal(e)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 2 || !strings.Contains(lines[0], "serverInfo") || !strings.Contains(lines[1], "create_download") {
		t.Fatal(out.String())
	}
	out.Reset()
	if e := RunMCPStdio(context.Background(), strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"ping"}`), &out, server.URL+"/mcp", "wrong"); e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(out.String(), `"error"`) {
		t.Fatal("auth failure not reported")
	}
}
