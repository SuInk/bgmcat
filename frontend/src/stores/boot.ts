import { computed, ref, watch } from "vue";

import {
  ApiRequestError,
  clearApiToken,
  persistApiToken,
  readApiToken,
  readDesktopShellToken
} from "../api";
import { t } from "../i18n";
import { agentsLoaded, refreshAgents } from "./agents";
import { isBusy, withBusy } from "./busy";
import {
  refreshHomepage,
  homepageAnime,
  homepageError,
  resetDiscoveryView
} from "./discovery";
import {
  clearUnreadCompletedDownloads,
  downloads,
  onlineDownloadCount,
  refreshDownloads,
  restorePlaybackFromLocation
} from "./downloads";
import { refreshFavorites } from "./favorites";
import { refreshLogs, refreshRecentLogs } from "./logs";
import {
  activeTab,
  lastAppTab,
  hasLoginCodeInLocation,
  hasTokenInLocation,
  readLoginCodeFromLocation,
  readTokenFromLocation,
  syncLocationWithTab,
  syncTabFromLocation,
  type TabKey
} from "./router";
import {
  completeGithubLogin,
  hasConsoleAccess,
  isAuthenticated,
  isDesktopShell,
  authChecked,
  loginToken,
  refreshPublicStatus,
  refreshSettings,
  refreshStatus
} from "./session";
import { refreshFeedItems, refreshSubscriptionList, resetSubscriptionForm } from "./subscriptions";
import { feedback, updateFeedback } from "./toasts";

const LIVE_REFRESH_ACTIVE_MS = 3000;
const LIVE_REFRESH_IDLE_MS = 10000;

export const isLoading = ref(false);
// 全屏同步遮罩只在初始加载、或用户正停留在首页且首页还没有数据时出现，
// 避免后台静默同步把其他页面也盖住
export const globalSyncOverlay = computed(
  () =>
    hasConsoleAccess.value &&
    (isLoading.value ||
      (activeTab.value === "home" && isBusy("discovery-home") && homepageAnime.value.length === 0))
);

let liveRefreshTimer: number | null = null;
let liveRefreshInFlight = false;

function loadFailureMessage(label: string, reason: unknown): string {
  const message = reason instanceof Error ? reason.message : String(reason);
  return t("dashboard.sectionFailure", { label, message });
}

export async function loadDashboard(): Promise<void> {
  isLoading.value = true;
  try {
    await Promise.all([refreshStatus(), refreshSettings()]);
    isAuthenticated.value = true;
    authChecked.value = true;

    // 日志和 MCP Agent 探测不在首屏拉取，进入对应页签时才加载
    const sections: Array<[string, Promise<unknown>]> = [
      ["订阅", refreshSubscriptionList()],
      ["条目", refreshFeedItems()],
      ["下载", refreshDownloads()],
      ["收藏", refreshFavorites()]
    ];
    const results = await Promise.allSettled(sections.map(([, task]) => task));
    const failures = results
      .map((result, index) => (result.status === "rejected" ? loadFailureMessage(sections[index][0], result.reason) : null))
      .filter((item): item is string => Boolean(item));

    // Bangumi 首页数据量大且走外网，单独拉，失败只在首页提示
    void refreshHomepage({ silent: true });

    if (failures.length) {
      updateFeedback(t("dashboard.partialLoadFailed", { details: failures.join("；") }), "error");
    } else {
      feedback.value = t("dashboard.ready");
    }
    await restorePlaybackFromLocation(activeTab.value);
    void loadTabData(activeTab.value);
  } catch (error) {
    if (error instanceof ApiRequestError && error.code === "AUTH_INVALID") {
      // 本地残留的旧 token 不能把桌面壳锁在门外：清掉后用壳注入的 token 再试一次
      if (isDesktopShell && readApiToken() !== readDesktopShellToken()) {
        clearApiToken();
        isLoading.value = false;
        await loadDashboard();
        return;
      }
      isAuthenticated.value = false;
      authChecked.value = true;
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
  if (!hasConsoleAccess.value || liveRefreshInFlight) {
    return;
  }

  liveRefreshInFlight = true;
  try {
    const [nextStatus, nextDownloads, nextLogs] = await Promise.allSettled([
      refreshStatus(),
      refreshDownloads(),
      activeTab.value === "logs" ? refreshRecentLogs(200) : Promise.resolve()
    ]);
    void nextDownloads;
    void nextLogs;

    if (nextStatus.status === "rejected") {
      const reason = nextStatus.reason;
      if (reason instanceof ApiRequestError && reason.code === "AUTH_INVALID" && !isDesktopShell) {
        isAuthenticated.value = false;
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
  const busyDownloads = onlineDownloadCount.value > 0;
  return activeTab.value === "downloads" || activeTab.value === "logs" || busyDownloads
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
      tasks.push(refreshHomepage({ silent: true }));
    }
    if (activeTab.value === "logs") {
      tasks.push(refreshLogs());
    }
    if (activeTab.value === "integrations") {
      tasks.push(refreshAgents());
    }
    await Promise.allSettled(tasks);
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
  }
  if (tab === "downloads") {
    // 打开下载管理即视为已读，清掉侧栏的完成角标
    clearUnreadCompletedDownloads();
  }
  if (tab === "subscriptions") {
    resetSubscriptionForm();
  }
}

export async function bootstrap(): Promise<void> {
  // URL 携带 token（服务端启动日志给出的直达链接）时静默登录
  const tokenFromLocation = readTokenFromLocation();
  if (tokenFromLocation) {
    persistApiToken(tokenFromLocation);
    loginToken.value = tokenFromLocation;
  }

  // GitHub 回调带回来的一次性兑换码：换成会话 token 后立刻把参数从地址栏抹掉
  const loginCode = readLoginCodeFromLocation();
  if (loginCode) {
    await completeGithubLogin(loginCode);
  }

  if (hasTokenInLocation() || hasLoginCodeInLocation()) {
    syncLocationWithTab("replace", { clearToken: true });
  }

  void refreshPublicStatus();

  window.addEventListener("popstate", handlePopState);
  document.addEventListener("visibilitychange", handleVisibilityChange);
  await loadDashboard();
  startLiveRefresh();
}

export function teardown(): void {
  stopLiveRefresh();
  window.removeEventListener("popstate", handlePopState);
  document.removeEventListener("visibilitychange", handleVisibilityChange);
}

function handleVisibilityChange(): void {
  // 从后台恢复可见时立即同步一次，并按可见节奏重排定时器
  if (!document.hidden) {
    void refreshLiveState();
  }
  startLiveRefresh();
}

function handlePopState(): void {
  syncTabFromLocation();
  void restorePlaybackFromLocation(activeTab.value);
}

watch(hasConsoleAccess, () => {
  startLiveRefresh();
});

/** 页签级懒加载：日志与 MCP Agent 探测只在进入对应页签时拉取 */
function loadTabData(tab: TabKey): void {
  if (!hasConsoleAccess.value) {
    return;
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
