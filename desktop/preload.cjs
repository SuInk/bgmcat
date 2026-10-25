const { contextBridge, ipcRenderer } = require('electron');
// Only the owned main frame can obtain this credential. Never put it in process arguments.
const token = ipcRenderer.sendSync('bgmcat:credential');
if (typeof token === 'string' && token) {
  contextBridge.exposeInMainWorld('animeRssShell', Object.freeze({ apiToken: token }));
}
