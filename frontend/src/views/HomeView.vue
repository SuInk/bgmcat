<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref, watch } from "vue";

import type { HomepageAnime } from "@shared/index";

import PagerBar from "../components/common/PagerBar.vue";
import AnimeCard from "../components/discovery/AnimeCard.vue";
import AnimeDetailDrawer from "../components/discovery/AnimeDetailDrawer.vue";
import AnimeGrid from "../components/discovery/AnimeGrid.vue";
import AppNavIcon from "../components/layout/AppNavIcon.vue";
import { extractEpisodeNumberFromTitle } from "@shared/episode";
import { resolveDownloadArtworkUrl } from "../api";
import { broadcastWeekday } from "../lib/format";
import { continueWatchingEntries, type ContinueWatchingEntry } from "../lib/playbackHistory";
import { selectTab } from "../stores/boot";
import { detailSearchKeyword, displayTitle, downloads, openPlayback } from "../stores/downloads";
import { isBusy } from "../stores/busy";
import {
  ANIME_PAGE_SIZE_OPTIONS,
  activeDiscoverySearch,
  animePage,
  animePageSize,
  animePaginationItems,
  animeSortMode,
  animeTotalPages,
  clearDiscoverySearchHistory,
  discoveryForm,
  discoverySearchHistory,
  displayedHomepageAnime,
  homepageAnime,
  homepageError,
  isHistoricalSearchActive,
  openAnimeDetail,
  pagedHomepageAnime,
  refreshHomepage,
  removeDiscoverySearchHistory,
  runDiscoverySearch,
  searchPlatformFailures,
  selectedAnimeId,
  setAnimePage,
  useDiscoverySearchHistory,
  WEEKDAY_TABS
} from "../stores/discovery";
import { saveSettingsFields, settings, settingsForm } from "../stores/session";
import { isMobileViewport } from "../stores/ui";

/** 海报网格 / 紧凑列表。只是本机浏览偏好，存 localStorage；读写失败就用默认的海报 */
type HomeLayout = "poster" | "row";
const LAYOUT_STORAGE_KEY = "bgmcat.homeLayout";
function readHomeLayout(): HomeLayout {
  try {
    return localStorage.getItem(LAYOUT_STORAGE_KEY) === "row" ? "row" : "poster";
  } catch {
    return "poster";
  }
}
const homeLayout = ref<HomeLayout>(readHomeLayout());
watch(homeLayout, (value) => {
  try {
    localStorage.setItem(LAYOUT_STORAGE_KEY, value);
  } catch {
    // 隐私模式等场景写不进去，本次会话内照常切换
  }
});

const scrollRef = ref<HTMLElement | null>(null);
const toolbarRef = ref<HTMLElement | null>(null);
const gridSectionRef = ref<HTMLElement | null>(null);
const searchBoxRef = ref<HTMLElement | null>(null);
const weekdayBarRef = ref<HTMLElement | null>(null);
const searchHistoryVisible = ref(false);

const todayWeekdayKey = String(new Date().getDay());
const todayLabel = WEEKDAY_TABS.find((tab) => tab.key === todayWeekdayKey)?.label ?? "";

function weekdayOf(anime: HomepageAnime): string {
  return String(broadcastWeekday(anime.broadcastAt) ?? anime.airDay);
}

function minuteOfDay(anime: HomepageAnime): number {
  const date = anime.broadcastAt ? new Date(anime.broadcastAt) : null;
  return date && !Number.isNaN(date.getTime()) ? date.getHours() * 60 + date.getMinutes() : Number.MAX_SAFE_INTEGER;
}

/**
 * 「今日更新」货架：只在看「全部」且没在搜索时出现，按首播时刻排。
 * 选了某一天时下面的网格本身就是那一天，再放货架就重复了；紧凑列表要的是密度，也不放。
 */
const todayShelf = computed(() => {
  if (homeLayout.value === "row" || discoveryForm.weekday !== "all" || isHistoricalSearchActive.value || discoveryForm.search.trim()) {
    return [];
  }
  return homepageAnime.value
    .filter((anime) => weekdayOf(anime) === todayWeekdayKey)
    .sort((left, right) => minuteOfDay(left) - minuteOfDay(right));
});

const gridTitle = computed(() => {
  if (isHistoricalSearchActive.value) {
    return `搜索「${activeDiscoverySearch.value}」`;
  }
  if (discoveryForm.weekday === "all") {
    return "本季全部";
  }
  const label = WEEKDAY_TABS.find((tab) => tab.key === discoveryForm.weekday)?.label ?? "";
  return discoveryForm.weekday === todayWeekdayKey ? `${label} · 今天` : `${label}更新`;
});

const showSearchButton = computed(() => Boolean(discoveryForm.search.trim()) || isHistoricalSearchActive.value);

/** 翻页后回到网格开头；已经在开头之上就不动，免得把「今日更新」顶出去 */
function scrollGridIntoView(): void {
  const scroller = scrollRef.value;
  const section = gridSectionRef.value;
  if (!scroller || !section) {
    return;
  }
  const target = Math.max(0, section.offsetTop - (toolbarRef.value?.offsetHeight ?? 0) - 8);
  if (scroller.scrollTop > target) {
    scroller.scrollTo({ top: target, behavior: "smooth" });
  }
}

watch(animePage, () => nextTick(scrollGridIntoView));
watch(() => discoveryForm.weekday, () => scrollRef.value?.scrollTo({ top: 0 }));

/**
 * 哪些信息平台这一轮没响应。结果照常显示，只在列表上方浅色说明一句——
 * 用户至少知道「少的那几条是因为某个平台没通」，而不是以为搜不到。
 */
const platformFailureNotice = computed(() => {
  const failures = searchPlatformFailures.value;
  if (!failures.length) {
    return "";
  }
  return `${failures.map((failure) => failure.displayName).join("、")} 这一轮没响应，下面是其余来源的结果。`;
});

const homepageLoading = computed(() => isBusy("discovery-home") && displayedHomepageAnime.value.length === 0);
const searching = computed(() => isBusy("discovery-search"));

function runSearch(): void {
  searchHistoryVisible.value = false;
  void runDiscoverySearch();
}

function useHistory(keyword: string): void {
  searchHistoryVisible.value = false;
  useDiscoverySearchHistory(keyword);
}

async function setSortMode(mode: "default" | "rating"): Promise<void> {
  if (animeSortMode.value === mode) {
    return;
  }
  animeSortMode.value = mode;
  animePage.value = 1;
  // 服务端持久化排序偏好；失败不阻塞本地切换
  if (settings.value && settings.value.homepageSortMode !== mode) {
    settingsForm.homepageSortMode = mode;
    await saveSettingsFields(["homepageSortMode"]);
  }
}

function handleDocumentPointerDown(event: MouseEvent): void {
  const target = event.target as Node | null;
  if (target && searchBoxRef.value?.contains(target)) {
    return;
  }
  searchHistoryVisible.value = false;
}

watch(
  () => settings.value?.homepageSortMode,
  (mode) => {
    if (mode) {
      animeSortMode.value = mode;
    }
  },
  { immediate: true }
);

/** 星期条改成单行滚动后，选中项可能落在可视区外，挂载与切换时都拉回来 */
function scrollActiveWeekdayIntoView(): void {
  const bar = weekdayBarRef.value;
  const active = bar?.querySelector<HTMLElement>(`[data-weekday="${discoveryForm.weekday}"]`);
  active?.scrollIntoView({ block: "nearest", inline: "nearest" });
}

watch(() => discoveryForm.weekday, () => nextTick(scrollActiveWeekdayIntoView));

/**
 * 「继续观看」（Netflix / B 站那种）：播放器在本机记着每个文件看到哪，
 * 这里把看了一半的挑出来摆在首页最上面，点一下直接从上次的位置接着放。
 * 进度存在 localStorage、不是响应式的：回到首页、窗口重新获得焦点时重读一次。
 */
const historyVersion = ref(0);
function refreshContinueWatching(): void {
  historyVersion.value += 1;
}

const continueWatching = computed<ContinueWatchingEntry[]>(() => {
  void historyVersion.value;
  if (isHistoricalSearchActive.value || discoveryForm.search.trim()) {
    return [];
  }
  return continueWatchingEntries(downloads.value).slice(0, 12);
});

/** 卡片上放番名（从发布名里拆出来的那段），拆不出来才用整条发布名 */
function continueTitle(entry: ContinueWatchingEntry): string {
  const keyword = detailSearchKeyword(entry.download);
  // 「[FX战士久留美 / FX Senshi Kurumi-chan」→「FX战士久留美」：去掉括号，多语言别名只留第一个
  const name = keyword.replace(/[[\]【】]/gu, " ").split(/\s+\/\s+/u)[0]?.trim();
  return name || displayTitle(entry.download);
}

function continueEpisodeLabel(entry: ContinueWatchingEntry): string {
  const episode = extractEpisodeNumberFromTitle(entry.download.feedItemTitle ?? "");
  return episode ? `第 ${episode} 集` : "";
}

function continueRemainingLabel(entry: ContinueWatchingEntry): string {
  const minutes = Math.max(1, Math.round((entry.record.duration - entry.record.currentTime) / 60));
  return `还剩 ${minutes} 分钟`;
}

function resumeEntry(entry: ContinueWatchingEntry): void {
  selectTab("downloads");
  void openPlayback(entry.download);
}

onMounted(() => {
  window.addEventListener("mousedown", handleDocumentPointerDown);
  window.addEventListener("focus", refreshContinueWatching);
  void nextTick(scrollActiveWeekdayIntoView);
});

onUnmounted(() => {
  window.removeEventListener("mousedown", handleDocumentPointerDown);
  window.removeEventListener("focus", refreshContinueWatching);
});
</script>

<template>
  <AnimeDetailDrawer plain>
    <template #main>
      <!--
        Apple TV 式布局：整页一起滚，不再套一层面板；工具栏吸顶并磨砂，
        下面依次是「今日更新」横向货架和本季网格，分页跟在网格后面。
      -->
      <div ref="scrollRef" class="home-scroll h-full min-h-0 overflow-y-auto overflow-x-hidden">
        <div ref="toolbarRef" class="home-toolbar">
          <!-- 星期：苹果式分段控件，窄屏横向滚动 -->
          <div
            ref="weekdayBarRef"
            class="seg-track weekday-scroller min-w-0 overflow-x-auto [scrollbar-width:none] [&::-webkit-scrollbar]:hidden"
            role="group"
            aria-label="按星期筛选"
          >
            <button
              v-for="weekday in WEEKDAY_TABS"
              :key="weekday.key"
              type="button"
              :data-weekday="weekday.key"
              class="seg-item"
              :class="{ 'is-active': weekday.key === discoveryForm.weekday, 'is-today': weekday.key === todayWeekdayKey }"
              :aria-pressed="weekday.key === discoveryForm.weekday"
              :aria-label="weekday.key === todayWeekdayKey ? `${weekday.label}（今日）` : weekday.label"
              @click="discoveryForm.weekday = weekday.key"
            >
              {{ weekday.key === todayWeekdayKey ? "今天" : weekday.label }}
            </button>
          </div>

          <span class="hidden flex-1 lg:block" />

          <!-- 手机宽度下搜索框独占一行，否则被排序、视图按钮挤成只剩一个「搜」字 -->
          <!--
            搜索 + 排序 + 视图这一组至少要 24rem：放不下就整组换到第二行，
            而不是把搜索框挤成一个圆圈（星期条吃掉大半行时会这样）。
          -->
          <div class="flex min-w-0 flex-1 basis-[24rem] flex-wrap items-center gap-2 sm:flex-nowrap lg:flex-none lg:basis-auto">
            <div ref="searchBoxRef" class="relative flex min-w-[9rem] flex-1 basis-full items-center gap-2 sm:basis-auto lg:w-[18rem] lg:flex-none">
              <label class="home-search">
                <AppNavIcon name="search" class="size-[15px] shrink-0" aria-hidden="true" />
                <input
                  v-model="discoveryForm.search"
                  class="min-w-0 flex-1 bg-transparent text-[13px] text-[var(--ink)] outline-none placeholder:text-[var(--muted)]"
                  aria-label="搜索番剧，回车搜索 Bangumi 全库"
                  :placeholder="isMobileViewport ? '搜索番剧' : '搜索番剧，回车搜全库'"
                  @focus="searchHistoryVisible = true"
                  @click="searchHistoryVisible = true"
                  @keyup.enter="runSearch"
                />
                <kbd v-if="!discoveryForm.search" class="search-kbd" title="按 / 聚焦搜索">/</kbd>
              </label>
              <button
                v-if="showSearchButton"
                type="button"
                class="btn-primary shrink-0 rounded-[9px] px-3 py-1 text-[13px] font-semibold"
                @click="runSearch"
              >
                {{ searching ? "搜索中…" : !discoveryForm.search.trim() && isHistoricalSearchActive ? "回到当季" : "搜索" }}
              </button>

              <div
                v-if="searchHistoryVisible && discoverySearchHistory.length"
                class="absolute left-0 right-0 top-[calc(100%+0.5rem)] z-30 grid gap-3 rounded-xl border border-[var(--line)] bg-[var(--card)] p-3 shadow-[var(--strong-shadow)]"
              >
                <div class="flex items-center justify-between gap-3">
                  <span class="text-xs font-medium text-[var(--muted)]">最近搜索</span>
                  <button
                    type="button"
                    class="text-xs font-semibold text-[var(--muted)] transition hover:text-[var(--accent-vivid)]"
                    @click="clearDiscoverySearchHistory"
                  >
                    清空
                  </button>
                </div>
                <div class="flex max-h-40 flex-wrap gap-2 overflow-auto">
                  <div v-for="keyword in discoverySearchHistory" :key="keyword" class="inline-flex items-center gap-1">
                    <button type="button" class="chip rounded-md px-3 py-1.5 text-[13px]" @click="useHistory(keyword)">
                      {{ keyword }}
                    </button>
                    <button
                      type="button"
                      class="chip rounded-md px-2 py-1 text-xs text-[var(--muted)]"
                      :aria-label="`删除搜索记录 ${keyword}`"
                      @click="removeDiscoverySearchHistory(keyword)"
                    >
                      ×
                    </button>
                  </div>
                </div>
              </div>
            </div>

            <div class="seg-track shrink-0" role="group" aria-label="排序方式">
              <button
                type="button"
                class="seg-item"
                :class="{ 'is-active': animeSortMode === 'default' }"
                :aria-pressed="animeSortMode === 'default'"
                @click="setSortMode('default')"
              >
                默认
              </button>
              <button
                type="button"
                class="seg-item"
                :class="{ 'is-active': animeSortMode === 'rating' }"
                :aria-pressed="animeSortMode === 'rating'"
                @click="setSortMode('rating')"
              >
                评分
              </button>
            </div>

            <div class="seg-track shrink-0" role="group" aria-label="显示方式">
              <button
                type="button"
                class="seg-item seg-icon"
                :class="{ 'is-active': homeLayout === 'poster' }"
                :aria-pressed="homeLayout === 'poster'"
                aria-label="海报网格"
                title="海报网格"
                @click="homeLayout = 'poster'"
              >
                <AppNavIcon name="grid" class="size-4" aria-hidden="true" />
              </button>
              <button
                type="button"
                class="seg-item seg-icon"
                :class="{ 'is-active': homeLayout === 'row' }"
                :aria-pressed="homeLayout === 'row'"
                aria-label="紧凑列表"
                title="紧凑列表"
                @click="homeLayout = 'row'"
              >
                <AppNavIcon name="list" class="size-4" aria-hidden="true" />
              </button>
            </div>

            <button
              type="button"
              class="home-icon-btn shrink-0"
              :disabled="isBusy('discovery-home')"
              :aria-label="isBusy('discovery-home') ? '正在同步当季番剧' : '同步当季番剧'"
              :title="isBusy('discovery-home') ? '同步中…' : '同步当季番剧'"
              @click="refreshHomepage()"
            >
              <AppNavIcon name="refresh" class="size-4" :class="{ 'animate-spin': isBusy('discovery-home') }" aria-hidden="true" />
            </button>
          </div>
        </div>

        <section v-if="continueWatching.length" class="home-section" aria-labelledby="home-continue-title">
          <div class="section-head">
            <h2 id="home-continue-title" class="section-title">继续观看</h2>
            <span class="section-sub">{{ continueWatching.length }} 部</span>
          </div>
          <div class="shelf-row">
            <button
              v-for="entry in continueWatching"
              :key="entry.download.gid"
              type="button"
              class="continue-card"
              :aria-label="`继续观看 ${continueTitle(entry)} ${continueEpisodeLabel(entry)}，${continueRemainingLabel(entry)}`"
              @click="resumeEntry(entry)"
            >
              <span class="continue-thumb">
                <img
                  v-if="resolveDownloadArtworkUrl(entry.download)"
                  :src="resolveDownloadArtworkUrl(entry.download) ?? ''"
                  alt=""
                  class="size-full object-cover"
                  loading="lazy"
                />
                <span class="continue-play" aria-hidden="true"><AppNavIcon name="play" class="size-4" /></span>
                <span class="continue-progress" aria-hidden="true">
                  <span :style="{ width: `${Math.min(100, (entry.record.currentTime / entry.record.duration) * 100)}%` }"></span>
                </span>
              </span>
              <span class="grid min-w-0 gap-0.5 px-0.5 text-left">
                <strong class="truncate text-[13px] font-semibold text-[var(--ink)]" :title="displayTitle(entry.download)">{{ continueTitle(entry) }}</strong>
                <span class="truncate text-[11.5px] text-[var(--muted)]">{{ [continueEpisodeLabel(entry), continueRemainingLabel(entry)].filter(Boolean).join(" · ") }}</span>
              </span>
            </button>
          </div>
        </section>

        <section v-if="todayShelf.length" class="home-section" aria-labelledby="home-today-title">
          <div class="section-head">
            <h2 id="home-today-title" class="section-title">今日更新</h2>
            <span class="section-sub">{{ todayLabel }} · {{ todayShelf.length }} 部</span>
            <span class="flex-1" />
            <button type="button" class="section-link" @click="discoveryForm.weekday = todayWeekdayKey">
              只看今天
              <AppNavIcon name="chevron-right" class="size-3.5" aria-hidden="true" />
            </button>
          </div>
          <div class="shelf-row">
            <AnimeCard
              v-for="anime in todayShelf"
              :key="anime.animeId"
              class="shelf-item"
              :anime="anime"
              :selected="selectedAnimeId === anime.animeId"
              :compact-meta="true"
              show-time
              @open="openAnimeDetail(anime)"
            />
          </div>
        </section>

        <section ref="gridSectionRef" class="home-section" aria-labelledby="home-grid-title">
          <div class="section-head">
            <h2 id="home-grid-title" class="section-title">{{ gridTitle }}</h2>
            <span class="section-sub">{{ displayedHomepageAnime.length }} 部</span>
          </div>

          <p
            v-if="platformFailureNotice"
            class="mb-3 rounded-lg bg-[var(--panel)] px-3 py-1.5 text-xs font-medium text-[var(--muted)]"
            role="status"
          >
            {{ platformFailureNotice }}
          </p>

          <AnimeGrid
            :items="pagedHomepageAnime"
            :selected-anime-id="selectedAnimeId"
            :loading="homepageLoading"
            :error-message="homepageError"
            :layout="homeLayout"
            @select="openAnimeDetail"
          />
          <PagerBar
            v-if="displayedHomepageAnime.length"
            class="mt-6"
            plain
            :current-page="animePage"
            :total-pages="animeTotalPages"
            :pagination-items="animePaginationItems"
            :row-count="animePageSize"
            :row-count-options="ANIME_PAGE_SIZE_OPTIONS"
            row-label="每页数量"
            @update:page="setAnimePage"
            @update:rowCount="animePageSize = $event"
          />
        </section>
      </div>
    </template>
  </AnimeDetailDrawer>
</template>

<style scoped>
.home-scroll {
  scroll-padding-top: 4rem;
}

/* 吸顶磨砂工具栏：滚动时内容从下面透过去，和顶栏同一套 shell 底色 */
.home-toolbar {
  position: sticky;
  top: 0;
  z-index: 20;
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 10px 12px;
  padding: 2px 2px 10px;
  background: color-mix(in srgb, var(--shell-bg) 86%, transparent);
  backdrop-filter: saturate(1.6) blur(14px);
}

/* 苹果式分段控件：一条浅底轨道，选中段是浮起的白块 */
.seg-track {
  display: inline-flex;
  align-items: center;
  gap: 2px;
  padding: 3px;
  border-radius: 10px;
  background: var(--panel);
}
.seg-item {
  flex: none;
  height: 28px;
  padding: 0 12px;
  border-radius: 7px;
  color: var(--ink-secondary);
  font-size: 13px;
  font-weight: 550;
  white-space: nowrap;
  transition: background 0.15s, color 0.15s, box-shadow 0.15s;
}
.seg-icon {
  display: inline-grid;
  place-items: center;
  width: 32px;
  padding: 0;
}
.seg-item:hover:not(.is-active) {
  color: var(--ink);
}
.seg-item.is-today:not(.is-active) {
  color: var(--accent-vivid);
}
.seg-item.is-active {
  background: var(--card);
  color: var(--ink);
  font-weight: 650;
  box-shadow: 0 1px 2px rgb(0 0 0 / 0.12), 0 0 0 0.5px rgb(0 0 0 / 0.04);
}

.home-search {
  display: flex;
  min-width: 0;
  flex: 1;
  align-items: center;
  gap: 7px;
  height: 34px;
  padding: 0 11px;
  border-radius: 10px;
  background: var(--panel);
  color: var(--muted);
  cursor: text;
  transition: box-shadow 0.15s, background 0.15s;
}
.search-kbd {
  display: none;
  min-width: 18px;
  padding: 0 5px;
  border: 1px solid var(--line);
  border-radius: 5px;
  color: var(--muted);
  font: 600 11px/16px ui-monospace, SFMono-Regular, Menlo, monospace;
  text-align: center;
}
@media (hover: hover) and (min-width: 1024px) {
  .search-kbd {
    display: inline-block;
  }
}
.home-search:focus-within .search-kbd {
  display: none;
}
.home-search:focus-within {
  background: var(--card);
  box-shadow: 0 0 0 3px color-mix(in srgb, var(--accent-vivid) 30%, transparent);
}

.home-icon-btn {
  display: inline-grid;
  place-items: center;
  width: 34px;
  height: 34px;
  border-radius: 10px;
  color: var(--ink-secondary);
  transition: background 0.15s, color 0.15s;
}
.home-icon-btn:hover:not(:disabled) {
  background: var(--panel);
  color: var(--ink);
}
.home-icon-btn:disabled {
  opacity: 0.6;
}

.home-section {
  padding: 2px 2px 18px;
}
.section-head {
  display: flex;
  align-items: baseline;
  gap: 10px;
  margin-bottom: 10px;
}
.section-title {
  color: var(--ink);
  font-size: 18px;
  font-weight: 700;
  letter-spacing: -0.01em;
}
.section-sub {
  color: var(--muted);
  font-size: 12px;
  font-weight: 500;
}
.section-link {
  display: inline-flex;
  align-items: center;
  gap: 2px;
  color: var(--accent-vivid);
  font-size: 13px;
  font-weight: 600;
}
.section-link:hover {
  opacity: 0.8;
}

/* 横向货架：上下留出空间给悬停放大和阴影，右侧渐隐提示还能滚 */
.shelf-row {
  display: flex;
  gap: 14px;
  margin: -10px -2px 0;
  padding: 10px 2px 6px;
  overflow-x: auto;
  scroll-snap-type: x proximity;
  scrollbar-width: none;
  mask-image: linear-gradient(to right, #000 calc(100% - 2rem), transparent 100%);
}
.shelf-row::-webkit-scrollbar {
  display: none;
}
.shelf-item {
  width: 7rem;
  flex: none;
  scroll-snap-align: start;
}

@media (min-width: 640px) {
  .shelf-item {
    width: 8.5rem;
  }
}

/* 继续观看：横版 16:9 缩略图，底部一条进度，和下面的竖版海报一眼区分开 */
.continue-card {
  display: grid;
  flex: none;
  gap: 6px;
  width: 13rem;
  scroll-snap-align: start;
  border-radius: 10px;
}
.continue-card:focus-visible {
  outline: 2px solid var(--accent);
  outline-offset: 2px;
}
.continue-thumb {
  position: relative;
  display: block;
  aspect-ratio: 16 / 9;
  overflow: hidden;
  border-radius: 10px;
  background: var(--panel);
  box-shadow: 0 1px 2px rgb(0 0 0 / 0.12), 0 6px 16px -8px rgb(0 0 0 / 0.35);
}
.continue-play {
  position: absolute;
  left: 8px;
  bottom: 10px;
  display: grid;
  place-items: center;
  width: 28px;
  height: 28px;
  border-radius: 999px;
  background: rgb(0 0 0 / 0.55);
  backdrop-filter: blur(8px);
  color: #fff;
  transition: transform 0.15s;
}
.continue-card:hover .continue-play {
  transform: scale(1.1);
}
.continue-progress {
  position: absolute;
  inset: auto 0 0 0;
  height: 3px;
  background: rgb(255 255 255 / 0.25);
}
.continue-progress > span {
  display: block;
  height: 100%;
  background: var(--accent-vivid);
}

/* 右侧渐隐：被裁掉的星期读作「还能滚」，而不是渲染坏了 */
@media (max-width: 1023px) {
  .weekday-scroller {
    max-width: 100%;
    mask-image: linear-gradient(to right, #000 calc(100% - 1.25rem), transparent 100%);
  }
}
</style>
