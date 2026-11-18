// 自更新是「装好新的、关掉旧的、新进程接管」，中间必然有一段没人应答的空窗——
// 实测约十几秒。这段时间里前端不该报故障：一次计划内的重启和一台真的挂掉的机器，
// 对用户的意义完全不同。
const INSTALL_MARKER_KEY = "bgmcat.update.installing";
const INSTALL_MARKER_TTL_MS = 5 * 60 * 1000;

export function markUpdateInstalling(): void {
  try {
    window.localStorage.setItem(INSTALL_MARKER_KEY, String(Date.now()));
  } catch {
    /* 隐私模式下写不进去：拿不到这个提示而已，不影响重连本身 */
  }
}

export function clearUpdateInstalling(): void {
  try {
    window.localStorage.removeItem(INSTALL_MARKER_KEY);
  } catch {
    /* 同上 */
  }
}

// 宽限期结束时整页重新加载一次：自更新换掉的不只是后端进程，前端产物的文件名也
// 变了，重新加载才能拿到新版界面。冷却期内只允许自动重载一次，避免刷新循环。
const RELOAD_MARKER_KEY = "bgmcat.backend.autoReloadAt";
const RELOAD_COOLDOWN_MS = 2 * 60 * 1000;

export function autoReloadAllowed(): boolean {
  try {
    const raw = window.sessionStorage.getItem(RELOAD_MARKER_KEY);
    const last = raw ? Number(raw) : 0;
    return !Number.isFinite(last) || Date.now() - last > RELOAD_COOLDOWN_MS;
  } catch {
    return false;
  }
}

export function markAutoReloaded(): void {
  try {
    window.sessionStorage.setItem(RELOAD_MARKER_KEY, String(Date.now()));
  } catch {
    /* 同上 */
  }
}

export function updateInstallingRecently(): boolean {
  let raw: string | null = null;
  try {
    raw = window.localStorage.getItem(INSTALL_MARKER_KEY);
  } catch {
    return false;
  }
  if (!raw) return false;
  const startedAt = Number(raw);
  if (!Number.isFinite(startedAt) || Date.now() - startedAt > INSTALL_MARKER_TTL_MS) {
    clearUpdateInstalling();
    return false;
  }
  return true;
}
