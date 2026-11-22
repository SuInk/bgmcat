const { mkdirSync, writeFileSync } = require('node:fs');
const { join } = require('node:path');

module.exports = {
  appId: 'io.github.suink.bgmcat', productName: 'Bangumi Cat',
  directories: { app: 'desktop', output: 'desktop-dist' },
  files: ['*.cjs', '!*.test.cjs', '!builder.cjs', 'package.json'],
  extraResources: [{ from: 'desktop/resources', to: '.', filter: ['**/*'] }, { from: 'LICENSE', to: 'LICENSE.bgmcat' }, { from: 'THIRD_PARTY_NOTICES.md', to: 'THIRD_PARTY_NOTICES.md' }],
  asar: true, npmRebuild: false, artifactName: 'bgmcat-desktop-${version}-${os}-${arch}.${ext}',
  // 有 Developer ID 证书时（CI 的 macOS 作业导入了证书）签名并开启 hardened runtime，
  // 设置了 APPLE_API_KEY / APPLE_API_KEY_ID / APPLE_API_ISSUER 时再提交 Apple 公证；没有证书时照旧不签名。
  mac: {
    icon: 'frontend/public/icons/icon-512.png', category: 'public.app-category.entertainment', target: ['zip'],
    hardenedRuntime: true, gatekeeperAssess: false,
    entitlements: 'desktop/entitlements.mac.plist', entitlementsInherit: 'desktop/entitlements.mac.plist'
  },
  win: { icon: 'frontend/public/icons/icon-512.png', target: ['nsis'] },
  nsis: { oneClick: true, perMachine: false, createDesktopShortcut: true, deleteAppDataOnUninstall: false },
  linux: { icon: 'frontend/public/icons/icon-512.png', target: ['AppImage', 'deb'], category: 'AudioVideo', maintainer: 'SuInk', executableName: 'bgmcat-desktop' },
  deb: { depends: ['libgtk-3-0', 'libnotify4', 'libnss3', 'libxss1', 'libxtst6', 'xdg-utils', 'libatspi2.0-0'] },
  async afterPack(context) {
    if (process.platform !== 'darwin' || !context || !context.outDir) return;
    mkdirSync(context.outDir, { recursive: true });
    writeFileSync(join(context.outDir, '.metadata_never_index'), '');
  }
};
