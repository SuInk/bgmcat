const { app, BrowserWindow, ipcMain, dialog, shell, Menu } = require('electron');
const { spawn } = require('node:child_process');
const { join, delimiter } = require('node:path');
const { mkdirSync } = require('node:fs');
const { createInterface } = require('node:readline');
const { isLocalPage, externalLink, parseReady } = require('./policy.cjs');

let child, window, origin = '', token = '', stopping = false, mayQuit = false;
app.setName('Bangumi Cat');
if (process.env.BGMCAT_DESKTOP_USER_DATA) app.setPath('userData', process.env.BGMCAT_DESKTOP_USER_DATA);
if (!app.requestSingleInstanceLock()) { app.quit(); } else {
  app.on('second-instance', () => { if (window) { if (window.isMinimized()) window.restore(); window.show(); window.focus(); } });
  app.on('before-quit', event => {
    if (mayQuit) return;
    event.preventDefault();
    if (stopping) return;
    stopping = true;
    void stopBackend().finally(() => { mayQuit = true; app.quit(); });
  });
  app.on('window-all-closed', () => app.quit());
  app.whenReady().then(start).catch(error => {
    dialog.showErrorBox('猫追番启动失败', `${error.message}\n请确认桌面服务端口未被占用。已有网页服务不受影响。`);
    app.quit();
  });
}

// Native folder picker for choosing where a download is saved. Same owner check
// as the credential channel: only our own main frame on the local page.
ipcMain.handle('bgmcat:pick-directory', async event => {
  if (!window || event.sender !== window.webContents || event.senderFrame !== window.webContents.mainFrame || !isLocalPage(event.senderFrame.url, origin)) return null;
  const result = await dialog.showOpenDialog(window, { title: '选择保存位置', properties: ['openDirectory', 'createDirectory'] });
  return result.canceled || !result.filePaths[0] ? null : result.filePaths[0];
});

ipcMain.on('bgmcat:credential', event => {
  event.returnValue = window && event.sender === window.webContents &&
    event.senderFrame === window.webContents.mainFrame && isLocalPage(event.senderFrame.url, origin) ? token : '';
});

async function startBackend() {
  const resources = app.isPackaged ? process.resourcesPath : join(__dirname, 'resources');
  const binary = join(resources, process.platform === 'win32' ? 'bgmcat.exe' : 'bgmcat');
  const data = join(app.getPath('userData'), 'server');
  mkdirSync(data, { recursive: true, mode: 0o700 });
  const env = { ...process.env };
  // Finder-launched applications do not inherit a terminal's Homebrew PATH.
  if (process.platform === 'darwin') env.PATH = [env.PATH, '/opt/homebrew/bin', '/usr/local/bin'].filter(Boolean).join(delimiter);
  child = spawn(binary, ['--desktop', '--data', data, '--listen', '127.0.0.1:39123'], {
    env, windowsHide: true, stdio: ['pipe', 'pipe', 'pipe']
  });
  child.stdin.on('error', () => {});
  child.stderr.resume(); // Do not persist credential-bearing backend messages in shell logs.
  const ready = await new Promise((resolve, reject) => {
    const lines = createInterface({ input: child.stdout });
    const timer = setTimeout(() => reject(new Error('后台服务启动超过 20 秒')), 20_000);
    const fail = () => { clearTimeout(timer); reject(new Error('后台服务未能启动')); };
    child.once('error', fail);
    child.once('exit', fail);
    lines.on('line', line => {
      try {
        const result = parseReady(line);
        if (result) { clearTimeout(timer); child.off('error', fail); child.off('exit', fail); resolve(result); }
      } catch { clearTimeout(timer); reject(new Error('后台服务认证握手失败')); }
    });
  });
  origin = ready.origin; token = ready.token;
  const response = await fetch(`${origin}/api/settings`, { headers: { Authorization: `Bearer ${token}` }, signal: AbortSignal.timeout(5000) });
  if (!response.ok) throw new Error('后台服务身份校验失败');
  child.on('error', () => {});
  child.on('exit', () => {
    if (!stopping) { dialog.showErrorBox('后台服务已停止', '请重新打开猫追番。任务和设置已保存在本机。'); app.quit(); }
  });
}

async function start() {
  await startBackend();
  window = new BrowserWindow({
    title: '猫追番 · Bangumi Cat', width: 1280, height: 840, minWidth: 360, minHeight: 480,
    show: false, backgroundColor: '#f7f5f1',
    // macOS: no grey system title bar; the traffic lights sit in the app's own
    // 56px header row (the page reserves room for them via data-shell="mac").
    ...(process.platform === 'darwin' ? { titleBarStyle: 'hidden', trafficLightPosition: { x: 18, y: 21 } } : {}),
    webPreferences: { preload: join(__dirname, 'preload.cjs'), contextIsolation: true,
      sandbox: true, nodeIntegration: false, webSecurity: true, partition: 'persist:bgmcat-desktop' }
  });
  window.webContents.setUserAgent(window.webContents.getUserAgent() + ' AnimeRSSShell/1');
  const session = window.webContents.session;
  session.setPermissionRequestHandler((_contents, _permission, callback) => callback(false));
  session.setPermissionCheckHandler(() => false);
  session.webRequest.onHeadersReceived({ urls: [`${origin}/*`] }, (details, callback) => {
    callback({ responseHeaders: { ...details.responseHeaders,
      'Content-Security-Policy': ["default-src 'self'; script-src 'self' blob:; worker-src 'self' blob:; style-src 'self' 'unsafe-inline'; img-src 'self' https: http: data: blob:; media-src 'self' blob:; connect-src 'self'; object-src 'none'; frame-src 'none'; base-uri 'self'; form-action 'self'"],
      'Referrer-Policy': ['no-referrer'] } });
  });
  const openExternal = url => { if (externalLink(url)) void shell.openExternal(url).catch(() => {}); };
  window.webContents.on('will-navigate', (event, url) => {
    if (!isLocalPage(url, origin)) { event.preventDefault(); openExternal(url); }
  });
  window.webContents.on('will-redirect', (event, url) => { if (!isLocalPage(url, origin)) event.preventDefault(); });
  window.webContents.setWindowOpenHandler(({ url }) => { openExternal(url); return { action: 'deny' }; });
  Menu.setApplicationMenu(Menu.buildFromTemplate([
    ...(process.platform === 'darwin' ? [{ label: '猫追番', submenu: [{ role: 'about' }, { type: 'separator' }, { role: 'quit' }] }] : []),
    { label: '编辑', submenu: [{ role: 'undo' }, { role: 'redo' }, { type: 'separator' }, { role: 'cut' }, { role: 'copy' }, { role: 'paste' }, { role: 'selectAll' }] },
    { label: '查看', submenu: [{ role: 'reload' }, { role: 'resetZoom' }, { role: 'zoomIn' }, { role: 'zoomOut' }, { role: 'togglefullscreen' }] },
    { label: '帮助', submenu: [
      { label: '打开数据目录', click: () => void shell.openPath(join(app.getPath('userData'), 'server')) },
      { label: '安装与依赖说明', click: () => openExternal('https://github.com/SuInk/bgmcat/blob/main/docs/desktop.md') },
      ...(process.platform !== 'darwin' ? [{ role: 'quit', label: '退出' }] : [])
    ] }
  ]));
  await window.loadURL(origin);
  window.show();
  // On a fresh machine, explain the optional download engine instead of silently failing.
  const response = await fetch(`${origin}/api/system/status`, { headers: { Authorization: `Bearer ${token}` } });
  const status = await response.json();
  if (status.data && !status.data.aria2Online && status.data.downloadClient === 'native') {
    void dialog.showMessageBox(window, { type: 'info', title: '下载引擎尚未就绪',
      message: '浏览和订阅可以使用；HTTP / BT 下载需要 aria2。',
      detail: '桌面包已包含 Go 服务和界面。请按安装说明安装 aria2，或在设置中配置已有的 qBittorrent，然后重新启动应用。',
      buttons: ['稍后配置', '查看说明'], defaultId: 0 }).then(({ response }) => {
      if (response === 1) openExternal('https://github.com/SuInk/bgmcat/blob/main/docs/desktop.md');
    });
  }
}

async function stopBackend() {
  if (!child || child.exitCode !== null || child.signalCode !== null) return;
  await new Promise(resolve => {
    const timer = setTimeout(() => { child.kill('SIGKILL'); resolve(); }, 7000);
    child.once('exit', () => { clearTimeout(timer); resolve(); });
    child.stdin.end(); // EOF also handles a crashed Electron parent; works on Windows.
  });
  token = '';
}
