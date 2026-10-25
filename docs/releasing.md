# 版本发布流程

参考 Diana 的“版本基线 → 验证 → 完整包 → 校验清单 → Release”流程。这里描述发布机制，不表示已创建仓库或发布版本。

## 仓库准备

项目使用私有仓库 `SuInk/bgmcat`，默认分支为 `main`。推送前排除 `.preview/`、数据库、下载文件、凭据、`node_modules/`、本地二进制和 `release/`；不要上传真实用户数据。按 Limited Redistribution License (SuInk) 要求向适用的分发或网络服务用户提供对应源码，私有仓库不改变许可义务。

仓库变量 `PUBLISH_CONTAINER=true` 可启用标签构建后的 GHCR 发布；默认只构建验证 Docker，不推送镜像。私有仓库的下载需要 GitHub 登录权限，不能使用匿名 raw 安装命令。不要开启来自不可信 PR 的写权限或执行 `pull_request_target` 中的贡献者代码。

## 版本规则

- `internal/version/VERSION` 是版本唯一基线，不带 `v`，例如 `0.2.0`。
- 发布标签必须完全匹配，例如 `v0.2.0`；RC 则同时改成 `0.2.0-rc.1` 和 `v0.2.0-rc.1`。
- 普通修复默认递增 patch；主次版本调整由维护者决定。
- `bgmcat --version`、API 和打包程序使用同一版本。
- CI 将带 `-` 的版本标记为 Prerelease；正式版镜像更新 `latest`，预发布只更新 `beta`，不覆盖 `latest`。当前不自动发布 Canary。

## 发布前

1. 更新 VERSION，以及 `docs/releases/<版本>.md` 的中文发布说明，写用户可感知的变化、行为边界、升级与测试结果。
2. 执行 `sh scripts/build.sh`、`go test -race ./...`、`go vet ./...`。依赖变化执行 `npm audit`，检查格式和 Git diff。
3. 执行 `python3 scripts/package.py`，确认五个平台均能编译。UI 已嵌入二进制，包中还有 `run.sh` / `run.bat`、VERSION、README、docs 和许可。
4. 合并到默认分支，等待 CI 测试、平台打包和 Docker 构建通过。标签只能指向默认分支中的提交。
5. 确认 README 和发布说明里的安装方式与当前产物一致后，再创建并推送版本标签。该动作会触发正式发布，不属于本地打包。

```sh
# 仅在准备好真正发布时执行；版本必须与 VERSION 完全一致
git tag v0.2.0
git push origin v0.2.0
```

## 工作流与产物

`.github/workflows/build.yml` 在 PR 和分支 push 上进行验证，在版本标签上额外发布。校验不通过时不会进入 Release 作业。Release 使用手写中文说明；不会只拿自动生成的提交列表当正文。

服务版预期 7 项 Release 资产：五个平台完整包、`SHA256SUMS`、`latest.json`。不上传独立裸二进制。版本清单 schema 1 提供版本、标签、预发布状态、各平台文件名/大小/SHA-256；这是发布元数据，目前未接入 WebUI 自更新器。

发布说明的安装命令使用 `SuInk/bgmcat` 并注明私有下载需要授权。无需为源码包捏造可下载的 Release URL。

## 发布后核验

- 检查工作流全部成功、Release 非 Draft、Prerelease 标记正确、服务版和桌面版资产齐全。
- 实际下载 macOS ARM64 包和 SHA256SUMS，在本机重新计算并验证。
- 解压检查启动脚本、文档和二进制，运行 `--version`，在隔离数据目录启动检查健康接口。
- Linux 与 Windows 在相应平台做原生启动和安装测试，尤其是升级时文件占用行为；交叉编译不能代替运行验证。
- 若启用 GHCR，检查对应版本和通道标签的镜像确实存在，并实测拉取。
- 验证完成后才宣称发布成功。保留历史 Release，不覆盖旧版本资产；修复发布使用新版本。

## 桌面发布

默认用户入口是 Electron 桌面安装包，服务版继续保留。`desktop` CI 在 macOS ARM64、macOS Intel、Windows x64、Linux x64 原生构建，包含 Go 后端生命周期测试。桌面作业失败时禁止发布 Release。产物合并进入相同 Release，桌面安装包摘要追加到 SHA256SUMS；现有 latest.json 仍仅描述服务版，不是桌面更新源。

`npm run desktop:prepare` 从 VERSION 同步桌面 manifest 并生成本机后端；`npm run desktop:test` 验证真实子进程认证、跨启动 token 持久化、退出清理和外链/IPC 地址策略。本地还需实际打开安装后的应用，验证无 token 登录页、重新打开、退出释放端口；CI 的后端测试不等于图形界面验证。

当前桌面产物没有正式签名/公证，CI 明确禁用身份自动发现。未来面向正式分发需配置签名并增加签名验收。桌面包不含 aria2（deb 声明系统依赖），发布说明必须保留安装依赖说明。
