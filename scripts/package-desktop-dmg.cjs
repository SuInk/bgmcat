// Use macOS's built-in disk image tooling; no separate dmgbuild download is needed.
//
// 有 Developer ID 证书时（CI 导入证书后设置 MAC_SIGNING=1）：
//   1. 先确认 electron-builder 已把 .app 签好名、公证并装订（stapled）；
//   2. 打好 dmg 后用同一张证书签名，再提交公证并装订，用户下载后双击就能打开。
// 没有证书时照旧只打包，不签名。
const { execFileSync } = require('node:child_process');
const { mkdtempSync, symlinkSync, rmSync, readFileSync, writeFileSync } = require('node:fs');
const { join, resolve } = require('node:path');
const { tmpdir } = require('node:os');
const LSREGISTER = '/System/Library/Frameworks/CoreServices.framework/Frameworks/LaunchServices.framework/Support/lsregister';
if (process.platform !== 'darwin') process.exit(0);
const version = readFileSync(resolve('internal/version/VERSION'), 'utf8').trim();
const stage = mkdtempSync(join(tmpdir(), 'bgmcat-dmg-'));
const directory = process.arch === 'x64' ? 'mac' : `mac-${process.arch}`;
const appPath = resolve('desktop-dist', directory, 'Bangumi Cat.app');
const dmgPath = resolve('desktop-dist', `bgmcat-desktop-${version}-mac-${process.arch}.dmg`);
const signing = process.env.MAC_SIGNING === '1';
const notarizing = Boolean(process.env.APPLE_API_KEY && process.env.APPLE_API_KEY_ID && process.env.APPLE_API_ISSUER);
const run = (cmd, args) => execFileSync(cmd, args, { stdio: 'inherit' });

if (signing) {
  // 签名和公证出错要在这里失败，而不是发出一个打不开的安装包。
  run('codesign', ['--verify', '--deep', '--strict', '--verbose=2', appPath]);
  if (notarizing) run('spctl', ['--assess', '--type', 'execute', '--verbose=2', appPath]);
}

try {
  execFileSync('ditto', [appPath, join(stage, 'Bangumi Cat.app')]);
  symlinkSync('/Applications', join(stage, 'Applications'));
  run('hdiutil', ['create', '-volname', 'Bangumi Cat', '-srcfolder', stage, '-ov', '-format', 'UDZO', dmgPath]);
} finally {
  rmSync(stage, { recursive: true, force: true });
  try {
    writeFileSync(resolve('desktop-dist', '.metadata_never_index'), '');
    execFileSync(LSREGISTER, ['-u', appPath], { stdio: 'ignore' });
  } catch {}
}

if (signing) {
  run('codesign', ['--sign', 'Developer ID Application', '--timestamp', dmgPath]);
  if (notarizing) {
    run('xcrun', ['notarytool', 'submit', dmgPath, '--key', process.env.APPLE_API_KEY,
      '--key-id', process.env.APPLE_API_KEY_ID, '--issuer', process.env.APPLE_API_ISSUER, '--wait']);
    run('xcrun', ['stapler', 'staple', dmgPath]);
    run('spctl', ['--assess', '--type', 'open', '--context', 'context:primary-signature', '--verbose=2', dmgPath]);
  }
}
