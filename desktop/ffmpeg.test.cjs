const test = require('node:test');
const assert = require('node:assert/strict');
const { readFileSync } = require('node:fs');
const { join } = require('node:path');
const { BUILDS } = require('../scripts/desktop-ffmpeg.cjs');

const nodeKey = { mac: 'darwin', win: 'win32', linux: 'linux' };

test('every desktop build in CI has a pinned ffmpeg', () => {
  const workflow = readFileSync(join(__dirname, '..', '.github/workflows/build.yml'), 'utf8');
  const targets = [...workflow.matchAll(/platform: (mac|win|linux), arch: (\w+)/g)].map(([, p, a]) => `${nodeKey[p]}-${a}`);
  assert.ok(targets.length >= 4, 'desktop matrix not found');
  for (const target of targets) {
    const build = BUILDS[target];
    assert.ok(build, `no ffmpeg for ${target}`);
    for (const archive of build.archives) {
      assert.match(archive.url, /^https:\/\//);
      assert.match(archive.sha256, /^[0-9a-f]{64}$/);
    }
  }
});
