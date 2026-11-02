# 第三方组件声明 / Third-Party Notices

猫追番（Bangumi Cat）以 [GNU Affero General Public License v3.0](./LICENSE)（AGPL-3.0-only）发布，沿用其前身 Anime RSS 项目的许可。
下列组件各自沿用自己的许可协议，不受本项目协议约束，也不改变本项目的协议。

Bangumi Cat is licensed under the AGPL-3.0-only (see `LICENSE`), inherited from its predecessor Anime RSS.
The components below remain under their own licenses.

## 编译进程序或随安装包分发 / Included in releases

| 组件 | 用在哪 | 协议 |
| --- | --- | --- |
| [Electron](https://www.electronjs.org/) 45.0.0 | 桌面版外壳；安装包内另附 Chromium 等组件的 `LICENSES.chromium.html` | MIT |
| [gofeed](https://github.com/mmcdole/gofeed) | Go 后端解析 RSS | MIT |
| [modernc.org/sqlite](https://gitlab.com/cznic/sqlite) | Go 后端数据库（SQLite 核心为公有领域） | BSD-3-Clause |
| [Vue](https://vuejs.org/) | 网页界面 | MIT |
| [ArtPlayer](https://artplayer.org/) / [artplayer-plugin-assjs](https://github.com/zhw2590582/ArtPlayer) / [ASS.js](https://github.com/weizhenye/ASS) | 网页播放器与 ASS 字幕 | MIT |
| [hls.js](https://github.com/video-dev/hls.js) | 网页播放 HLS | Apache-2.0 |

Go 的间接依赖见 `go.mod` / `go.sum`，网页依赖的确切版本见 `package-lock.json`，它们的许可文本随各自的源码包保留。
[anacrolix/torrent](https://github.com/anacrolix/torrent)（MPL-2.0）只在测试里充当本机 BT 种子服务，不进入发布的程序。
Vite、Tailwind CSS、TypeScript、electron-builder 等只用于构建，不随程序分发。

## 外部程序 / External programs

这些程序不随 Go 发布包和桌面安装包分发，由用户或系统包管理器安装，运行时以独立进程调用，不与本项目代码链接。
Docker 镜像从 Debian 官方源安装 aria2 和 FFmpeg，它们的许可文件位于镜像内 `/usr/share/doc/<包名>/copyright`。

| 程序 | 用途 | 协议 |
| --- | --- | --- |
| [aria2](https://aria2.github.io/) | 内置下载器（通过本机私有 JSON-RPC 管理） | GPL-2.0-or-later（附 OpenSSL 例外） |
| [FFmpeg / FFprobe](https://ffmpeg.org/) | 在线播放的转封装与转码 | LGPL-2.1-or-later 或 GPL，取决于编译配置（`ffmpeg -version` 可查看） |
| [cloudflared](https://github.com/cloudflare/cloudflared) | 可选的 Cloudflare 隧道 | Apache-2.0 |
| [qBittorrent](https://www.qbittorrent.org/) | 可选的外部下载器（通过其 WebUI API） | GPL-2.0-or-later |

## 数据来源 / Data sources

番剧信息、评分与封面来自 [Bangumi 番组计划](https://bgm.tv/)、[AniList](https://anilist.co/)、
[MyAnimeList](https://myanimelist.net/)，以及启用后的 [TMDB](https://www.themoviedb.org/)；资源检索默认使用 [蜜柑计划](https://mikanani.me/) 的 RSS。
本产品使用 TMDB API，但未经 TMDB 认可或认证（This product uses the TMDB API but is not endorsed or certified by TMDB）。
各平台数据的版权归其所有者，使用时请遵守对应平台的服务条款。
