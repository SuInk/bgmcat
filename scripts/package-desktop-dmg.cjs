// Use macOS's built-in disk image tooling; no separate dmgbuild download is needed.
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
try {
  execFileSync('ditto', [appPath, join(stage, 'Bangumi Cat.app')]);
  symlinkSync('/Applications', join(stage, 'Applications'));
  execFileSync('hdiutil', ['create', '-volname', 'Bangumi Cat', '-srcfolder', stage, '-ov', '-format', 'UDZO',
    resolve('desktop-dist', `bgmcat-desktop-${version}-mac-${process.arch}.dmg`)], { stdio: 'inherit' });
} finally {
  rmSync(stage, { recursive: true, force: true });
  try {
    writeFileSync(resolve('desktop-dist', '.metadata_never_index'), '');
    execFileSync(LSREGISTER, ['-u', appPath], { stdio: 'ignore' });
  } catch {}
}
