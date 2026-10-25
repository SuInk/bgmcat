# 安装、升级与卸载

## 选择安装方式

| 方式 | 安装前准备 | 运行时依赖 |
| --- | --- | --- |
| Release 完整包 | 已发布的对应平台资产；SHA-256 工具 | aria2c；可选 FFmpeg、cloudflared |
| 私有 Release 安装器 | GitHub CLI、`gh auth login`、仓库读取权限 | 同上 |
| 公开 Release 安装器 | Shell/curl 或 PowerShell | 同上 |
| Docker 源码构建 | Docker + Compose、项目源码 | 镜像包含 aria2 和 FFmpeg |
| 源码构建 | Go 1.26+、Node.js 22+、npm | 构建后同 Release |

仓库为 `SuInk/bgmcat`，Release 安装仍须等待实际发布对应版本。

## 原生安装

先在已克隆的仓库目录运行。私有仓库使用 GitHub CLI 已保存的登录态：

```sh
gh auth login
sh scripts/install.sh --repository SuInk/bgmcat --private
"$HOME/.local/bin/bgmcat"
```

Windows PowerShell：

```powershell
gh auth login
.\scripts\install.ps1 -Repository SuInk/bgmcat -Private
& "$env:LOCALAPPDATA\AnimeRSS\bin\bgmcat.exe"
```

将安装目录加入 PATH 后可直接使用 `bgmcat`。安装器不改动系统 PATH、安装主程序无需管理员权限，安装 aria2 系统包可能需要 sudo；不配置开机自启。不要把访问令牌直接写进命令；自动化可按 GitHub CLI 的机制安全注入认证环境。

若将来仓库公开，省略 `--private` / `-Private` 即可。可以下载并审阅 `scripts/install.sh` 与同目录的 `install-aria2.sh`，或 Windows 的 `install.ps1` 后执行；私有仓库的 raw 链接不支持匿名 `curl | sh`，不要照搬公开仓库的一行命令。

指定版本和安装目录：

```sh
sh scripts/install.sh --repository SuInk/bgmcat --private --version v0.2.0 --dir "$HOME/.local/bin"
```

```powershell
.\scripts\install.ps1 -Repository SuInk/bgmcat -Private -Version v0.2.0 -InstallDir "$env:LOCALAPPDATA\AnimeRSS\bin"
```

默认 `latest` 选择 GitHub 最新正式 Release；测试版必须明确指定 `v0.2.0-rc.1` 之类的标签。该标签也必须真实存在。

## 手动解压

下载适合机器的完整包和 `SHA256SUMS`：

| 平台 | 文件 |
| --- | --- |
| Linux x86-64 | `bgmcat-linux-amd64.tar.gz` |
| Linux ARM64 | `bgmcat-linux-arm64.tar.gz` |
| macOS Intel | `bgmcat-macos-amd64.tar.gz` |
| macOS Apple Silicon | `bgmcat-macos-arm64.tar.gz` |
| Windows x86-64 | `bgmcat-windows-amd64.zip` |

计算对应文件的 SHA-256，与 `SHA256SUMS` 中该文件的行比较。Linux 用 `sha256sum 文件名`，macOS 用 `shasum -a 256 文件名`，Windows 用 `Get-FileHash 文件名 -Algorithm SHA256`。校验匹配后解压，运行包内 `run.sh` 或 `run.bat`。校验用于发现下载损坏和内容不匹配，不等同于独立签名。macOS 包尚无 Developer ID 签名或公证。

本地构建的包也可通过安装器验证：

```sh
sh scripts/install.sh --local-dir "$PWD/release"
```

## 升级与回滚

1. 在网页创建数据库备份并另存；确保当前无必须保持的转码或上传任务。
2. 停止现有进程。Windows 必须先停止，否则系统可能拒绝替换 exe。
3. 重跑安装命令或解压新完整包，保持原来的 `--data` 参数。
4. 重启并检查 `--version`、网页登录、订阅及下载列表。

安装器保留一个 `.previous` 程序副本。出问题时停服，用副本替换程序，再启动；跨数据库格式版本回退可能还需恢复升级前备份。安装器不自动回滚数据库、不承诺旧程序兼容新数据。

Diana 的 MCP 配置指向安装时的可执行文件及服务地址；移动程序、改端口或轮换口令后，重新接入并在 Diana 重新加载 MCP。

## Docker

当前可用的是源码构建：

```sh
docker compose up -d --build
docker compose logs -f bgmcat
```

升级源码后再次执行 `docker compose up -d --build`。持久化卷保留数据。服务默认仅映射到宿主机 `127.0.0.1:3001`；需要外部访问时调整端口映射、反向代理及 `ANIME_RSS_PUBLIC_URL`。

维护者启用 GHCR 发布后，可改用 `ghcr.io/实际账号/实际仓库:实际版本`。私有镜像必须先通过 Docker/GHCR 身份认证；本项目目前没有已经可拉取的镜像地址。

## 卸载

停止原生服务后，移除安装目录中的 `bgmcat`（Windows 为 `bgmcat.exe`）及其 `.previous` 副本。默认数据目录 `~/.anime-rss-go` 独立保留，恢复安装后可以继续使用。

Docker 用 `docker compose down` 移除容器，保留数据卷；不要加 `-v`，除非明确要删除全部持久化数据。

## 托管 aria2 下载引擎

HTTP 与 BT 使用独立的 `aria2c`，由 Go 服务自动启动、关闭与恢复。RPC 仅监听随机本机端口，密钥保存在 0600 配置文件中，无需填写旧版 RPC 地址。主程序仍可 `CGO_ENABLED=0` 构建。

安装器默认安装缺失的 aria2：macOS 使用 Homebrew，Linux 使用 apt/dnf/pacman/apk（可能需要 sudo），Windows 使用 winget。离线或仅使用 qBittorrent 时可传 `--skip-aria2` / `-SkipAria2`；手动解压运行前请自行安装 aria2，或执行包内 `sh scripts/install-aria2.sh`。Docker 已包含 aria2。

查找顺序是 `BGMCAT_ARIA2_PATH`、bgmcat 可执行文件旁的 aria2c、系统 PATH。发行包本身不捆绑 aria2，安装依赖需要网络；Windows 安装后可能需要重开终端以刷新 PATH。

暂停、关闭服务和进程恢复使用 `.aria2` 控制文件续传。不要删除任务目录中的这些文件。旧 Go 下载器的 `.part` 文件保留，首次切换会从头下载；旧 BT 文件由 aria2 按种子校验后复用。完成下载后不继续做种。Google Drive 上传、OAuth 和按需播放仍使用 Go 实现，IP 直连策略不变。
