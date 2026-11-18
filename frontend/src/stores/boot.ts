import { computed, ref, watch } from "vue";

import {
  ApiRequestError,
  clearApiToken,
  getBootstrapPayload,
  isBackendUnreachable,
  persistApiToken,
  readApiToken,
  readDesktopShellToken,
  type BootstrapResponse
} from "../api";
import { t } from "../i18n";
import {
  autoReloadAllowed,
  clearUpdateInstalling,
  markAutoReloaded,
  updateInstallingRecently
} from "../lib/backendState";
import { agentsLoaded, refreshAgents } from "./agents";
import { withBusy } from "./busy";
import {
  closeAnimeDetail,
  discoveryMode,
  hydrateHomepageAnime,
  refreshCatalogAnime,
  refreshHomepage,
  homepageAnime,
  homepageError,
  resetDiscoveryView
} from "./discovery";
import { bumpPlaybackHistoryVersion } from "../lib/playbackHistory";
import {
  clearUnreadCompletedDownloads,
  connectDownloadEvents,
  disconnectDownloadEvents,
  hydrateDownloads,
  onlineDownloadCount,
  refreshDownloads,
  restorePlaybackFromLocation,
  sseConnected
} from "./downloads";
import { favorites, refreshFavorites } from "./favorites";
import { hydrateHiddenAnime, refreshHiddenAnime } from "./hiddenAnime";
import { refreshActiveFiles } from "./files";
import { refreshLogs, refreshRecentLogs } from "./logs";
import {
  activeTab,
  lastAppTab,
  hasAuthErrorInLocation,
  hasLoginCodeInLocation,
  hasTokenInLocation,
  readAuthErrorFromLocation,
  readAutofillFromLocation,
  readLoginCodeFromLocation,
  readTokenFromLocation,
  syncLocationWithTab,
  syncTabFromLocation,
  type TabKey
} from "./router";
import {
  authCallbackError,
  completeGithubLogin,
  hasConsoleAccess,
  hydrateSessionFromBootstrap,
  isAuthenticated,
  isDesktopShell,
  authChecked,
  loginToken,
  prefillPassword,
  prefillUsername,
  refreshAuthStatus,
  refreshSettings,
  refreshStatus,
  status
} from "./session";
import { feedItems, refreshFeedItems, refreshSubscriptionList, resetSubscriptionForm, subscriptions } from "./subscriptions";
import { feedback, updateFeedback } from "./toasts";

const LIVE_REFRESH_ACTIVE_MS = 3000;
const LIVE_REFRESH_IDLE_MS = 12000;
const LIVE_REFRESH_SSE_HEARTBEAT_MS = 30000;
const BACKEND_GRACE_SECONDS = 15;
const BACKEND_RETRY_MIN_SECONDS = 5;
const BACKEND_RETRY_MAX_SECONDS = 60;

export const isLoading = ref(false);
// 顶栏 .scope-progress 已提供非阻塞同步进度条，不再用全屏遮罩阻挡移动端首屏点击与滑动。
export const globalSyncOverlay = computed(() => false);

// 对齐 Diana 的后端断连提示状态机：宽限期 15 秒内按秒静默重连，超时后升级为排查页并退避重试。
export const backendDown = ref(false);
export const backendDownDetail = ref("");
export const backendRetrying = ref(false);
export const backendRetryDelay = ref(1);
export const backendWaiting = ref(true);
export const backendUpdating = ref(false);
export const backendGraceLeft = ref(BACKEND_GRACE_SECONDS);

let backendRetryTimer: number | null = null;
let backendDownSince = 0;
let versionBeforeOutage = "";
let consecutiveUnreachable = 0;

let liveRefreshTimer: number | null = null;
let liveRefreshInFlight = false;

async function canSafelyReloadEntryHtml(): Promise<boolean> {
  try {
    const res = await fetch(window.location.pathname || "/", {
      method: "GET",
      cache: "no-store"
    });
    if (!res.ok) {
      return false;
    }
    const html = await res.text();
    const currentScript = document.querySelector<HTMLScriptElement>('script[type="module"][src*="/assets/index-"]')?.getAttribute("src") ?? "";
    if (!currentScript) {
      return false;
    }
    // 只有静态入口可达且前端产物哈希已变化时才整页重载；若进程直接停了，绝不重载进浏览器的 ERR_CONNECTION_REFUSED 页
    return html.includes("/assets/index-") && !html.includes(currentScript);
  } catch {
    return false;
  }
}

function clearBackendRetryTimer(): void {
  if (backendRetryTimer !== null) {
    window.clearTimeout(backendRetryTimer);
    backendRetryTimer = null;
  }
}

function scheduleBackendRetry(): void {
  clearBackendRetryTimer();
  backendRetryTimer = window.setTimeout(() => {
    void retryBackend();
  }, backendRetryDelay.value * 1000);
}

export function showBackendDown(detail: string): void {
  if (import.meta.env.VITE_DEMO_MODE === "true") {
    return;
  }
  stopLiveRefresh();
  disconnectDownloadEvents();
  if (!backendDown.value) {
    versionBeforeOutage = status.value?.appVersion ?? "";
  }
  backendDownDetail.value = detail || "连不上 Bangumi Cat 后端服务";
  backendDown.value = true;
  backendRetrying.value = false;
  backendWaiting.value = true;
  backendUpdating.value = updateInstallingRecently();
  backendRetryDelay.value = 1;
  backendGraceLeft.value = BACKEND_GRACE_SECONDS;
  backendDownSince = Date.now();
  authChecked.value = true;
  scheduleBackendRetry();
}

export async function retryBackend(): Promise<void> {
  clearBackendRetryTimer();
  if (backendRetrying.value) {
    return;
  }
  backendRetrying.value = true;
  const outageVersion = versionBeforeOutage;
  let reachable = false;
  try {
    await refreshAuthStatus({ throwOnUnreachable: true });
    reachable = true;
  } catch (err) {
    backendDownDetail.value = err instanceof Error ? err.message : "连不上 Bangumi Cat 后端服务";
    reachable = false;
  }
  backendRetrying.value = false;

  if (reachable) {
    backendDown.value = false;
    backendWaiting.value = true;
    backendUpdating.value = false;
    consecutiveUnreachable = 0;
    clearUpdateInstalling();
    authChecked.value = true;

    if (hasConsoleAccess.value || readApiToken()) {
      await loadDashboard();
      const nowVersion = status.value?.appVersion ?? "";
      if (outageVersion && nowVersion && nowVersion !== outageVersion) {
        updateFeedback(`后端已更新到 v${nowVersion}，正在重新载入界面…`, "success");
        versionBeforeOutage = "";
        if (autoReloadAllowed()) {
          markAutoReloaded();
          window.setTimeout(() => window.location.reload(), 1200);
        }
        return;
      }
    }
    versionBeforeOutage = "";
    startLiveRefresh();
    return;
  }

  const elapsed = Math.floor((Date.now() - backendDownSince) / 1000);
  backendGraceLeft.value = Math.max(BACKEND_GRACE_SECONDS - elapsed, 0);
  if (backendGraceLeft.value > 0) {
    backendRetryDelay.value = 1;
    scheduleBackendRetry();
    return;
  }
  if (backendWaiting.value && autoReloadAllowed() && (await canSafelyReloadEntryHtml())) {
    markAutoReloaded();
    window.location.reload();
    return;
  }
  backendWaiting.value = false;
  backendRetryDelay.value =
    backendRetryDelay.value < BACKEND_RETRY_MIN_SECONDS
      ? BACKEND_RETRY_MIN_SECONDS
      : Math.min(backendRetryDelay.value * 2, BACKEND_RETRY_MAX_SECONDS);
  scheduleBackendRetry();
}

function loadFailureMessage(label: string, reason: unknown): string {
  const message = reason instanceof Error ? reason.message : String(reason);
  return t("dashboard.sectionFailure", { label, message });
}

let feedItemsLoaded = false;

async function applyBootstrapSnapshot(payload: BootstrapResponse): Promise<boolean> {
  const authed = hydrateSessionFromBootstrap(payload.auth, payload.status ?? null, payload.settings ?? null);
  authChecked.value = true;
  if (!authed && !isDesktopShell) {
    return false;
  }
  if (Array.isArray(payload.favorites)) {
    favorites.value = payload.favorites;
  }
  if (Array.isArray(payload.hidden)) {
    hydrateHiddenAnime(payload.hidden);
  }
  if (Array.isArray(payload.subscriptions)) {
    subscriptions.value = payload.subscriptions;
  }
  if (Array.isArray(payload.downloads)) {
    hydrateDownloads(payload.downloads);
  }
  if (Array.isArray(payload.homepage) && payload.homepage.length > 0) {
    hydrateHomepageAnime(payload.homepage);
  }
  if (!payload.homepageFresh || !payload.homepage?.length) {
    void refreshHomepage({ silent: true });
  }
  if (discoveryMode.value === "catalog") {
    void refreshCatalogAnime({ silent: true });
  }
  feedback.value = t("dashboard.ready");
  connectDownloadEvents();
  await restorePlaybackFromLocation(activeTab.value);
  loadTabData(activeTab.value);
  return true;
}

export async function loadDashboard(): Promise<void> {
  isLoading.value = true;
  try {
    const payload = await getBootstrapPayload();
    if (payload.auth.authenticated) {
      await applyBootstrapSnapshot(payload);
      return;
    }

    await Promise.all([refreshStatus(), refreshSettings()]);
    isAuthenticated.value = true;
    authChecked.value = true;
    void refreshHomepage({ silent: true });
    if (discoveryMode.value === "catalog") {
      void refreshCatalogAnime({ silent: true });
    }

    const sections: Array<[string, Promise<unknown>]> = [
      ["订阅", refreshSubscriptionList()],
      ["下载", refreshDownloads()],
      ["收藏", refreshFavorites()],
      ["隐藏列表", refreshHiddenAnime()]
    ];
    const results = await Promise.allSettled(sections.map(([, task]) => task));
    const failures = results
      .map((result, index) => (result.status === "rejected" ? loadFailureMessage(sections[index][0], result.reason) : null))
      .filter((item): item is string => Boolean(item));


    if (failures.length) {
      updateFeedback(t("dashboard.partialLoadFailed", { details: failures.join("；") }), "error");
    } else {
      feedback.value = t("dashboard.ready");
    }
    connectDownloadEvents();
    await restorePlaybackFromLocation(activeTab.value);
    void loadTabData(activeTab.value);
  } catch (error) {
    if (error instanceof ApiRequestError && error.code === "AUTH_INVALID") {
      // 本地残留的旧静态 token 不能把桌面壳或有效 Cookie 挡在门外：清掉后重试一次
      if (readApiToken() && readApiToken() !== readDesktopShellToken()) {
        clearApiToken();
        loginToken.value = readDesktopShellToken();
        isLoading.value = false;
        const authed = await refreshAuthStatus();
        if (isDesktopShell || authed) {
          await loadDashboard();
          return;
        }
      }
      isAuthenticated.value = false;
      authChecked.value = true;
    } else if (isBackendUnreachable(error)) {
      showBackendDown(error instanceof Error ? error.message : "连不上 Bangumi Cat 后端服务");
    } else {
      updateFeedback(error instanceof Error ? error.message : t("dashboard.loadFailed"), "error");
      authChecked.value = true;
    }
  } finally {
    isLoading.value = false;
  }
}

/**
 * 实时轮询：下载页或有活跃任务时 3s，其余情况 10s；页面隐藏时降到 30s
 * （不能完全停掉——Electron 最小化后仍要保持大致同步）。
 * 每轮只拉 status + downloads（日志页时附带日志）。
 */
async function refreshLiveState(): Promise<void> {
  if (!hasConsoleAccess.value || backendDown.value || liveRefreshInFlight) {
    return;
  }

  liveRefreshInFlight = true;
  try {
    const shouldPollDownloads = !sseConnected.value || document.hidden;
    const [nextStatus, nextDownloads, nextLogs] = await Promise.allSettled([
      refreshStatus(),
      shouldPollDownloads ? refreshDownloads() : Promise.resolve(),
      activeTab.value === "logs" ? refreshRecentLogs(200) : Promise.resolve()
    ]);
    void nextDownloads;
    void nextLogs;

    if (nextStatus.status === "fulfilled") {
      consecutiveUnreachable = 0;
    } else {
      const reason = nextStatus.reason;
      if (reason instanceof ApiRequestError && reason.code === "AUTH_INVALID" && !isDesktopShell) {
        consecutiveUnreachable = 0;
        isAuthenticated.value = false;
      } else if (isBackendUnreachable(reason)) {
        consecutiveUnreachable += 1;
        if (updateInstallingRecently() || consecutiveUnreachable >= 2) {
          showBackendDown(reason instanceof Error ? reason.message : "连不上 Bangumi Cat 后端服务");
          return;
        }
        window.setTimeout(() => void refreshLiveState(), 1500);
      }
    }
  } finally {
    liveRefreshInFlight = false;
  }
}

const LIVE_REFRESH_HIDDEN_MS = 30000;

function liveRefreshInterval(): number {
  if (document.hidden) {
    return LIVE_REFRESH_HIDDEN_MS;
  }
  if (activeTab.value === "logs") {
    return LIVE_REFRESH_ACTIVE_MS;
  }
  if (sseConnected.value) {
    return LIVE_REFRESH_SSE_HEARTBEAT_MS;
  }
  const busyDownloads = onlineDownloadCount.value > 0;
  return activeTab.value === "downloads" || busyDownloads
    ? LIVE_REFRESH_ACTIVE_MS
    : LIVE_REFRESH_IDLE_MS;
}

function scheduleLiveRefresh(): void {
  stopLiveRefresh();
  if (!hasConsoleAccess.value) {
    return;
  }
  liveRefreshTimer = window.setTimeout(async () => {
    await refreshLiveState();
    scheduleLiveRefresh();
  }, liveRefreshInterval());
}

export function startLiveRefresh(): void {
  scheduleLiveRefresh();
}

export function stopLiveRefresh(): void {
  if (liveRefreshTimer !== null) {
    window.clearTimeout(liveRefreshTimer);
    liveRefreshTimer = null;
  }
}

/** 顶栏「刷新」：静默刷新当前数据,不弹全屏加载遮罩(避免整页闪动)。 */
export async function refreshCurrentView(): Promise<void> {
  await withBusy("refresh-view", async () => {
    const tasks: Promise<unknown>[] = [
      refreshStatus(),
      refreshSubscriptionList(),
      refreshFeedItems(),
      refreshDownloads(),
      refreshFavorites()
    ];
    if (activeTab.value === "home") {
      tasks.push(
        discoveryMode.value === "catalog"
          ? refreshCatalogAnime({ silent: true })
          : refreshHomepage({ silent: true })
      );
    }
    if (activeTab.value === "files") {
      tasks.push(refreshActiveFiles());
    }
    if (activeTab.value === "history") {
      bumpPlaybackHistoryVersion();
    }
    if (activeTab.value === "logs") {
      tasks.push(refreshLogs());
    }
    if (activeTab.value === "integrations") {
      tasks.push(refreshAgents());
    }
    const results = await Promise.allSettled(tasks);
    if (results[0]?.status === "rejected" && isBackendUnreachable(results[0].reason)) {
      showBackendDown(
        results[0].reason instanceof Error ? results[0].reason.message : "连不上 Bangumi Cat 后端服务"
      );
    }
  });
}

export function selectTab(tab: TabKey): void {
  if (activeTab.value !== "settings") {
    lastAppTab.value = activeTab.value;
  }
  resetTabState(tab);
  activeTab.value = tab;
  syncLocationWithTab("push");
}

function resetTabState(tab: TabKey): void {
  if (tab === "home" || tab === "downloads") {
    resetDiscoveryView();
  } else if (tab === "favorites" || tab === "history") {
    closeAnimeDetail();
  }
  if (tab === "downloads") {
    // 打开下载管理即视为已读，清掉侧栏的完成角标
    clearUnreadCompletedDownloads();
  }
  if (tab === "subscriptions") {
    resetSubscriptionForm();
  }
}

function handleUnauthorized(): void {
  if (isDesktopShell) {
    return;
  }
  clearBackendRetryTimer();
  backendDown.value = false;
  consecutiveUnreachable = 0;
  isAuthenticated.value = false;
  authChecked.value = true;
  stopLiveRefresh();
  disconnectDownloadEvents();
}

export async function bootstrap(): Promise<void> {
  // URL 携带 token（服务端启动日志给出的直达链接）时静默登录
  const tokenFromLocation = readTokenFromLocation();
  if (tokenFromLocation) {
    persistApiToken(tokenFromLocation);
    loginToken.value = tokenFromLocation;
  }

  const authError = readAuthErrorFromLocation();
  if (authError) {
    authCallbackError.value = authError;
    updateFeedback(authError, "error");
  }

  const autofill = readAutofillFromLocation();
  if (autofill.username) {
    prefillUsername.value = autofill.username;
  }
  if (autofill.password) {
    prefillPassword.value = autofill.password;
  }

  // OAuth / 扫码回调带回来的一次性兑换码：换成会话 token 后立刻把参数从地址栏抹掉
  const loginCode = readLoginCodeFromLocation();
  if (loginCode) {
    await completeGithubLogin(loginCode);
  }

  if (hasTokenInLocation() || hasLoginCodeInLocation() || hasAuthErrorInLocation() || autofill.hadAutofill) {
    syncLocationWithTab("replace", { clearToken: true });
  }

  window.addEventListener("popstate", handlePopState);
  window.addEventListener("bgmcat:unauthorized", handleUnauthorized);
  document.addEventListener("visibilitychange", handleVisibilityChange);

  try {
    const payload = await getBootstrapPayload();
    if (payload.auth.authenticated) {
      await applyBootstrapSnapshot(payload);
    } else if (readApiToken() && readApiToken() !== readDesktopShellToken()) {
      // 本地残留旧静态 token 失效时清掉重试一次，避免阻断有效 Cookie 会话
      clearApiToken();
      loginToken.value = readDesktopShellToken();
      const retryPayload = await getBootstrapPayload();
      if (retryPayload.auth.authenticated) {
        await applyBootstrapSnapshot(retryPayload);
      } else if (isDesktopShell) {
        await loadDashboard();
      } else {
        hydrateSessionFromBootstrap(retryPayload.auth);
        authChecked.value = true;
      }
    } else if (isDesktopShell) {
      await loadDashboard();
    } else {
      hydrateSessionFromBootstrap(payload.auth);
      authChecked.value = true;
    }
  } catch (err) {
    showBackendDown(err instanceof Error ? err.message : "连不上 Bangumi Cat 后端服务");
    return;
  }
  if (!backendDown.value && hasConsoleAccess.value) {
    startLiveRefresh();
  }
}

export function teardown(): void {
  clearBackendRetryTimer();
  stopLiveRefresh();
  disconnectDownloadEvents();
  window.removeEventListener("popstate", handlePopState);
  window.removeEventListener("bgmcat:unauthorized", handleUnauthorized);
  document.removeEventListener("visibilitychange", handleVisibilityChange);
}

function handleVisibilityChange(): void {
  // 从后台恢复可见时立即同步一次，并按可见节奏重排定时器
  if (!document.hidden) {
    if (backendDown.value) {
      void retryBackend();
      return;
    }
    if (hasConsoleAccess.value) {
      connectDownloadEvents();
    }
    void refreshLiveState();
  }
  if (!backendDown.value) {
    startLiveRefresh();
  }
}

watch(sseConnected, (connected, prev) => {
  if (!connected && prev === true && hasConsoleAccess.value && !backendDown.value && !document.hidden) {
    window.setTimeout(() => void refreshLiveState(), 800);
  }
});

function handlePopState(): void {
  syncTabFromLocation();
  void restorePlaybackFromLocation(activeTab.value);
}

watch(hasConsoleAccess, (allowed) => {
  if (allowed) {
    connectDownloadEvents();
  } else {
    disconnectDownloadEvents();
  }
  startLiveRefresh();
});

/** 页签级懒加载：日志与 MCP Agent 探测只在进入对应页签时拉取 */
function loadTabData(tab: TabKey): void {
  if (!hasConsoleAccess.value) {
    return;
  }
  if (tab === "subscriptions" && (!feedItemsLoaded || !feedItems.value.length)) {
    feedItemsLoaded = true;
    void refreshFeedItems();
  }
  if (tab === "logs") {
    void refreshLogs();
  }
  if (tab === "integrations" && !agentsLoaded()) {
    void refreshAgents().catch(() => undefined);
  }
  if (tab === "home" && !homepageAnime.value.length && !homepageError.value) {
    void refreshHomepage({ silent: true });
  }
}

watch(activeTab, () => {
  syncLocationWithTab("replace");
  startLiveRefresh();
  loadTabData(activeTab.value);
});
