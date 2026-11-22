# 版本发布流程

项目遵循「单一版本基线 → 全套自动化测试 → 多端并行打包 → 统一 SHA-256 校验清单 → GitHub Release 与 Pages 同步」的交付标准，支持先在 **私有仓库（Private）** 完整跑通首个 Release 验证，确认无误后再切换为 **公开仓库（Public）**。

## 仓库与安全边界

- 仓库地址为 `SuInk/bgmcat`，默认分支为 `main`。
- 推送前确保 `.gitignore` 已排除 `.preview/`、`.claude/`、`_site/`、`release/`、`desktop-dist/`、`*.apk`、`*.keystore`、本地 SQLite 数据库及任何真实 OAuth 凭据。
- 仓库变量 `PUBLISH_CONTAINER=true` 开启后，版本标签构建会自动推送 GHCR 容器镜像；未设置时仅在 CI 内执行 `docker build` 与启动验证。
- Android 构建支持自定义正式签名密钥：可在仓库 Actions Secrets 中配置 `ANDROID_KEYSTORE_BASE64`、`ANDROID_KEYSTORE_PASSWORD`、`ANDROID_KEY_ALIAS`、`ANDROID_KEY_PASSWORD`；未配置时 CI 会自动生成标准 PKCS12 密钥完成 V1+V2 签名，确保产物可直接安装验证。

## 版本基线规则

- `internal/version/VERSION` 是全仓唯一版本基线（不带 `v` 前缀，例如 `0.2.0`）。
- Go 服务（`bgmcat --version`）、前端侧栏、Electron 桌面端（`desktop/package.json`）与 Android 客户端（`android/app/build.gradle.kts` 的 `versionName` / `versionCode`）全部自动从该文件派生版本。
- Git 标签必须严格匹配 `v<VERSION>`（例如 `v0.2.0` 或预发布 `v0.2.0-rc.1`）；带 `-` 的标签由 CI 自动标记为 GitHub Prerelease 且不会覆盖 Docker `latest` 标签。

## Canary 测试版

- 每次合并到 `main`，CI 自动发一个 Canary 预发布，不用手动打标签。版本号是最新正式版的下一个修订号加序号，例如正式版是 `v0.2.1` 时依次发 `v0.2.2-canary.1`、`v0.2.2-canary.2`……
- Canary 和正式版是同一套产物：五平台服务包、四个桌面安装包和 APK。发布说明自动生成，列出上一个版本以来的提交。
- 某次合并改了 `internal/version/VERSION`，就当作在准备发正式版，这次不发 Canary。否则 Canary 的版本号会高过紧接着发的正式版，Canary 用户会以为正式版是退版。
- 发正式版 `vX.Y.Z` 时，自动删掉版本号不高于它的 Canary 及其标签；手动发的 beta / rc 不动。应用内检查更新只看最近 30 条 Release，不清理的话正式版会被挤出去。
- Canary 都标成 GitHub Prerelease，不会成为 Latest。一键安装脚本和官网下载页只取 Latest，所以默认用户不会装到 Canary。想装某个 Canary，可以给安装脚本指定版本：`sh scripts/install.sh --version v0.2.2-canary.1`。
- 应用内「设置 → 版本」的更新通道选 Canary 后，检查更新时会把 Canary（以及 beta / rc）算进去；选稳定版只看正式版。
- 仓库变量 `PUBLISH_CONTAINER=true` 时，Canary 也会推送 GHCR 镜像，标签为版本号和 `canary`。

## 发布前本地检查清单

1. 更新 `internal/version/VERSION` 并编写中文发布说明 `docs/releases/<版本>.md`（不得包含 `YOUR_ACCOUNT` 等占位符，否则 CI `Validate release tag` 步骤会直接拦截失败）。
2. 在本机执行完整验证：
   ```sh
   npm ci
   npm run typecheck
   npm run desktop:prepare
   npm run desktop:test
   go test -race ./...
   go vet ./...
   python3 scripts/test_install.py
   python3 scripts/test_commit_message.py
   python3 scripts/package.py
   rm -rf release
   ```
3. 将改动合并入默认分支 `main` 并推送，等待 GitHub Actions 分支构建全部绿灯。
4. 确认提交已位于 `origin/main` 后，打上匹配的版本标签并推送以触发正式 Release：
   ```sh
   git tag v0.2.0
   git push origin v0.2.0
   ```

## CI 工作流与全平台产物

### 1. 构建与发布流水线（`.github/workflows/build.yml`）

在每次 Push / PR 上运行全套单元与集成测试、五平台交叉打包、Linux / Windows 原生安装升级测试、四矩阵 Electron 桌面端构建测试与 Android APK 构建；当触发源为 `vX.Y.Z` 标签时，`release` 作业会汇总以下全部产物并生成统一 `SHA256SUMS` 发布至 GitHub Release：

- **5 平台独立服务包**：`bgmcat-{linux,macos}-{amd64,arm64}.tar.gz`、`bgmcat-windows-amd64.zip` 及元数据 `latest.json`
- **4 平台电脑桌面安装包**：`bgmcat-desktop-<版本>-mac-{arm64,x64}.dmg`（含 `.zip`）、`bgmcat-desktop-<版本>-win-x64.exe`、`bgmcat-desktop-<版本>-linux-x64.{AppImage,deb}`
- **Android 手机客户端**：`bgmcat-android-<版本>-universal.apk`
- **完整性校验清单**：包含上述全部归档、桌面安装包与 APK 摘要的 `SHA256SUMS`

### 2. 官网、下载页与在线演示流水线（`.github/workflows/pages.yml`）

当 `main` 分支的 `site/`、`frontend/` 或安装脚本更新时，自动以 `VITE_DEMO_MODE=true` 构建纯前端交互式演示至 `/demo/`，与 `site/` 下的产品落地页、设备自适应下载中心及可搜索文档中心组装后发布至 GitHub Pages。

> **关于 GitHub Pages 说明**：仓库公开（Public）并开启 Pages（Settings → Pages → Source 选择 **GitHub Actions**）后，`pages.yml` 会自动部署产品落地页与纯前端交互演示，下载中心也会自动通过 GitHub Releases API 将下载按钮关联为直链。

## 发布后核验

- 检查 GitHub Actions 所有作业绿灯、Release 非 Draft、桌面端 / 移动端 / 服务端资产与 `SHA256SUMS` 齐全。
- 下载本机对应的桌面安装包与 `SHA256SUMS` 核对哈希，实际启动验证免密进入主页、全库筛选、内置引擎在线状态与局域网共享开关。
- 在 Android 手机安装 `bgmcat-android-*-universal.apk`，确认不填地址即可进入首页（内置服务），再测试切换到局域网主地址连接与本地 / Google Drive 视频横屏流式播放。
