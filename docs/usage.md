# 猫追番 · Bangumi Cat 使用说明

猫追番使用单个 Go 服务端统一承载番剧发现与全库筛选、RSS 自动追番、纯 Go HTTP / BitTorrent 下载、Google Drive 分块归档与按需流式播放、以及 Diana MCP 接口，前端 Vue 控制台已编译嵌入可执行文件。

运行时**无需安装 Node.js、aria2、gdirect 或 rclone**。`ffmpeg` / `ffprobe` 是实时转码 MKV / HEVC 与提取内封字幕的可选组件（Docker 镜像已内置，普通 MP4 原画流式播放不依赖它）；`cloudflared` 仅在启用 Cloudflare Tunnel 远程穿透时需要。

## 本地启动与登录认证

从源码构建并启动（需要 Go 1.26+、Node.js 22+ 与 npm）：

```sh
sh scripts/build.sh
./bin/bgmcat
```

打开 `http://127.0.0.1:3001`，使用首次启动终端输出的引导管理员账号与密码登录（桌面版窗口通过内部管道自动完成会话握手，无需手动输入）。密码采用 210,000 轮 `PBKDF2-HMAC-SHA256` 加盐哈希存储。

常用命令行参数：

```sh
# 指定数据目录与监听地址
./bin/bgmcat --data /absolute/path/data --listen 127.0.0.1:3001

# 忘记管理员密码时一键重置
./bin/bgmcat --data ~/.bgmcat --reset-password
```

在「设置 → 安全与访问」修改管理员用户名或密码后，会自动轮换会话盐值并将其他已登录设备踢下线。

## 番剧发现与全库多维筛选

控制台首页「发现番剧」提供两种无缝切换的浏览模式：

- **本季排期**：按「全部 / 周一 … 周日」展示当季新番每日更新表，支持即时过滤与按默认/评分排序。
- **全库索引**：点击「全库」切换至完整番剧片库（对接 Bangumi `/v0/search/subjects` 索引并带本地持久化缓存），支持按 **类型**（TV / 剧场版 / OVA / Web）、**地区**（日本 / 中国 / 欧美）、**受众 / 来源**（原创 / 漫画改 / 轻小说改 / BL / GL 等）、**年份与年代区间**、**季度**（1 / 4 / 7 / 10 月）、**风格标签** 以及 **排名 / 热度 / 评分 / 时间** 自由组合筛选，滚动到底部自动翻页加载。
- 点击任意番剧打开详情抽屉，并发检索蜜柑（Mikan）、Nyaa、ACG.RIP 资源列表，可一键单集下载或创建连载 RSS 订阅。

## 内置纯 Go 下载引擎（NativeEngine）

- **HTTP / HTTPS 下载**：原生支持多连接分块并发下载、断点续传（`.bgmcat.json` 状态元数据）及服务端不支持 Range 时的安全单流回退。
- **BitTorrent / Magnet 下载**：纯 Go 实现（基于 `anacrolix/torrent`，无 CGO 依赖），支持 `.torrent` 种子与 `magnet:` 磁力链、DHT、Tracker、PEX 与 uTP，默认最多 4 个并发活动任务，下载完成后自动停止做种。
- **顺序分片与边下边播**：观看尚未下载完成的视频时，引擎优先调度文件首尾元数据块与当前播放进度位置附近的分片，无需等整集下完即可流式起播。
- **可选外部下载器**：在「设置 → 下载」中也可切换为外部 `qBittorrent` WebUI（需与 `bgmcat` 共享可访问的下载路径）。

## Google Drive

打开「设置 → 云盘归档」，按内置直达步骤配置并一键浏览器授权：

1. **新建项目**（`https://console.cloud.google.com/projectcreate`）：填写 **项目名称** → 点击 **创建**。
2. **启用 Drive API**（`https://console.cloud.google.com/apis/library/drive.googleapis.com`）：点击 **启用**（已启用显示 **管理**）。
3. **初始化 Google Auth Platform**（`https://console.cloud.google.com/auth/overview`）：点击 **开始** → 填写 **应用名称**、选择 **用户支持邮箱** → 受众群体选 **外部** → 填写 **电子邮件地址** → 勾选同意 → **继续** → **创建**。
4. **添加测试用户**（`https://console.cloud.google.com/auth/audience`）：在 **测试用户** 栏点击 **添加用户** → 输入自己的 Gmail 邮箱回车 → 点击 **保存**（未添加测试用户会导致授权时返回 `403 access_denied`）。
5. **创建客户端**（`https://console.cloud.google.com/auth/clients/create`）：**应用类型** 选择 **桌面应用** → 点击 **创建** → 在弹窗点击 **下载 JSON** → 回到「设置 → 云盘归档」点击 **导入 JSON 授权**，浏览器登录确认后自动跳回并开启归档（远程 HTTPS 部署也可使用 **Web 应用** 类型并将 `https://<域名>/api/cloud/callback` 加入已获授权的重定向 URI）。
6. Go 后端以 **8 MiB 分块断点续传**上传已完成的文件，预先持久化远端文件 ID 防止重试重复创建；仅当全部视频文件的云端大小与 `md5Checksum` 校验通过、可读且当前无人播放时，才按设置清理本地文件释放磁盘空间。
7. **云端按需播放与文件管理**：打开侧栏「文件管理」（`/files`）可在「本地存储」与「Google Drive 云盘」之间一键切换，按目录层级浏览、搜索、新建或清理文件夹，并直接点击任意本地或云盘视频通过 HTTP Range / FFmpeg 实时转码在线播放（无需挂载整盘）。注意 Google Auth Platform 处于「测试」状态时 refresh token 会在 7 天后过期，点击 **重连** 即可恢复。

## 手机客户端与远程播放

1. **局域网直连**：在「设置 → 远程与隧道 → 手机与局域网直连」开启 **允许局域网设备访问**，服务端会立即在本机局域网 / Tailscale IPv4 接口挂载监听（无需重启），展示可一键复制的连接地址。
2. **Cloudflare Tunnel 外网穿透**：本机安装 `cloudflared` 后，可在同一页面开启临时快速隧道（`trycloudflare.com`）或填入固定域名的命名隧道 Token / 凭据文件（支持异常断线有限次数自动重连）。
3. **Android 客户端与 PWA**：Android 手机安装 `bgmcat-android-*-universal.apk` 后可同时填写局域网主地址与穿透备用地址，启动时自动优先走局域网、外出自动回退穿透地址，并支持横屏沉浸全屏播放；iPhone / iPad 可通过 Safari「添加到主屏幕」作为全屏 PWA 使用。

## Google Drive IP 直连与独立 TLS SNI

在「设置 → 下载与资源 → 直连探测」（也可从「设置 → 云盘归档」一键进入）可扫描低延迟可用 IP 并独立配置 TLS SNI：

- 默认合并系统 DNS、近 7 天历史可用地址与内置种子，也支持粘贴自定义候选公网 IP（每行一个，最多 256 个）。
- 最多 8 路并发探测，严格按真实 Google 域名执行 `x509.Verify` 校验服务端证书链、有效期与 Drive API 预期响应，绝不跳过证书安全验证。
- 支持「原始域名（默认）」「自定义 ASCII 域名」「不发送 SNI」三种模式，作用于 Go 发出的 Drive API 查询、分块上传与云端 Range 流式读取（OAuth 授权与普通 BT/HTTP 下载继续走系统默认网络）。

## Diana MCP 接入

控制台「MCP」页面支持自动探测本机 Diana 配置目录，一键写入 `--mcp-stdio` 模式配置（保留原有其他 MCP 服务并以 `0600` 权限原子备份写入），同时提供 `POST /mcp` 与 `/mcp/sse` 接口。修改服务端口、移动可执行文件或重置管理员凭据后，请在页面点击重新接入并在 Diana 中重载 MCP。

## 数据迁移

如需从早期 TypeScript 旧版迁移数据，请先停止旧服务并复制数据库副本，再导入到一个**全新的空数据目录**：

```sh
./bin/bgmcat --data /absolute/path/new-go-data --import-legacy /absolute/path/old-app-copy.db
./bin/bgmcat --data /absolute/path/new-go-data
```

迁移在单事务内导入设置、订阅、RSS 记录、下载列表与收藏；未完成的旧任务以暂停状态导入，恢复后由内置纯 Go 引擎接管下载。

## 验证与版本检查

```sh
npm ci
npm run typecheck
npm run build
go test -race ./...
go vet ./...
```

侧边栏底部可切换「跟随系统 / 浅色 / 深色」主题，点击版本号可打开「版本与更新」弹窗按需检查 `SuInk/bgmcat` 最新稳定版 Release（私有仓库阶段支持读取服务端 `GH_TOKEN` 或本机已登录的 `gh` CLI 凭据）。
