# 第三方组件声明 / Third-Party Notices

猫追番（Bangumi Cat）按 [`Limited Redistribution License (SuInk)`](./LICENSE) 许可发布。
下列第三方开源组件与外部程序各自沿用其自身许可协议，不受本项目许可约束，也不改变本项目的许可条款。

Bangumi Cat is released under the `Limited Redistribution License (SuInk)` (see `LICENSE`).
The third-party libraries and external tools below remain under their respective licenses.

## Go 后端直接依赖 / Go Runtime Dependencies

| 组件 | 用途 | 协议 |
| --- | --- | --- |
| [`github.com/anacrolix/torrent`](https://github.com/anacrolix/torrent) | 纯 Go BitTorrent / Magnet / DHT 下载引擎 | MPL-2.0 |
| [`github.com/mmcdole/gofeed`](https://github.com/mmcdole/gofeed) | RSS / Atom 订阅流解析 | MIT |
| [`modernc.org/sqlite`](https://gitlab.com/cznic/sqlite) | 纯 Go SQLite 驱动（SQLite 核心为公有领域） | BSD-3-Clause |

完整的 Go 依赖与哈希由 `go.mod` 与 `go.sum` 记录。

## 前端与桌面端依赖 / Frontend & Desktop Dependencies

| 组件 | 用途 | 协议 |
| --- | --- | --- |
| [Vue 3](https://vuejs.org/) | Web 控制台界面框架 | MIT |
| [`@lucide/vue`](https://lucide.dev/) | 界面矢量图标 | ISC |
| [ArtPlayer](https://artplayer.org/) / [ASS.js](https://github.com/weizhenye/ASS) | 在线视频播放器与 ASS 弹幕/字幕渲染 | MIT |
| [hls.js](https://github.com/video-dev/hls.js) | HLS 实时转码流播放 | Apache-2.0 |
| [Zod](https://zod.dev/) | 配置与接口数据结构校验 | MIT |
| [Electron](https://www.electronjs.org/) | 桌面客户端外壳（安装包内保留 Chromium 的 `LICENSES.chromium.html`） | MIT |

构建期工具（Vite、Tailwind CSS、TypeScript、`electron-builder`、Gradle）仅用于编译打包，不作为运行时代码随独立服务包分发。前端锁定的确切版本见 `package-lock.json`。

## 随桌面安装包分发的程序 / Programs Bundled with the Desktop App

桌面安装包内附带 FFmpeg / FFprobe 的预编译静态程序，用于 MKV / HEVC 等非原生编码的实时转码、内封字幕提取和缩略图生成。它们是独立的可执行文件，猫追番只通过子进程调用，未与本项目代码静态或动态链接。这些构建启用了 libx264 等 GPL 组件，整体按 **GNU General Public License v3** 分发，许可全文随安装包附在 `ffmpeg-COPYING.GPLv3.txt`。设置里「ffmpeg 路径」留空时优先使用内置程序，找不到再使用系统安装的 FFmpeg。

The desktop app ships prebuilt static FFmpeg / FFprobe executables, invoked only as separate processes. These builds are distributed under the GNU GPL v3; the full license text is included as `ffmpeg-COPYING.GPLv3.txt`.

| 平台 | 版本 | 预编译来源 | 对应源码 |
| --- | --- | --- | --- |
| macOS arm64 / x64 | 9.1 | [martin-riedl.de](https://ffmpeg.martin-riedl.de/)（构建配置见下载目录中的 `versions.txt`） | [ffmpeg-9.1.tar.xz](https://ffmpeg.org/releases/ffmpeg-9.1.tar.xz)、[构建脚本](https://git.martin-riedl.de/ffmpeg/build-script) |
| Windows x64 | 9.1 | [gyan.dev](https://www.gyan.dev/ffmpeg/builds/) release essentials（[GyanD/codexffmpeg 9.1](https://github.com/GyanD/codexffmpeg/releases/tag/9.1)） | [FFmpeg 74e4393595](https://github.com/FFmpeg/FFmpeg/commit/74e4393595)、包内 `README.txt` 列出的组件版本 |
| Linux x64 | 7.0.2 | [johnvansickle.com](https://johnvansickle.com/ffmpeg/) amd64 static | [release-source](https://johnvansickle.com/ffmpeg/release-source/) |

各构建的下载地址与 SHA-256 固定在 `scripts/desktop-ffmpeg.cjs`。如需上表源码而无法从上述地址取得，可在本项目 GitHub Issues 中索取，我们会提供对应版本的源码副本。

## 可选外部程序 / Optional External Programs

以下外部工具不随 Go 发布包捆绑分发（FFmpeg 除外：桌面安装包内置，见上节），由用户或系统包管理器按需安装，运行时仅通过独立进程或网络接口调用，未与本项目代码静态或动态链接（Docker 镜像从 Debian 官方源安装 `ffmpeg`，其版权说明位于镜像内 `/usr/share/doc/ffmpeg/copyright`）：

| 程序 | 用途 | 协议 |
| --- | --- | --- |
| [FFmpeg / FFprobe](https://ffmpeg.org/) | 可选的非原生编码实时转封装/转码与内封字幕提取 | LGPL-2.1-or-later 或 GPL（取决于用户本机编译配置，可通过 `ffmpeg -version` 查看） |
| [cloudflared](https://github.com/cloudflare/cloudflared) | 可选的 Cloudflare Tunnel 公网/远程访问隧道 | Apache-2.0 |
| [qBittorrent](https://www.qbittorrent.org/) / [Transmission](https://transmissionbt.com/) / [aria2](https://aria2.github.io/) | 可选的外部下载器（仅通过 WebUI / RPC API 远程连接） | GPL-2.0-or-later |

## 数据来源声明 / Data Sources

番剧与漫画元数据、评分、封面及正版播放站点索引来自 [Bangumi 番组计划](https://bgm.tv/)、[bangumi-data](https://github.com/bangumi-data/bangumi-data)（MIT）、[AniList](https://anilist.co/)、[MyAnimeList](https://myanimelist.net/)、[MangaDex](https://mangadex.org/)，以及用户自行配置密钥后启用的 [TMDB](https://www.themoviedb.org/)。
本产品支持可选调用 TMDB API，但未经 TMDB 认可或认证（This product uses the TMDB API but is not endorsed or certified by TMDB）。
各平台数据的版权归其各自权利人所有，使用时请遵守对应平台的服务条款。
