package app

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

func dianaConfigPath(explicit string, roots []string) (string, error) {
	if explicit = strings.TrimSpace(explicit); explicit != "" {
		if !filepath.IsAbs(explicit) {
			return "", errors.New("Diana MCP 配置路径必须是绝对路径")
		}
		return filepath.Clean(explicit), nil
	}
	found := map[string]bool{}
	for _, root := range roots {
		work := filepath.Join(root, "workspace")
		pinned := ""
		for _, name := range []string{filepath.Join(".diana", "extension-paths.json"), ".extension-paths.json"} {
			raw, e := os.ReadFile(filepath.Join(work, name))
			if os.IsNotExist(e) {
				continue
			}
			if e != nil {
				return "", e
			}
			var paths struct {
				MCP string `json:"mcp_config_path"`
			}
			if json.Unmarshal(raw, &paths) != nil {
				return "", errors.New("Diana 路径记录损坏，请手动指定配置文件")
			}
			pinned = strings.TrimSpace(paths.MCP)
			break
		}
		if pinned != "" {
			if !filepath.IsAbs(pinned) {
				pinned = filepath.Join(work, pinned)
			}
			found[filepath.Clean(pinned)] = true
			continue
		}
		path := filepath.Join(root, ".mcp.json")
		_, fileErr := os.Stat(path)
		info, workErr := os.Stat(work)
		if fileErr == nil || (workErr == nil && info.IsDir()) {
			found[path] = true
		}
	}
	if len(found) > 1 {
		return "", errors.New("检测到多个 Diana 数据目录，请填写目标 MCP 配置路径")
	}
	for path := range found {
		return path, nil
	}
	return "", errors.New("未找到 Diana 数据目录，请填写数据目录中的 .mcp.json 绝对路径")
}
func (a *App) dianaPath() (string, error) {
	cache, _ := os.UserCacheDir()
	cwd, _ := os.Getwd()
	return dianaConfigPath(str(a.Settings()["dianaMcpConfigPath"]), []string{filepath.Join(cache, "diana"), filepath.Join(cwd, "..", "diana", "data")})
}
func (a *App) dianaServerConfig() (Obj, error) {
	exe, e := os.Executable()
	if e != nil {
		return nil, e
	}
	mcpURL := a.cfg.PublicURL + "/mcp"
	token := str(a.Settings()["token"])
	return Obj{"command": exe, "args": []string{"--mcp-stdio"}, "env": Obj{"BGMCAT_MCP_URL": mcpURL, "BGMCAT_MCP_TOKEN": token}, "enabled": true}, nil
}
