<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref, watch } from "vue";

import type {
  AnimeCatalogFormat,
  AnimeCatalogRegion,
  AnimeCatalogSeason,
  AnimeCatalogSort,
  AnimeCatalogStatus,
  HomepageAnime
} from "@shared/index";

import AppDialog from "../components/common/AppDialog.vue";
import AnimeCard from "../components/discovery/AnimeCard.vue";
import AnimeDetailDrawer from "../components/discovery/AnimeDetailDrawer.vue";
import AnimeGrid from "../components/discovery/AnimeGrid.vue";
import AppNavIcon from "../components/layout/AppNavIcon.vue";
import { broadcastWeekday } from "../lib/format";
import { isBusy } from "../stores/busy";
import {
  activeDiscoverySearch,
  animePage,
  animeSortMode,
  CATALOG_FORMAT_OPTIONS,
  CATALOG_GENRE_OPTIONS,
  CATALOG_RATING_OPTIONS,
  CATALOG_REGION_OPTIONS,
  CATALOG_SEASON_OPTIONS,
  CATALOG_SORT_OPTIONS,
  CATALOG_STATUS_OPTIONS,
  CATALOG_YEAR_OPTIONS,
  catalogError,
  catalogFilters,
  catalogHasMore,
  catalogInitialized,
  catalogItems,
  catalogLoadingInitial,
  catalogLoadingMore,
  catalogTotal,
  clearDiscoverySearchHistory,
  discoveryForm,
  discoveryMode,
  discoverySearchHistory,
  displayedHomepageAnime,
  hasActiveCatalogFilters,
  hasAnimeCover,
  hasMoreHomepageAnime,
  homepageAnime,
  homepageError,
  isHistoricalSearchActive,
  isMultiYearRangeSelected,
  loadMoreCatalogAnime,
  loadMoreHomepageAnime,
  openAnimeDetail,
  pagedHomepageAnime,
  refreshCatalogAnime,
  removeDiscoverySearchHistory,
  resetCatalogFilters,
  runDiscoverySearch,
  searchPlatformFailures,
  selectedAnimeId,
  useDiscoverySearchHistory,
  visibleCatalogItems,
  WEEKDAY_TABS,
  type DiscoveryMode
} from "../stores/discovery";
import {
  hiddenAnimeIds,
  hiddenAnimeList,
  hideAnime,
  isAnimeHidden,
  unhideAllAnime,
  unhideAnime
} from "../stores/hiddenAnime";
import { saveSettingsFields, settings, settingsForm } from "../stores/session";

type HomeLayout = "poster" | "row";
type FilterMenuKey = "format" | "region" | "year" | "genre" | "more";

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
const loadMoreSentinelRef = ref<HTMLElement | null>(null);
const searchBoxRef = ref<HTMLElement | null>(null);
const filterBarRef = ref<HTMLElement | null>(null);
const weekdayBarRef = ref<HTMLElement | null>(null);
const searchHistoryVisible = ref(false);
const openMenu = ref<FilterMenuKey | null>(null);
const hiddenDialogOpen = ref(false);
let loadMoreObserver: IntersectionObserver | null = null;

watch(
  () => hiddenAnimeList.value.length,
  (count) => {
    if (count === 0) {
      hiddenDialogOpen.value = false;
    }
  }
);

function handleCardHide(anime: HomepageAnime): void {
  if (isAnimeHidden(anime.animeId)) {
    void unhideAnime(anime.animeId);
    return;
  }
  void hideAnime(anime);
}

function openHiddenItemDetail(anime: HomepageAnime): void {
  hiddenDialogOpen.value = false;
  openAnimeDetail(anime);
}

const nowDate = ref(new Date());
let nowTickTimer: number | null = null;

function syncNowDate(): void {
  nowDate.value = new Date();
}

const todayWeekdayKey = computed(() => String(nowDate.value.getDay()));
const todayLabel = computed(() => WEEKDAY_TABS.find((tab) => tab.key === todayWeekdayKey.value)?.label ?? "");

const currentSeasonLabel = computed(() => {
  const d = nowDate.value;
  const year = d.getFullYear();
  const month = d.getMonth() + 1;
  const seasonMonth = month <= 3 ? 1 : month <= 6 ? 4 : month <= 9 ? 7 : 10;
  return `${year}年${seasonMonth}月新番`;
});

function weekdayDateOf(key: string): Date | null {
  if (key === "all") {
    return null;
  }
  const base = nowDate.value;
  const todayIso = base.getDay() === 0 ? 7 : base.getDay();
  const targetIso = key === "0" ? 7 : Number(key);
  if (!Number.isFinite(targetIso)) {
    return null;
  }
  const target = new Date(base.getFullYear(), base.getMonth(), base.getDate() + (targetIso - todayIso));
  return target;
}

function weekdayButtonLabel(weekday: { key: string; label: string }): string {
  return weekday.label;
}

watch(todayWeekdayKey, (next, prev) => {
  if (prev && discoveryForm.weekday === prev) {
    discoveryForm.weekday = next;
  }
});

function toggleMenu(menu: FilterMenuKey): void {
  searchHistoryVisible.value = false;
  openMenu.value = openMenu.value === menu ? null : menu;
}

const formatTriggerLabel = computed(() =>
  catalogFilters.format === "all"
    ? "类型"
    : CATALOG_FORMAT_OPTIONS.find((x) => x.key === catalogFilters.format)?.label ?? catalogFilters.format
);

const regionTriggerLabel = computed(() =>
  catalogFilters.region === "all"
    ? "地区"
    : CATALOG_REGION_OPTIONS.find((x) => x.key === catalogFilters.region)?.label ?? catalogFilters.region
);

const isYearMenuActive = computed(
  () => catalogFilters.year !== "all" || (!isMultiYearRangeSelected.value && catalogFilters.season !== "all")
);

const yearTriggerLabel = computed(() => {
  const yearLabel =
    catalogFilters.year === "all"
      ? ""
      : CATALOG_YEAR_OPTIONS.find((x) => x.key === catalogFilters.year)?.label ?? catalogFilters.year;
  const seasonLabel =
    !isMultiYearRangeSelected.value && catalogFilters.season !== "all"
      ? CATALOG_SEASON_OPTIONS.find((x) => x.key === catalogFilters.season)?.label?.replace(/^\d+月/, "") ?? ""
      : "";
  if (yearLabel && seasonLabel) {
    return `${yearLabel} · ${seasonLabel}`;
  }
  if (yearLabel) {
    return yearLabel;
  }
  if (seasonLabel) {
    return `${new Date().getFullYear()} · ${seasonLabel}`;
  }
  return "年份 / 季度";
});

const genreTriggerLabel = computed(() => (catalogFilters.genre === "all" ? "风格" : catalogFilters.genre));

const isMoreMenuActive = computed(() => catalogFilters.status !== "all" || catalogFilters.minRating > 0);

const moreTriggerLabel = computed(() => {
  const parts: string[] = [];
  if (catalogFilters.status !== "all") {
    parts.push(CATALOG_STATUS_OPTIONS.find((x) => x.key === catalogFilters.status)?.label ?? "");
  }
  if (catalogFilters.minRating > 0) {
    parts.push(`${catalogFilters.minRating}分+`);
  }
  return parts.filter(Boolean).join(" · ") || "状态 / 评分";
});

const catalogCountText = computed(() => {
  if (catalogLoadingInitial.value && catalogItems.value.length === 0) {
    return "加载中…";
  }
  return catalogTotal.value >= 1000 ? "1,000+ 部" : `${catalogTotal.value} 部`;
});

function switchMode(mode: DiscoveryMode): void {
  if (discoveryMode.value === mode) {
    return;
  }
  openMenu.value = null;
  searchHistoryVisible.value = false;
  discoveryMode.value = mode;
  scrollRef.value?.scrollTo({ top: 0 });
  if (mode === "catalog" && !catalogInitialized.value && !catalogLoadingInitial.value) {
    void refreshCatalogAnime({ silent: true });
  }
}

function selectFormat(format: AnimeCatalogFormat): void {
  openMenu.value = null;
  if (catalogFilters.format === format) return;
  catalogFilters.format = format;
  void refreshCatalogAnime({ silent: true });
}

function selectRegion(region: AnimeCatalogRegion): void {
  openMenu.value = null;
  if (catalogFilters.region === region) return;
  catalogFilters.region = region;
  void refreshCatalogAnime({ silent: true });
}

function selectYear(year: string): void {
  if (catalogFilters.year === year) {
    openMenu.value = null;
    return;
  }
  catalogFilters.year = year;
  if (year.includes("-") || year === "older") {
    catalogFilters.season = "all";
    openMenu.value = null;
  }
  void refreshCatalogAnime({ silent: true });
}

function selectSeason(season: AnimeCatalogSeason): void {
  openMenu.value = null;
  if (catalogFilters.season === season && !isMultiYearRangeSelected.value) return;
  if (isMultiYearRangeSelected.value) {
    catalogFilters.year = String(Math.max(2026, new Date().getFullYear()));
  }
  catalogFilters.season = season;
  void refreshCatalogAnime({ silent: true });
}

function clearYearAndSeason(): void {
  openMenu.value = null;
  if (!isYearMenuActive.value) return;
  catalogFilters.year = "all";
  catalogFilters.season = "all";
  void refreshCatalogAnime({ silent: true });
}

function selectGenre(genre: string): void {
  openMenu.value = null;
  if (catalogFilters.genre === genre) return;
  catalogFilters.genre = genre;
  void refreshCatalogAnime({ silent: true });
}

function selectStatus(status: AnimeCatalogStatus): void {
  if (catalogFilters.status === status) return;
  catalogFilters.status = status;
  void refreshCatalogAnime({ silent: true });
}

function selectMinRating(rating: number): void {
  openMenu.value = null;
  if (catalogFilters.minRating === rating) return;
  catalogFilters.minRating = rating;
  void refreshCatalogAnime({ silent: true });
}

function clearStatusAndRating(): void {
  openMenu.value = null;
  if (!isMoreMenuActive.value) return;
  catalogFilters.status = "all";
  catalogFilters.minRating = 0;
  void refreshCatalogAnime({ silent: true });
}

function selectCatalogSort(sort: AnimeCatalogSort): void {
  if (catalogFilters.sort === sort) return;
  catalogFilters.sort = sort;
  scrollRef.value?.scrollTo({ top: 0 });
  void refreshCatalogAnime({ silent: true });
}

function runCatalogKeywordSearch(): void {
  searchHistoryVisible.value = false;
  openMenu.value = null;
  void refreshCatalogAnime({ silent: true });
}

function clearCatalogKeyword(): void {
  if (!catalogFilters.keyword) return;
  catalogFilters.keyword = "";
  void refreshCatalogAnime({ silent: true });
}

function weekdayOf(anime: HomepageAnime): string {
  return String(broadcastWeekday(anime.broadcastAt) ?? anime.airDay);
}

function minuteOfDay(anime: HomepageAnime): number {
  const date = anime.broadcastAt ? new Date(anime.broadcastAt) : null;
  return date && !Number.isNaN(date.getTime()) ? date.getHours() * 60 + date.getMinutes() : Number.MAX_SAFE_INTEGER;
}

const todayShelf = computed(() => {
  if (
    discoveryMode.value !== "schedule" ||
    homeLayout.value === "row" ||
    discoveryForm.weekday !== "all" ||
    isHistoricalSearchActive.value ||
    discoveryForm.search.trim()
  ) {
    return [];
  }
  return homepageAnime.value
    .filter((anime) => !hiddenAnimeIds.value.has(anime.animeId) && weekdayOf(anime) === todayWeekdayKey.value)
    .sort((left, right) => {
      const coverDiff = (hasAnimeCover(left) ? 0 : 1) - (hasAnimeCover(right) ? 0 : 1);
      return coverDiff !== 0 ? coverDiff : minuteOfDay(left) - minuteOfDay(right);
    });
});

const showScheduleSectionHeading = computed(
  () => isHistoricalSearchActive.value || todayShelf.value.length > 0 || discoveryForm.weekday !== "all"
);

const gridTitle = computed(() => {
  if (isHistoricalSearchActive.value) {
    return `搜索「${activeDiscoverySearch.value}」`;
  }
  if (discoveryForm.weekday === "all") {
    return "本季全部";
  }
  const label = WEEKDAY_TABS.find((tab) => tab.key === discoveryForm.weekday)?.label ?? "";
  return discoveryForm.weekday === todayWeekdayKey.value ? "今日更新" : `${label}更新`;
});

const gridSubtitle = computed(() => {
  if (isHistoricalSearchActive.value || discoveryForm.weekday === "all") {
    return `${displayedHomepageAnime.value.length} 部`;
  }
  const d = weekdayDateOf(discoveryForm.weekday);
  const label = WEEKDAY_TABS.find((tab) => tab.key === discoveryForm.weekday)?.label ?? "";
  const datePart = d ? `${d.getMonth() + 1}月${d.getDate()}日 ${label}` : label;
  return `${currentSeasonLabel.value} · ${datePart} · ${displayedHomepageAnime.value.length} 部`;
});

const showSearchButton = computed(() => Boolean(discoveryForm.search.trim()) || isHistoricalSearchActive.value);
const hasMoreCurrentView = computed(() =>
  discoveryMode.value === "catalog" ? catalogHasMore.value : hasMoreHomepageAnime.value
);

function maybeLoadMore(): void {
  const scroller = scrollRef.value;
  if (!scroller) {
    return;
  }
  if (scroller.scrollHeight - scroller.scrollTop - scroller.clientHeight > 360) {
    return;
  }
  if (discoveryMode.value === "catalog") {
    if (catalogHasMore.value && !catalogLoadingInitial.value && !catalogLoadingMore.value) {
      void loadMoreCatalogAnime().then((loaded) => {
        if (loaded) {
          void nextTick(maybeLoadMore);
        }
      });
    }
    return;
  }
  if (!hasMoreHomepageAnime.value) {
    return;
  }
  if (loadMoreHomepageAnime()) {
    void nextTick(maybeLoadMore);
  }
}

function setupLoadMoreObserver(): void {
  loadMoreObserver?.disconnect();
  loadMoreObserver = null;
  if (typeof IntersectionObserver === "undefined" || !scrollRef.value || !loadMoreSentinelRef.value) {
    return;
  }
  loadMoreObserver = new IntersectionObserver(
    (entries) => {
      if (entries.some((entry) => entry.isIntersecting)) {
        maybeLoadMore();
      }
    },
    { root: scrollRef.value, rootMargin: "360px 0px" }
  );
  loadMoreObserver.observe(loadMoreSentinelRef.value);
}

watch(() => discoveryForm.weekday, () => scrollRef.value?.scrollTo({ top: 0 }));
watch(
  [
    discoveryMode,
    () => displayedHomepageAnime.value.length,
    () => pagedHomepageAnime.value.length,
    () => visibleCatalogItems.value.length,
    catalogHasMore,
    homeLayout
  ],
  () => {
    void nextTick(() => {
      setupLoadMoreObserver();
      maybeLoadMore();
    });
  }
);

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
  if (discoveryMode.value === "catalog") {
    catalogFilters.keyword = keyword;
    void refreshCatalogAnime({ silent: true });
    return;
  }
  useDiscoverySearchHistory(keyword);
}

async function setSortMode(mode: "default" | "rating"): Promise<void> {
  if (animeSortMode.value === mode) {
    return;
  }
  animeSortMode.value = mode;
  animePage.value = 1;
  if (settings.value && settings.value.homepageSortMode !== mode) {
    settingsForm.homepageSortMode = mode;
    await saveSettingsFields(["homepageSortMode"]);
  }
}

function handleDocumentPointerDown(event: MouseEvent): void {
  const target = event.target as Node | null;
  if (target && !searchBoxRef.value?.contains(target)) {
    searchHistoryVisible.value = false;
  }
  if (target && !filterBarRef.value?.contains(target)) {
    openMenu.value = null;
  }
}

function handleEscapeKey(event: KeyboardEvent): void {
  if (event.key !== "Escape") return;
  if (openMenu.value !== null) {
    openMenu.value = null;
    event.preventDefault();
    event.stopPropagation();
  } else if (searchHistoryVisible.value) {
    searchHistoryVisible.value = false;
    event.preventDefault();
    event.stopPropagation();
  }
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

function scrollActiveWeekdayIntoView(): void {
  const bar = weekdayBarRef.value;
  const active = bar?.querySelector<HTMLElement>(`[data-weekday="${discoveryForm.weekday}"]`);
  active?.scrollIntoView({ block: "nearest", inline: "nearest" });
}

watch(() => discoveryForm.weekday, () => nextTick(scrollActiveWeekdayIntoView));

onMounted(() => {
  syncNowDate();
  if (!isHistoricalSearchActive.value && !discoveryForm.search.trim()) {
    discoveryMode.value = "schedule";
  }
  nowTickTimer = window.setInterval(syncNowDate, 60_000);
  document.addEventListener("visibilitychange", syncNowDate);
  window.addEventListener("mousedown", handleDocumentPointerDown);
  window.addEventListener("keydown", handleEscapeKey, true);
  if (discoveryMode.value === "catalog" && !catalogInitialized.value && !catalogLoadingInitial.value) {
    void refreshCatalogAnime({ silent: true });
  }
  void nextTick(() => {
    scrollActiveWeekdayIntoView();
    setupLoadMoreObserver();
    maybeLoadMore();
  });
});

onUnmounted(() => {
  if (nowTickTimer !== null) {
    window.clearInterval(nowTickTimer);
    nowTickTimer = null;
  }
  document.removeEventListener("visibilitychange", syncNowDate);
  loadMoreObserver?.disconnect();
  loadMoreObserver = null;
  window.removeEventListener("mousedown", handleDocumentPointerDown);
  window.removeEventListener("keydown", handleEscapeKey, true);
});
</script>

<template>
  <AnimeDetailDrawer plain>
    <template #main>
      <div
        ref="scrollRef"
        class="home-scroll h-full min-h-0 overflow-y-auto overflow-x-hidden max-md:pb-[max(5.5rem,calc(env(safe-area-inset-bottom)+4.5rem))]"
        @scroll.passive="maybeLoadMore"
      >
        <div ref="toolbarRef" class="home-toolbar">
          <!-- 第一层：左侧平直下划线页签（当季新番 / 番剧片库），右侧搜索与排列视图，中间留足呼吸空间 -->
          <div class="home-head">
            <div class="mode-tabs" role="tablist" aria-label="发现范围">
              <button
                type="button"
                role="tab"
                class="mode-tab"
                :class="{ 'is-active': discoveryMode === 'schedule' }"
                :aria-selected="discoveryMode === 'schedule'"
                @click="switchMode('schedule')"
              >
                当季新番
              </button>
              <button
                type="button"
                role="tab"
                class="mode-tab"
                :class="{ 'is-active': discoveryMode === 'catalog' }"
                :aria-selected="discoveryMode === 'catalog'"
                @click="switchMode('catalog')"
              >
                番剧片库
              </button>
            </div>

            <div class="head-controls">
              <div ref="searchBoxRef" class="search-wrap">
                <label v-if="discoveryMode === 'schedule'" class="home-search">
                  <AppNavIcon name="search" class="size-[14px] shrink-0" aria-hidden="true" />
                  <input
                    v-model="discoveryForm.search"
                    class="home-search-input"
                    aria-label="搜索番剧，回车搜索全库"
                    placeholder="搜索番剧"
                    @focus="searchHistoryVisible = true; openMenu = null"
                    @click="searchHistoryVisible = true; openMenu = null"
                    @keyup.enter="runSearch"
                  />
                  <kbd v-if="!discoveryForm.search" class="search-kbd" title="按 / 聚焦搜索">/</kbd>
                </label>

                <label v-else class="home-search">
                  <AppNavIcon name="search" class="size-[14px] shrink-0" aria-hidden="true" />
                  <input
                    v-model="catalogFilters.keyword"
                    class="home-search-input"
                    aria-label="搜索片库关键词"
                    placeholder="搜索片库番剧"
                    @focus="searchHistoryVisible = true; openMenu = null"
                    @click="searchHistoryVisible = true; openMenu = null"
                    @keyup.enter="runCatalogKeywordSearch"
                  />
                  <button
                    v-if="catalogFilters.keyword"
                    type="button"
                    class="search-clear"
                    aria-label="清除关键词"
                    @click.prevent="clearCatalogKeyword"
                  >
                    ×
                  </button>
                  <kbd v-else class="search-kbd" title="按 / 聚焦搜索">/</kbd>
                </label>

                <button
                  v-if="discoveryMode === 'schedule' && showSearchButton"
                  type="button"
                  class="search-submit"
                  @click="runSearch"
                >
                  {{ searching ? "搜索中…" : !discoveryForm.search.trim() && isHistoricalSearchActive ? "返回当季" : "搜索" }}
                </button>

                <div v-if="searchHistoryVisible && discoverySearchHistory.length" class="history-popover">
                  <div class="flex items-center justify-between gap-3">
                    <span class="text-xs font-medium text-[var(--muted)]">最近搜索</span>
                    <button
                      type="button"
                      class="text-xs font-medium text-[var(--muted)] transition hover:text-[var(--accent)]"
                      @click="clearDiscoverySearchHistory"
                    >
                      清空
                    </button>
                  </div>
                  <div class="flex max-h-36 flex-wrap gap-1.5 overflow-auto">
                    <div v-for="keyword in discoverySearchHistory" :key="keyword" class="inline-flex items-center gap-1">
                      <button type="button" class="chip px-2.5 py-1 text-xs" @click="useHistory(keyword)">
                        {{ keyword }}
                      </button>
                      <button
                        type="button"
                        class="chip px-1.5 py-1 text-xs text-[var(--muted)]"
                        :aria-label="`删除搜索记录 ${keyword}`"
                        @click="removeDiscoverySearchHistory(keyword)"
                      >
                        ×
                      </button>
                    </div>
                  </div>
                </div>
              </div>

              <div class="view-switch" role="group" aria-label="显示方式">
                <button
                  type="button"
                  class="view-btn"
                  :class="{ 'is-active': homeLayout === 'poster' }"
                  :aria-pressed="homeLayout === 'poster'"
                  aria-label="海报网格"
                  title="海报网格"
                  @click="homeLayout = 'poster'"
                >
                  <AppNavIcon name="grid" class="size-[15px]" aria-hidden="true" />
                </button>
                <button
                  type="button"
                  class="view-btn"
                  :class="{ 'is-active': homeLayout === 'row' }"
                  :aria-pressed="homeLayout === 'row'"
                  aria-label="紧凑列表"
                  title="紧凑列表"
                  @click="homeLayout = 'row'"
                >
                  <AppNavIcon name="list" class="size-[15px]" aria-hidden="true" />
                </button>
              </div>
            </div>
          </div>

          <!-- 第二层：上下文筛选条 + 右侧平直排序与数量（不套气泡胶囊，空间不足时自然折行） -->
          <div v-if="discoveryMode === 'schedule'" class="home-subbar">
            <div
              ref="weekdayBarRef"
              class="weekday-bar"
              role="group"
              aria-label="按星期筛选"
            >
              <button
                v-for="weekday in WEEKDAY_TABS"
                :key="weekday.key"
                type="button"
                :data-weekday="weekday.key"
                class="weekday-btn"
                :class="{ 'is-active': weekday.key === discoveryForm.weekday, 'is-today': weekday.key === todayWeekdayKey }"
                :aria-pressed="weekday.key === discoveryForm.weekday"
                :aria-label="weekdayButtonLabel(weekday)"
                @click="discoveryForm.weekday = weekday.key"
              >
                {{ weekdayButtonLabel(weekday) }}
              </button>
            </div>

            <div class="subbar-right">
              <div class="sort-links" role="group" aria-label="排序方式">
                <button
                  type="button"
                  class="sort-link"
                  :class="{ 'is-active': animeSortMode === 'default' }"
                  :aria-pressed="animeSortMode === 'default'"
                  @click="setSortMode('default')"
                >
                  默认排序
                </button>
                <button
                  type="button"
                  class="sort-link"
                  :class="{ 'is-active': animeSortMode === 'rating' }"
                  :aria-pressed="animeSortMode === 'rating'"
                  @click="setSortMode('rating')"
                >
                  按评分
                </button>
              </div>
              <span class="subbar-sep" aria-hidden="true" />
              <span class="subbar-count">{{ displayedHomepageAnime.length }} 部</span>
            </div>
          </div>

          <div v-else ref="filterBarRef" class="home-subbar">
            <div class="filter-row" role="group" aria-label="片库筛选条件">
              <!-- 1. 类型 -->
              <div class="filter-anchor">
                <div class="filter-select" :class="{ 'is-selected': catalogFilters.format !== 'all', 'is-open': openMenu === 'format' }">
                  <button type="button" class="filter-select-btn" :aria-expanded="openMenu === 'format'" @click="toggleMenu('format')">
                    <span>{{ formatTriggerLabel }}</span>
                    <svg v-if="catalogFilters.format === 'all'" class="select-caret" viewBox="0 0 12 12" fill="none" stroke="currentColor" stroke-width="1.6" aria-hidden="true">
                      <path d="M3 4.5L6 7.5L9 4.5" stroke-linecap="round" stroke-linejoin="round" />
                    </svg>
                  </button>
                  <button
                    v-if="catalogFilters.format !== 'all'"
                    type="button"
                    class="filter-select-clear"
                    aria-label="清除类型筛选"
                    @click.stop="selectFormat('all')"
                  >
                    ×
                  </button>
                </div>
                <div v-if="openMenu === 'format'" class="filter-popover w-[11rem]">
                  <div class="popover-grid grid-cols-2">
                    <button
                      v-for="opt in CATALOG_FORMAT_OPTIONS"
                      :key="opt.key"
                      type="button"
                      class="popover-item"
                      :class="{ 'is-active': catalogFilters.format === opt.key }"
                      @click="selectFormat(opt.key)"
                    >
                      {{ opt.label }}
                    </button>
                  </div>
                </div>
              </div>

              <!-- 2. 地区 -->
              <div class="filter-anchor">
                <div class="filter-select" :class="{ 'is-selected': catalogFilters.region !== 'all', 'is-open': openMenu === 'region' }">
                  <button type="button" class="filter-select-btn" :aria-expanded="openMenu === 'region'" @click="toggleMenu('region')">
                    <span>{{ regionTriggerLabel }}</span>
                    <svg v-if="catalogFilters.region === 'all'" class="select-caret" viewBox="0 0 12 12" fill="none" stroke="currentColor" stroke-width="1.6" aria-hidden="true">
                      <path d="M3 4.5L6 7.5L9 4.5" stroke-linecap="round" stroke-linejoin="round" />
                    </svg>
                  </button>
                  <button
                    v-if="catalogFilters.region !== 'all'"
                    type="button"
                    class="filter-select-clear"
                    aria-label="清除地区筛选"
                    @click.stop="selectRegion('all')"
                  >
                    ×
                  </button>
                </div>
                <div v-if="openMenu === 'region'" class="filter-popover w-[10rem]">
                  <div class="popover-grid grid-cols-2">
                    <button
                      v-for="opt in CATALOG_REGION_OPTIONS"
                      :key="opt.key"
                      type="button"
                      class="popover-item"
                      :class="{ 'is-active': catalogFilters.region === opt.key }"
                      @click="selectRegion(opt.key)"
                    >
                      {{ opt.label }}
                    </button>
                  </div>
                </div>
              </div>

              <!-- 3. 年份 / 季度 -->
              <div class="filter-anchor">
                <div class="filter-select" :class="{ 'is-selected': isYearMenuActive, 'is-open': openMenu === 'year' }">
                  <button type="button" class="filter-select-btn" :aria-expanded="openMenu === 'year'" @click="toggleMenu('year')">
                    <span>{{ yearTriggerLabel }}</span>
                    <svg v-if="!isYearMenuActive" class="select-caret" viewBox="0 0 12 12" fill="none" stroke="currentColor" stroke-width="1.6" aria-hidden="true">
                      <path d="M3 4.5L6 7.5L9 4.5" stroke-linecap="round" stroke-linejoin="round" />
                    </svg>
                  </button>
                  <button
                    v-if="isYearMenuActive"
                    type="button"
                    class="filter-select-clear"
                    aria-label="清除年份与季度筛选"
                    @click.stop="clearYearAndSeason()"
                  >
                    ×
                  </button>
                </div>
                <div v-if="openMenu === 'year'" class="filter-popover w-[17.5rem]">
                  <span class="popover-caption">年份</span>
                  <div class="popover-grid grid-cols-4">
                    <button
                      v-for="opt in CATALOG_YEAR_OPTIONS"
                      :key="opt.key"
                      type="button"
                      class="popover-item"
                      :class="{ 'is-active': catalogFilters.year === opt.key }"
                      @click="selectYear(opt.key)"
                    >
                      {{ opt.label }}
                    </button>
                  </div>
                  <div class="popover-divider" />
                  <span class="popover-caption">季度</span>
                  <div class="popover-grid grid-cols-5">
                    <button
                      v-for="opt in CATALOG_SEASON_OPTIONS"
                      :key="opt.key"
                      type="button"
                      class="popover-item px-1"
                      :class="{ 'is-active': !isMultiYearRangeSelected && catalogFilters.season === opt.key }"
                      @click="selectSeason(opt.key)"
                    >
                      {{ opt.label }}
                    </button>
                  </div>
                </div>
              </div>

              <!-- 4. 风格题材 -->
              <div class="filter-anchor">
                <div class="filter-select" :class="{ 'is-selected': catalogFilters.genre !== 'all', 'is-open': openMenu === 'genre' }">
                  <button type="button" class="filter-select-btn" :aria-expanded="openMenu === 'genre'" @click="toggleMenu('genre')">
                    <span>{{ genreTriggerLabel }}</span>
                    <svg v-if="catalogFilters.genre === 'all'" class="select-caret" viewBox="0 0 12 12" fill="none" stroke="currentColor" stroke-width="1.6" aria-hidden="true">
                      <path d="M3 4.5L6 7.5L9 4.5" stroke-linecap="round" stroke-linejoin="round" />
                    </svg>
                  </button>
                  <button
                    v-if="catalogFilters.genre !== 'all'"
                    type="button"
                    class="filter-select-clear"
                    aria-label="清除风格筛选"
                    @click.stop="selectGenre('all')"
                  >
                    ×
                  </button>
                </div>
                <div v-if="openMenu === 'genre'" class="filter-popover w-[18rem]">
                  <div class="popover-grid grid-cols-4">
                    <button
                      v-for="opt in CATALOG_GENRE_OPTIONS"
                      :key="opt.key"
                      type="button"
                      class="popover-item"
                      :class="{ 'is-active': catalogFilters.genre === opt.key }"
                      @click="selectGenre(opt.key)"
                    >
                      {{ opt.label }}
                    </button>
                  </div>
                </div>
              </div>

              <!-- 5. 状态 / 评分 -->
              <div class="filter-anchor">
                <div class="filter-select" :class="{ 'is-selected': isMoreMenuActive, 'is-open': openMenu === 'more' }">
                  <button type="button" class="filter-select-btn" :aria-expanded="openMenu === 'more'" @click="toggleMenu('more')">
                    <span>{{ moreTriggerLabel }}</span>
                    <svg v-if="!isMoreMenuActive" class="select-caret" viewBox="0 0 12 12" fill="none" stroke="currentColor" stroke-width="1.6" aria-hidden="true">
                      <path d="M3 4.5L6 7.5L9 4.5" stroke-linecap="round" stroke-linejoin="round" />
                    </svg>
                  </button>
                  <button
                    v-if="isMoreMenuActive"
                    type="button"
                    class="filter-select-clear"
                    aria-label="清除状态与评分筛选"
                    @click.stop="clearStatusAndRating()"
                  >
                    ×
                  </button>
                </div>
                <div v-if="openMenu === 'more'" class="filter-popover right-0 sm:left-0 sm:right-auto w-[15.5rem]">
                  <span class="popover-caption">播出状态</span>
                  <div class="popover-grid grid-cols-4">
                    <button
                      v-for="opt in CATALOG_STATUS_OPTIONS"
                      :key="opt.key"
                      type="button"
                      class="popover-item"
                      :class="{ 'is-active': catalogFilters.status === opt.key }"
                      @click="selectStatus(opt.key)"
                    >
                      {{ opt.label }}
                    </button>
                  </div>
                  <div class="popover-divider" />
                  <span class="popover-caption">最低评分</span>
                  <div class="popover-grid grid-cols-3">
                    <button
                      v-for="opt in CATALOG_RATING_OPTIONS"
                      :key="opt.key"
                      type="button"
                      class="popover-item"
                      :class="{ 'is-active': catalogFilters.minRating === opt.key }"
                      @click="selectMinRating(opt.key)"
                    >
                      {{ opt.label }}
                    </button>
                  </div>
                </div>
              </div>

              <button
                v-if="hasActiveCatalogFilters"
                type="button"
                class="reset-link"
                @click="resetCatalogFilters({ keepSort: true })"
              >
                重置
              </button>
            </div>

            <div class="subbar-right">
              <div class="sort-links" role="group" aria-label="片库排序">
                <button
                  v-for="opt in CATALOG_SORT_OPTIONS"
                  :key="opt.key"
                  type="button"
                  class="sort-link"
                  :class="{ 'is-active': catalogFilters.sort === opt.key }"
                  :aria-pressed="catalogFilters.sort === opt.key"
                  @click="selectCatalogSort(opt.key)"
                >
                  {{ opt.label }}
                </button>
              </div>
              <span class="subbar-sep" aria-hidden="true" />
              <span class="subbar-count">{{ catalogCountText }}</span>
            </div>
          </div>
        </div>

        <!-- ==================== 当季排期内容区 ==================== -->
        <template v-if="discoveryMode === 'schedule'">
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
                :allow-hide="true"
                :is-hidden="isAnimeHidden(anime.animeId)"
                show-time
                @open="openAnimeDetail(anime)"
                @hide="handleCardHide"
              />
            </div>
          </section>

          <section ref="gridSectionRef" class="home-section" aria-label="当季番剧列表">
            <div v-if="showScheduleSectionHeading" class="section-head">
              <h2 class="section-title">{{ gridTitle }}</h2>
              <span class="section-sub">{{ gridSubtitle }}</span>
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
              :catalog-meta="isHistoricalSearchActive"
              :layout="homeLayout"
              :allow-hide="true"
              :hidden-ids="hiddenAnimeIds"
              @select="openAnimeDetail"
              @hide="handleCardHide"
            />
          </section>
        </template>

        <!-- ==================== 番剧片库内容区 ==================== -->
        <template v-else>
          <section
            ref="gridSectionRef"
            class="home-section transition-opacity duration-150"
            :class="{ 'opacity-65': catalogLoadingInitial && visibleCatalogItems.length > 0 }"
            aria-label="番剧片库列表"
          >
            <AnimeGrid
              :items="visibleCatalogItems"
              :selected-anime-id="selectedAnimeId"
              :loading="catalogLoadingInitial && visibleCatalogItems.length === 0"
              :error-message="catalogError"
              :catalog-meta="true"
              :layout="homeLayout"
              :allow-hide="true"
              :hidden-ids="hiddenAnimeIds"
              @select="openAnimeDetail"
              @hide="handleCardHide"
            >
              <template #empty>
                <div class="grid max-w-sm gap-3 text-center">
                  <p class="text-sm font-semibold text-[var(--ink)]">没有找到符合当前筛选的番剧</p>
                  <button
                    v-if="hasActiveCatalogFilters"
                    type="button"
                    class="btn-primary justify-self-center px-4 py-1.5 text-xs font-semibold"
                    @click="resetCatalogFilters()"
                  >
                    重置全部筛选
                  </button>
                </div>
              </template>
            </AnimeGrid>

            <div v-if="catalogLoadingMore" class="py-4 text-center text-xs font-medium text-[var(--muted)]">
              正在加载更多…
            </div>
          </section>
        </template>

        <div
          v-if="hasMoreCurrentView"
          ref="loadMoreSentinelRef"
          class="h-6"
          aria-hidden="true"
        />

        <div v-if="hiddenAnimeList.length > 0" class="hidden-footer">
          <button
            type="button"
            class="hidden-footer-btn"
            @click="hiddenDialogOpen = true"
          >
            <AppNavIcon name="eye-off" class="size-3.5" aria-hidden="true" />
            <span>已隐藏 {{ hiddenAnimeList.length }} 部番剧 · 管理</span>
          </button>
        </div>
      </div>

      <AppDialog
        v-if="hiddenDialogOpen"
        title="已隐藏的番剧"
        size="sm"
        @close="hiddenDialogOpen = false"
      >
        <div class="grid max-h-[56vh] gap-1 overflow-y-auto pr-0.5">
          <div
            v-for="item in hiddenAnimeList"
            :key="item.animeId"
            class="hidden-dialog-row"
          >
            <button
              type="button"
              class="hidden-dialog-main"
              @click="openHiddenItemDetail(item)"
            >
              <span class="hidden-dialog-thumb">
                <img
                  v-if="item.imageUrl"
                  :src="item.imageUrl"
                  alt=""
                  class="size-full object-cover"
                  loading="lazy"
                />
                <span v-else aria-hidden="true">{{ item.title.slice(0, 1) || "番" }}</span>
              </span>
              <span class="min-w-0 truncate text-sm font-semibold text-[var(--ink)]" :title="item.title">
                {{ item.title }}
              </span>
            </button>
            <button
              type="button"
              class="btn-ghost shrink-0 px-2.5 py-1 text-xs font-semibold"
              @click="unhideAnime(item.animeId)"
            >
              恢复
            </button>
          </div>
        </div>

        <template #footer>
          <button
            v-if="hiddenAnimeList.length > 1"
            type="button"
            class="btn-ghost mr-auto px-3 py-1.5 text-xs font-semibold"
            @click="unhideAllAnime()"
          >
            全部恢复
          </button>
          <button
            type="button"
            class="btn-primary px-3.5 py-1.5 text-xs font-semibold"
            @click="hiddenDialogOpen = false"
          >
            完成
          </button>
        </template>
      </AppDialog>
    </template>
  </AnimeDetailDrawer>
</template>

<style scoped>
.home-scroll {
  scroll-padding-top: 6rem;
  padding-right: 4px;
  scrollbar-gutter: stable;
}

.home-toolbar {
  position: sticky;
  top: 0;
  z-index: 25;
  padding: 0 2px;
  background: color-mix(in srgb, var(--shell-bg) 92%, transparent);
  backdrop-filter: blur(12px);
}

@media (max-width: 639px) {
  .home-toolbar {
    position: static;
  }
}

/* ---------- 第一层：下划线标签 + 右侧搜索与视图 ---------- */
.home-head {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: space-between;
  gap: 8px 16px;
  border-bottom: 1px solid var(--line);
}

.mode-tabs {
  display: flex;
  align-items: center;
  gap: 20px;
}

.mode-tab {
  position: relative;
  height: 38px;
  padding: 0 2px;
  color: var(--muted);
  font-size: 14px;
  font-weight: 550;
  white-space: nowrap;
  transition: color 0.15s ease;
}

.mode-tab:hover:not(.is-active) {
  color: var(--ink);
}

.mode-tab.is-active {
  color: var(--ink);
  font-weight: 650;
}

.mode-tab.is-active::after {
  content: "";
  position: absolute;
  left: 0;
  right: 0;
  bottom: -1px;
  height: 2px;
  border-radius: 1px;
  background: var(--accent);
}

.head-controls {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-left: auto;
  padding-bottom: 5px;
}

@media (max-width: 560px) {
  .head-controls {
    width: 100%;
    padding-bottom: 8px;
  }
}

.search-wrap {
  position: relative;
  display: flex;
  align-items: center;
  gap: 6px;
  width: 224px;
  min-width: 0;
}

@media (max-width: 560px) {
  .search-wrap {
    flex: 1;
    width: auto;
  }
}

.home-search {
  display: flex;
  flex: 1;
  min-width: 0;
  align-items: center;
  gap: 7px;
  height: 32px;
  padding: 0 10px;
  border-radius: 8px;
  border: 1px solid var(--line);
  background: var(--input-bg);
  color: var(--muted);
  cursor: text;
  transition: border-color 0.15s ease, box-shadow 0.15s ease;
}

.home-search:focus-within {
  border-color: var(--accent);
  box-shadow: 0 0 0 3px var(--accent-soft);
}

.home-search-input {
  min-width: 0;
  flex: 1;
  border: 0;
  background: transparent;
  color: var(--ink);
  font-size: 13px;
  outline: none;
}

.home-search-input:focus,
.home-search-input:focus-visible {
  outline: none;
}

.search-clear {
  display: inline-grid;
  place-items: center;
  width: 18px;
  height: 18px;
  border-radius: 4px;
  color: var(--muted);
  font-size: 13px;
  line-height: 1;
}

.search-clear:hover {
  background: var(--panel);
  color: var(--ink);
}

.search-kbd {
  display: none;
  min-width: 18px;
  height: 18px;
  padding: 0 5px;
  border: 1px solid var(--line);
  border-radius: 5px;
  background: var(--panel);
  color: var(--muted);
  font: 600 11px/16px ui-monospace, SFMono-Regular, Menlo, monospace;
  text-align: center;
}

@media (hover: hover) and (min-width: 960px) {
  .search-kbd {
    display: inline-block;
  }
}

.home-search:focus-within .search-kbd {
  display: none;
}

.search-submit {
  flex-shrink: 0;
  height: 32px;
  padding: 0 11px;
  border-radius: 8px;
  background: var(--accent);
  color: var(--accent-contrast);
  font-size: 12.5px;
  font-weight: 600;
  white-space: nowrap;
}

.search-submit:hover {
  background: var(--accent-strong);
}

.history-popover {
  position: absolute;
  left: 0;
  right: 0;
  top: calc(100% + 6px);
  z-index: 35;
  display: grid;
  gap: 8px;
  padding: 10px;
  border-radius: 10px;
  border: 1px solid var(--line);
  background: var(--card);
  box-shadow: var(--strong-shadow);
}

.view-switch {
  display: inline-flex;
  flex-shrink: 0;
  align-items: center;
  gap: 2px;
  height: 38px;
  padding: 3px;
  border-radius: 9px;
  background: var(--panel);
}

.view-btn {
  display: inline-grid;
  place-items: center;
  width: 34px;
  height: 32px;
  border-radius: 7px;
  color: var(--muted);
  transition: background 0.15s ease, color 0.15s ease;
}

.view-btn:hover:not(.is-active) {
  color: var(--ink);
}

.view-btn.is-active {
  background: var(--card);
  color: var(--ink);
  box-shadow: var(--soft-shadow);
}

/* ---------- 第二层：平直星期 / 独立下拉框 + 文字排序 ---------- */
.home-subbar {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: space-between;
  gap: 8px 16px;
  padding: 10px 0 12px;
}

.weekday-bar {
  display: flex;
  min-width: 0;
  align-items: center;
  gap: 2px;
  overflow-x: auto;
  scrollbar-width: none;
}

.weekday-bar::-webkit-scrollbar {
  display: none;
}

.weekday-btn {
  flex: none;
  height: 32px;
  padding: 0 9px;
  border-radius: 8px;
  color: var(--ink-secondary);
  font-size: 13px;
  font-weight: 500;
  white-space: nowrap;
  transition: background 0.15s ease, color 0.15s ease;
}

@media (max-width: 639px) {
  .weekday-bar {
    display: grid;
    width: 100%;
    grid-template-columns: repeat(8, minmax(0, 1fr));
    gap: 2px;
    overflow-x: visible;
  }

  .weekday-btn {
    height: 30px;
    padding: 0;
    font-size: 12px;
    text-align: center;
  }
}

.weekday-btn:hover:not(.is-active) {
  background: var(--panel);
  color: var(--ink);
}

.weekday-btn.is-today:not(.is-active) {
  color: var(--accent);
  font-weight: 600;
}

.weekday-btn.is-active {
  background: var(--accent-soft);
  color: var(--accent);
  font-weight: 650;
}

/* 独立克制的筛选下拉框（对齐 Diana .app-select-trigger 圆角与边框语言） */
.filter-row {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 8px;
}

.filter-anchor {
  position: relative;
  display: inline-flex;
  align-items: center;
}

.filter-select {
  display: inline-flex;
  align-items: center;
  height: 30px;
  border-radius: 8px;
  border: 1px solid var(--line);
  background: var(--card);
  color: var(--ink-secondary);
  font-size: 12.5px;
  font-weight: 500;
  white-space: nowrap;
  transition: border-color 0.15s ease, background 0.15s ease, color 0.15s ease;
}

.filter-select:hover,
.filter-select.is-open {
  border-color: var(--accent-line);
  color: var(--ink);
}

.filter-select.is-selected {
  border-color: var(--accent-line);
  background: var(--accent-soft);
  color: var(--accent);
  font-weight: 600;
}

.filter-select-btn {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  height: 100%;
  padding: 0 10px;
}

.filter-select.is-selected .filter-select-btn {
  padding-right: 4px;
}

.select-caret {
  width: 11px;
  height: 11px;
  color: var(--muted);
  transition: transform 0.15s ease;
}

.filter-select.is-open .select-caret {
  transform: rotate(180deg);
}

.filter-select-clear {
  display: inline-grid;
  place-items: center;
  width: 18px;
  height: 18px;
  margin-right: 5px;
  border-radius: 4px;
  font-size: 13px;
  line-height: 1;
  opacity: 0.72;
}

.filter-select-clear:hover {
  background: color-mix(in srgb, currentColor 14%, transparent);
  opacity: 1;
}

.reset-link {
  padding: 0 4px;
  color: var(--muted);
  font-size: 12.5px;
  font-weight: 500;
  white-space: nowrap;
  transition: color 0.15s ease;
}

.reset-link:hover {
  color: var(--accent);
}

.filter-popover {
  position: absolute;
  left: 0;
  top: calc(100% + 6px);
  z-index: 40;
  max-width: calc(100vw - 28px);
  display: grid;
  gap: 6px;
  padding: 10px;
  border-radius: 10px;
  border: 1px solid var(--line);
  background: var(--card);
  box-shadow: var(--strong-shadow);
}

.popover-caption {
  padding: 0 4px;
  color: var(--muted);
  font-size: 11px;
  font-weight: 600;
}

.popover-divider {
  height: 1px;
  margin: 2px 0;
  background: var(--line);
}

.popover-grid {
  display: grid;
  gap: 4px;
}

.popover-item {
  height: 28px;
  padding: 0 8px;
  border-radius: 6px;
  color: var(--ink-secondary);
  font-size: 12.5px;
  font-weight: 500;
  text-align: center;
  white-space: nowrap;
  transition: background 0.12s ease, color 0.12s ease;
}

.popover-item:hover:not(.is-active) {
  background: var(--panel);
  color: var(--ink);
}

.popover-item.is-active {
  background: var(--accent-soft);
  color: var(--accent);
  font-weight: 650;
}

/* 右侧平直文字排序 + 计数 */
.subbar-right {
  display: flex;
  align-items: center;
  gap: 10px;
  margin-left: auto;
}

.sort-links {
  display: flex;
  align-items: center;
  gap: 12px;
}

.sort-link {
  padding: 2px 0;
  color: var(--muted);
  font-size: 12.5px;
  font-weight: 500;
  white-space: nowrap;
  transition: color 0.15s ease;
}

.sort-link:hover:not(.is-active) {
  color: var(--ink);
}

.sort-link.is-active {
  color: var(--accent);
  font-weight: 650;
}

.subbar-sep {
  width: 1px;
  height: 12px;
  background: var(--line);
}

.subbar-count {
  color: var(--muted);
  font-size: 12px;
  font-variant-numeric: tabular-nums;
  white-space: nowrap;
}

.hidden-entry-btn {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  padding: 2px 6px;
  border-radius: 6px;
  color: var(--muted);
  font-size: 12px;
  font-weight: 550;
  font-variant-numeric: tabular-nums;
  white-space: nowrap;
  transition: background 0.15s ease, color 0.15s ease;
}

.hidden-entry-btn:hover {
  background: var(--panel);
  color: var(--ink);
}

.hidden-dialog-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
  padding: 6px 8px 6px 6px;
  border-radius: 10px;
  transition: background 0.15s ease;
}

.hidden-dialog-row:hover {
  background: var(--panel);
}

.hidden-dialog-main {
  display: flex;
  flex: 1;
  min-width: 0;
  align-items: center;
  gap: 10px;
  text-align: left;
}

.hidden-dialog-thumb {
  display: grid;
  flex-shrink: 0;
  place-items: center;
  width: 36px;
  height: 48px;
  overflow: hidden;
  border-radius: 7px;
  background: var(--panel);
  color: var(--muted);
  font-size: 13px;
  font-weight: 600;
}

/* ---------- 内容分节与横向货架 ---------- */
.home-section {
  padding: 2px 2px 18px;
}

.section-head {
  display: flex;
  align-items: baseline;
  gap: 8px;
  margin-bottom: 10px;
}

.section-title {
  color: var(--ink);
  font-size: 15.5px;
  font-weight: 650;
}

.section-sub {
  color: var(--muted);
  font-size: 12px;
}

.section-link {
  display: inline-flex;
  align-items: center;
  gap: 2px;
  color: var(--accent);
  font-size: 12.5px;
  font-weight: 600;
}

.section-link:hover {
  opacity: 0.8;
}

.shelf-row {
  display: flex;
  gap: 14px;
  margin: -8px -2px 0;
  padding: 8px 2px 6px;
  overflow-x: auto;
  scroll-snap-type: x proximity;
  scrollbar-width: none;
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
.hidden-footer {
  display: flex;
  justify-content: center;
  padding: 18px 0 8px;
}
.hidden-footer-btn {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  height: 30px;
  padding: 0 12px;
  border-radius: 999px;
  color: var(--muted);
  font-size: 12px;
  font-weight: 500;
  transition: background 0.15s ease, color 0.15s ease;
}
.hidden-footer-btn:hover {
  background: var(--panel);
  color: var(--ink);
}
</style>
