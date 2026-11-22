// Copyright (c) 2025-now SuInk.
// Licensed under the Limited Redistribution License in the repository root.

// 桌面包内置的 ffmpeg / ffprobe：按平台下载固定版本的静态构建，校验 SHA-256 后放进
// desktop/resources，和后端可执行文件放在一起（后端在自己旁边先找它们）。
// 转码固定用 libx264，所以只能用 GPL 构建；许可全文和源码出处见 THIRD_PARTY_NOTICES.md。
// 升级版本时同时改这里的地址和校验值，以及 THIRD_PARTY_NOTICES.md 里的版本说明。
const { createHash } = require('node:crypto');
const { chmodSync, copyFileSync, existsSync, mkdirSync, mkdtempSync, readdirSync, readFileSync, renameSync, rmSync, statSync, writeFileSync } = require('node:fs');
const { join } = require('node:path');
const { execFileSync } = require('node:child_process');

const RIEDL = 'https://ffmpeg.martin-riedl.de/download/macos';
const BUILDS = {
  'darwin-arm64': {
    version: '9.1',
    archives: [
      { url: `${RIEDL}/arm64/1795207104_9.1/ffmpeg.zip`, sha256: '1f66e9a690a7f1c9e60b012379f8869c11818d1797f45b21dbb2f2564ba61ae1' },
      { url: `${RIEDL}/arm64/1795207104_9.1/ffprobe.zip`, sha256: '6ef2df5f575042923a28537ab37adf936f28bf3e7b85de23a1447ed447ec1dbd' }
    ]
  },
  'darwin-x64': {
    version: '9.1',
    archives: [
      { url: `${RIEDL}/amd64/1795212433_9.1/ffmpeg.zip`, sha256: '35b3d82cccc2c1d644d49d15f5e57eb913a08ef68f798042c90eecec62c51f1e' },
      { url: `${RIEDL}/amd64/1795212433_9.1/ffprobe.zip`, sha256: '41ca7b7cba7992e33f1da3f1bf54db8ec103a4d387b1bfac3162062e8b1074ad' }
    ]
  },
  'win32-x64': {
    version: '9.1',
    archives: [
      { url: 'https://github.com/GyanD/codexffmpeg/releases/download/9.1/ffmpeg-9.1-essentials_build.zip', sha256: 'b9bbbb3acece000cbeec994f856649354e52c773807841e78908374b24d658fd' }
    ]
  },
  // johnvansickle 只在这个地址放当前版本；作者升级后校验会失败，届时按新版本更新这里。
  'linux-x64': {
    version: '7.0.2',
    archives: [
      { url: 'https://johnvansickle.com/ffmpeg/releases/ffmpeg-release-amd64-static.tar.xz', sha256: 'abda8d77ce8309141f83ab8edf0596834087c52467f6badf376a6a2a4c87cf67' }
    ]
  }
};
const GPL_TEXT = { url: 'https://raw.githubusercontent.com/FFmpeg/FFmpeg/n8.0/COPYING.GPLv3', sha256: '8ceb4b9ee5adedde47b31e975c1d90c73ad27b6b165a1dcd80c7c545eb65b903' };

function sha256(file) {
  return createHash('sha256').update(readFileSync(file)).digest('hex');
}

async function fetchVerified({ url, sha256: expected }, cacheDir) {
  mkdirSync(cacheDir, { recursive: true });
  const file = join(cacheDir, `${expected}-${url.split('/').pop()}`);
  if (existsSync(file) && sha256(file) === expected) return file;
  console.log(`下载 ${url}`);
  const response = await fetch(url, { redirect: 'follow', signal: AbortSignal.timeout(10 * 60_000) });
  if (!response.ok) throw new Error(`下载失败 ${response.status}: ${url}`);
  const partial = `${file}.part`;
  writeFileSync(partial, Buffer.from(await response.arrayBuffer()));
  const actual = sha256(partial);
  if (actual !== expected) {
    rmSync(partial, { force: true });
    throw new Error(`校验失败 ${url}\n  期望 ${expected}\n  实际 ${actual}\n上游可能换了文件，核对后更新 scripts/desktop-ffmpeg.cjs`);
  }
  renameSync(partial, file);
  return file;
}

function findFile(dir, name) {
  for (const entry of readdirSync(dir, { withFileTypes: true })) {
    const path = join(dir, entry.name);
    if (entry.isDirectory()) {
      const found = findFile(path, name);
      if (found) return found;
    } else if (entry.name === name) {
      return path;
    }
  }
  return '';
}

async function bundleFFmpeg({ root, resources, platform = process.platform, arch = process.arch }) {
  const key = `${platform}-${arch}`;
  const build = BUILDS[key];
  if (!build) throw new Error(`没有为 ${key} 配置内置 ffmpeg，见 scripts/desktop-ffmpeg.cjs`);
  const cacheDir = join(root, 'node_modules', '.cache', 'bgmcat-ffmpeg');
  const suffix = platform === 'win32' ? '.exe' : '';
  mkdirSync(cacheDir, { recursive: true });
  const work = mkdtempSync(join(cacheDir, 'extract-'));
  try {
    for (const archive of build.archives) {
      const file = await fetchVerified(archive, cacheDir);
      // macOS / Windows 自带的 bsdtar 能解 zip，Linux 的 GNU tar 解 .tar.xz。
      // Windows 上显式用系统的 bsdtar，Git 自带的 GNU tar 解不了 zip。
      const tar = platform === 'win32' ? join(process.env.SystemRoot || 'C:\\Windows', 'System32', 'tar.exe') : 'tar';
      execFileSync(tar, ['-xf', file, '-C', work], { stdio: 'inherit' });
    }
    for (const name of ['ffmpeg', 'ffprobe']) {
      const source = findFile(work, name + suffix);
      if (!source) throw new Error(`${key} 的压缩包里没找到 ${name}${suffix}`);
      const target = join(resources, name + suffix);
      copyFileSync(source, target);
      chmodSync(target, 0o755);
    }
  } finally {
    rmSync(work, { recursive: true, force: true });
  }
  copyFileSync(await fetchVerified(GPL_TEXT, cacheDir), join(resources, 'ffmpeg-COPYING.GPLv3.txt'));
  const size = ['ffmpeg', 'ffprobe'].reduce((n, name) => n + statSync(join(resources, name + suffix)).size, 0);
  console.log(`内置 ffmpeg ${build.version}（${key}，${Math.round(size / 1048576)} MB）`);
}

module.exports = { BUILDS, bundleFFmpeg };
