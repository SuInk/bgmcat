# 在线服务与故障处理

本文按当前 Go 实现列出所有外部在线交互与本地可选工具依赖。第三方站点不提供永久在线承诺；系统内置多层缓存与降级回退，不将任何私人中转或不可信免费代理作为必需依赖。

## 番剧元数据与资源站点

| 服务 / 域名 | 用途与接口 | 默认使用 | 故障时的行为与限制 |
| --- | --- | --- | --- |
| [Bangumi API](https://bangumi.github.io/api/) · `api.bgm.tv` | `/calendar` 当季每日放送，`/v0/search/subjects` 全库多维筛选与关键词搜索，`/v0/subjects` 简介评分，`/v0/episodes` 分集 | 首页当季与全库主源 | 成功的当季日历、全库筛选页、条目详情与分集均持久化缓存；短期上游波动可自动回退近 7 天缓存数据。 |
| [AniList API](https://github.com/AniList/ApiV2-GraphQL-Docs) · `graphql.anilist.co` | GraphQL 番剧聚合搜索、封面、简介、评分 | 聚合搜索开启 | 独立超时隔离；失败只记录在平台状态提示中，不影响 Bangumi 与其他平台返回。 |
| [Jikan](https://docs.api.jikan.moe/) · `api.jikan.moe` | MyAnimeList 番剧搜索补充 | 默认关闭 | 第三方非官方 MAL 接口，仅作可选补充。 |
| [TMDB API](https://developer.themoviedb.org/docs/getting-started) · `api.themoviedb.org` | TV 搜索、简介评分与 `image.tmdb.org` 封面 | 默认关闭（需配置 API Key） | 不配置不影响任何核心追番与全库筛选功能。 |
| 蜜柑（Mikan）· 默认 `mikanani.me` | `/RSS/Search?searchstr=...` 资源检索与连载订阅 | 默认启用，可在设置修改域名 | 站点超时或地区网络受阻时回退最近有效缓存并在详情抽屉提示源状态；旧 `mikanime.tv` 地址会自动规范化至 `mikanani.me`。 |
| Nyaa · `nyaa.si` | RSS 资源检索与订阅 | 默认关闭，可按需开启 | 独立并发请求与失败隔离，不阻塞其他资源站。 |
| Acg.Rip · `acg.rip` | `.xml?term=...` RSS 资源检索与订阅 | 默认关闭，可按需开启 | 同上。 |
| BT Tracker / DHT 网络与 HTTP 直链源 | 内置纯 Go `NativeEngine` 下载实际媒体文件 | 下载任务执行时 | 可用性与速度取决于做种 peer 数量、Tracker 可达性及本机 UDP/TCP 网络环境。 |
| 封面图片 CDN | 浏览器与手机客户端懒加载海报封面 | 展示卡片时 | 采用异步解码与失败占位兜底，封面不可达不阻塞列表筛选、订阅或播放。 |

详情抽屉采用两阶段渐进加载：先通过 `?view=summary` 快速返回番剧简介与评分，再并发拉取分集列表与各资源站 RSS（单次上游等待预算 **6 秒**），切换或关闭抽屉不会让旧响应串台覆盖当前番剧。

## 缓存与首屏体验

- 前端首屏优先渲染浏览器本地缓存的上次排期快照，后台静默同步最新数据，不再因外网 API 慢响应阻塞界面。
- 当季日历缓存 1 小时，全库多维索引查询按筛选组合缓存，Bangumi 条目详情缓存 6 小时，资源检索缓存 2 分钟；并发相同查询自动合并（Singleflight），本地 SQLite 持久化最多 512 条发现缓存。
- 应用自身的脚本、样式与图标全部内置打包在二进制或 APK 本地资源中，不依赖任何第三方前端公共 CDN。

## 其他在线与本机可选组件

| 服务 / 组件 | 用途 | 是否必需 |
| --- | --- | --- |
| Google Drive API · `accounts.google.com`、`oauth2.googleapis.com`、`www.googleapis.com`、`drive.usercontent.google.com` | 网盘 OAuth 授权、8 MiB 分块归档上传、云端 HTTP Range 流式播放与可选 IP 直连测速 | 仅启用 Google Drive 归档时需要；纯本地下载与局域网播放无需连接 Google。 |
| GitHub · `github.com`、`api.github.com` | 检查版本更新、下载 Release 资产、可选 GitHub 白名单登录 | 日常运行与本地账号密码登录完全不依赖 GitHub。 |
| Cloudflare Tunnel · `trycloudflare.com` 或自有绑定域名 | 外网加密穿透供手机远程点播 | 仅外网远程访问需要；家庭 Wi-Fi 局域网直连或 Tailscale 无需开启。 |
| 本机 `ffmpeg` / `ffprobe` | MKV / HEVC 等非浏览器原生格式的按需 HLS 实时转码与软字幕提取 | 可选组件（Docker 镜像已内置）；H.264 MP4 原画流式播放无需安装。 |
| 本机 / 局域网 `qBittorrent` | 可选替代外部下载客户端 | 不必需；默认由内置纯 Go `NativeEngine` 直接完成全部 HTTP 与 BT 下载。 |
| 本机 / 远程 `Ollama`（默认 `127.0.0.1:11434`） | 可选 AI 模型辅助对话与筛选 | 不参与核心追番、下载与播放链路。 |

## 版本检查说明

仅当用户在侧栏底部打开「版本与更新」并主动点击「检查更新」时，服务端才会请求 `api.github.com/repos/SuInk/bgmcat/releases`（超时预算 15 秒）；页面平时打开绝不静默联网上报。私有仓库阶段支持读取服务端环境变量 `GH_TOKEN` / `GITHUB_TOKEN` 或已登录的 `gh` CLI 凭据。
