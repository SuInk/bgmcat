<script setup lang="ts">
import { computed, defineAsyncComponent, nextTick, onMounted, onUnmounted, ref, watch } from "vue";

import AppToastStack from "./components/common/AppToastStack.vue";
import CuteLoader from "./components/common/CuteLoader.vue";
import AppSidebar from "./components/layout/AppSidebar.vue";
import AppNavIcon from "./components/layout/AppNavIcon.vue";
import HomeView from "./views/HomeView.vue";
import { loadDashboard, bootstrap, globalSyncOverlay, isLoading, refreshCurrentView, selectTab, teardown } from "./stores/boot";
import { isBusy } from "./stores/busy";
import { onlineDownloadCount, unreadCompletedDownloadCount } from "./stores/downloads";
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
function closeDrawer(event: KeyboardEvent) { if (event.key === "Escape") mobileSidebarOpen.value = false; }

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
  window.addEventListener("keydown", closeDrawer);
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
  window.removeEventListener("keydown", closeDrawer);
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
            class="icon-btn size-9 shrink-0"
            :aria-expanded="isMobileViewport ? mobileSidebarOpen : !sidebarCollapsed"
            aria-controls="app-sidebar"
            aria-label="打开侧栏"
            @click="handleSidebarToggle"
          >
            <AppNavIcon name="panel-left" />
          </button>

          <div class="min-w-0 flex-1">
            <h1 class="topbar-title">{{ activeTabMeta.label }}</h1>

          </div>

          <div class="flex shrink-0 items-center gap-2">
            <span
              v-if="status?.restartRequired"
              class="hidden items-center gap-1.5 rounded-full border border-[var(--warn-soft)] bg-[var(--warn-soft)] px-3 py-1.5 text-xs font-semibold text-[var(--warn)] sm:inline-flex dark:text-[var(--warn)]"
              title="监听地址或端口已修改，重启应用后生效"
            >
              需重启生效
            </span>
            <span v-if="downloadClientOnline !== null" class="download-status" :class="downloadClientOnline ? 'online' : 'offline'" :title="`${downloadClientLabel} ${downloadClientOnline ? '在线' : '离线，请检查设置'}`">
              <span class="connection-dot" aria-hidden="true" />
              <span class="download-client-label">{{ downloadClientLabel }}</span>{{ downloadClientOnline ? '在线' : '离线' }}
            </span>

            <button type="button" class="icon-btn topbar-repo" title="GitHub 源码仓库" aria-label="GitHub 源码仓库" @click="openExternalLink('https://github.com/SuInk/bgmcat')"><AppNavIcon name="external" /></button>
            <button type="button" class="icon-btn" title="刷新数据" :disabled="refreshing" @click="refreshCurrentView">
              <AppNavIcon name="refresh" :class="{ 'animate-spin': refreshing }" />
            </button>
            <button v-if="!isDesktopShell" type="button" class="icon-btn" title="退出登录" @click="logout">
              <AppNavIcon name="logout" />
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
.app-topbar { position:relative; z-index:30; display:flex; align-items:center; gap:12px; height:56px; padding:0 24px; border-bottom:1px solid var(--line); background:color-mix(in srgb,var(--shell-bg) 82%,transparent); backdrop-filter:blur(10px); }
.download-status { display:inline-flex; align-items:center; gap:6px; padding:4px 8px; border-radius:8px; font-size:11px; white-space:nowrap; }.download-status.online { color:var(--ok); background:var(--ok-soft); }.download-status.offline { color:var(--err); background:var(--err-soft); }.connection-dot { width:6px; height:6px; border-radius:50%; background:currentColor; }
.topbar-title { font-size:16px; font-weight:650; color:var(--ink); white-space:nowrap; overflow:hidden; text-overflow:ellipsis; }
.app-topbar .icon-btn { border:none; background:transparent; box-shadow:none; width:32px; height:32px; border-radius:8px; }
.app-topbar .icon-btn:hover { background:var(--panel); }
.app-content { padding:18px clamp(16px, 2vw, 32px) 20px; }
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
