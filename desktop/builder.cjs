const { mkdirSync, writeFileSync } = require('node:fs');
const { join } = require('node:path');
const { execFileSync } = require('node:child_process');

// Mac 签名有两种（CI 导入证书后通过 MAC_SIGNING 告知）：
//   '1'          Apple Developer ID：electron-builder 自己签名、开 hardened runtime 并公证；
//   'selfsigned' 自签名证书：afterPack 里用证书哈希签整个 .app，不开 hardened runtime、不公证。
//                它的作用是让 macOS 按「App ID + 这张证书」认应用，升级后权限不丢。
// 都没有时不签名（arm64 只有链接器加的临时签名）。
const macSigning = process.env.MAC_SIGNING || '';

module.exports = {
  appId: 'io.github.suink.bgmcat', productName: 'Bangumi Cat',
  directories: { app: 'desktop', output: 'desktop-dist' },
  files: ['*.cjs', '!*.test.cjs', '!builder.cjs', 'package.json'],
  extraResources: [{ from: 'desktop/resources', to: '.', filter: ['**/*'] }, { from: 'LICENSE', to: 'LICENSE.bgmcat' }, { from: 'THIRD_PARTY_NOTICES.md', to: 'THIRD_PARTY_NOTICES.md' }],
  asar: true, npmRebuild: false, artifactName: 'bgmcat-desktop-${version}-${os}-${arch}.${ext}',
  // 设置了 APPLE_API_KEY / APPLE_API_KEY_ID / APPLE_API_ISSUER 时，electron-builder 签名后会再提交 Apple 公证。
  mac: {
    icon: 'frontend/public/icons/icon-512.png', category: 'public.app-category.entertainment', target: ['zip'],
    hardenedRuntime: macSigning === '1', gatekeeperAssess: false,
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
    if (macSigning === 'selfsigned' && context.electronPlatformName === 'darwin') {
      const app = join(context.appOutDir, `${context.packager.appInfo.productFilename}.app`);
      // 自签名证书不受系统信任，按证书哈希指定身份才能签；--deep 连同 Helper、Framework 和内置的 bgmcat / ffmpeg 一起签。
      execFileSync('codesign', ['--force', '--deep', '--timestamp=none', '--keychain', process.env.CSC_KEYCHAIN,
        '--sign', process.env.MAC_SELFSIGN_IDENTITY, app], { stdio: 'inherit' });
      execFileSync('codesign', ['--verify', '--deep', '--strict', '--verbose=2', app], { stdio: 'inherit' });
    }
  }
};
