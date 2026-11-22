<script setup lang="ts">
import { computed, onMounted, ref } from "vue";

import type { HomepageAnime } from "@shared/index";

import { searchAnime } from "../api";
import AnimeDetailDrawer from "../components/discovery/AnimeDetailDrawer.vue";
import AppNavIcon from "../components/layout/AppNavIcon.vue";
import {
  bumpPlaybackHistoryVersion,
  clearAllPlaybackHistory,
  playbackHistoryVersion,
  removePlaybackHistoryKeys,
  watchHistoryEntries,
  type WatchHistoryItem
} from "../lib/playbackHistory";
import { selectTab } from "../stores/boot";
import { isBusy, withBusy } from "../stores/busy";
import {
  homepageAnime,
  openAnimeDetail,
  openDownloadAnimeDetail
} from "../stores/discovery";
import { displayTitle, downloads, openPlayback, shortAnimeTitle } from "../stores/downloads";
import { favorites } from "../stores/favorites";
import { openFilePlayback, setFileSource } from "../stores/files";
import { toastError, updateFeedback } from "../stores/toasts";

type HistoryFilter = "all" | "watching" | "completed";

interface HistorySection {
  key: string;
  label: string;
  items: WatchHistoryItem[];
}

const FILTERS: Array<{ key: HistoryFilter; label: string }> = [
  { key: "all", label: "全部" },
  { key: "watching", label: "未看完" },
  { key: "completed", label: "已看完" }
];

const activeFilter = ref<HistoryFilter>("all");
const query = ref("");
const confirmingClear = ref(false);

onMounted(() => {
  bumpPlaybackHistoryVersion();
});

const knownAnimeByTitle = computed(() => {
  const map = new Map<string, { animeId: string; imageUrl: string | null }>();
  for (const fav of favorites.value) {
    if (fav.title) {
      map.set(fav.title.trim().toLowerCase(), { animeId: fav.animeId, imageUrl: fav.imageUrl ?? null });
    }
  }
  for (const item of homepageAnime.value) {
    const key = item.title?.trim().toLowerCase();
    if (key && !map.has(key)) {
      map.set(key, { animeId: item.animeId, imageUrl: item.imageUrl ?? null });
    }
  }
  return map;
});

const knownAnimeById = computed(() => {
  const map = new Map<string, { title: string; imageUrl: string | null }>();
  for (const fav of favorites.value) {
    map.set(fav.animeId, { title: fav.title, imageUrl: fav.imageUrl ?? null });
  }
  for (const item of homepageAnime.value) {
    if (!map.has(item.animeId)) {
      map.set(item.animeId, { title: item.title, imageUrl: item.imageUrl ?? null });
    }
  }
  return map;
});

const allEntries = computed<WatchHistoryItem[]>(() => {
  void playbackHistoryVersion.value;
  const rawList = watchHistoryEntries(downloads.value, {
    shortTitle: shortAnimeTitle,
    fullTitle: displayTitle
  });
  return rawList.map((entry) => {
    if (entry.imageUrl && entry.animeId) {
      return entry;
    }
    const byId = entry.animeId ? knownAnimeById.value.get(entry.animeId) : undefined;
    const byTitle = knownAnimeByTitle.value.get(entry.title.trim().toLowerCase());
    const fallbackImage = entry.imageUrl || byId?.imageUrl || byTitle?.imageUrl || null;
    const fallbackAnimeId = entry.animeId || byTitle?.animeId || null;
    if (fallbackImage === entry.imageUrl && fallbackAnimeId === entry.animeId) {
      return entry;
    }
    return {
      ...entry,
      imageUrl: fallbackImage,
      animeId: fallbackAnimeId
    };
  });
});

const filterCounts = computed<Record<HistoryFilter, number>>(() => {
  let watching = 0;
  let completed = 0;
  for (const item of allEntries.value) {
    if (item.completed) {
      completed += 1;
    } else {
      watching += 1;
    }
  }
  return {
    all: allEntries.value.length,
    watching,
    completed
  };
});

const filteredEntries = computed<WatchHistoryItem[]>(() => {
  const kw = query.value.trim().toLowerCase();
  return allEntries.value.filter((item) => {
    if (activeFilter.value === "watching" && item.completed) {
      return false;
    }
    if (activeFilter.value === "completed" && !item.completed) {
      return false;
    }
    if (!kw) {
      return true;
    }
    return (
      item.title.toLowerCase().includes(kw) ||
      item.rawTitle.toLowerCase().includes(kw) ||
      Boolean(item.episodeLabel && item.episodeLabel.toLowerCase().includes(kw)) ||
      Boolean(item.subgroup && item.subgroup.toLowerCase().includes(kw)) ||
      item.sourceLabel.toLowerCase().includes(kw)
    );
  });
});

function startOfLocalDay(ts: number): number {
  const d = new Date(ts);
  d.setHours(0, 0, 0, 0);
  return d.getTime();
}

const visibleGroups = computed<HistorySection[]>(() => {
  const now = Date.now();
  const todayStart = startOfLocalDay(now);
  const yesterdayStart = todayStart - 24 * 3600 * 1000;
  const weekStart = todayStart - 6 * 24 * 3600 * 1000;

  const buckets: Record<"today" | "yesterday" | "week" | "older", WatchHistoryItem[]> = {
    today: [],
    yesterday: [],
    week: [],
    older: []
  };

  for (const item of filteredEntries.value) {
    const ts = item.updatedAt || 0;
    if (ts >= todayStart) {
      buckets.today.push(item);
    } else if (ts >= yesterdayStart) {
      buckets.yesterday.push(item);
    } else if (ts >= weekStart) {
      buckets.week.push(item);
    } else {
      buckets.older.push(item);
    }
  }

  const sections: HistorySection[] = [
    { key: "today", label: "今天", items: buckets.today },
    { key: "yesterday", label: "昨天", items: buckets.yesterday },
    { key: "week", label: "近 7 天", items: buckets.week },
    { key: "older", label: "更早", items: buckets.older }
  ];
  return sections.filter((section) => section.items.length > 0);
});

function formatDurationClock(seconds: number): string {
  const safe = Math.max(0, Math.floor(seconds));
  const hours = Math.floor(safe / 3600);
  const minutes = Math.floor((safe % 3600) / 60);
  const rest = safe % 60;
  if (hours > 0) {
    return `${hours}:${String(minutes).padStart(2, "0")}:${String(rest).padStart(2, "0")}`;
  }
  return `${String(minutes).padStart(2, "0")}:${String(rest).padStart(2, "0")}`;
}

function formatWatchedAt(ts: number): string {
  if (!ts || !Number.isFinite(ts)) {
    return "";
  }
  const diffSec = Math.floor((Date.now() - ts) / 1000);
  if (diffSec >= 0 && diffSec < 60) {
    return "刚刚";
  }
  const date = new Date(ts);
  if (Number.isNaN(date.getTime())) {
    return "";
  }
  const now = new Date();
  const hh = String(date.getHours()).padStart(2, "0");
  const mm = String(date.getMinutes()).padStart(2, "0");
  const todayStart = startOfLocalDay(now.getTime());
  const yesterdayStart = todayStart - 24 * 3600 * 1000;

  if (ts >= todayStart) {
    return `${hh}:${mm}`;
  }
  if (ts >= yesterdayStart) {
    return `昨天 ${hh}:${mm}`;
  }
  const month = String(date.getMonth() + 1).padStart(2, "0");
  const day = String(date.getDate()).padStart(2, "0");
  if (date.getFullYear() === now.getFullYear()) {
    return `${month}-${day} ${hh}:${mm}`;
  }
  return `${date.getFullYear()}-${month}-${day}`;
}

function formatMetaLine(item: WatchHistoryItem): string {
  const parts: Array<string | null | undefined> = [
    item.episodeLabel,
    item.subgroup,
    item.resolution,
    !item.subgroup && item.folderLabel ? item.folderLabel : null,
    formatWatchedAt(item.updatedAt)
  ];
  return parts.filter(Boolean).join(" · ");
}

function sourcePillClass(kind: WatchHistoryItem["sourceKind"]): string {
  if (kind === "cloud-file" || kind === "cloud-download") {
    return "status-pill-accent";
  }
  if (kind === "removed") {
    return "status-pill-warn";
  }
  return "status-pill-muted";
}

function posterPaletteStyle(seed: string): Record<string, string> {
  let hash = 0;
  for (let i = 0; i < seed.length; i += 1) {
    hash = (hash * 31 + seed.charCodeAt(i)) | 0;
  }
  const hue = Math.abs(hash) % 360;
  return {
    background: `linear-gradient(145deg, hsl(${hue} 42% 34%), hsl(${(hue + 28) % 360} 48% 22%))`
  };
}

function coverMonogram(title: string): string {
  const clean = title.replace(/[\s[\]【】()（）]/gu, "");
  return clean.slice(0, 2).toUpperCase() || "番";
}

function handlePlayItem(item: WatchHistoryItem): void {
  if (item.download) {
    selectTab("downloads");
    void openPlayback(item.download);
    return;
  }
  if (item.filePlayback) {
    setFileSource(item.filePlayback.source);
    selectTab("files");
    void openFilePlayback(item.filePlayback.entry, {
      source: item.filePlayback.source,
      folderLabel: item.filePlayback.folderLabel
    });
    return;
  }
  void handleOpenAnimeDetail(item);
}

async function handleOpenAnimeDetail(item: WatchHistoryItem): Promise<void> {
  if (item.download) {
    await openDownloadAnimeDetail(item.download);
    return;
  }
  if (item.animeId) {
    const stub: HomepageAnime = {
      animeId: item.animeId,
      bangumiId: item.animeId.startsWith("bgm-") ? item.animeId.slice(4) : null,
      title: item.title,
      imageUrl: item.imageUrl,
      searchKeyword: item.title,
      airDay: null,
      isOnAir: true,
      isRestricted: false,
      rating: null,
      source: "bangumi",
      mikanId: null,
      officialSite: null
    };
    await openAnimeDetail(stub);
    return;
  }

  const keyword = item.title.trim();
  if (!keyword) {
    return;
  }
  await withBusy(`history-detail:${item.id}`, async () => {
    try {
      const result = await searchAnime(keyword);
      const first = result.items[0];
      if (!first) {
        updateFeedback(`没有找到与「${keyword}」匹配的番剧详情`, "info");
        return;
      }
      await openAnimeDetail(first);
    } catch (error) {
      toastError(error, "查询番剧详情失败");
    }
  });
}

function handleRemoveItem(item: WatchHistoryItem): void {
  removePlaybackHistoryKeys(item.keys);
  updateFeedback(`已移除「${item.title}」的观看记录`, "info");
}

function handleClearAll(): void {
  clearAllPlaybackHistory();
  confirmingClear.value = false;
  updateFeedback("已清空全部观看记录", "success");
}
</script>

<template>
  <AnimeDetailDrawer>
    <template #main>
      <div class="page-shell">
        <div v-if="allEntries.length" class="page-toolbar">
          <div class="toolbar-row">
            <div class="toolbar-chips" role="group" aria-label="按观看状态筛选">
              <button
                v-for="filter in FILTERS"
                :key="filter.key"
                type="button"
                class="chip"
                :class="{ 'chip-active': activeFilter === filter.key }"
                :aria-pressed="activeFilter === filter.key"
                @click="activeFilter = filter.key"
              >
                {{ filter.label }}
                <span class="chip-count">{{ filterCounts[filter.key] }}</span>
              </button>
            </div>
            <span class="toolbar-meta">共 {{ allEntries.length }} 条</span>
          </div>

          <div class="toolbar-row">
            <label class="search-field min-w-0">
              <AppNavIcon name="search" aria-hidden="true" />
              <input
                v-model="query"
                type="search"
                placeholder="搜索历史…"
                aria-label="在观看历史里搜索"
              />
            </label>

            <template v-if="confirmingClear">
              <button
                type="button"
                class="btn-danger shrink-0 px-3 py-1.5 text-xs font-semibold"
                @click="handleClearAll"
              >
                确认清空
              </button>
              <button
                type="button"
                class="btn-ghost shrink-0 px-2.5 py-1.5 text-xs font-semibold"
                @click="confirmingClear = false"
              >
                取消
              </button>
            </template>
            <button
              v-else
              type="button"
              class="btn-ghost shrink-0 px-3 py-1.5 text-xs font-semibold text-[var(--muted)] hover:text-[var(--err)]"
              @click="confirmingClear = true"
            >
              清空记录
            </button>
          </div>
        </div>

        <div class="page-body" :class="{ 'row-span-2': !allEntries.length }">
          <div v-if="!allEntries.length" class="empty-state h-full">
            <div class="grid gap-2.5">
              <span class="empty-icon">
                <AppNavIcon name="history" class="size-6" aria-hidden="true" />
              </span>
              <p class="text-sm font-semibold text-[var(--ink)]">暂无观看记录</p>
            </div>
          </div>

          <p v-else-if="!visibleGroups.length" class="empty-state text-sm text-[var(--muted)]">
            无匹配记录
          </p>

          <div v-else class="grid gap-6 py-2">
            <section
              v-for="section in visibleGroups"
              :key="section.key"
              class="grid gap-2"
              :aria-labelledby="`history-sec-${section.key}`"
            >
              <h2 :id="`history-sec-${section.key}`" class="history-section-title">
                {{ section.label }}
                <span class="tabular-nums">{{ section.items.length }}</span>
              </h2>

              <div class="grid gap-1">
                <article
                  v-for="item in section.items"
                  :key="item.id"
                  class="history-row"
                  @click="handlePlayItem(item)"
                >
                  <div class="history-poster" :style="!item.imageUrl ? posterPaletteStyle(item.id) : undefined">
                    <img
                      v-if="item.imageUrl"
                      :src="item.imageUrl"
                      :alt="item.title"
                      class="absolute inset-0 size-full object-cover"
                      loading="lazy"
                    />
                    <div v-else class="absolute inset-0 grid place-items-center px-1 text-center text-white/90">
                      <AppNavIcon
                        v-if="item.filePlayback"
                        name="file-video"
                        class="size-5 opacity-90"
                        aria-hidden="true"
                      />
                      <span v-else class="text-xs font-semibold">{{ coverMonogram(item.title) }}</span>
                    </div>
                  </div>

                  <div class="grid min-w-0 flex-1 gap-1">
                    <div class="flex min-w-0 items-center gap-2">
                      <strong
                        class="truncate text-[15px] font-semibold leading-snug text-[var(--ink)]"
                        :title="item.rawTitle"
                      >
                        {{ item.title }}
                      </strong>
                      <span class="status-pill shrink-0" :class="sourcePillClass(item.sourceKind)">
                        {{ item.sourceLabel }}
                      </span>
                    </div>

                    <p class="truncate text-xs text-[var(--muted)]" :title="item.rawTitle">
                      {{ formatMetaLine(item) }}
                    </p>

                    <div class="flex min-w-0 items-center gap-2.5">
                      <span class="progress-track w-24 shrink-0 sm:w-32">
                        <span
                          class="progress-fill"
                          :class="{ '!bg-[var(--ok)]': item.completed }"
                          :style="{ width: `${item.progressPercent}%` }"
                        />
                      </span>
                      <span class="min-w-0 truncate text-xs tabular-nums text-[var(--muted)]">
                        <template v-if="item.completed">
                          <span class="font-semibold text-[var(--ok)]">已看完</span>
                        </template>
                        <template v-else>
                          {{ formatDurationClock(item.currentTime) }} / {{ formatDurationClock(item.duration) }}
                        </template>
                      </span>
                    </div>
                  </div>

                  <div class="flex shrink-0 items-center gap-1" @click.stop>
                    <button
                      type="button"
                      class="icon-btn reveal-on-hover hover:!border-[var(--err-soft)] hover:!bg-[var(--err-soft)] hover:!text-[var(--err)]"
                      :aria-label="`移除 ${item.title} 的观看记录`"
                      title="移除此条记录"
                      @click="handleRemoveItem(item)"
                    >
                      <AppNavIcon name="trash" aria-hidden="true" />
                    </button>

                    <button
                      type="button"
                      class="icon-btn reveal-on-hover"
                      :disabled="isBusy(`history-detail:${item.id}`)"
                      :aria-label="`查看 ${item.title} 的番剧详情`"
                      title="番剧详情"
                      @click="handleOpenAnimeDetail(item)"
                    >
                      <AppNavIcon name="info" aria-hidden="true" />
                    </button>

                    <button
                      v-if="item.playable"
                      type="button"
                      class="history-play-pill"
                      :aria-label="`${item.completed ? '重新播放' : '继续播放'} ${item.title} ${item.episodeLabel ?? ''}`"
                      :title="item.completed ? '重新播放' : '继续观看'"
                      @click="handlePlayItem(item)"
                    >
                      <AppNavIcon name="play" class="size-3 shrink-0 fill-current" aria-hidden="true" />
                      <span class="max-sm:sr-only">{{ item.completed ? "重播" : "继续" }}</span>
                    </button>
                  </div>
                </article>
              </div>
            </section>
          </div>
        </div>
      </div>
    </template>
  </AnimeDetailDrawer>
</template>

<style scoped>
.history-section-title {
  display: flex;
  align-items: baseline;
  gap: 6px;
  padding: 0 4px;
  color: var(--ink);
  font-size: 14px;
  font-weight: 650;
}

.history-section-title > span {
  color: var(--muted);
  font-size: 12px;
  font-weight: 600;
}

.history-row {
  display: flex;
  min-width: 0;
  align-items: center;
  gap: 12px;
  padding: 10px 12px;
  border-radius: var(--radius-lg);
  border: 1px solid transparent;
  cursor: pointer;
  transition: background-color 150ms ease, border-color 150ms ease;
}

.history-row:hover {
  background: var(--tint-2);
  border-color: color-mix(in srgb, var(--line) 75%, transparent);
}

.history-poster {
  position: relative;
  width: 52px;
  height: 70px;
  flex-shrink: 0;
  overflow: hidden;
  border-radius: 10px;
  background: var(--panel);
  box-shadow: var(--soft-shadow);
}

.history-play-pill {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 4px;
  height: 30px;
  padding: 0 11px;
  border-radius: 999px;
  border: 1px solid var(--accent-line);
  background: color-mix(in srgb, var(--accent-soft) 72%, var(--card));
  color: var(--accent-vivid);
  font-size: 12px;
  font-weight: 600;
  white-space: nowrap;
  transition: background-color 150ms ease, border-color 150ms ease, color 150ms ease, transform 150ms ease;
}

.history-play-pill:hover {
  border-color: var(--accent);
  background: var(--accent);
  color: var(--accent-contrast);
}

.history-play-pill:active {
  transform: scale(0.96);
}

@media (max-width: 639px) {
  .history-play-pill {
    width: 32px;
    height: 32px;
    padding: 0;
  }
}

@media (max-width: 520px) {
  .history-row {
    padding: 8px 8px;
    gap: 10px;
  }

  .history-poster {
    width: 46px;
    height: 62px;
    border-radius: 8px;
  }
}
</style>
