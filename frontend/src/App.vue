<script setup lang="ts">
import { computed, defineAsyncComponent, nextTick, onMounted, onUnmounted, ref, watch } from "vue";

import AppDialog from "./components/common/AppDialog.vue";
import AppToastStack from "./components/common/AppToastStack.vue";
import AsyncPagePlaceholder from "./components/common/AsyncPagePlaceholder.vue";
import BrandLogo from "./components/common/BrandLogo.vue";
import CuteLoader from "./components/common/CuteLoader.vue";
import AppSidebar from "./components/layout/AppSidebar.vue";
import AppNavIcon from "./components/layout/AppNavIcon.vue";
import HomeView from "./views/HomeView.vue";
import {
  backendDown,
  backendDownDetail,
  backendGraceLeft,
  backendRetryDelay,
  backendRetrying,
  backendUpdating,
  backendWaiting,
  bootstrap,
  globalSyncOverlay,
  isLoading,
  loadDashboard,
  refreshCurrentView,
  retryBackend,
  selectTab,
  showBackendDown,
  teardown
} from "./stores/boot";
import { busyCount, isBusy } from "./stores/busy";
import {
  activePlayback,
  closePlayback,
  onlineDownloadCount,
  sseConnected,
  unreadCompletedDownloadCount
} from "./stores/downloads";
import { activeFilePlayback, closeFilePlayback } from "./stores/files";
import { animeDetail, animeDetailError, closeAnimeDetail } from "./stores/discovery";
import { refreshUpdateStatus, updateAvailable } from "./stores/update";
import { openExternalLink } from "./lib/actions";
import { activeTab, stripRouteBase, TABS, type TabKey } from "./stores/router";
import {
  authChecked,
  hasConsoleAccess,
  isDesktopShell,
  logout,
  status
} from "./stores/session";
import { initTheme, teardownTheme } from "./stores/theme";
import { shareModalOpen } from "./stores/shares";
import { dismissToast, toasts } from "./stores/toasts";
import {
  handleWindowResize,
  isMobileViewport,
  mobileSidebarOpen,
  sidebarCollapsed,
  sidebarResizing,
  sidebarWidth
} from "./stores/ui";

// 首页以外的视图按需加载,缩小首包(下载页拖着播放器相关依赖尤其值得拆)
const BackendDownView = defineAsyncComponent(() => import("./views/BackendDownView.vue"));
const LoginView = defineAsyncComponent(() => import("./views/LoginView.vue"));
const VersionModal = defineAsyncComponent(() => import("./components/settings/VersionModal.vue"));
const ShareModal = defineAsyncComponent(() => import("./components/shares/ShareModal.vue"));
const SharePublicView = defineAsyncComponent(() => import("./views/SharePublicView.vue"));
const versionOpen = ref(false);
const logoutConfirmOpen = ref(false);
function openVersion() { mobileSidebarOpen.value = false; versionOpen.value = true; }
function confirmLogout() { logoutConfirmOpen.value = false; void logout(); }

const publicShareCode = ref(
  /^\/s\/([A-Za-z0-9_-]{6,32})\/?$/u.exec(stripRouteBase(window.location.pathname))?.[1] ?? ""
);

const lazyPage = (loader: () => Promise<any>) =>
  defineAsyncComponent({ loader, loadingComponent: AsyncPagePlaceholder, delay: 80 });

const DownloadsView = lazyPage(() => import("./views/DownloadsView.vue"));
const FavoritesView = lazyPage(() => import("./views/FavoritesView.vue"));
const FilesView = lazyPage(() => import("./views/FilesView.vue"));
const HistoryView = lazyPage(() => import("./views/HistoryView.vue"));
const LogsView = lazyPage(() => import("./views/LogsView.vue"));
const MangaView = lazyPage(() => import("./views/MangaView.vue"));
const SettingsView = lazyPage(() => import("./views/SettingsView.vue"));
const SubscriptionsView = lazyPage(() => import("./views/SubscriptionsView.vue"));

const activeTabMeta = computed(() => TABS.find((tab) => tab.key === activeTab.value) ?? TABS[0]);
const isDemoMode = import.meta.env.VITE_DEMO_MODE === "true";
/** 演示挂在官网的 demo/ 子目录下，官网首页就是它的上一级；深层路由下不能用相对的 ../ */
const demoSiteHome = new URL("../", new URL(import.meta.env.BASE_URL, window.location.origin)).pathname;

const refreshing = computed(() => isBusy("refresh-view"));

const downloadClientOnline = computed(() => {
  if (!status.value) {
    return null;
  }
  if (typeof status.value.downloadClientOnline === "boolean") {
    return status.value.downloadClientOnline;
  }
  return status.value.downloadClient === "qbittorrent" ? status.value.qbittorrentOnline : status.value.aria2Online;
});

const downloadClientLabel = computed(() => {
  if (status.value?.downloadClientName) {
    return status.value.downloadClientName;
  }
  switch (status.value?.downloadClient) {
    case "qbittorrent":
      return "qBittorrent";
    case "aria2-rpc":
      return "Aria2 RPC";
    case "transmission":
      return "Transmission";
    default:
      return "内置引擎";
  }
});

const systemOnlineState = computed<"ok" | "warn" | "err">(() => {
  if (downloadClientOnline.value === false) {
    return "err";
  }
  if (!isDemoMode && !sseConnected.value) {
    return "warn";
  }
  return "ok";
});

const systemOnlineLabel = computed(() => {
  if (downloadClientOnline.value === false) {
    return `${downloadClientLabel.value}离线`;
  }
  if (!isDemoMode && !sseConnected.value) {
    return "重连中";
  }
  return "在线";
});

const systemOnlineTitle = computed(() => {
  const clientPart = `${downloadClientLabel.value}${downloadClientOnline.value === false ? "离线，请检查设置" : "在线"}`;
  if (isDemoMode) {
    return clientPart;
  }
  const ssePart = sseConnected.value ? "实时推送正常" : "实时连接重连中";
  return `${clientPart} · ${ssePart}`;
});

const menuButton = ref<HTMLButtonElement | null>(null);
watch(mobileSidebarOpen, async open => { if (!open) { await nextTick(); menuButton.value?.focus(); } });
watch(sidebarCollapsed, async collapsed => { if (collapsed) { await nextTick(); menuButton.value?.focus(); } });
function isTypingTarget(target: EventTarget | null): target is HTMLElement {
  const el = target as HTMLElement | null;
  return Boolean(el && (el.isContentEditable || /^(INPUT|TEXTAREA|SELECT)$/u.test(el.tagName)));
}

/**
 * 全局快捷键（参考 Linear / YouTube）：
 * - 「/」聚焦当前页的搜索框；
 * - Esc 一层层往回退：输入框失焦 → 关侧栏抽屉 → 关播放页 → 关番剧详情。
 * 对话框开着时一律不管，Esc 由 AppDialog 自己处理；播放器全屏时 Esc 留给退出全屏。
 */
function handleGlobalKeydown(event: KeyboardEvent): void {
  if (event.defaultPrevented || event.isComposing || document.querySelector('[role="dialog"]')) {
    return;
  }
  if (event.key === "Escape") {
    if (isTypingTarget(event.target)) {
      event.target.blur();
    } else if (mobileSidebarOpen.value) {
      mobileSidebarOpen.value = false;
    } else if (document.fullscreenElement || document.querySelector(".art-fullscreen-web")) {
      return;
    } else if (activePlayback.value) {
      closePlayback();
    } else if (activeFilePlayback.value) {
      closeFilePlayback();
    } else if (animeDetail.value || animeDetailError.value) {
      closeAnimeDetail();
    }
    return;
  }
  // 中文输入法下 / 键打出来的是「、」，按物理键位也认
  if ((event.key === "/" || event.code === "Slash") && !event.metaKey && !event.ctrlKey && !event.altKey && !isTypingTarget(event.target)) {
    const input = [...document.querySelectorAll<HTMLInputElement>(
      '.app-content input[type="search"], .app-content input[placeholder*="搜索"], .app-content input[aria-label*="搜索"]'
    )].find((el) => el.offsetParent !== null);
    if (input) {
      event.preventDefault();
      input.focus();
      input.select();
    }
  }
}

function handleTabSelection(tab: string): void {
  selectTab(tab as TabKey);
  if (isMobileViewport.value) {
    mobileSidebarOpen.value = false;
  }
}

function handleSidebarToggle(): void {
  if (isMobileViewport.value) {
    mobileSidebarOpen.value = !mobileSidebarOpen.value;
  } else {
    sidebarCollapsed.value = !sidebarCollapsed.value;
  }
}

async function handleLoginSuccess(): Promise<void> {
  await loadDashboard();
}

// 侧栏只读取最近一次检查状态，不在后台自动访问 GitHub。
const UPDATE_BADGE_REFRESH_MS = 30 * 60 * 1000;
let updateBadgeTimer: number | null = null;

onMounted(() => {
  initTheme();
  if (publicShareCode.value) {
    return;
  }
  window.addEventListener("resize", handleWindowResize);
  window.addEventListener("keydown", handleGlobalKeydown);
  void bootstrap();
  void refreshUpdateStatus();
  updateBadgeTimer = window.setInterval(() => void refreshUpdateStatus(), UPDATE_BADGE_REFRESH_MS);
});

onUnmounted(() => {
  if (updateBadgeTimer !== null) {
    window.clearInterval(updateBadgeTimer);
    updateBadgeTimer = null;
  }
  teardown();
  teardownTheme();
  window.removeEventListener("resize", handleWindowResize);
  window.removeEventListener("keydown", handleGlobalKeydown);
});
</script>

<template>
  <!-- Toast 放在分支外：登录页与启动态也需要提示，挂在 app-shell 里会被锁定态吞掉 -->
  <AppToastStack :toasts="toasts" @dismiss="dismissToast" />

  <SharePublicView v-if="publicShareCode" :code="publicShareCode" />

  <div v-else-if="!authChecked" class="startup-loading" role="status" aria-live="polite">
    <BrandLogo :size="48" />
    <CuteLoader mode="connecting" label="正在连接 Bangumi Cat…" />
  </div>

  <BackendDownView
    v-else-if="backendDown"
    :message="backendDownDetail"
    :retrying="backendRetrying"
    :retry-in-seconds="backendRetryDelay"
    :waiting="backendWaiting"
    :grace-left="backendGraceLeft"
    :updating="backendUpdating"
    @retry="retryBackend"
  />

  <LoginView v-else-if="!hasConsoleAccess" @success="handleLoginSuccess" @unreachable="showBackendDown" />

  <main
    v-else
    class="app-shell grid h-[100dvh] min-h-screen overflow-hidden bg-[var(--shell-bg)]"
    :class="{ 'is-resizing': sidebarResizing }"
    :style="{ gridTemplateColumns: isMobileViewport ? 'minmax(0,1fr)' : sidebarCollapsed ? '0px minmax(0,1fr)' : `${sidebarWidth}px minmax(0,1fr)` }"
  >
    <AppSidebar
      v-if="!isMobileViewport"
      :tabs="TABS"
      :active-tab="activeTab"
      :collapsed="sidebarCollapsed"
      :mobile="false"
      :mobile-open="true"
      :has-console-access="hasConsoleAccess"
      :active-download-count="onlineDownloadCount"
      :unread-completed-download-count="unreadCompletedDownloadCount"
      :update-available="updateAvailable"
      @select-tab="handleTabSelection"
      @toggle-collapse="handleSidebarToggle"
      @open-version="openVersion"
    />

    <div
      v-if="isMobileViewport"
      class="fixed inset-0 z-40 bg-black/45 transition-opacity duration-300"
      :class="mobileSidebarOpen ? 'pointer-events-auto opacity-100' : 'pointer-events-none opacity-0'"
      @click="mobileSidebarOpen = false"
    ></div>

    <AppSidebar
      v-if="isMobileViewport"
      :tabs="TABS"
      :active-tab="activeTab"
      :collapsed="false"
      :mobile="true"
      :mobile-open="mobileSidebarOpen"
      :has-console-access="hasConsoleAccess"
      :active-download-count="onlineDownloadCount"
      :unread-completed-download-count="unreadCompletedDownloadCount"
      :update-available="updateAvailable"
      @select-tab="handleTabSelection"
      @toggle-collapse="handleSidebarToggle"
      @open-version="openVersion"
    />

    <section class="app-main h-full min-h-0 min-w-0 overflow-hidden" :inert="isMobileViewport && mobileSidebarOpen">
      <section class="relative grid h-full min-h-0 grid-cols-[minmax(0,1fr)] grid-rows-[auto_minmax(0,1fr)] overflow-hidden">
        <header
          v-if="hasConsoleAccess"
          class="app-topbar"
        >
          <button
            v-if="isMobileViewport || sidebarCollapsed"
            ref="menuButton"
            type="button"
            class="topbar-btn topbar-menu"
            :aria-expanded="isMobileViewport ? mobileSidebarOpen : !sidebarCollapsed"
            aria-controls="app-sidebar"
            aria-label="打开侧栏"
            @click="handleSidebarToggle"
          >
            <AppNavIcon name="panel-left-open" />
          </button>

          <h1 class="topbar-title">{{ activeTabMeta.label }}</h1>
          <span class="flex-1" />

          <a
            v-if="isDemoMode"
            :href="demoSiteHome"
            class="topbar-badge topbar-demo transition hover:opacity-85"
            title="当前为纯前端演示环境，点击返回产品官网与下载页"
          >
            演示环境 · 返回官网
          </a>
          <span
            v-if="status?.restartRequired"
            class="topbar-badge warn hidden sm:inline-flex"
            title="监听地址或端口已修改，重启应用后生效"
          >
            需重启生效
          </span>
          <span
            class="topbar-badge topbar-client"
            :class="systemOnlineState"
            :title="systemOnlineTitle"
          >
            <span class="status-dot" :class="{ pulse: systemOnlineState === 'ok' }" aria-hidden="true" />
            <span>{{ systemOnlineLabel }}</span>
          </span>

          <!-- GitHub / 刷新 / 退出登录收成一组，间距比顶栏本身更紧，和左边的状态信息拉开 -->
          <div class="topbar-actions">
            <button
              type="button"
              class="topbar-btn topbar-repo"
              title="GitHub 源码仓库"
              aria-label="GitHub 源码仓库"
              @click="openExternalLink('https://github.com/SuInk/bgmcat')"
            >
              <svg viewBox="0 0 16 16" width="15" height="15" aria-hidden="true">
                <path
                  fill="currentColor"
                  d="M8 0C3.58 0 0 3.58 0 8c0 3.54 2.29 6.53 5.47 7.59.4.07.55-.17.55-.38 0-.19-.01-.82-.01-1.49-2.01.37-2.53-.49-2.69-.94-.09-.23-.48-.94-.82-1.13-.28-.15-.68-.52-.01-.53.63-.01 1.08.58 1.23.82.72 1.21 1.87.87 2.33.66.07-.52.28-.87.51-1.07-1.78-.2-3.64-.89-3.64-3.95 0-.87.31-1.59.82-2.15-.08-.2-.36-1.02.08-2.12 0 0 .67-.21 2.2.82.64-.18 1.32-.27 2-.27s1.36.09 2 .27c1.53-1.04 2.2-.82 2.2-.82.44 1.1.16 1.92.08 2.12.51.56.82 1.27.82 2.15 0 3.07-1.87 3.75-3.65 3.95.29.25.54.73.54 1.48 0 1.07-.01 1.93-.01 2.2 0 .21.15.46.55.38A8.01 8.01 0 0 0 16 8c0-4.42-3.58-8-8-8Z"
                />
              </svg>
            </button>
            <button type="button" class="topbar-btn" title="刷新数据" aria-label="刷新数据" :disabled="refreshing" @click="refreshCurrentView">
              <AppNavIcon name="refresh" :class="{ 'animate-spin': refreshing }" />
            </button>
            <button v-if="!isDesktopShell && !isDemoMode" type="button" class="topbar-btn topbar-logout" title="退出登录" aria-label="退出登录" @click="logoutConfirmOpen = true">
              <AppNavIcon name="logout" />
            </button>
          </div>
          <div v-if="isLoading || refreshing || busyCount > 0" class="scope-progress" role="progressbar" aria-label="正在加载数据"><span /></div>
        </header>

        <div class="app-content h-full min-h-0 overflow-hidden">
          <HomeView v-if="activeTab === 'home'" />
          <MangaView v-else-if="activeTab === 'manga'" />
          <FavoritesView v-else-if="activeTab === 'favorites'" />
          <HistoryView v-else-if="activeTab === 'history'" />
          <SubscriptionsView v-else-if="activeTab === 'subscriptions'" />
          <DownloadsView v-else-if="activeTab === 'downloads'" />
          <FilesView v-else-if="activeTab === 'files'" />
          <LogsView v-else-if="activeTab === 'logs'" />
          <SettingsView v-else-if="activeTab === 'settings'" />
        </div>
      </section>
    </section>

    <Transition name="sync-overlay">
      <div v-if="globalSyncOverlay" class="fixed inset-0 z-40 grid place-items-center bg-black/25 backdrop-blur-[2px] px-5">
        <section class="grid w-[min(21rem,calc(100vw-2rem))] place-items-center gap-3 rounded-[20px] border border-[var(--line)] bg-[var(--card)] px-8 py-7 text-center shadow-[var(--strong-shadow)]">
          <CuteLoader mode="loading" :label="isLoading ? '正在加载控制台…' : '正在同步番剧数据…'" />
          <p class="text-xs font-medium text-[var(--muted)]">从数据源同步中，请稍候</p>
        </section>
      </div>
    </Transition>

    <VersionModal v-if="versionOpen" @close="versionOpen = false" />
    <ShareModal v-if="shareModalOpen" />
    <AppDialog v-if="logoutConfirmOpen" title="确认退出登录？" @close="logoutConfirmOpen = false">
      <p class="text-sm leading-6 text-[var(--muted)]">
        退出后当前浏览器需要重新验证账号密码或访问令牌才能进入控制台。
      </p>
      <template #footer>
        <button type="button" class="btn-ghost" @click="logoutConfirmOpen = false">取消</button>
        <button type="button" class="btn-danger" @click="confirmLogout">退出登录</button>
      </template>
    </AppDialog>
  </main>
</template>

<style scoped>
.startup-loading { min-height:100dvh; display:flex; flex-direction:column; align-items:center; justify-content:center; gap:14px; background:var(--shell-bg); padding:24px; }
.app-shell { transition:grid-template-columns .2s ease; }
.app-shell.is-resizing { transition:none; }
/* 顶栏尺寸、字号对齐 Diana 控制台（.app-topbar / .badge / .btn.ghost.small.icon-only） */
.app-topbar { position:relative; z-index:30; display:flex; min-width:0; align-items:center; gap:12px; height:56px; padding:0 24px; border-bottom:1px solid var(--line); background:color-mix(in srgb,var(--shell-bg) 82%,transparent); backdrop-filter:blur(10px); }
.topbar-title { min-width:0; overflow:hidden; color:var(--ink); font-size:16px; font-weight:650; text-overflow:ellipsis; white-space:nowrap; }
.topbar-badge { display:inline-flex; flex:none; align-items:center; gap:6px; padding:3px 10px; border-radius:999px; background:var(--panel); color:var(--ink-secondary); font-size:11.5px; font-weight:600; white-space:nowrap; }
.topbar-badge.ok { background:var(--ok-soft); color:var(--ok); }.topbar-badge.warn { background:var(--warn-soft); color:var(--warn); }.topbar-badge.err { background:var(--err-soft); color:var(--err); }
.status-dot { width:8px; height:8px; flex-shrink:0; border-radius:50%; background:currentColor; }
.status-dot.pulse { animation:status-pulse 2.2s ease-in-out infinite; }
@keyframes status-pulse { 0%, 100% { box-shadow:0 0 0 0 color-mix(in srgb, var(--ok) 38%, transparent); } 50% { box-shadow:0 0 0 5px transparent; } }
.topbar-actions { display:flex; flex:none; align-items:center; gap:6px; }
.topbar-btn { display:inline-flex; align-items:center; justify-content:center; gap:7px; min-width:38px; height:38px; padding:8px; border:1px solid transparent; border-radius:10px; background:transparent; color:var(--ink-secondary); font-size:12.5px; font-weight:550; white-space:nowrap; transition:background .15s, border-color .15s, color .15s; }
.topbar-btn:hover:not(:disabled) { background:var(--panel); color:var(--ink); }.topbar-btn:disabled { opacity:.55; cursor:not-allowed; }
.topbar-btn :deep(svg) { width:16px; height:16px; flex-shrink:0; }
.topbar-btn svg[width="15"] { width:15px; height:15px; }
.topbar-repo:hover:not(:disabled) { border-color:var(--accent-line); background:var(--accent-soft); color:var(--accent); }
.topbar-logout:hover:not(:disabled) { border-color:var(--err); background:var(--err-soft); color:var(--err); }
.topbar-menu { width:38px; min-width:38px; height:38px; padding:6px; border-radius:10px; }.topbar-menu :deep(svg) { width:18px; height:18px; }
.app-content { width:100%; max-width:1240px; margin:0 auto; padding:10px clamp(16px, 2.5vw, 28px) 16px; }
@media(max-width:960px) { .app-topbar { padding:0 14px; } }
@media(max-width:480px) { .topbar-client { padding:3px 6px; font-size:0; gap:0; } }
@media(max-width:640px) { .app-topbar { height:48px; padding:0 12px; gap:8px; }.app-content { padding:8px 10px 12px; }.topbar-repo { display:none; } }

.sync-overlay-enter-active,
.sync-overlay-leave-active {
  transition: opacity 200ms ease;
}

.sync-overlay-enter-from,
.sync-overlay-leave-to {
  opacity: 0;
}
</style>
