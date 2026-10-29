# 猫追番移动端

Flutter 写的安卓 / iOS App，连接你自己部署的 bgmcat 后端，通过它的 HTTP API 工作。

## 功能

- 登录：填服务器地址和访问口令，保存在本机；口令不对（`AUTH_INVALID`）时给出提示
- 新番：按星期筛选、搜索、海报墙、「继续观看」
- 番剧详情：封面、评分、简介、收藏、追更（订阅自动下载）、剧集格子、资源按字幕组 / 分辨率筛选后下载
- 离线缓存：「正在缓存」带进度、可暂停 / 继续，「已缓存」封面墙，点进去是分集列表
- 播放：后端 HLS 转封装 / 转码流，ASS 字幕（含 `\pos` 定位的屏幕文字），记住进度，横屏全屏
- 设置：切换服务器、退出登录

## 开发

```bash
flutter pub get
flutter analyze
flutter test
```

连本机开发后端调试时，可以把地址和口令写进 `dart_defines.local.json`（已在 `.gitignore` 里，格式见 `dart_defines.example.json`），debug 包的登录页会自动填好：

```bash
flutter run --dart-define-from-file=dart_defines.local.json
```

## 构建

```bash
flutter build apk --release
```

```bash
flutter build ios --release
```

安卓 release 包目前用的是 debug 签名，正式分发前要在 `android/app/build.gradle.kts` 里配自己的签名。

## 已知限制

- ASS 只还原文字、对齐、`\pos`、字号和颜色，不做动画、旋转和自定义字体
- 访问口令存在 SharedPreferences 里，没有进系统钥匙串
- 后端是 http 时，iOS 靠 `NSAllowsArbitraryLoads`、安卓靠 `usesCleartextTraffic` 放行明文请求
