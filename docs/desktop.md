# 桌面版与手机协同（默认推荐）

电脑桌面版（`bgmcat-desktop`）使用 Electron 窗口封装完整前端控制台与 Go 核心后端。双击应用即可自动启动本机服务并完成窗口内免密会话握手，无需安装 Go、Node.js 或任何外部下载器。

## 下载与安装

在 GitHub Releases（或官网下载中心）选择对应客户端资产：

| 设备与系统 | 安装包格式 |
| --- | --- |
| macOS Apple Silicon (`M1`–`M4`) | `bgmcat-desktop-版本-mac-arm64.dmg` |
| macOS Intel (`x64`) | `bgmcat-desktop-版本-mac-x64.dmg` |
| Windows 10 / 11 (`x64`) | `bgmcat-desktop-版本-win-x64.exe` |
| Ubuntu / Debian (`x64`) | `bgmcat-desktop-版本-linux-x64.deb` |
| 其他 Linux (`x64`) | `bgmcat-desktop-版本-linux-x64.AppImage` |
| Android 手机 / 平板（播放终端） | `bgmcat-android-版本-universal.apk` |

- **macOS**：打开 `.dmg` 将 **Bangumi Cat** 拖入 `Applications`。当前构建未绑定付费 Apple Developer ID 公证，首次打开如提示无法验证开发者，可在「系统设置 → 隐私与安全性」点击「仍要打开」。
- **Windows**：运行 `.exe` 安装器（默认安装到当前用户目录，卸载时默认保留用户数据）。
- **Linux**：Debian / Ubuntu 执行 `sudo apt install ./bgmcat-desktop-*.deb`；AppImage 赋予可执行权限后直接运行。
- 所有发行包与 APK 的 SHA-256 摘要均记录在同一 Release 的 `SHA256SUMS` 中。

## 内置纯 Go 下载引擎与可选工具

- **开箱即用零 `aria2` 依赖**：桌面包内置纯 Go 自研下载引擎（`NativeEngine`），原生提供 HTTP 多连接断点续传、BitTorrent / Magnet / DHT / PEX 下载与顺序分片边下边播，**无需额外安装 `aria2`**。
- **可选外部客户端**：如果你本机已在运行主力 `qBittorrent`，可在「设置 → 下载」切换下载器类型。
- **可选媒体与穿透组件**：
  - `ffmpeg` / `ffprobe`：普通 MP4 原画流式播放无需安装；如需实时转码 MKV / HEVC 10-bit 视频或提取内封软字幕，安装系统 `ffmpeg` 后桌面端会自动识别（macOS Finder 启动也会自动搜索 `/opt/homebrew/bin` 与 `/usr/local/bin`）。
  - `cloudflared`：仅在启用 Cloudflare Tunnel 外网穿透时需要。

## 手机客户端与局域网直连播放

桌面版主控进程固定监听 `127.0.0.1:39123` 以保障本机 IPC 与 Google OAuth 回调稳定。当你需要用手机或平板直连电脑看番时：

1. 在电脑桌面端打开 **「设置 → 安全与访问」**，确认已设置好管理员用户名与密码。
2. 打开 **「设置 → 远程与隧道 → 手机与局域网直连」**，开启 **「允许局域网设备访问」** 并保存。后端会立即在本机私有局域网 / Tailscale 网卡（如 `192.168.x.x:39123`、`100.x.y.z:39123`）动态挂载监听服务，**无需重启桌面端**，并展示可一键复制的手机连接地址。
3. **Android 手机端**：安装 `bgmcat-android-*-universal.apk`，在首次启动连接页将上述局域网地址填入「主地址」，如有 Cloudflare Tunnel 地址可一并填入「备用公网 / 穿透地址」。App 打开时会自动优先探测局域网（2 秒超时），外出不在同一 Wi-Fi 时自动无缝切至穿透地址，并在视频全屏播放时自动锁定横屏沉浸模式与屏幕常亮。
4. **iPhone / iPad / 网页端**：使用 Safari 打开同一连接地址登录，点击分享 → **添加到主屏幕** 即可作为全屏 PWA 使用。

## 本机认证与数据目录

- 桌面自有窗口通过受控子进程管道和预加载受限 IPC 自动完成本机管理员会话握手，不在命令行参数或 URL 中暴露凭据；外部浏览器与手机访问局域网端口时必须通过管理员账号密码登录。
- 第二次启动会唤醒已有窗口；退出桌面应用会优雅关闭其托管的 Go 服务并保存下载进度，下次打开自动按任务状态恢复。
- 数据保存在 Electron 用户数据目录的 `server/` 子目录：
  - macOS：`~/Library/Application Support/Bangumi Cat/server`
  - Windows：`%APPDATA%\Bangumi Cat\server`
  - Linux：`~/.config/Bangumi Cat/server`
  - 可通过应用菜单「帮助 → 打开数据目录」一键定位；升级桌面安装包时直接覆盖安装即可保留全部数据。
- Google Drive OAuth 默认使用桌面端显示的固定回调地址 `http://127.0.0.1:39123/api/cloud/callback`。

## 从源码打包桌面端与 Android 端

```sh
# 电脑桌面端（生成当前操作系统与架构的安装包）
npm ci
npm run desktop:prepare
npm run desktop:test
npm run desktop:pack

# Android 客户端 APK（需要 JDK 21 与 Android SDK Platform 35）
cd android && ./gradlew assembleRelease
```

仅开发调试桌面端可执行 `npm run desktop:dev`。自动化测试支持通过 `BGMCAT_DESKTOP_USER_DATA` 指定隔离的用户目录。
