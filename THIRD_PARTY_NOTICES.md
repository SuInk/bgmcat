# Go 版本依赖说明

Go Release 压缩包不分发 Electron、aria2、gdirect 或 rclone；安装器通过系统包管理器安装 aria2，Docker 镜像包含发行版 aria2。新版 `bgmcat-desktop` 另行分发 Electron 45.0.0（MIT，含 Chromium 等第三方许可）和 Go 后端，不捆绑 aria2、FFmpeg 或 cloudflared。Electron 的许可文件随运行时保留。electron-builder 26.18.0（MIT）仅用于构建。下面保留原项目的许可记录供来源追溯，其中旧版附带外部程序的描述不适用于当前桌面包。

当前 Go 版直接依赖：

- github.com/anacrolix/torrent v1.61.0：MPL-2.0，仅用于集成测试的 BT 种子服务。
- github.com/mmcdole/gofeed v1.3.0：MIT。
- modernc.org/sqlite v1.61.0：BSD 风格许可，SQLite 核心为 public domain。
- Vue、Vite、HLS.js、Artplayer 等网页依赖按 package-lock.json 固定版本，沿用各自许可。

完整 Go 依赖清单由 go.mod/go.sum 记录。可选 FFmpeg 的许可取决于其编译选项；Docker 从 Debian 官方包源安装。Cloudflared 由用户自行安装，Apache-2.0。

---

# 第三方组件声明 / Third-Party Notices

Anime RSS 本身以 [Limited Redistribution License (SuInk)](./LICENSE)（Limited Redistribution License (SuInk)）发布。
下列组件随安装包一起分发，它们**各自沿用自己的许可协议**，不受本项目协议约束，也不改变本项目的协议。

Anime RSS itself is licensed under the Limited Redistribution License (SuInk) (see `LICENSE`). The components below are
redistributed alongside it and remain under their own licenses.

## 随安装包分发的独立程序 / Bundled executables

这些程序以独立进程方式调用，未与本项目代码链接。

| 组件 | 用途 | 协议 | 源码 |
| --- | --- | --- | --- |
| [aria2](https://aria2.github.io/) 1.37.0 | 内置下载器 | GPL-2.0-or-later（附 OpenSSL 例外） | <https://github.com/aria2/aria2/releases/tag/release-1.37.0> |
| [FFmpeg / FFprobe](https://ffmpeg.org/)（GPL 构建，含 libx264 / libx265） | 在线播放的转封装与转码 | GPL-2.0-or-later（GPL 构建，启用 `--enable-gpl`；若某平台构建另启用了 `--enable-version3` 则为 GPL-3.0-or-later，以该二进制 `ffmpeg -version` 显示的配置为准） | <https://ffmpeg.org/download.html#get-sources> |
| [cloudflared](https://github.com/cloudflare/cloudflared) 2026.9.1 | Cloudflare 隧道 | Apache-2.0 | <https://github.com/cloudflare/cloudflared/releases/tag/2026.9.1> |

各平台二进制的具体来源 URL 与 sha256 记录在打包时生成的 `resources/<组件>/<平台>/SOURCE.txt` 中；
取得方式见 `resources/aria2/README.md`、`resources/ffmpeg/README.md`、`resources/cloudflared/README.md`。

- **aria2**：Windows 为官方发布包；Linux 为 [abcfy2/aria2-static-build](https://github.com/abcfy2/aria2-static-build) 的静态构建；macOS 取自构建机本地安装（Homebrew）。
- **FFmpeg**：Windows / Linux 为 [BtbN/FFmpeg-Builds](https://github.com/BtbN/FFmpeg-Builds)（tag `autobuild-2026-09-11-13-20`，`n8.1.2-52-g5a03dfa0f6`，含完整构建脚本）；macOS x64 为 [evermeet.cx](https://evermeet.cx/ffmpeg/)；macOS arm64 为 [osxexperts.net](https://www.osxexperts.net/)。可用 `ffmpeg -version` 查看每个二进制的确切版本与编译配置。

如需上述 GPL 组件与本安装包所含二进制对应的完整源码，可按上表链接获取，或通过本项目仓库的 Issue 联系作者索取。

## 应用运行时依赖 / Runtime dependencies

| 组件 | 协议 |
| --- | --- |
| [Electron](https://www.electronjs.org/)（安装包内另附 Chromium 的 `LICENSES.chromium.html`） | MIT |
| [Vue](https://vuejs.org/) | MIT |
| [Express](https://expressjs.com/) | MIT |
| [cors](https://github.com/expressjs/cors) | MIT |
| [better-sqlite3](https://github.com/WiseLibs/better-sqlite3)（内含 SQLite，公有领域） | MIT |
| [axios](https://axios-http.com/) | MIT |
| [rss-parser](https://github.com/rbren/rss-parser) | MIT |
| [zod](https://zod.dev/) | MIT |
| [electron-store](https://github.com/sindresorhus/electron-store) | MIT |
| [Model Context Protocol SDK](https://github.com/modelcontextprotocol/typescript-sdk) | MIT |
| [ArtPlayer](https://artplayer.org/) / [ASS.js](https://github.com/weizhenye/ASS) | MIT |
| [hls.js](https://github.com/video-dev/hls.js) | Apache-2.0 |

以上依赖的传递依赖各自的协议文本保留在其 `node_modules/<包名>/` 目录中。

## 数据来源 / Data sources

番剧信息、评分与封面来自 [Bangumi 番组计划](https://bgm.tv/)、[AniList](https://anilist.co/)、
[MyAnimeList](https://myanimelist.net/)，以及启用后的 [TMDB](https://www.themoviedb.org/)。
本产品使用 TMDB API，但未经 TMDB 认可或认证（This product uses the TMDB API but is not endorsed or certified by TMDB）。
各平台数据的版权归其所有者，使用时请遵守对应平台的服务条款。

## aria2 下载进程

运行时通过私有本机 JSON-RPC 管理 aria2c（GPL-2.0-or-later，项目 https://github.com/aria2/aria2）。它不链接进 Go 程序，不引入 CGO。安装器使用系统包管理器获取；Docker 包含发行版的 aria2 包及其许可文件。anacrolix/torrent 仅作为本机 BT 集成测试的种子服务，不再进入生产下载器。
