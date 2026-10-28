<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref, watch } from "vue";

import type { HomepageAnime } from "@shared/index";

import PagerBar from "../components/common/PagerBar.vue";
import AnimeCard from "../components/discovery/AnimeCard.vue";
import AnimeDetailDrawer from "../components/discovery/AnimeDetailDrawer.vue";
import AnimeGrid from "../components/discovery/AnimeGrid.vue";
import AppNavIcon from "../components/layout/AppNavIcon.vue";
import { broadcastWeekday } from "../lib/format";
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

onMounted(() => {
  window.addEventListener("mousedown", handleDocumentPointerDown);
  void nextTick(scrollActiveWeekdayIntoView);
});

onUnmounted(() => {
  window.removeEventListener("mousedown", handleDocumentPointerDown);
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

/* 右侧渐隐：被裁掉的星期读作「还能滚」，而不是渲染坏了 */
@media (max-width: 1023px) {
  .weekday-scroller {
    max-width: 100%;
    mask-image: linear-gradient(to right, #000 calc(100% - 1.25rem), transparent 100%);
  }
}
</style>
