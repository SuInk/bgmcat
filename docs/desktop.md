# 桌面版（默认推荐）

桌面版使用 Electron 窗口，内置 Go 后端和完整界面。双击应用后自动启动本机服务并认证，刷新窗口或重新打开无需输入 token。无需安装 Go、Node.js 或单独启动网页服务。

## 下载与安装

正式 Release 发布后，在仓库 Releases 中选择 **bgmcat-desktop** 开头的资产：

| 系统 | 默认安装包 |
| --- | --- |
| macOS Apple Silicon | `bgmcat-desktop-版本-mac-arm64.dmg` |
| macOS Intel | `bgmcat-desktop-版本-mac-x64.dmg` |
| Windows x64 | `bgmcat-desktop-版本-win-x64.exe` |
| Ubuntu / Debian x64 | `bgmcat-desktop-版本-linux-x64.deb` |
| 其他 Linux x64 | `bgmcat-desktop-版本-linux-x64.AppImage` |

macOS 将应用拖到 Applications，Windows 运行安装器（默认当前用户安装），Debian/Ubuntu 使用 `sudo apt install ./bgmcat-desktop-*.deb`。AppImage 赋予执行权限后运行。每个发行资产的 SHA-256 会列在统一的 `SHA256SUMS` 中。

当前首个正式 Release 尚未发布；本地构建的安装包位于 `desktop-dist/`，不能把 CI 构建配置等同于已经发布。仓库为私有，访问发行资产仍需要仓库权限。

目前没有配置 Apple Developer ID 公证或 Windows 代码签名证书。未签名分发可能出现系统提示；本机生成的 Mac 包用于测试，不宣称已具备正式签名发布条件。

## 下载引擎

桌面包内置 Go 与 UI，**尚未捆绑 aria2、FFmpeg 或 cloudflared**。它会自动识别已安装的 aria2；缺少 aria2 时给出提示。Ubuntu/Debian 的 deb 声明 aria2 系统依赖，使用 apt 安装时会自动安装它。

其他平台安装一次 aria2：

```sh
# macOS（需要 Homebrew）
brew install aria2
# Debian / Ubuntu 的 AppImage 用户
sudo apt install aria2
```

```powershell
# Windows（需要 winget），装好后重新打开应用
winget install --id aria2.aria2 --exact
```

macOS 从 Finder 启动时也会查找 `/opt/homebrew/bin` 和 `/usr/local/bin`。已有 qBittorrent 的用户可以在设置中改用它。FFmpeg 仍是转码功能的可选依赖。

## 本机认证与数据

- 桌面服务固定仅监听 `127.0.0.1:39123`，便于重启后保留浏览器状态及配置 OAuth 回调。端口被其他程序占用时直接报错，不连接不明服务，也不会终止现有网页服务。
- token 通过子进程管道和受限 IPC 交给自有窗口，不写入进程参数或启动 URL。服务的 HTTP API 仍需要认证。
- 第二次启动会唤醒已有窗口；关闭最后一个窗口或选择退出，会关闭桌面版启动的 Go 服务及其托管 aria2。应用关闭期间不会继续下载；重新启动后按服务的任务恢复逻辑处理。
- 数据保存在 Electron 用户目录的 `server/` 子目录。macOS 通常为 `~/Library/Application Support/Bangumi Cat/server`，Windows 为 `%APPDATA%/Bangumi Cat/server`，Linux 为 `~/.config/Bangumi Cat/server`。菜单“帮助 → 打开数据目录”可找到实际位置。
- 桌面版使用独立数据目录，不自动覆盖旧网页实例或 `.preview`。迁移时用网页内备份/恢复功能，先备份原数据。卸载 Windows 应用默认保留数据。
- Google OAuth 使用桌面设置中显示的回调地址配置（通常为 `http://127.0.0.1:39123/api/cloud/callback`）。手机、远程浏览器仍使用服务版和原有访问认证。
- 当前没有托盘驻留、开机自启或桌面自动更新器；升级需下载新安装包覆盖应用，保留数据目录。

## 从源码打包

```sh
npm ci
npm run desktop:prepare
npm run desktop:test
npm run desktop:pack
```

首次构建需要访问 Electron 官方发行资产和 electron-builder 工具下载地址。`desktop:prepare` 生成当前平台的 Go 后端，`desktop:pack` 使用本机架构生成桌面包。不要跨架构传参数却打包本机后端；CI 在各架构原生 runner 上构建。

仅开发运行：`npm run desktop:dev`。自动化测试可用 `BGMCAT_DESKTOP_USER_DATA` 指定独立的 Electron 用户目录（服务端口仍为 39123）。
