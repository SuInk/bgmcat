const { readFileSync, writeFileSync, mkdirSync } = require('node:fs');
const { join, resolve } = require('node:path');
const { execFileSync } = require('node:child_process');
const root = resolve(__dirname, '..');
const version = readFileSync(join(root, 'internal/version/VERSION'), 'utf8').trim();
const manifestPath = join(root, 'desktop/package.json');
const manifest = JSON.parse(readFileSync(manifestPath, 'utf8'));
manifest.version = version;
writeFileSync(manifestPath, JSON.stringify(manifest, null, 2) + '\n');
const resources = join(root, 'desktop/resources');
mkdirSync(resources, { recursive: true });
execFileSync('go', ['build', '-trimpath', '-ldflags', '-s -w', '-o', join(resources, process.platform === 'win32' ? 'bgmcat.exe' : 'bgmcat'), './cmd/bgmcat'], {
  cwd: root, env: { ...process.env, CGO_ENABLED: '0' }, stdio: 'inherit'
});
