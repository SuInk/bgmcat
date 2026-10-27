<script setup lang="ts">
import { computed, nextTick, provide, ref, watch } from "vue";
import type { Component } from "vue";

import type { AppSettings } from "@shared/index";

import AppNavIcon from "../components/layout/AppNavIcon.vue";
import { settingsPageVisibleKey, type SettingsPageKey } from "../components/settings/context";
import AboutPage from "../components/settings/pages/AboutPage.vue";
import DataPage from "../components/settings/pages/DataPage.vue";
import CloudPage from "../components/settings/pages/CloudPage.vue";
import DownloadPage from "../components/settings/pages/DownloadPage.vue";
import GeneralPage from "../components/settings/pages/GeneralPage.vue";
import GithubPage from "../components/settings/pages/GithubPage.vue";
import MetadataPage from "../components/settings/pages/MetadataPage.vue";
import RemotePage from "../components/settings/pages/RemotePage.vue";
import SourcesPage from "../components/settings/pages/SourcesPage.vue";
import TunnelPage from "../components/settings/pages/TunnelPage.vue";
import {
  changedSettingKeys,
  retrySettingsSave,
  saveSettingsFields,
  settingsForm,
  settingsRetryable,
  settingsSaveError,
  settingsSaveState,
  status
} from "../stores/session";

/*
 * 设置页外壳：左边分类导航 + 搜索，右边一次看一个分类，内容限宽。
 * 每个分类的表单在 components/settings/pages/ 里各管各的数据和接口；
 * 这里只负责导航、跨分类搜索和自动保存。
 */
interface SettingsPage {
  key: SettingsPageKey;
  label: string;
  desc: string;
  icon: string;
  component: Component;
  /** 只有一个小节：小节标题和页标题说的是一回事，不再重复显示 */
  single?: boolean;
}

const PAGE_GROUPS: Array<{ label: string; pages: SettingsPage[] }> = [
  {
    label: "应用",
    pages: [
      { key: "general", label: "常规", desc: "外观、刷新频率和 Go 服务。", icon: "settings", component: GeneralPage },
      { key: "data", label: "数据与备份", desc: "订阅、下载记录和设置都存在数据目录里，可以随时备份与恢复。", icon: "database", component: DataPage, single: true },
      { key: "about", label: "关于与更新", desc: "当前版本与自动更新策略。", icon: "info", component: AboutPage, single: true }
    ]
  },
  {
    label: "下载与资源",
    pages: [
      { key: "download", label: "下载", desc: "下载目录、下载器、Tracker 和在线播放。", icon: "downloads", component: DownloadPage },
      { key: "cloud", label: "云盘归档", desc: "下载完成后自动上传到 Google Drive，校验通过后可清理本地文件。", icon: "cloud-upload", component: CloudPage, single: true },
      { key: "sources", label: "资源站", desc: "种子从哪些站点找，以及蜜柑计划的访问方式。", icon: "subscriptions", component: SourcesPage },
      { key: "metadata", label: "番剧信息", desc: "番名、评分、简介和封面从哪儿来。", icon: "book", component: MetadataPage, single: true }
    ]
  },
  {
    label: "远程访问",
    pages: [
      { key: "remote", label: "访问与口令", desc: "从其他设备打开 WebUI 需要的地址、口令和网络设置。", icon: "globe", component: RemotePage },
      { key: "tunnel", label: "Cloudflare 隧道", desc: "不开端口、不配路由器，把 WebUI 安全地放到公网上。", icon: "cloud", component: TunnelPage, single: true },
      { key: "github", label: "GitHub 登录", desc: "从公网打开时，用 GitHub 账号登录代替粘贴 token。", icon: "key", component: GithubPage, single: true }
    ]
  }
];

const ALL_PAGES = PAGE_GROUPS.flatMap((group) => group.pages);

// —— 分类导航 ——
const SETTINGS_PAGE_KEY = "anime-rss.settings.page";

function readStoredPage(): SettingsPageKey {
  try {
    const stored = window.localStorage.getItem(SETTINGS_PAGE_KEY) as SettingsPageKey | null;
    return stored && ALL_PAGES.some((page) => page.key === stored) ? stored : "general";
  } catch {
    return "general";
  }
}

const activePage = ref<SettingsPageKey>(readStoredPage());
const activePageMeta = computed(() => ALL_PAGES.find((page) => page.key === activePage.value) ?? ALL_PAGES[0]!);
const settingsLayoutRef = ref<HTMLElement | null>(null);
const settingsShellRef = ref<HTMLElement | null>(null);

function openPage(key: SettingsPageKey): void {
  activePage.value = key;
  settingsQuery.value = "";
  settingsLayoutRef.value?.scrollTo({ top: 0 });
  try {
    window.localStorage.setItem(SETTINGS_PAGE_KEY, key);
  } catch {
    // 存不了就每次从「常规」开始，不影响使用
  }
}

// —— 搜索 ——
// 各分类页同时挂载、用 v-show 切换；搜索时全部放出来，按 DOM 文本过滤：
// 命中的行留下，整节都没命中的小节和分类藏起来，命中项在「高级」里就顺手展开。
const settingsQuery = ref("");
const searching = computed(() => Boolean(settingsQuery.value.trim()));
const pageMatchCount = ref<Partial<Record<SettingsPageKey, number>>>({});
const hasSearchHits = computed(() => Object.keys(pageMatchCount.value).length > 0);

function isPageVisible(key: SettingsPageKey): boolean {
  return searching.value || activePage.value === key;
}

provide(settingsPageVisibleKey, isPageVisible);

function applySettingsSearch(): void {
  const root = settingsShellRef.value;
  if (!root) {
    return;
  }
  const query = settingsQuery.value.trim().toLowerCase();
  const counts: Partial<Record<SettingsPageKey, number>> = {};

  root.querySelectorAll<HTMLElement>("[data-section]").forEach((section) => {
    const title = section.querySelector(".settings-section-title")?.textContent?.toLowerCase() ?? "";
    const titleHit = !query || title.includes(query);
    let itemHit = false;

    section.querySelectorAll<HTMLElement>(".settings-item").forEach((item) => {
      const hit = titleHit || (item.textContent ?? "").toLowerCase().includes(query);
      item.classList.toggle("settings-search-miss", !hit);
      if (hit && query && !titleHit) {
        item.closest("details")?.setAttribute("open", "");
      }
      itemHit ||= hit;
    });
    // 小节里除了标题以外的块：卡片 / 高级折叠区一行都没命中就整块藏；
    // 隧道检查、日志、状态格这类不是设置项的块，只有按小节标题命中时才留着
    for (const child of Array.from(section.children) as HTMLElement[]) {
      if (child.classList.contains("settings-section-head")) {
        continue;
      }
      const holdsItems = child.matches(".settings-group, details") || Boolean(child.querySelector(".settings-item"));
      const miss = holdsItems ? Boolean(query) && !child.querySelector(".settings-item:not(.settings-search-miss)") : !titleHit;
      child.classList.toggle("settings-search-miss", miss);
    }

    const sectionHit = titleHit || itemHit;
    section.classList.toggle("settings-search-miss", !sectionHit);
    const page = section.closest<HTMLElement>("[data-page]")?.dataset.page as SettingsPageKey | undefined;
    if (sectionHit && query && page) {
      counts[page] = (counts[page] ?? 0) + 1;
    }
  });

  root.querySelectorAll<HTMLElement>("[data-page]").forEach((page) => {
    page.classList.toggle("settings-search-miss", !page.querySelector("[data-section]:not(.settings-search-miss)"));
  });

  pageMatchCount.value = counts;
}

watch(settingsQuery, async () => {
  // 等 v-show 把其他分类放出来再过滤
  await nextTick();
  applySettingsSearch();
  settingsLayoutRef.value?.scrollTo({ top: 0 });
});

// 这几个开关会让小节或行出现/消失，搜索中的话重新过滤一遍
watch(
  () => [settingsForm.downloadClient, settingsForm.githubOAuthEnabled, settingsForm.tunnelMode, settingsForm.tunnelCredentialMode],
  () => {
    if (searching.value) {
      void nextTick(applySettingsSearch);
    }
  }
);

/* —— 自动保存 ——
   页面底部没有「保存修改」栏。两类控件提交时机不同：

   1. 开关 / 分段 / 预设 / 下拉：一次点击就是一个完整意图，改完立刻提交。
      下面这个 watcher 盯着 IMMEDIATE_KEYS，和已存快照做差后只提交变了的字段。
   2. 文本框 / 数字框：失焦或回车才提交。用户把端口从 3002 删到空再打新值，
      那个空字符串的中间态一旦落库就是事故。走表单上的 focusout / Enter 委托。 */

const IMMEDIATE_KEYS = [
  // themeAccent 不在这里：取色器拖动时每一帧都会写它，得由外观页自己决定何时提交
  "exposeMcpPublicly",
  "githubOAuthEnabled",
  "minimizeToTray",
  "launchAtLogin",
  "downloadClient",
  "aria2Enabled",
  "aria2SeedEnabled",
  "cloudArchiveEnabled",
  "cloudDeleteLocal",
  "qbittorrentEnabled",
  "mikanDirectAccessMode",
  "mikanDirectAccessEnabled",
  "homepageSortMode",
  "tunnelMode",
  "tunnelCredentialMode",
  "tunnelAutoRestart",
  "enabledResourceProviders",
  "resourceProviderOrder"
] as const satisfies ReadonlyArray<keyof AppSettings>;

/** 其余全是自由文本/数字，只在失焦或回车时提交。 */
const DEFERRED_KEYS = (Object.keys(settingsForm) as Array<keyof AppSettings>).filter(
  (key) => !(IMMEDIATE_KEYS as ReadonlyArray<string>).includes(key)
);

watch(
  () => IMMEDIATE_KEYS.map((key) => settingsForm[key]),
  () => {
    const changed = changedSettingKeys(IMMEDIATE_KEYS);
    if (changed.length) {
      void saveSettingsFields(changed);
    }
  },
  { deep: true }
);

/** 失焦 / 回车：把文本类字段里真正改过的提交掉。 */
function commitTextFields(): void {
  const changed = changedSettingKeys(DEFERRED_KEYS);
  if (changed.length) {
    void saveSettingsFields(changed);
  }
}

const SAVE_STATE_LABEL: Record<string, string> = {
  saving: "保存中…",
  saved: "已保存 ✓",
  error: "保存失败"
};
</script>

<template>
  <div ref="settingsLayoutRef" class="settings-app">
    <aside class="settings-sidebar">
      <label class="settings-sidebar-search">
        <AppNavIcon name="search" aria-hidden="true" />
        <input v-model="settingsQuery" type="search" placeholder="搜索设置…" aria-label="搜索设置" />
      </label>

      <nav class="settings-nav" aria-label="设置分类">
        <div v-for="group in PAGE_GROUPS" :key="group.label" class="settings-nav-group">
          <span class="settings-nav-group-label">{{ group.label }}</span>
          <button
            v-for="page in group.pages"
            :key="page.key"
            type="button"
            class="settings-nav-link"
            :class="{
              'settings-nav-link-active': !searching && activePage === page.key,
              'settings-nav-link-dim': searching && !pageMatchCount[page.key]
            }"
            :aria-current="!searching && activePage === page.key ? 'page' : undefined"
            @click="openPage(page.key)"
          >
            <AppNavIcon :name="page.icon" aria-hidden="true" />
            <span class="min-w-0 flex-1 truncate">{{ page.label }}</span>
            <span v-if="searching && pageMatchCount[page.key]" class="settings-nav-count">{{ pageMatchCount[page.key] }}</span>
          </button>
        </div>
      </nav>
    </aside>

    <section ref="settingsShellRef" class="settings-shell">
      <div class="settings-content">
        <header class="settings-page-head">
          <div class="min-w-0">
            <h2 class="settings-page-title">{{ searching ? `搜索「${settingsQuery.trim()}」` : activePageMeta.label }}</h2>
            <p class="settings-page-desc">
              {{ searching ? (hasSearchHits ? "下面是所有分类里匹配的设置项。" : "没有匹配的设置项，换个关键词试试。") : activePageMeta.desc }}
            </p>
          </div>
          <!--
            自动保存的代价是「存没存上」变得不可见，所以这条指示器是必需品：
            失败时不能只弹个 toast 就完事，得留在原地、能点重试。
          -->
          <span
            v-if="settingsSaveState !== 'idle'"
            class="status-pill shrink-0"
            :class="settingsSaveState === 'error' ? 'status-pill-err' : settingsSaveState === 'saved' ? 'status-pill-ok' : 'status-pill-muted'"
            role="status"
            aria-live="polite"
            :title="settingsSaveState === 'error' ? settingsSaveError ?? undefined : undefined"
          >
            {{ SAVE_STATE_LABEL[settingsSaveState] }}
            <button v-if="settingsRetryable" type="button" class="ml-1 underline" @click="retrySettingsSave">重试</button>
          </span>
          <span v-else class="shrink-0 pt-1.5 text-xs text-[var(--muted)]">改动自动保存</span>
        </header>

        <form
          class="settings-panel"
          :class="{ 'settings-single-section': !searching && activePageMeta.single }"
          @submit.prevent="commitTextFields"
          @focusout="commitTextFields"
          @keydown.enter="commitTextFields"
        >
          <div v-for="page in ALL_PAGES" v-show="isPageVisible(page.key)" :key="page.key" class="settings-list" :data-page="page.key">
            <h2 v-show="searching" class="settings-category-title">{{ page.label }}</h2>
            <component :is="page.component" />
          </div>

          <div v-if="status?.restartRequired" class="settings-restart-note">
            监听地址或端口已修改，重启应用后新配置才会生效。当前仍监听 {{ status.activeHost }}:{{ status.activePort }}。
          </div>
        </form>
      </div>
    </section>
  </div>
</template>

<style src="../styles/settings.css"></style>
