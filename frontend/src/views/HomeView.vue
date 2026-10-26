<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref, watch } from "vue";

import PagerBar from "../components/common/PagerBar.vue";
import AnimeDetailDrawer from "../components/discovery/AnimeDetailDrawer.vue";
import AnimeGrid from "../components/discovery/AnimeGrid.vue";
import AppNavIcon from "../components/layout/AppNavIcon.vue";
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
  discoveryBaseTotal,
  discoveryForm,
  discoverySearchHistory,
  displayedHomepageAnime,
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

const searchBoxRef = ref<HTMLElement | null>(null);
const weekdayBarRef = ref<HTMLElement | null>(null);
const searchHistoryVisible = ref(false);

const todayWeekdayKey = String(new Date().getDay());

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
  <AnimeDetailDrawer>
    <template #main>
      <!--
        任何宽度都是「工具栏 / 卡片区自己滚 / 分页器」三行。以前 <1024px 时让整页一起滚，
        可外层容器是 overflow-hidden，结果窄窗口里卡片区下半截和分页器被裁掉、根本滚不到。
      -->
      <div class="relative grid h-full min-h-0 min-w-0 grid-rows-[auto_minmax(0,1fr)_auto] overflow-hidden">
        <div class="grid min-w-0 gap-2 px-4 pb-2 pt-3">
          <!-- 星期不再换行：一行横向滚动，把首屏让给内容 -->
          <div class="flex min-w-0 items-center gap-2">
            <div
              ref="weekdayBarRef"
              class="weekday-scroller flex min-w-0 flex-1 items-center gap-1.5 overflow-x-auto pb-0.5 pr-2 [scrollbar-width:none] [&::-webkit-scrollbar]:hidden"
              role="group"
              aria-label="按星期筛选"
            >
              <button
                v-for="weekday in WEEKDAY_TABS"
                :key="weekday.key"
                type="button"
                :data-weekday="weekday.key"
                class="chip shrink-0 rounded-md px-2.5 py-1 text-[13px]"
                :class="{ 'chip-active': weekday.key === discoveryForm.weekday }"
                :aria-pressed="weekday.key === discoveryForm.weekday"
                :aria-label="weekday.key === todayWeekdayKey ? `${weekday.label}（今日）` : weekday.label"
                @click="discoveryForm.weekday = weekday.key"
              >
                <span>{{ weekday.label }}</span>
                <span v-if="weekday.key === todayWeekdayKey" class="text-[11px] font-semibold opacity-80">今日</span>
              </button>
            </div>
            <span class="shrink-0 whitespace-nowrap text-xs font-semibold text-[var(--muted)]">
              {{ isHistoricalSearchActive ? `搜索「${activeDiscoverySearch}」` : "当季" }}
              {{ displayedHomepageAnime.length }} / {{ discoveryBaseTotal }}
            </span>
          </div>

          <div class="flex min-w-0 items-center gap-2">
            <div ref="searchBoxRef" class="relative flex min-w-0 flex-1 items-center gap-2">
              <div class="input-base flex min-w-0 flex-1 items-center gap-2 rounded-md px-3 py-1">
                <AppNavIcon name="search" class="size-4 shrink-0 text-[var(--muted)]" aria-hidden="true" />
                <input
                  v-model="discoveryForm.search"
                  class="min-w-0 flex-1 bg-transparent text-[13px] outline-none"
                  aria-label="搜索番剧，回车搜索 Bangumi 全库"
                  :placeholder="isMobileViewport ? '搜索番剧' : '搜索番剧（回车搜索 Bangumi 全库）'"
                  @focus="searchHistoryVisible = true"
                  @click="searchHistoryVisible = true"
                  @keyup.enter="runSearch"
                />
              </div>
              <button
                type="button"
                class="shrink-0 rounded-md px-3 py-1 text-[13px] font-semibold transition"
                :class="discoveryForm.search.trim() ? 'btn-primary' : 'chip'"
                @click="runSearch"
              >
                {{ searching ? "搜索中…" : !discoveryForm.search.trim() && isHistoricalSearchActive ? "回到当季" : "搜索" }}
              </button>

              <div
                v-if="searchHistoryVisible && discoverySearchHistory.length"
                class="absolute left-0 right-0 top-[calc(100%+0.55rem)] z-30 grid gap-3 rounded-lg border border-[var(--line)] bg-[var(--card)] p-3 shadow-[var(--strong-shadow)]"
              >
                <div class="flex items-center justify-between gap-3">
                  <span class="text-xs font-medium text-[var(--muted)]">最近搜索</span>
                  <button
                    type="button"
                    class="text-sm font-semibold text-[var(--muted)] transition hover:text-[var(--accent-vivid)]"
                    @click="clearDiscoverySearchHistory"
                  >
                    清空
                  </button>
                </div>
                <div class="flex max-h-40 flex-wrap gap-2 overflow-auto">
                  <div v-for="keyword in discoverySearchHistory" :key="keyword" class="inline-flex items-center gap-1">
                    <button type="button" class="chip rounded-md px-3 py-2 text-sm" @click="useHistory(keyword)">
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

            <!-- 分段控件用低调的选中态：主色留给筛选与主操作，不在工具栏里抢视线 -->
            <div
              class="grid shrink-0 grid-cols-2 gap-0.5 rounded-md border border-[var(--line)] bg-[var(--panel)] p-0.5"
              role="group"
              aria-label="排序方式"
            >
              <button
                type="button"
                class="rounded-[0.3rem] px-2.5 py-0.5 text-[13px] font-semibold transition"
                :class="animeSortMode === 'default' ? 'bg-[var(--card)] text-[var(--ink)] shadow-[var(--soft-shadow)]' : 'text-[var(--muted)]'"
                :aria-pressed="animeSortMode === 'default'"
                @click="setSortMode('default')"
              >
                默认
              </button>
              <button
                type="button"
                class="rounded-[0.3rem] px-2.5 py-0.5 text-[13px] font-semibold transition"
                :class="animeSortMode === 'rating' ? 'bg-[var(--card)] text-[var(--ink)] shadow-[var(--soft-shadow)]' : 'text-[var(--muted)]'"
                :aria-pressed="animeSortMode === 'rating'"
                @click="setSortMode('rating')"
              >
                评分
              </button>
            </div>

            <button
              type="button"
              class="icon-btn shrink-0"
              :class="{ 'animate-spin': isBusy('discovery-home') }"
              :disabled="isBusy('discovery-home')"
              :aria-label="isBusy('discovery-home') ? '正在同步当季番剧' : '同步当季番剧'"
              :title="isBusy('discovery-home') ? '同步中…' : '同步'"
              @click="refreshHomepage()"
            >
              <AppNavIcon name="refresh" aria-hidden="true" />
            </button>
          </div>
        </div>

        <p
          v-if="platformFailureNotice"
          class="mx-4 mb-2 rounded border border-[var(--line)] bg-[var(--panel)] px-3 py-1.5 text-xs font-medium text-[var(--muted)]"
          role="status"
        >
          {{ platformFailureNotice }}
        </p>

        <div class="min-h-0 overflow-hidden">
          <AnimeGrid
            :items="pagedHomepageAnime"
            :selected-anime-id="selectedAnimeId"
            :loading="homepageLoading"
            :error-message="homepageError"
            @select="openAnimeDetail"
          />
        </div>
        <PagerBar
          :current-page="animePage"
          :total-pages="animeTotalPages"
          :pagination-items="animePaginationItems"
          :row-count="animePageSize"
          :row-count-options="ANIME_PAGE_SIZE_OPTIONS"
          row-label="每页数量"
          @update:page="setAnimePage"
          @update:rowCount="animePageSize = $event"
        />
      </div>
    </template>
  </AnimeDetailDrawer>
</template>

<style scoped>
/* 右侧渐隐：被裁掉的星期读作「还能滚」，而不是渲染坏了 */
.weekday-scroller {
  mask-image: linear-gradient(to right, #000 calc(100% - 1.25rem), transparent 100%);
}
</style>
