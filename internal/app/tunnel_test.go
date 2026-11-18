package app

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestTunnelManagedProcess(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX fixture")
	}
	a := testApp(t)
	tmpDir := t.TempDir()
	argsFile := filepath.Join(tmpDir, "args.txt")
	binary := filepath.Join(tmpDir, "cloudflared-test")
	script := "#!/bin/sh\nprintf '%s' \"$*\" > \"" + argsFile + "\"\necho 'https://fixture-preview.trycloudflare.com' >&2\nexec sleep 30\n"
	if e := os.WriteFile(binary, []byte(script), 0700); e != nil {
		t.Fatal(e)
	}
	a.Store.Patch("config", "settings", Obj{"tunnelBinaryPath": binary, "tunnelAutoRestart": false, "tunnelMode": "quick"})
	if e := a.startTunnel(); e != nil {
		t.Fatal(e)
	}
	deadline := time.Now().Add(time.Second)
	for str(a.tunnelStatus()["state"]) != "online" && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond * 10)
	}
	if str(a.tunnelStatus()["publicUrl"]) != "https://fixture-preview.trycloudflare.com" {
		t.Fatal(a.tunnelStatus())
	}
	a.tunnel.stop()
	st := a.tunnelStatus()
	if str(st["state"]) != "stopped" || str(st["publicUrl"]) != "" {
		t.Fatalf("expected stopped & empty publicUrl after stop, got %+v", st)
	}
	argsRaw, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatal(err)
	}
	argsText := string(argsRaw)
	if !strings.Contains(argsText, "--config ") || !strings.Contains(argsText, "tunnel-empty.yml") {
		t.Fatalf("expected quick tunnel to isolate config with tunnel-empty.yml, got args=%q", argsText)
	}
}
