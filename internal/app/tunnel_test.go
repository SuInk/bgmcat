package app

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestTunnelManagedProcess(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX fixture")
	}
	a := testApp(t)
	binary := filepath.Join(t.TempDir(), "cloudflared-test")
	if e := os.WriteFile(binary, []byte("#!/bin/sh\necho 'https://fixture-preview.trycloudflare.com' >&2\nexec sleep 30\n"), 0700); e != nil {
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
	if str(a.tunnelStatus()["state"]) != "stopped" {
		t.Fatal(a.tunnelStatus())
	}
}
