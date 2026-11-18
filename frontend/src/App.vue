<script setup lang="ts">
import { computed, defineAsyncComponent, nextTick, onMounted, onUnmounted, ref, watch } from "vue";

import AppToastStack from "./components/common/AppToastStack.vue";
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
  totalCloudUploadSpeed,
  totalDownloadSpeed,
  totalUploadSpeed,
  unreadCompletedDownloadCount
} from "./stores/downloads";
import { activeFilePlayback, closeFilePlayback } from "./stores/files";
import { animeDetail, animeDetailError, closeAnimeDetail } from "./stores/discovery";
import { refreshUpdateStatus, updateAvailable } from "./stores/update";
import { openExternalLink } from "./lib/actions";
import { formatSpeed } from "./lib/format";
import { activeTab, TABS, type TabKey } from "./stores/router";
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
  sidebarCollapsed
} from "./stores/ui";

// 首页以外的视图按需加载,缩小首包(下载页拖着播放器相关依赖尤其值得拆)
const BackendDownView = defineAsyncComponent(() => import("./views/BackendDownView.vue"));
const LoginView = defineAsyncComponent(() => import("./views/LoginView.vue"));
const VersionModal = defineAsyncComponent(() => import("./components/settings/VersionModal.vue"));
const ShareModal = defineAsyncComponent(() => import("./components/shares/ShareModal.vue"));
const SharePublicView = defineAsyncComponent(() => import("./views/SharePublicView.vue"));
const versionOpen = ref(false);
function openVersion() { mobileSidebarOpen.value = false; versionOpen.value = true; }

const publicShareCode = ref(
  /^\/s\/([A-Za-z0-9_-]{6,32})\/?$/u.exec(window.location.pathname)?.[1] ?? ""
);

const DownloadsView = defineAsyncComponent(() => import("./views/DownloadsView.vue"));
const FavoritesView = defineAsyncComponent(() => import("./views/FavoritesView.vue"));
const FilesView = defineAsyncComponent(() => import("./views/FilesView.vue"));
const HistoryView = defineAsyncComponent(() => import("./views/HistoryView.vue"));
const IntegrationsView = defineAsyncComponent(() => import("./views/IntegrationsView.vue"));
const LogsView = defineAsyncComponent(() => import("./views/LogsView.vue"));
const MangaView = defineAsyncComponent(() => import("./views/MangaView.vue"));
const SettingsView = defineAsyncComponent(() => import("./views/SettingsView.vue"));
const SubscriptionsView = defineAsyncComponent(() => import("./views/SubscriptionsView.vue"));

const activeTabMeta = computed(() => TABS.find((tab) => tab.key === activeTab.value) ?? TABS[0]);
const isDemoMode = import.meta.env.VITE_DEMO_MODE === "true";

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

const combinedUploadSpeed = computed(() => totalUploadSpeed.value + totalCloudUploadSpeed.value);
const isTransferSpeedActive = computed(() => totalDownloadSpeed.value > 0 || combinedUploadSpeed.value > 0);
const transferSpeedTitle = computed(() => {
  const parts = [
    `实时速率：下载 ${formatSpeed(totalDownloadSpeed.value)} · 上传 ${formatSpeed(combinedUploadSpeed.value)}`
  ];
  if (totalCloudUploadSpeed.value > 0 && totalUploadSpeed.value > 0) {
    parts.push(`（做种 ${formatSpeed(totalUploadSpeed.value)} + 云盘 ${formatSpeed(totalCloudUploadSpeed.value)}）`);
  } else if (totalCloudUploadSpeed.value > 0) {
    parts.push("（云盘转存上传）");
  }
  if (activeTab.value !== "downloads") {
    parts.push(" · 点击前往下载管理");
  }
  return parts.join("");
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
    :style="{ gridTemplateColumns: isMobileViewport ? 'minmax(0,1fr)' : sidebarCollapsed ? '0px minmax(0,1fr)' : '236px minmax(0,1fr)' }"
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
      <section class="relative grid h-full min-h-0 grid-rows-[auto_minmax(0,1fr)] overflow-hidden">
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
            href="../"
            class="topbar-badge transition hover:opacity-85"
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
            v-if="downloadClientOnline !== null"
            class="topbar-badge"
            :class="downloadClientOnline ? 'ok' : 'err'"
            :title="`${downloadClientLabel} ${downloadClientOnline ? '在线' : '离线，请检查设置'}`"
          >
            <span class="status-dot" :class="{ pulse: downloadClientOnline }" aria-hidden="true" />
            <span class="download-client-label">{{ downloadClientLabel }}</span>{{ downloadClientOnline ? "在线" : "离线" }}
          </span>

          <button
            type="button"
            class="topbar-speeds"
            :class="{ 'is-active': isTransferSpeedActive, 'is-idle': !isTransferSpeedActive }"
            :title="transferSpeedTitle"
            :aria-label="transferSpeedTitle"
            @click="handleTabSelection('downloads')"
          >
            <span class="topbar-speed-item" :class="{ 'is-down': totalDownloadSpeed > 0 }">
              <span class="topbar-speed-arrow" aria-hidden="true">↓</span>
              <span class="topbar-speed-val">{{ formatSpeed(totalDownloadSpeed) }}</span>
            </span>
            <span class="topbar-speed-sep" aria-hidden="true" />
            <span class="topbar-speed-item" :class="{ 'is-up': combinedUploadSpeed > 0 }">
              <span class="topbar-speed-arrow" aria-hidden="true">↑</span>
              <span class="topbar-speed-val">{{ formatSpeed(combinedUploadSpeed) }}</span>
            </span>
          </button>

          <span
            v-if="!isDemoMode"
            class="topbar-stream"
            :title="sseConnected ? '事件实时推送中' : '实时通道已断开，页面数据可能不是最新'"
          >
            <span class="status-dot" :class="sseConnected ? 'is-ok' : 'is-err'" aria-hidden="true" />
            <span>{{ sseConnected ? "实时连接正常" : "实时连接已断开" }}</span>
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
            <button v-if="!isDesktopShell && !isDemoMode" type="button" class="topbar-btn topbar-logout" title="退出登录" @click="logout">
              <AppNavIcon name="logout" />
              <span class="topbar-logout-text">退出登录</span>
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
          <IntegrationsView v-else-if="activeTab === 'integrations'" />
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
  </main>
</template>

<style scoped>
.startup-loading { min-height:100dvh; display:flex; flex-direction:column; align-items:center; justify-content:center; gap:14px; background:var(--shell-bg); padding:24px; }
.app-shell { transition:grid-template-columns .2s ease; }
/* 顶栏尺寸、字号对齐 Diana 控制台（.app-topbar / .badge / .btn.ghost.small.icon-only） */
.app-topbar { position:relative; z-index:30; display:flex; align-items:center; gap:12px; height:56px; padding:0 24px; border-bottom:1px solid var(--line); background:color-mix(in srgb,var(--shell-bg) 82%,transparent); backdrop-filter:blur(10px); }
.topbar-title { min-width:0; overflow:hidden; color:var(--ink); font-size:16px; font-weight:650; text-overflow:ellipsis; white-space:nowrap; }
.topbar-badge { display:inline-flex; flex:none; align-items:center; gap:5px; padding:3px 10px; border-radius:999px; background:var(--panel); color:var(--ink-secondary); font-size:11.5px; font-weight:600; white-space:nowrap; }
.topbar-badge.ok { background:var(--ok-soft); color:var(--ok); }.topbar-badge.warn { background:var(--warn-soft); color:var(--warn); }.topbar-badge.err { background:var(--err-soft); color:var(--err); }
.topbar-speeds { display:inline-flex; flex:none; align-items:center; gap:8px; min-height:32px; padding:4px 12px; border:1px solid var(--line); border-radius:999px; background:var(--panel); color:var(--muted); font-size:11.5px; font-weight:600; font-variant-numeric:tabular-nums; white-space:nowrap; transition:background .15s, border-color .15s, color .15s; }
.topbar-speeds:hover { border-color:var(--accent-line); background:var(--tint-2); color:var(--ink); }
.topbar-speeds.is-active { border-color:color-mix(in srgb, var(--accent) 24%, var(--line)); }
.topbar-speed-item { display:inline-flex; align-items:center; gap:3px; }
.topbar-speed-sep { width:1px; height:10px; background:var(--line); flex-shrink:0; }
.topbar-speed-item.is-down .topbar-speed-arrow { color:var(--ok); }
.topbar-speed-item.is-up .topbar-speed-arrow { color:var(--accent); }
.topbar-speed-item.is-down .topbar-speed-val, .topbar-speed-item.is-up .topbar-speed-val { min-width:4.6em; text-align:right; color:var(--ink); }
.topbar-stream { display:inline-flex; align-items:center; gap:6px; flex:none; color:var(--muted); font-size:12px; font-weight:500; white-space:nowrap; }
.status-dot.is-ok { background:var(--ok); }
.status-dot.is-err { background:var(--err); }
.status-dot { width:8px; height:8px; flex-shrink:0; border-radius:50%; background:currentColor; }
.status-dot.pulse { animation:status-pulse 2.2s ease-in-out infinite; }
@keyframes status-pulse { 0%, 100% { box-shadow:0 0 0 0 color-mix(in srgb, var(--ok) 38%, transparent); } 50% { box-shadow:0 0 0 5px transparent; } }
.topbar-actions { display:flex; flex:none; align-items:center; gap:6px; }
.topbar-btn { display:inline-flex; align-items:center; justify-content:center; gap:7px; min-width:38px; height:38px; padding:8px; border:1px solid transparent; border-radius:10px; background:transparent; color:var(--ink-secondary); font-size:12.5px; font-weight:550; white-space:nowrap; transition:background .15s, border-color .15s, color .15s; }
.topbar-btn:hover:not(:disabled) { background:var(--panel); color:var(--ink); }.topbar-btn:disabled { opacity:.55; cursor:not-allowed; }
.topbar-btn :deep(svg) { width:16px; height:16px; flex-shrink:0; }
.topbar-btn svg[width="15"] { width:15px; height:15px; }
.topbar-repo:hover:not(:disabled) { border-color:var(--accent-line); background:var(--accent-soft); color:var(--accent); }
.topbar-logout { height:36px; padding:6px 13px; border-radius:999px; }.topbar-logout:hover:not(:disabled) { border-color:var(--err); color:var(--err); }
.topbar-menu { width:38px; min-width:38px; height:38px; padding:6px; border-radius:10px; }.topbar-menu :deep(svg) { width:18px; height:18px; }
.app-content { width:100%; max-width:1240px; margin:0 auto; padding:10px clamp(16px, 2.5vw, 28px) 16px; }
@media(max-width:960px) { .app-topbar { padding:0 14px; } }
@media(max-width:768px) { .topbar-stream { display:none; } }
@media(max-width:700px) { .topbar-logout-text { display:none; } .topbar-logout { width:34px; height:34px; padding:8px; } }
@media(max-width:640px) { .app-topbar { height:48px; padding:0 12px; gap:8px; }.app-content { padding:8px 10px 12px; }.topbar-repo, .download-client-label, .topbar-speeds.is-idle { display:none; }.topbar-speeds { padding:2px 8px; gap:6px; font-size:11px; } }

.sync-overlay-enter-active,
.sync-overlay-leave-active {
  transition: opacity 200ms ease;
}

.sync-overlay-enter-from,
.sync-overlay-leave-to {
  opacity: 0;
}
</style>
