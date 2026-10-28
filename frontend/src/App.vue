<script setup lang="ts">
import { computed, defineAsyncComponent, nextTick, onMounted, onUnmounted, ref, watch } from "vue";

import AppToastStack from "./components/common/AppToastStack.vue";
import CuteLoader from "./components/common/CuteLoader.vue";
import AppSidebar from "./components/layout/AppSidebar.vue";
import AppNavIcon from "./components/layout/AppNavIcon.vue";
import HomeView from "./views/HomeView.vue";
import { loadDashboard, bootstrap, globalSyncOverlay, isLoading, refreshCurrentView, selectTab, teardown } from "./stores/boot";
import { isBusy } from "./stores/busy";
import { activePlayback, closePlayback, onlineDownloadCount, unreadCompletedDownloadCount } from "./stores/downloads";
import { animeDetail, animeDetailError, closeAnimeDetail } from "./stores/discovery";
import { refreshUpdateStatus, updateAvailable } from "./stores/update";
import { openExternalLink } from "./lib/actions";
import { activeTab, TABS, type TabKey } from "./stores/router";
import {
  authChecked,
  githubOAuthAvailable,
  hasConsoleAccess,
  isDesktopShell,
  login,
  loginToken,
  logout,
  startGithubLogin,
  status
} from "./stores/session";
import { initTheme, teardownTheme } from "./stores/theme";
import { dismissToast, feedback, toasts } from "./stores/toasts";
import {
  handleWindowResize,
  isMobileViewport,
  mobileSidebarOpen,
  sidebarCollapsed
} from "./stores/ui";

// 首页以外的视图按需加载,缩小首包(下载页拖着播放器相关依赖尤其值得拆)
const VersionModal = defineAsyncComponent(() => import("./components/settings/VersionModal.vue"));
const versionOpen = ref(false);
function openVersion() { mobileSidebarOpen.value = false; versionOpen.value = true; }

const DownloadsView = defineAsyncComponent(() => import("./views/DownloadsView.vue"));
const FavoritesView = defineAsyncComponent(() => import("./views/FavoritesView.vue"));
const IntegrationsView = defineAsyncComponent(() => import("./views/IntegrationsView.vue"));
const LogsView = defineAsyncComponent(() => import("./views/LogsView.vue"));
const SettingsView = defineAsyncComponent(() => import("./views/SettingsView.vue"));
const SubscriptionsView = defineAsyncComponent(() => import("./views/SubscriptionsView.vue"));

const activeTabMeta = computed(() => TABS.find((tab) => tab.key === activeTab.value) ?? TABS[0]);

const refreshing = computed(() => isBusy("refresh-view"));

const downloadClientOnline = computed(() => {
  if (!status.value) {
    return null;
  }
  return status.value.downloadClient === "qbittorrent" ? status.value.qbittorrentOnline : status.value.aria2Online;
});

const downloadClientLabel = computed(() =>
  status.value?.downloadClient === "qbittorrent" ? "qBittorrent" : "托管 aria2"
);

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

async function handleLoginSubmit(): Promise<void> {
  const succeeded = await login();
  if (succeeded) {
    await loadDashboard();
  }
}

// 侧栏只读取最近一次检查状态，不在后台自动访问 GitHub。
const UPDATE_BADGE_REFRESH_MS = 30 * 60 * 1000;
let updateBadgeTimer: number | null = null;

onMounted(() => {
  initTheme();
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
  <main
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
            <span class="status-dot" aria-hidden="true" />
            <span class="download-client-label">{{ downloadClientLabel }}</span>{{ downloadClientOnline ? "在线" : "离线" }}
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
            <button v-if="!isDesktopShell" type="button" class="topbar-btn topbar-logout" title="退出登录" @click="logout">
              <AppNavIcon name="logout" />
              <span class="topbar-logout-text">退出登录</span>
            </button>
          </div>
        </header>

        <div class="app-content h-full min-h-0 overflow-hidden">
          <section
            v-if="!authChecked"
            class="grid h-full place-items-center rounded-lg border border-[var(--line)] bg-[var(--table-bg)] p-8"
          >
            <div class="grid place-items-center gap-4 text-center">
              <CuteLoader mode="connecting" />
              <p class="text-sm font-semibold text-[var(--muted)]">连接本机服务中…</p>
            </div>
          </section>

          <section
            v-else-if="!hasConsoleAccess"
            class="grid h-full place-items-center overflow-auto rounded-lg border border-[var(--line)] bg-[var(--card)] p-8"
          >
            <form class="grid w-full max-w-2xl gap-5" @submit.prevent="handleLoginSubmit">
              <div>
                <h2 class="text-2xl font-semibold text-[var(--ink)]">登录 Bangumi Cat</h2>
                <p class="mt-2 text-lg text-[var(--muted)]">
                  本机和远程访问均需要登录。请填写 Go 服务启动日志中显示的访问口令。
                </p>
              </div>
              <div class="flex flex-wrap gap-3">
                <input
                  v-model="loginToken"
                  type="password"
                  class="input-base min-w-[280px] flex-1 px-4 py-4 text-lg"
                  placeholder="Bangumi Cat token"
                  autocomplete="current-password"
                  autocapitalize="off"
                  autocorrect="off"
                  spellcheck="false"
                />
                <button type="submit" class="btn-primary rounded-lg px-6 py-4 text-base font-semibold">
                  进入控制台
                </button>
              </div>
              <div v-if="githubOAuthAvailable" class="grid gap-2">
                <button
                  type="button"
                  class="btn-primary flex items-center justify-center gap-2 rounded-lg px-6 py-4 text-base font-semibold"
                  @click="startGithubLogin"
                >
                  使用 GitHub 登录
                </button>
                <p class="text-sm text-[var(--muted)]">只有白名单内的 GitHub 账号可以登录。</p>
              </div>
              <p class="rounded-lg border border-[var(--line)] bg-[var(--tint-2)] px-4 py-3 text-sm text-[var(--muted)]">
                {{ feedback }}
              </p>
            </form>
          </section>

          <template v-else>
            <HomeView v-if="activeTab === 'home'" />
            <FavoritesView v-else-if="activeTab === 'favorites'" />
            <SubscriptionsView v-else-if="activeTab === 'subscriptions'" />
            <DownloadsView v-else-if="activeTab === 'downloads'" />
            <LogsView v-else-if="activeTab === 'logs'" />
            <IntegrationsView v-else-if="activeTab === 'integrations'" />
            <SettingsView v-else-if="activeTab === 'settings'" />
          </template>
        </div>
      </section>
    </section>

    <Transition name="sync-overlay">
      <div v-if="globalSyncOverlay" class="fixed inset-0 z-40 grid place-items-center bg-black/20 px-5">
        <section class="grid w-[min(20rem,calc(100vw-2rem))] place-items-center gap-4 rounded-lg border border-[var(--line)] bg-[var(--card)] px-8 py-8 text-center shadow-[var(--strong-shadow)]">
          <CuteLoader mode="loading" />
          <div class="grid gap-1">
            <h2 class="text-lg font-semibold text-[var(--ink)]">{{ isLoading ? "正在加载控制台…" : "正在同步番剧数据…" }}</h2>
            <p class="text-sm font-semibold text-[var(--muted)]">从数据源同步中，请稍候</p>
          </div>
        </section>
      </div>
    </Transition>

    <VersionModal v-if="versionOpen" @close="versionOpen = false" />
    <AppToastStack :toasts="toasts" @dismiss="dismissToast" />
  </main>
</template>

<style scoped>
.app-shell { transition:grid-template-columns .2s ease; }
/* 顶栏尺寸、字号对齐 Diana 控制台（.app-topbar / .badge / .btn.ghost.small.icon-only） */
.app-topbar { position:relative; z-index:30; display:flex; align-items:center; gap:12px; height:56px; padding:0 24px; border-bottom:1px solid var(--line); background:color-mix(in srgb,var(--shell-bg) 82%,transparent); backdrop-filter:blur(10px); }
.topbar-title { min-width:0; overflow:hidden; color:var(--ink); font-size:16px; font-weight:650; text-overflow:ellipsis; white-space:nowrap; }
.topbar-badge { display:inline-flex; flex:none; align-items:center; gap:5px; padding:2px 9px; border-radius:999px; background:var(--panel); color:var(--ink-secondary); font-size:11.5px; font-weight:600; white-space:nowrap; }
.topbar-badge.ok { background:var(--ok-soft); color:var(--ok); }.topbar-badge.warn { background:var(--warn-soft); color:var(--warn); }.topbar-badge.err { background:var(--err-soft); color:var(--err); }
.status-dot { width:8px; height:8px; flex-shrink:0; border-radius:50%; background:currentColor; }
.topbar-actions { display:flex; flex:none; align-items:center; gap:6px; }
.topbar-btn { display:inline-flex; align-items:center; justify-content:center; gap:7px; min-width:34px; height:34px; padding:8px; border:1px solid transparent; border-radius:8px; background:transparent; color:var(--ink-secondary); font-size:12.5px; font-weight:550; white-space:nowrap; transition:background .15s, border-color .15s, color .15s; }
.topbar-btn:hover:not(:disabled) { background:var(--panel); color:var(--ink); }.topbar-btn:disabled { opacity:.55; cursor:not-allowed; }
.topbar-btn :deep(svg) { width:16px; height:16px; flex-shrink:0; }
.topbar-btn svg[width="15"] { width:15px; height:15px; }
.topbar-repo:hover:not(:disabled) { border-color:var(--accent); color:var(--accent); }
.topbar-logout { height:31px; padding:5px 10px; }.topbar-logout:hover:not(:disabled) { border-color:var(--err); color:var(--err); }
.topbar-menu { width:32px; min-width:32px; height:32px; padding:6px; }.topbar-menu :deep(svg) { width:18px; height:18px; }
.app-content { width:100%; max-width:1240px; margin:0 auto; padding:14px clamp(16px, 3vw, 32px) 20px; }
@media(max-width:960px) { .app-topbar { padding:0 14px; } }
@media(max-width:700px) { .topbar-logout-text { display:none; } .topbar-logout { width:34px; height:34px; padding:8px; } }
@media(max-width:640px) { .app-topbar { padding:0 12px; gap:8px; }.app-content { padding:12px 10px; }.topbar-repo, .download-client-label { display:none; } }

.sync-overlay-enter-active,
.sync-overlay-leave-active {
  transition: opacity 200ms ease;
}

.sync-overlay-enter-from,
.sync-overlay-leave-to {
  opacity: 0;
}
</style>
