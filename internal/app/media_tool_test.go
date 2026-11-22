package app

import (
	"os"
	"path/filepath"
	"testing"
)

func writeFakeTool(t *testing.T, dir, name string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if e := os.WriteFile(p, []byte("#!/bin/sh\n"), 0o755); e != nil {
		t.Fatal(e)
	}
	return p
}

func TestResolveMediaToolOrder(t *testing.T) {
	t.Setenv("PATH", "")
	bundled := t.TempDir()
	system := t.TempDir()
	orig := mediaToolDirs
	t.Cleanup(func() { mediaToolDirs = orig })
	mediaToolDirs = map[string][]string{"testos": {system}}

	// 什么都没有：返回裸命令名，调用方按「未安装」处理。
	if got := resolveMediaTool("ffmpeg", "", bundled, "testos"); got != "ffmpeg" {
		t.Fatalf("missing everywhere: got %q", got)
	}

	// 只有系统里装了：访达启动没有 Homebrew PATH 也能找到。
	sys := writeFakeTool(t, system, "ffmpeg")
	if got := resolveMediaTool("ffmpeg", "", bundled, "testos"); got != sys {
		t.Fatalf("system fallback: got %q want %q", got, sys)
	}

	// 内置的优先于系统的。
	own := writeFakeTool(t, bundled, "ffmpeg")
	if got := resolveMediaTool("ffmpeg", "", bundled, "testos"); got != own {
		t.Fatalf("bundled first: got %q want %q", got, own)
	}

	// 设置里填了路径就用它，ffprobe 跟着同一个目录。
	custom := filepath.Join(t.TempDir(), "bin", "ffmpeg")
	if got := resolveMediaTool("ffmpeg", " "+custom+" ", bundled, "testos"); got != custom {
		t.Fatalf("configured ffmpeg: got %q", got)
	}
	if got, want := resolveMediaTool("ffprobe", custom, bundled, "testos"), filepath.Join(filepath.Dir(custom), "ffprobe"); got != want {
		t.Fatalf("configured ffprobe: got %q want %q", got, want)
	}
}

func TestResolveMediaToolSkipsNonExecutable(t *testing.T) {
	t.Setenv("PATH", "")
	bundled := t.TempDir()
	if e := os.WriteFile(filepath.Join(bundled, "ffprobe"), nil, 0o644); e != nil {
		t.Fatal(e)
	}
	if got := resolveMediaTool("ffprobe", "", bundled, "darwin"); got == filepath.Join(bundled, "ffprobe") {
		t.Fatalf("non-executable file must not be picked: %q", got)
	}
}

func TestResolveMediaToolWindowsSuffix(t *testing.T) {
	t.Setenv("PATH", "")
	bundled := t.TempDir()
	if e := os.WriteFile(filepath.Join(bundled, "ffprobe.exe"), nil, 0o644); e != nil {
		t.Fatal(e)
	}
	if got, want := resolveMediaTool("ffprobe", "", bundled, "windows"), filepath.Join(bundled, "ffprobe.exe"); got != want {
		t.Fatalf("windows bundled: got %q want %q", got, want)
	}
	if got, want := resolveMediaTool("ffprobe", `C:\tools\ffmpeg.exe`, bundled, "windows"), filepath.Join(filepath.Dir(`C:\tools\ffmpeg.exe`), "ffprobe.exe"); got != want {
		t.Fatalf("windows configured: got %q want %q", got, want)
	}
}
