package app

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// 桌面包把 ffmpeg/ffprobe 放在后端可执行文件旁边（desktop/resources → Resources/）。
// 访达启动的 App 不继承终端的 PATH，所以 PATH 之外再找几个常见的安装位置。
var mediaToolDirs = map[string][]string{
	"darwin": {"/opt/homebrew/bin", "/usr/local/bin", "/opt/local/bin"},
	"linux":  {"/usr/local/bin", "/usr/bin"},
}

func (m *Media) ffmpeg() string  { return m.tool("ffmpeg") }
func (m *Media) ffprobe() string { return m.tool("ffprobe") }

func (m *Media) tool(name string) string {
	exeDir := ""
	if exe, e := os.Executable(); e == nil {
		exeDir = filepath.Dir(exe)
	}
	return resolveMediaTool(name, str(m.a.Settings()["ffmpegPath"]), exeDir, runtime.GOOS)
}

// resolveMediaTool 按「设置里填的路径 → 内置 → PATH → 常见安装位置」找 ffmpeg 或 ffprobe。
// ffprobe 跟着设置里的 ffmpeg 走同一个目录。都找不到时返回裸命令名，调用方照旧报「未安装」。
func resolveMediaTool(name, configured, exeDir, goos string) string {
	file := name
	if goos == "windows" {
		file += ".exe"
	}
	if configured = strings.TrimSpace(configured); configured != "" {
		if name == "ffmpeg" {
			return configured
		}
		return filepath.Join(filepath.Dir(configured), file)
	}
	if exeDir != "" {
		if p := filepath.Join(exeDir, file); isExecutableFile(p, goos) {
			return p
		}
	}
	if p, e := exec.LookPath(file); e == nil {
		return p
	}
	for _, dir := range mediaToolDirs[goos] {
		if p := filepath.Join(dir, file); isExecutableFile(p, goos) {
			return p
		}
	}
	return file
}

func isExecutableFile(path, goos string) bool {
	info, e := os.Stat(path)
	if e != nil || !info.Mode().IsRegular() {
		return false
	}
	return goos == "windows" || info.Mode().Perm()&0o111 != 0
}
