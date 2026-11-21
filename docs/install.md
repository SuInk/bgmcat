# 服务版安装、升级与卸载

个人电脑日常使用默认推荐 [Electron 桌面版](desktop.md)（双击启动并支持一键开启手机局域网直连）。本文说明无窗口的独立 Go 服务版与 Docker 容器部署，适合 Linux 服务器、家庭 NAS 或常驻后台运行。

## 选择安装方式

| 方式 | 安装前准备 | 运行时依赖 |
| --- | --- | --- |
| 一键安装脚本 | `curl` + `sh` 或 Windows PowerShell | **零必需外部依赖**（内置纯 Go 下载引擎）；可选 `ffmpeg`、`cloudflared` |
| Release 完整包手动解压 | 对应平台归档包 + SHA-256 校验工具 | 同上 |
| Docker Compose 部署 | Docker + Docker Compose | 容器镜像已预装 `ffmpeg` |
| 从源码构建 | Go 1.26+、Node.js 22+、npm | 构建后同 Release 单二进制 |

## 命令行一键安装

`bgmcat` 将前端静态页面与纯 Go HTTP / BitTorrent 下载引擎完整嵌入单个无 CGO 二进制中，安装器会自动检测当前系统与 CPU 架构、校验 `SHA256SUMS` 并保留旧版本备份。

macOS / Linux：

```sh
curl -fsSL https://raw.githubusercontent.com/SuInk/bgmcat/main/scripts/install.sh | sh -s -- --start
```

Windows PowerShell：

```powershell
irm https://raw.githubusercontent.com/SuInk/bgmcat/main/scripts/install.ps1 | iex
```

### 常用安装参数与默认目录

- `--start` / `-Start`：安装完成后立即在当前终端前台启动，按 `Ctrl+C` 停止；若省略该参数则仅安装二进制文件。
- 默认安装路径：macOS / Linux 为 `$HOME/.local/bin/bgmcat`，Windows 为 `$env:LOCALAPPDATA\AnimeRSS\bin\bgmcat.exe`（无需管理员提权，不擅自修改系统全局 `PATH`）。
- 指定版本与自定义安装目录：

```sh
curl -fsSL https://raw.githubusercontent.com/SuInk/bgmcat/main/scripts/install.sh | sh -s -- --version v0.2.1 --dir "$HOME/.local/bin"
```

```powershell
.\scripts\install.ps1 -Version v0.2.1 -InstallDir "$env:LOCALAPPDATA\AnimeRSS\bin"
```

## 手动解压完整包

前往 Releases 下载对应平台的服务包与 `SHA256SUMS`：

| 平台 | 归档文件 |
| --- | --- |
| Linux x86-64 | `bgmcat-linux-amd64.tar.gz` |
| Linux ARM64 | `bgmcat-linux-arm64.tar.gz` |
| macOS Intel | `bgmcat-macos-amd64.tar.gz` |
| macOS Apple Silicon | `bgmcat-macos-arm64.tar.gz` |
| Windows x86-64 | `bgmcat-windows-amd64.zip` |

先核对 SHA-256（Linux：`sha256sum`，macOS：`shasum -a 256`，Windows：`Get-FileHash -Algorithm SHA256`），确认与 `SHA256SUMS` 完全一致后解压，运行包内的 `run.sh` 或 `run.bat`。

本地打包产物也可直接通过安装器做离线安装验证：

```sh
sh scripts/install.sh --local-dir "$PWD/release"
```

## 账号密码与手机跨设备访问

- 服务首次启动会在终端日志打印初始管理员用户名与随机引导密码；登录后可在「设置 → 安全与访问」修改账号密码，忘记密码可执行：
  ```sh
  bgmcat --data ~/.bgmcat --reset-password
  ```
- 默认仅监听 `127.0.0.1:3001`。如需让同一局域网下的 **Android 客户端（`.apk`）** 或 **iOS / 手机浏览器** 直接播放视频，可在网页「设置 → 远程与隧道」一键开启 **允许局域网设备访问**（动态监听本机局域网 IP，无需重启），或在启动时指定 `--listen 0.0.0.0:3001`。

## 升级与回滚

1. 在控制台「设置 → 备份与恢复」创建一份数据库备份归档。
2. 停止正在运行的 `bgmcat` 进程（Windows 升级前必须先停止进程以释放 `.exe` 文件锁）。
3. 重新执行安装脚本或解压新版归档，使用原有的 `--data` 数据目录启动。
4. 安装器会自动将上一版可执行文件保存为 `bgmcat.previous`（或 `bgmcat.exe.previous`）；如需紧急回退，停服后将 `.previous` 覆盖回原文件名即可（跨数据结构变更回退请同步恢复升级前备份）。

## Docker 部署

```sh
docker compose up -d --build
docker compose logs -f bgmcat
```

- 容器镜像基于多阶段构建，内置 `ffmpeg` 与 `ca-certificates`，无需任何外部下载器进程，并以非 root 用户运行。
- 默认映射 `127.0.0.1:3001:3001`；通过反向代理提供 HTTPS 公网访问时，请同步设置 `ANIME_RSS_PUBLIC_URL` 为外部访问根地址，确保 Google Drive OAuth 回调地址准确。
- 仓库启用 `PUBLISH_CONTAINER=true` 变量并推送版本标签后，CI 也会构建对应的 GHCR 镜像。

## 卸载

- **原生服务版**：停止进程后删除安装目录中的 `bgmcat`（或 `bgmcat.exe`）及 `.previous` 备份。默认数据目录 `~/.bgmcat` 独立保留，确认不再需要时再手动删除。
- **Docker 版**：执行 `docker compose down` 移除容器并保留持久化数据卷（仅在确认彻底清空全部数据库与下载缓存时才附加 `-v`）。
