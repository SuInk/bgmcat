<script setup lang="ts">
import { computed, defineAsyncComponent, onMounted, onUnmounted, ref } from "vue";

import AppToastStack from "./components/common/AppToastStack.vue";
import CuteLoader from "./components/common/CuteLoader.vue";
import AppSidebar from "./components/layout/AppSidebar.vue";
import AppNavIcon from "./components/layout/AppNavIcon.vue";
import HomeView from "./views/HomeView.vue";
import { loadDashboard, bootstrap, globalSyncOverlay, isLoading, refreshCurrentView, selectTab, teardown } from "./stores/boot";
import { isBusy } from "./stores/busy";
import { onlineDownloadCount, unreadCompletedDownloadCount } from "./stores/downloads";
import { refreshUpdateStatus, updateAvailable } from "./stores/update";
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
import { activeThemeLabel, initTheme, setThemeMode, teardownTheme, themeMode } from "./stores/theme";
import { dismissToast, feedback, toasts } from "./stores/toasts";
import {
  handleWindowResize,
  isMobileViewport,
  mobileSidebarOpen,
  sidebarCollapsed
} from "./stores/ui";

// 首页以外的视图按需加载,缩小首包(下载页拖着播放器相关依赖尤其值得拆)
const DownloadsView = defineAsyncComponent(() => import("./views/DownloadsView.vue"));
const FavoritesView = defineAsyncComponent(() => import("./views/FavoritesView.vue"));
const IntegrationsView = defineAsyncComponent(() => import("./views/IntegrationsView.vue"));
const LogsView = defineAsyncComponent(() => import("./views/LogsView.vue"));
const SettingsView = defineAsyncComponent(() => import("./views/SettingsView.vue"));
const SubscriptionsView = defineAsyncComponent(() => import("./views/SubscriptionsView.vue"));

/** 设置页和 ChatGPT 桌面端一样接管整个窗口：主侧栏和顶栏让位给设置自己的侧栏 */
const settingsTakeover = computed(() => hasConsoleAccess.value && activeTab.value === "settings");

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

const THEME_OPTIONS = [
  { key: "system", label: "自动", icon: "theme" },
  { key: "light", label: "浅色", icon: "sun" },
  { key: "dark", label: "深色", icon: "moon" }
] as const;

const themeMenuOpen = ref(false);
const themeMenuRef = ref<HTMLElement | null>(null);

const themeIconName = computed(
  () => THEME_OPTIONS.find((option) => option.key === themeMode.value)?.icon ?? "theme"
);

function pickTheme(mode: (typeof THEME_OPTIONS)[number]["key"]): void {
  setThemeMode(mode);
  themeMenuOpen.value = false;
}

function handleDocumentPointerDown(event: MouseEvent): void {
  const target = event.target as Node | null;
  if (target && themeMenuRef.value?.contains(target)) {
    return;
  }
  themeMenuOpen.value = false;
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
  }
}

async function handleLoginSubmit(): Promise<void> {
  const succeeded = await login();
  if (succeeded) {
    await loadDashboard();
  }
}

// 侧栏小黄点只读服务端已经算好的状态，不触发一次网络检查——真正的检查由服务端
// 的后台调度做（启动 5 分钟后首检，之后每 6 小时）。半小时刷一次足够让点亮起来。
const UPDATE_BADGE_REFRESH_MS = 30 * 60 * 1000;
let updateBadgeTimer: number | null = null;

onMounted(() => {
  initTheme();
  window.addEventListener("resize", handleWindowResize);
  window.addEventListener("mousedown", handleDocumentPointerDown);
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
  window.removeEventListener("mousedown", handleDocumentPointerDown);
});
</script>

<template>
  <main
    class="grid h-[100dvh] min-h-screen overflow-hidden bg-[var(--shell-bg)]"
    :style="{ gridTemplateColumns: isMobileViewport || settingsTakeover ? 'minmax(0,1fr)' : sidebarCollapsed ? '88px minmax(0,1fr)' : '236px minmax(0,1fr)' }"
  >
    <AppSidebar
      v-if="!isMobileViewport && !settingsTakeover"
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
    />

    <div
      v-if="isMobileViewport"
      class="fixed inset-0 z-40 bg-black/45 transition-opacity duration-300"
      :class="mobileSidebarOpen ? 'pointer-events-auto opacity-100' : 'pointer-events-none opacity-0'"
      @click="mobileSidebarOpen = false"
    ></div>

    <AppSidebar
      v-if="isMobileViewport && !settingsTakeover"
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
    />

    <SettingsView v-if="settingsTakeover" />

    <section v-else class="h-full min-h-0 overflow-hidden">
      <section class="relative grid h-full min-h-0 grid-rows-[auto_minmax(0,1fr)] gap-2 overflow-hidden p-2 sm:p-3">
        <header
          v-if="hasConsoleAccess"
          class="flex items-center gap-3 rounded-lg border border-[var(--line)] bg-[var(--topbar-bg)] px-4 py-2.5 shadow-[var(--soft-shadow)] sm:px-5"
        >
          <button
            v-if="isMobileViewport"
            type="button"
            class="icon-btn size-9 shrink-0"
            aria-label="打开侧栏"
            @click="mobileSidebarOpen = true"
          >
            <AppNavIcon name="menu" />
          </button>

          <div class="min-w-0 flex-1">
            <h1 class="truncate text-lg font-semibold text-[var(--ink)] sm:text-xl">{{ activeTabMeta.label }}</h1>
            <p v-if="!isMobileViewport" class="mt-0.5 truncate text-xs font-semibold text-[var(--muted)]">
              {{ activeTabMeta.subtitle }}
            </p>
          </div>

          <div class="flex shrink-0 items-center gap-2">
            <span
              v-if="status?.restartRequired"
              class="hidden items-center gap-1.5 rounded-full border border-[var(--warn-soft)] bg-[var(--warn-soft)] px-3 py-1.5 text-xs font-semibold text-[var(--warn)] sm:inline-flex dark:text-[var(--warn)]"
              title="监听地址或端口已修改，重启应用后生效"
            >
              需重启生效
            </span>
            <span
              v-if="downloadClientOnline === false"
              class="inline-flex items-center gap-1.5 rounded-full border border-[var(--err-soft)] bg-[var(--err-soft)] px-3 py-1.5 text-xs font-medium text-[var(--err)]"
              :title="`${downloadClientLabel} 离线，请检查设置`"
            >
              <span class="size-1.5 animate-pulse rounded-full bg-[var(--err)]"></span>
              {{ downloadClientLabel }} 离线
            </span>

            <div ref="themeMenuRef" class="relative">
              <button
                type="button"
                class="icon-btn"
                :class="{ 'border-[var(--accent)] text-[var(--ink)]': themeMenuOpen }"
                :title="`界面主题：${activeThemeLabel}`"
                aria-haspopup="menu"
                :aria-expanded="themeMenuOpen"
                @click="themeMenuOpen = !themeMenuOpen"
              >
                <AppNavIcon :name="themeIconName" />
              </button>
              <div
                v-if="themeMenuOpen"
                class="absolute right-0 top-[calc(100%+0.375rem)] z-40 w-32 rounded-md border border-[var(--line)] bg-[var(--card)] p-1 shadow-[var(--strong-shadow)]"
                role="menu"
              >
                <button
                  v-for="option in THEME_OPTIONS"
                  :key="option.key"
                  type="button"
                  role="menuitemradio"
                  :aria-checked="themeMode === option.key"
                  class="flex w-full items-center gap-2 rounded px-2.5 py-1.5 text-left text-sm transition"
                  :class="themeMode === option.key ? 'bg-[var(--panel)] font-medium text-[var(--ink)]' : 'text-[var(--muted)] hover:bg-[var(--panel)] hover:text-[var(--ink)]'"
                  @click="pickTheme(option.key)"
                >
                  <AppNavIcon :name="option.icon" class="size-4 shrink-0" />
                  <span class="flex-1">{{ option.label }}</span>
                  <span v-if="themeMode === option.key" class="text-[var(--accent)]">✓</span>
                </button>
              </div>
            </div>
            <button type="button" class="icon-btn" title="刷新数据" :disabled="refreshing" @click="refreshCurrentView">
              <AppNavIcon name="refresh" :class="{ 'animate-spin': refreshing }" />
            </button>
            <button v-if="!isDesktopShell" type="button" class="icon-btn" title="退出登录" @click="logout">
              <AppNavIcon name="logout" />
            </button>
          </div>
        </header>

        <div class="h-full min-h-0 overflow-hidden">
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

    <AppToastStack :toasts="toasts" @dismiss="dismissToast" />
  </main>
</template>

<style scoped>
.sync-overlay-enter-active,
.sync-overlay-leave-active {
  transition: opacity 200ms ease;
}

.sync-overlay-enter-from,
.sync-overlay-leave-to {
  opacity: 0;
}
</style>
