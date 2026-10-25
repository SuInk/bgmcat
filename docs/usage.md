# 猫追番 · Bangumi Cat

Go 服务端的番剧订阅、下载与 Google Drive 归档。HTTP API、SQLite、RSS、HTTP/BitTorrent 下载、云盘 OAuth/上传/读取、MCP 都在 Go 进程内运行。保留 Vue 浏览器界面，构建后嵌入可执行文件。

不需要 Node.js、Electron、gdirect 或 rclone；HTTP/BT 下载需要 aria2c，由 bgmcat 自动启动和管理。Node.js 仅用于从源码构建网页。FFmpeg/ffprobe 是 MKV、HEVC 等转码及字幕提取的可选组件，Docker 镜像包含它们；普通 MP4 直播放不依赖它们。Cloudflare Tunnel 仅在启用穿透时需要 cloudflared。

## 本地启动

开发构建需要 Go 1.26+、Node.js 22+ 与 npm：

```sh
sh scripts/build.sh
./bin/bgmcat
```

打开 http://127.0.0.1:3001 ，输入启动输出中的访问口令。本机访问也需要口令。默认数据目录 `~/.anime-rss-go`，默认仅监听回环地址。

```sh
./bin/bgmcat --data /absolute/path/data --listen 127.0.0.1:3001
```

本地安装可用 `sh scripts/install-local.sh`，放入 `~/.local/bin/bgmcat`。这是从本项目源码构建安装，不是已经发布到互联网的远程安装器。

Docker 一条命令构建启动：

```sh
docker compose up -d --build
docker compose logs bgmcat
```

容器使用独立持久化卷与非 root 用户。公网部署由反向代理提供 HTTPS，设置 `ANIME_RSS_PUBLIC_URL` 为实际外部 URL，并自行调整 Compose 端口映射。不要把访问口令提交到仓库。

## Google Drive

打开「设置 → 下载」里的「让番剧有个家」向导；也可选择暂时使用本地存储，之后点击「配置向导」重新打开。

1. 在 Google Cloud 启用 Drive API，配置 OAuth 同意屏幕并将自己的邮箱加入测试用户，创建 **Web application** OAuth 客户端。
2. 将向导显示的回调地址添加到授权重定向 URI：当前网页预览是 `http://127.0.0.1:3001/api/cloud/callback`；桌面版是 `http://127.0.0.1:39123/api/cloud/callback`；远程部署使用实际 HTTPS 地址。必须与访问的服务一致。
3. 下载 OAuth JSON 并导入向导，或手动填写 ID 和密钥。当前仅支持 Web application 凭据，不接受服务账号 / Desktop 类型 JSON。点击连接后在浏览器授权，向导会在十分钟内自动检查授权结果，也可手动检查。
4. 浏览「我的云盘」的文件夹，选择当前目录，或新建 `Bangumi Cat`。分页目录可点击加载更多；共享云盘目录仍可通过高级设置手动填写 ID。
5. 检测会创建临时文本文件、上传随机内容、读回校验并删除测试文件。失败会显示原因；清理失败会给出测试文件 ID，供手动删除。通过后才能在向导中完成并开启自动归档。
6. 默认保留本地副本。可主动选择归档后清理，仅在全部文件云端大小及 MD5 校验通过、可读取且没有正在播放时删除。

TLS / IP 扫描和手动配置收在「高级设置」。导入 JSON 不会改变网络模式；默认使用原始 TLS。当前仍需自行创建 Google OAuth 客户端，并非内置公共客户端的一键登录。

API 实现依据 [Google files.list](https://developers.google.com/workspace/drive/api/reference/rest/v3/files/list) 与 [files.create](https://developers.google.com/workspace/drive/api/reference/rest/v3/files/create)。

Go 直接使用 Google Drive API，以 8 MiB 分块断点上传；预先保存远端 ID，避免重试重复创建。凭据、会话地址仅保存在私有服务端 SQLite/备份中，不返回上传会话地址给网页。

本应用不是云端 BT 离线下载服务：下载期间仍需临时本地空间，完成后上传并按设置清理。已归档视频按 HTTP Range 从云端读取，不需要挂载完整云盘。播放速度取决于网络、Drive 限制和视频编码，不能保证始终更快。转码按需生成最多三分钟窗口，限制同时两路，闲置缓存五分钟后清理。

External / Testing 模式的 Drive OAuth refresh token 会在 7 天后过期，需要重新授权，见 [Google 授权说明](https://developers.google.com/identity/protocols/oauth2#expiration)。真实账号授权和上传需用户完成，自动测试使用模拟 Drive 服务。

## 数据迁移

原 TypeScript 项目和它的数据保持独立。先停止旧服务或用 SQLite backup API 生成一致性数据库副本，再导入到一个**新的空数据目录**：

```sh
./bin/bgmcat --data /absolute/path/new-go-data --import-legacy /absolute/path/old-app-copy.db
./bin/bgmcat --data /absolute/path/new-go-data
```

导入设置、订阅、RSS 条目、下载记录和收藏；事务失败时不留下半份数据。旧数据库只读打开。旧未完成下载以暂停状态导入，恢复后使用 Go 独立目录重新下载，不复用 aria2/qBittorrent 的私有断点格式。旧完整文件可继续本地播放。云盘归档默认关闭，需要重新授权；旧版挂载及归档记录不自动接管。旧 Electron 会话不迁移。Go 版备份是独立 ZIP 格式，包含敏感凭据，恢复前自动备份当前数据库。

## 运行差异

- 支持浏览器和 Electron 桌面窗口。关闭网页不停止独立 Go 服务；退出桌面版会停止它托管的服务。
- Go 托管 aria2c HTTP/BT 下载，默认最多四个任务同时执行，下载完成后不持续做种。qBittorrent 可选，需要本机能访问相同下载目录。
- RSS 关键词过滤、合集过滤、自动下载；订阅刷新去重、相同下载 URL 去重。同集按字幕组、清晰度偏好择优，已开始下载的版本不会被自动替换。
- Bangumi 首页；Bangumi、AniList、MAL/Jikan、TMDB 搜索（TMDB 需 Key）；蜜柑、Nyaa、ACG.RIP 资源 RSS。
- MCP：`POST /mcp`（HTTP JSON-RPC），以及 `/mcp/sse` + `/mcp/messages`，使用 `Authorization: Bearer <token>`。
- 可选 GitHub 白名单登录、Ollama、Cloudflare Tunnel。命名隧道支持 token/credentials-file，并支持异常退出后有限次数自动重连。
- 当前为源码构建，没有已发布的 Go 在线更新源；更新需重新构建替换二进制。构建工作流可生成 Linux/macOS/Windows 产物。

## 验证

```sh
npm ci
npm run typecheck
npm run build
go test -race ./...
go vet ./...
```

测试覆盖鉴权与页面路由、RSS 去重、HTTP 续传及错误 Range、原生 BT 本地真实传输、备份恢复、旧库原子迁移、Drive 上传校验与删除保护、云端 Range 播放、MCP、FFmpeg HLS。没有安装 FFmpeg 的机器会跳过媒体集成测试。Docker 与各操作系统产物需在对应环境继续验证。

许可证沿用原项目 Limited Redistribution License (SuInk)，详见 ../LICENSE；依赖许可见 ../THIRD_PARTY_NOTICES.md。

## Diana MCP 接入

MCP 页面支持检测 Diana、接入、诊断与移除。默认寻找系统缓存目录 `diana/workspace` 对应的数据目录、相邻 `diana/data`，优先遵循工作目录 `.diana/extension-paths.json` 的固定路径。检测到多个位置时要求手动选择，不能默默写入另一份配置。也可展开“Diana 配置位置”，填写 `.mcp.json` 绝对路径。

接入会保留其他 MCP 服务，备份原配置并以 0600 权限原子写入。Diana 默认限制 HTTP MCP 的内网地址，因此本机接入使用当前 Go 程序的 `--mcp-stdio` 模式，凭据保存在 Diana MCP 配置的环境变量中；不会更改 Diana 网络限制。Bangumi Cat 服务需要持续运行，移动可执行文件或更换口令后需重新接入，再在 Diana 重新加载 MCP 或重启。此按钮用于两者可以访问同一文件系统的本机部署；不同容器或远程 Diana 请在对应环境配置可访问的服务。

## Google Drive IP 直连

设置 → 下载 → **Google Drive IP 直连** 中可扫描候选 IP，再开启直连。扫描无需 Google 登录，不携带账号凭据。

- 默认候选来自系统 DNS、近 7 天验证过的历史地址，以及一个内置种子地址。也可填写自己的公网 IP，每行一个，支持 `#` 注释，最多 256 个；填写后仅扫描该列表。
- 同时最多探测 8 个地址，检查真实 Google 域名的 TLS 证书、Drive API 的预期 401 响应和一个公开测试文件。测试文件丢失或内容改变时会显示“仅通过 API 验证”，不承诺文件下载正常。
- 扫描最长约 60 秒，超时保存已完成的部分结果。选择最近 24 小时验证成功、探测耗时最低的地址。到期需重新扫描；没有有效地址时自动使用系统网络。
- 开启后作用于 Go 服务的 Drive API、上传及云端播放读取，OAuth 授权和令牌刷新继续走原来的系统网络。实际传输连接失败会标记该 IP 不可用，下次请求选择其他有效 IP 或系统网络；不会自动重放失败的上传请求。
- 不修改系统 DNS。SNI 可选择原始域名（默认）、自定义 ASCII 域名或不发送；证书链、有效期、服务端用途和真实 Google 域名仍严格校验。直连模式不使用 HTTP 代理，但 TUN/VPN 仍可能接管连接。
- 探测结果属于运行 bgmcat 的机器，响应耗时不是下载带宽测试；这项功能不会生成浏览器绕过 bgmcat 的播放直链。

网络配置与扫描结果持久化在应用数据库中。API 为 `GET/PATCH /api/cloud/network` 和 `POST /api/cloud/network/scan`，均要求应用鉴权；扫描请求体示例 `{"candidates":""}`，开关请求体示例 `{"enabled":true}`。

### Drive TLS / SNI 模式

在“设置 → 下载 → Google Drive IP 直连 → TLS SNI 模式”选择模式。自定义时只填写域名，不含协议、端口、路径或通配符。点击“扫描可用 IP”会先保存当前模式，再使用同一模式探测；修改模式会清除旧结果并关闭旧连接池。已有进行中的请求仍按启动时模式完成。扫描中修改模式时，该次旧模式结果不会写回。

可用结果选中后，Go 发出的 Drive API、上传及云端 Range 播放请求都会使用此模式。没有可用结果或关闭直连时，回到原系统网络，不应用自定义 SNI。OAuth 认证/刷新始终使用系统网络；独立 aria2c 的普通 HTTP/BT 下载不受此设置影响。

这实现了 gdirect 补丁中独立控制 SNI 的同类行为，使用 Go 标准库，不链接修改后的 libaria2，也不模拟它的 OpenSSL 指纹。自定义模式通过独立 TLS 握手发送 SNI，并在 `VerifyConnection` 中用原始请求域名执行 `x509.Verify`；证书不可信、过期、域名不符或用途不符均拒绝。自定义/无 SNI 使用 HTTP/1.1，原始模式仍保留默认协议协商。

参考：[gdirect 的 aria2 补丁](https://github.com/mizorewww/gdirect/blob/350d1bed3b5604c58c96dcd85f7106d4958aca2c/vendor/aria2-1.37.0.patch)、[Go VerifyConnection](https://pkg.go.dev/crypto/tls#Config.VerifyConnection)。本地握手测试验证的是 SNI 与证书校验行为；真实 Google 连接是否成功仍需在部署网络中扫描，不能从本地测试推断无代理可达性。
