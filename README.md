<div align="center">

# 猫追番 · Bangumi Cat

**电脑端自动追番并归档到云盘，手机与网页随时直连流式点播 —— 单二进制自托管，内置纯 Go BT 引擎、正版播放页直达与漫画阅读。**

<sub>Electron 桌面端 · Android 客户端 · iOS / Web PWA · 纯 Go BT / HTTP 引擎 · Google Drive / OneDrive / WebDAV · Diana MCP</sub>

[![CI](https://github.com/SuInk/bgmcat/actions/workflows/build.yml/badge.svg)](https://github.com/SuInk/bgmcat/actions/workflows/build.yml)
[![Release](https://img.shields.io/github/v/release/SuInk/bgmcat?color=4b83ff)](https://github.com/SuInk/bgmcat/releases/latest)
[![Go](https://img.shields.io/badge/Go-1.26-00ADD8)](https://go.dev/)
[![License](https://img.shields.io/badge/License-Limited%20Redistribution-informational)](./LICENSE)

[桌面版与手机直连](docs/desktop.md) · [安装与部署](docs/install.md) · [云盘与日常使用](docs/usage.md) · [在线服务与排障](docs/online-services.md) · [版本发布](docs/releasing.md)

</div>

## 猫追番是什么

猫追番（Bangumi Cat）围绕**「本机自动追番下片 + 云盘自动归档省空间 + 手机与浏览器随时点播」**构建一站式自托管追番与阅读中心。单个 Go 可执行文件已内嵌编译好的 Vue 3 控制台与纯 Go 下载内核，装完即跑：

- **装完就能用** —— 零外部 `aria2` / `Node.js` / `rclone` 依赖；桌面版双击自动免密握手，服务版一条命令完成安装与校验。
- **数据在你手里** —— 订阅规则、观看进度、漫画阅读历史、凭据全部保存在本机 SQLite（默认 `~/.bgmcat`），支持备份恢复与存储空间环形占比可视化。
- **小硬盘也能养全套番库** —— 下载完成后按分块断点上传至 **Google Drive / OneDrive / WebDAV**，云端大小与哈希校验通过后再按策略释放本机空间；点播时直接按 HTTP Range 流式拉取或实时转码。
- **全番剧正版直达 + 多源聚合** —— 每部番剧自动聚合哔哩哔哩、巴哈姆特动画疯、木棉花 / Ani-One YouTube、Netflix、Crunchyroll 等正版播放页直达入口，同时并发检索蜜柑（Mikan）、动漫花园、萌番组、Nyaa 等多 BT 资源源。

## 三分钟跑起来

**① 一键安装服务版（Linux / macOS / Windows）或运行桌面版：**

```sh
# Linux / macOS 一键安装并启动
curl -fsSL https://raw.githubusercontent.com/SuInk/bgmcat/main/scripts/install.sh | sh -s -- --start
```

```powershell
# Windows PowerShell
irm https://raw.githubusercontent.com/SuInk/bgmcat/main/scripts/install.ps1 | iex
```

```sh
# 或者使用 Docker Compose（镜像已内置 ffmpeg）
docker compose up -d --build
```

原生一键安装不需要管理员权限，**升级也是重跑同一条命令**（自动校验 SHA-256 并保留上一版 `.previous` 备份），详见 [安装文档](docs/install.md)。电脑日常使用可直接安装打包好的 `bgmcat-desktop`（`.dmg` / `.exe` / `.deb` / `.AppImage`），详见 [桌面版说明](docs/desktop.md)。

**② 登录控制台：**

浏览器打开 `http://127.0.0.1:3001`。首次启动会在终端打印一次性的初始管理员账号与随机密码（**只显示一次，请当场保存**）；忘记密码可执行 `bgmcat --reset-password` 重置。手机端可在「设置 → 远程访问」直接扫码免输口令一键登录。

**③ 开始追番：**

1. **番剧发现**：默认聚焦「当季新番 · 今日放送」（附真实周历日期，无封面条目自动沉底），也可切换「番剧片库」按类型、地区、年份、季度、题材与评分多维筛选；
2. **订阅或直达观看**：点开任意番剧默认定位最新话，既可一键直达正版播放页，也可锁定字幕组/清晰度开启 RSS 自动追更，或用一句话智能筛选资源；
3. **手机随时看**：开启「局域网设备访问」或「Cloudflare 公网隧道」后，手机浏览器 / PWA 或 Android 客户端即可随时流式点播本地与云盘番剧。

## 多端形态一览

| 端形态 | 适用设备 | 说明 |
| --- | --- | --- |
| **电脑桌面端（推荐）** | macOS (`arm64` / `x64`)、Windows `x64`、Linux `x64` | 内置受管 Go 后端与独立窗口，开机即用、免输口令，详见 [桌面版说明](docs/desktop.md) |
| **iOS / 移动端网页 PWA** | iPhone / iPad Safari、手机与桌面浏览器 | 首屏单次 `/api/bootstrap` + Gzip 极速加载，适配刘海与底部安全区，支持添加到主屏幕与扫码秒登 |
| **Android App** | Android 8.0+ 手机 / 平板（arm64 / x86_64） | 内置服务，装好即用；也可切换为连接远程服务器（局域网主地址 + Cloudflare Tunnel 备用地址自动切换）；横屏沉浸全屏与常亮 |
| **无头服务版 / Docker** | Linux / macOS / Windows 服务器、NAS | 单二进制常驻运行，资源占用极低，详见 [安装文档](docs/install.md) |

## 内置能力

- **当季时间表与全库片库** —— 当季每日放送表支持周历真实日期标注与午夜自动切换，全库模式支持类型（TV / 剧场版 / OVA / WEB）、地区、受众、年份区间、季度、风格标签与评分组合检索。
- **多平台播放页直达** —— 融合 `bangumi-data` 全量索引、B 站番剧/国创 `ss` 播放页匹配、AniList Streaming 外链与 Bangumi 信息栏，无论有无 BT 片源均可一键跳往对应正版平台观看。
- **多源 BT 聚合与智能选源** —— 并发检索蜜柑计划、AnimeGarden、动漫花园、萌番组、末日动漫、ACG.RIP、Nyaa 等资源源，自动解析字幕组画像、简繁内嵌/内封规格与发布时间，支持自然语言条件筛选与可选本地 AI 挑片。
- **漫画发现与沉浸式阅读器** —— 内置独立「漫画阅读」页签，支持热门、最新更新、高分与全彩条漫浏览搜索，提供「下拉条漫」与「左右翻页（含日漫右起模式 + 键盘方向键）」双阅读模式，服务端代理免除跨域防盗链困扰并自动记忆阅读章节进度。
- **纯 Go 下载引擎与实时速率监控** —— 自研 `NativeEngine` 原生支持 HTTP 多连接断点续传与 BitTorrent / Magnet / DHT / PEX 下载（支持顺序分片边下边播，顶栏实时展示上下行速率），也可切换外部 `qBittorrent` / `Transmission` / `aria2 RPC`。
- **多云盘归档、在线文件管理与外链分享** —— 支持 **Google Drive**（含低延迟 IP 优选与独立 TLS SNI 探测）、**OneDrive** 与 **WebDAV** 自动归档、本地与云盘双视图文件浏览器、ArtPlayer + ASS 弹幕字幕在线播放，以及带访问密码与过期时间的剧集/整部番剧公开分享链接（`/s/...`）。
- **多种登录方式与 Diana MCP 集成** —— 支持本地账号密码（PBKDF2-HMAC-SHA256）、手机扫码免输口令登录、GitHub / Google / 自定义 OIDC 单点登录与 Diana 主人配对登录；内置 MCP 服务支持一键写入本机 Diana 配置（`--mcp-stdio` / `/mcp/sse`）。

## 日常管理与源码构建

常用命令行参数：

```sh
bgmcat --version                                      # 查看当前版本
bgmcat --data ~/.bgmcat --listen 127.0.0.1:3001       # 指定数据目录与监听地址启动
bgmcat --data ~/.bgmcat --reset-password              # 重置管理员密码并吊销旧会话
```

从源码构建与完整回归验证（需要 Go 1.26+、Node.js 22+）：

```sh
npm ci
npm run desktop:prepare       # 前端类型检查 + Vite 构建 + 编译本机 Go 单二进制
npm run desktop:test          # 桌面端进程握手与安全边界测试
go test -race ./...           # Go 后端全套单元与并发测试
go vet ./...
bash scripts/check-public-repo.sh
python3 scripts/test_install.py
```

版本唯一基线位于 [`internal/version/VERSION`](internal/version/VERSION)。推送匹配的 `vX.Y.Z` 标签时，[`.github/workflows/build.yml`](.github/workflows/build.yml) 自动执行仓库审计、全套测试、五平台服务包、四平台桌面安装包、Android APK 与 Docker 构建并发布 GitHub Release；[`site/`](site/index.html) 官网与在线演示由 [`.github/workflows/pages.yml`](.github/workflows/pages.yml) 自动部署至 GitHub Pages。详见 [发布文档](docs/releasing.md)。

## 安全须知

- 控制台默认强制登录鉴权，仅监听 `127.0.0.1:3001`；密码错误按来源 IP 退避限流，防止暴力枚举。
- OAuth 密钥、网盘刷新令牌、隧道 Token 等敏感凭据落库后不在读接口回传明文；开启局域网访问或公网穿透时请妥善保管管理员凭据，公网环境建议使用 Cloudflare Tunnel 或 HTTPS 反向代理。

## 使用声明

猫追番仅作个人使用的追番进度、RSS 订阅与下载归档管理工具，程序本身不提供、不托管、不分发任何影视或漫画资源文件。订阅哪些来源、访问或下载何种内容完全由使用者自行配置与决定；请遵守所在地法律法规及各内容平台、数据站点的服务条款，仅访问与下载拥有合法权利的内容。猫追番是独立项目，与番组计划（Bangumi）等第三方平台无隶属关系。

## 许可证

`Limited Redistribution License (SuInk)`，详见 [LICENSE](./LICENSE)；第三方开源组件声明见 [THIRD_PARTY_NOTICES.md](./THIRD_PARTY_NOTICES.md)。
