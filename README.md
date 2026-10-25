<div align="center">

# 猫追番 · Bangumi Cat

**订阅番剧、自动下载、归档到 Google Drive，再直接播放。**

Go 单文件服务 · 内置 HTTP / BitTorrent · Vue 控制台 · Diana / MCP

[安装与部署](docs/install.md) · [云盘与使用说明](docs/usage.md) · [版本发布](docs/releasing.md) · [Limited Redistribution License (SuInk)](LICENSE)

</div>

## 这个项目做什么

- **自动追番**：RSS 订阅、关键词过滤、同集字幕组与清晰度择优。
- **内置下载**：Go 实现 HTTP 断点续传与 BT 下载，也可连接 qBittorrent。
- **云盘归档**：直接连接 Google Drive，分块上传、大小和 MD5 校验后按设置清理本地文件。
- **在线播放**：本地或云端 Range 播放；可选 FFmpeg 提供转码和字幕。
- **AI 接入**：MCP 查询订阅、管理下载，支持 Diana 本机一键配置。

运行发布包不需要 Go、Node.js、Electron、aria2、gdirect 或 rclone。Vue 页面已编译并嵌入 Go 程序。FFmpeg/ffprobe 和 cloudflared 是按功能选择的外部组件，Docker 包含 FFmpeg。

> 当前正在准备首个 Go Release，仓库为 `SuInk/bgmcat`。下面的源码启动现在可用；Release 安装须等发布资产后使用。推荐先用私有仓库验证。

## 先跑起来

源码环境需要 Go 1.26+、Node.js 22+：

```sh
sh scripts/build.sh
./bin/bgmcat
```

打开 `http://127.0.0.1:3001`，输入启动终端显示的访问口令。默认只监听本机，数据保存在 `~/.anime-rss-go`。关闭网页不会停止服务。

或者在项目目录用 Docker：

```sh
docker compose up -d --build
docker compose logs bgmcat
```

## 发布包安装

Release 提供 Linux amd64/arm64、macOS Intel/Apple Silicon、Windows amd64 五个平台的完整包，包含程序、启动脚本、文档和许可证，以及统一的 `SHA256SUMS`、`latest.json`。网页已嵌入程序，不需要另行部署。

**私有仓库**：先 `gh auth login` 并取得仓库读取权限，再在已克隆的项目目录执行：

```sh
sh scripts/install.sh --repository SuInk/bgmcat --private
```

Windows PowerShell：

```powershell
.\scripts\install.ps1 -Repository SuInk/bgmcat -Private
```

安装器只安装程序、校验 SHA-256，保留旧程序副本和应用数据；不会自行启动或重启服务。升级时使用同一条命令，之后重启服务。默认安装位置、指定版本、公开仓库安装及手动解压见[安装文档](docs/install.md)。

## Google Drive 与空间占用

在设置 → 下载中填写 Google OAuth 客户端信息并连接账号，指定文件夹后开启归档。自动删除本地文件默认关闭。

下载阶段仍需要临时本地空间；归档校验成功后才能清理。云端播放按需读取，速度取决于网络和 Drive 服务。转码会产生有上限、会清理的临时缓存。具体步骤见[使用说明](docs/usage.md#google-drive)。

## 日常管理

```sh
bgmcat --version
bgmcat --data /absolute/path/data --listen 127.0.0.1:3001
```

本项目目前没有 Diana 的 `status`、`restart`、`passwd` 或 WebUI 自更新命令。原生进程由终端或你配置的系统服务管理，Docker 使用 Compose 管理。数据迁移前请阅读[迁移说明](docs/usage.md#数据迁移)。

## 开发与发布

```sh
npm ci
npm run typecheck
npm run build
go test -race ./...
go vet ./...
python3 scripts/package.py
```

版本基线在 [`internal/version/VERSION`](internal/version/VERSION)。推送匹配的 `vX.Y.Z` 标签时，CI 完成测试、五平台打包与 Docker 构建后生成 Release；预发布标签不会覆盖 Docker `latest`。目前不自动生成 Canary。完整约定见[发布文档](docs/releasing.md)。

沿用原 Anime RSS 项目的 Limited Redistribution License (SuInk) 许可，依赖声明见 [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md)。参考 Diana 的文档组织和交付流程；没有复制它的安装器实现，也没有改变本项目许可证。

猫追番是独立项目，与番组计划（Bangumi）无隶属关系。为兼容已有数据，当前保留 `ANIME_RSS_*` 环境变量、`~/.anime-rss-go` 数据目录及已有 MCP 配置标识。

开发者提交配置：运行 `sh scripts/setup-hooks.sh` 启用提交信息清理钩子，需要 Python 3。脚本仅移除 Claude 自动署名，不改动作者、许可证或历史提交。
