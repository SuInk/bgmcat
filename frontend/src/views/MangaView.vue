<script setup lang="ts">
import { computed, onMounted, ref } from "vue";

import type { MangaChapter, MangaDetailResult, MangaReadProgress, MangaSeries } from "@shared/index";

import {
  deleteMangaProgress,
  discoverManga,
  getMangaDetail,
  listMangaProgress,
  resolveMangaImageUrl,
  saveMangaProgress
} from "../api";
import AppNavIcon from "../components/layout/AppNavIcon.vue";
import MangaReader from "../components/manga/MangaReader.vue";
import { toastError } from "../stores/toasts";

const searchQuery = ref("");
const sortMode = ref<"latest" | "popular">("latest");
const langMode = ref<"zh" | "all">("zh");
const loadingList = ref(false);
const loadingMore = ref(false);
const listError = ref("");
const items = ref<MangaSeries[]>([]);
const total = ref(0);
const offset = ref(0);
const PAGE_SIZE = 24;

const progressList = ref<MangaReadProgress[]>([]);

const selectedDetail = ref<MangaDetailResult | null>(null);
const loadingDetail = ref(false);
const detailError = ref("");
const detailLang = ref<"zh" | "all">("zh");

const activeChapter = ref<MangaChapter | null>(null);
const activeInitialPage = ref(0);

const hasMore = computed(() => items.value.length < total.value);
const sortedItems = computed(() =>
  [...items.value].sort((a, b) => (a.coverUrl ? 0 : 1) - (b.coverUrl ? 0 : 1))
);

const orderedChapters = computed(() => selectedDetail.value?.chapters ?? []);
const currentChapterIndex = computed(() =>
  activeChapter.value ? orderedChapters.value.findIndex((c) => c.id === activeChapter.value?.id) : -1
);
// chapters are sorted newest-first (desc), so "下一话" is index - 1, "上一话" is index + 1
const nextChapter = computed(() =>
  currentChapterIndex.value > 0 ? orderedChapters.value[currentChapterIndex.value - 1] ?? null : null
);
const prevChapter = computed(() =>
  currentChapterIndex.value >= 0 && currentChapterIndex.value + 1 < orderedChapters.value.length
    ? orderedChapters.value[currentChapterIndex.value + 1] ?? null
    : null
);

const currentMangaProgress = computed(() =>
  selectedDetail.value ? progressList.value.find((p) => p.mangaId === selectedDetail.value?.manga.id) ?? null : null
);

function statusLabel(status: string): string {
  switch (status) {
    case "ongoing":
      return "连载中";
    case "completed":
      return "已完结";
    case "hiatus":
      return "休刊";
    case "cancelled":
      return "已取消";
    default:
      return status || "漫画";
  }
}

async function fetchDiscover(reset = true): Promise<void> {
  if (reset) {
    loadingList.value = true;
    offset.value = 0;
  } else {
    loadingMore.value = true;
  }
  listError.value = "";
  try {
    const res = await discoverManga({
      q: searchQuery.value,
      sort: sortMode.value,
      lang: langMode.value,
      limit: PAGE_SIZE,
      offset: reset ? 0 : items.value.length
    });
    if (reset) {
      items.value = res.items;
    } else {
      const seen = new Set(items.value.map((i) => i.id));
      items.value = [...items.value, ...res.items.filter((i) => !seen.has(i.id))];
    }
    total.value = res.total;
    offset.value = items.value.length;
  } catch (err) {
    listError.value = err instanceof Error ? err.message : "加载漫画列表失败";
    toastError(err, "加载漫画列表失败");
  } finally {
    loadingList.value = false;
    loadingMore.value = false;
  }
}

async function fetchProgress(): Promise<void> {
  try {
    progressList.value = await listMangaProgress();
  } catch {
    // 非阻塞
  }
}

async function openManga(mangaId: string, lang: "zh" | "all" = langMode.value): Promise<void> {
  loadingDetail.value = true;
  detailError.value = "";
  detailLang.value = lang;
  try {
    selectedDetail.value = await getMangaDetail(mangaId, lang);
  } catch (err) {
    detailError.value = err instanceof Error ? err.message : "加载漫画详情失败";
    toastError(err, "加载漫画详情失败");
  } finally {
    loadingDetail.value = false;
  }
}

function startChapter(chapter: MangaChapter, startPage = 0): void {
  activeInitialPage.value = startPage;
  activeChapter.value = chapter;
}

function resumeReading(): void {
  const prog = currentMangaProgress.value;
  if (prog) {
    const found = orderedChapters.value.find((c) => c.id === prog.chapterId);
    if (found) {
      startChapter(found, prog.pageIndex);
      return;
    }
  }
  const firstReadable = orderedChapters.value[orderedChapters.value.length - 1] ?? orderedChapters.value[0];
  if (firstReadable) {
    startChapter(firstReadable, 0);
  }
}

async function handleReaderProgress(payload: { pageIndex: number; totalPages: number }): Promise<void> {
  const detail = selectedDetail.value;
  const ch = activeChapter.value;
  if (!detail || !ch) {
    return;
  }
  try {
    const saved = await saveMangaProgress({
      mangaId: detail.manga.id,
      title: detail.manga.title,
      coverUrl: detail.manga.coverUrl,
      chapterId: ch.id,
      chapterLabel: ch.label,
      pageIndex: payload.pageIndex,
      totalPages: payload.totalPages
    });
    const idx = progressList.value.findIndex((p) => p.mangaId === saved.mangaId);
    if (idx >= 0) {
      const next = [...progressList.value];
      next[idx] = saved;
      progressList.value = next;
    } else {
      progressList.value = [saved, ...progressList.value];
    }
  } catch {
    // 静默忽略偶发保存失败
  }
}

async function removeProgressItem(mangaId: string): Promise<void> {
  try {
    await deleteMangaProgress(mangaId);
    progressList.value = progressList.value.filter((p) => p.mangaId !== mangaId);
  } catch (err) {
    toastError(err, "移除阅读记录失败");
  }
}

onMounted(() => {
  void Promise.all([fetchDiscover(true), fetchProgress()]);
});
</script>

<template>
  <section class="page-shell">
    <!-- 1. 详情页（选中漫画后展示） -->
    <template v-if="selectedDetail || loadingDetail">
      <div class="manga-detail-scroll min-h-0 overflow-y-auto pb-6 max-md:pb-[max(5.5rem,calc(env(safe-area-inset-bottom)+4.5rem))]">
        <div class="mb-3 flex items-center justify-between gap-2 border-b border-[var(--line)] pb-2">
          <button
            type="button"
            class="btn-ghost min-h-[38px] px-3 py-1.5 text-sm font-semibold"
            @click="selectedDetail = null; detailError = ''"
          >
            <AppNavIcon name="chevron-left" class="size-4" aria-hidden="true" />
            <span>返回漫画列表</span>
          </button>
          <div v-if="selectedDetail" class="flex items-center gap-1.5">
            <button
              type="button"
              class="chip px-3 py-1 text-xs"
              :class="{ 'chip-active': detailLang === 'zh' }"
              @click="openManga(selectedDetail.manga.id, 'zh')"
            >
              仅中文
            </button>
            <button
              type="button"
              class="chip px-3 py-1 text-xs"
              :class="{ 'chip-active': detailLang === 'all' }"
              @click="openManga(selectedDetail.manga.id, 'all')"
            >
              全部语言
            </button>
          </div>
        </div>

        <div v-if="loadingDetail && !selectedDetail" class="py-12 text-center text-sm text-[var(--muted)]">
          正在加载漫画详情…
        </div>
        <div v-else-if="detailError" class="rounded-xl border border-[var(--err-soft)] bg-[var(--err-soft)] p-4 text-sm text-[var(--err)]">
          {{ detailError }}
        </div>
        <template v-else-if="selectedDetail">
          <header class="mb-4 grid grid-cols-[96px_minmax(0,1fr)] gap-4 sm:grid-cols-[120px_minmax(0,1fr)]">
            <div class="relative w-full aspect-[3/4] overflow-hidden rounded-xl border border-[var(--line)] bg-[var(--panel)]">
              <img
                v-if="selectedDetail.manga.coverUrl"
                :src="resolveMangaImageUrl(selectedDetail.manga.coverUrl)"
                :alt="selectedDetail.manga.title"
                class="absolute inset-0 size-full object-cover"
              />
            </div>
            <div class="grid content-start gap-2 min-w-0">
              <h2 class="text-lg font-bold leading-snug text-[var(--ink)]">{{ selectedDetail.manga.title }}</h2>
              <p v-if="selectedDetail.manga.altTitle" class="truncate text-xs text-[var(--muted)]">
                {{ selectedDetail.manga.altTitle }}
              </p>
              <div class="flex flex-wrap items-center gap-2 text-xs text-[var(--ink-secondary)]">
                <span class="rounded-md bg-[var(--panel)] px-2 py-0.5 font-semibold">{{ statusLabel(selectedDetail.manga.status) }}</span>
                <span v-if="selectedDetail.manga.year">{{ selectedDetail.manga.year }}</span>
                <span v-if="selectedDetail.manga.authors.length">{{ selectedDetail.manga.authors.join(" / ") }}</span>
              </div>
              <div v-if="selectedDetail.manga.tags.length" class="flex flex-wrap gap-1.5">
                <span
                  v-for="tag in selectedDetail.manga.tags"
                  :key="tag"
                  class="rounded-full border border-[var(--line)] px-2.5 py-0.5 text-[11px] text-[var(--muted)]"
                >
                  {{ tag }}
                </span>
              </div>
              <div class="pt-1 flex flex-wrap items-center gap-2">
                <button
                  type="button"
                  class="btn-primary px-4 py-2 text-sm font-semibold"
                  :disabled="!orderedChapters.length"
                  @click="resumeReading"
                >
                  {{ currentMangaProgress ? `继续阅读 · ${currentMangaProgress.chapterLabel}` : "开始阅读" }}
                </button>
              </div>
            </div>
          </header>

          <p v-if="selectedDetail.manga.description" class="mb-4 rounded-xl border border-[var(--line)] bg-[var(--card)] p-3 text-xs leading-relaxed text-[var(--ink-secondary)]">
            {{ selectedDetail.manga.description }}
          </p>

          <section class="grid gap-2">
            <div class="flex items-center justify-between">
              <h3 class="text-sm font-semibold text-[var(--ink)]">章节列表（{{ orderedChapters.length }}）</h3>
              <span class="text-xs text-[var(--muted)]">点击章节直接在线阅读</span>
            </div>
            <div v-if="!orderedChapters.length" class="rounded-xl border border-dashed border-[var(--line)] py-8 text-center text-xs text-[var(--muted)]">
              当前语言下暂无可在线阅读章节，可切换右上角「全部语言」查看。
            </div>
            <div v-else class="grid gap-1.5 sm:grid-cols-2 lg:grid-cols-3">
              <button
                v-for="ch in orderedChapters"
                :key="ch.id"
                type="button"
                class="manga-ch-btn"
                :class="{ 'is-current': currentMangaProgress?.chapterId === ch.id }"
                @click="startChapter(ch, currentMangaProgress?.chapterId === ch.id ? currentMangaProgress.pageIndex : 0)"
              >
                <span class="min-w-0 flex-1 truncate text-left font-semibold">{{ ch.label }}</span>
                <span class="shrink-0 text-[11px] text-[var(--muted)]">
                  {{ ch.language.toUpperCase() }} · {{ ch.pages }}P
                </span>
              </button>
            </div>
          </section>
        </template>
      </div>
    </template>

    <!-- 2. 漫画发现与检索主页 -->
    <template v-else>
      <div class="page-toolbar">
        <div class="toolbar-row flex-wrap">
          <div class="flex flex-wrap items-center gap-1.5">
            <button
              type="button"
              class="chip px-3 py-1 text-xs"
              :class="{ 'chip-active': sortMode === 'latest' }"
              @click="sortMode = 'latest'; fetchDiscover(true)"
            >
              最近更新
            </button>
            <button
              type="button"
              class="chip px-3 py-1 text-xs"
              :class="{ 'chip-active': sortMode === 'popular' }"
              @click="sortMode = 'popular'; fetchDiscover(true)"
            >
              热门连载
            </button>
            <button
              type="button"
              class="chip px-3 py-1 text-xs"
              :class="{ 'chip-active': langMode === 'zh' }"
              @click="langMode = langMode === 'zh' ? 'all' : 'zh'; fetchDiscover(true)"
            >
              {{ langMode === "zh" ? "仅看中文" : "全部语言" }}
            </button>
          </div>

          <form class="ml-auto flex min-w-[14rem] flex-1 items-center gap-2 sm:max-w-[24rem]" @submit.prevent="fetchDiscover(true)">
            <label class="search-field min-w-0 flex-1">
              <AppNavIcon name="search" aria-hidden="true" />
              <input v-model="searchQuery" type="search" aria-label="搜索漫画" placeholder="搜索漫画名（中/英/日）…" />
            </label>
            <button type="submit" class="btn-primary px-3.5 text-xs font-semibold">搜索</button>
          </form>
        </div>
      </div>

      <div class="min-h-0 overflow-y-auto pb-4 max-md:pb-[max(5.5rem,calc(env(safe-area-inset-bottom)+4.5rem))]">
        <!-- 最近阅读历史 -->
        <section v-if="progressList.length && !searchQuery.trim()" class="mb-5 grid gap-2">
          <h2 class="text-sm font-semibold text-[var(--ink)]">继续阅读</h2>
          <div class="flex gap-3 overflow-x-auto pb-1">
            <div
              v-for="prog in progressList"
              :key="prog.mangaId"
              class="group relative flex w-[13.5rem] shrink-0 items-center gap-2.5 rounded-xl border border-[var(--line)] bg-[var(--card)] p-2 transition hover:border-[var(--accent-line)]"
            >
              <button type="button" class="flex min-w-0 flex-1 items-center gap-2.5 text-left" @click="openManga(prog.mangaId)">
                <img
                  v-if="prog.coverUrl"
                  :src="resolveMangaImageUrl(prog.coverUrl)"
                  :alt="prog.title"
                  class="h-14 w-10 shrink-0 rounded-md object-cover bg-[var(--panel)]"
                />
                <span class="grid min-w-0 gap-0.5">
                  <strong class="truncate text-xs font-semibold text-[var(--ink)]">{{ prog.title }}</strong>
                  <span class="truncate text-[11px] text-[var(--accent-vivid)]">{{ prog.chapterLabel }}</span>
                  <span class="text-[10.5px] text-[var(--muted)]">第 {{ prog.pageIndex + 1 }} / {{ prog.totalPages }} 页</span>
                </span>
              </button>
              <button
                type="button"
                class="grid size-7 shrink-0 place-items-center rounded-md text-[var(--muted)] hover:bg-[var(--tint-2)] hover:text-[var(--ink)]"
                aria-label="移除阅读记录"
                title="移除记录"
                @click.stop="removeProgressItem(prog.mangaId)"
              >
                <AppNavIcon name="close" class="size-3.5" aria-hidden="true" />
              </button>
            </div>
          </div>
        </section>

        <div v-if="loadingList" class="py-12 text-center text-sm text-[var(--muted)]">
          正在加载漫画列表…
        </div>
        <div v-else-if="listError" class="rounded-xl border border-[var(--err-soft)] bg-[var(--err-soft)] p-4 text-sm text-[var(--err)]">
          {{ listError }}
        </div>
        <div v-else-if="!items.length" class="rounded-xl border border-dashed border-[var(--line)] py-12 text-center text-sm text-[var(--muted)]">
          未找到匹配的漫画，试试切换「全部语言」或换个关键词。
        </div>
        <template v-else>
          <div class="grid grid-cols-[repeat(auto-fill,minmax(8.5rem,1fr))] gap-3.5 sm:grid-cols-[repeat(auto-fill,minmax(10rem,1fr))]">
            <button
              v-for="item in sortedItems"
              :key="item.id"
              type="button"
              class="manga-card grid content-start gap-1.5 text-left"
              @click="openManga(item.id)"
            >
              <span class="relative block aspect-[3/4] w-full overflow-hidden rounded-2xl bg-[var(--panel)] shadow-sm">
                <img
                  v-if="item.coverUrl"
                  :src="resolveMangaImageUrl(item.coverUrl)"
                  :alt="item.title"
                  class="absolute inset-0 size-full object-cover transition duration-200"
                  loading="lazy"
                />
                <span v-else class="grid size-full place-items-center text-xl font-bold text-[var(--muted)]">
                  {{ item.title.slice(0, 1) || "漫" }}
                </span>
                <span
                  v-if="item.lastChapter"
                  class="absolute bottom-2 right-2 rounded-full bg-[#0f1320]/70 px-2 py-0.5 text-[10.5px] font-semibold text-white backdrop-blur"
                >
                  {{ item.lastChapter }} 话
                </span>
              </span>
              <strong class="truncate px-0.5 text-[13px] font-semibold text-[var(--ink)]" :title="item.title">
                {{ item.title }}
              </strong>
              <span class="truncate px-0.5 text-[11px] text-[var(--muted)]">
                {{ [statusLabel(item.status), item.year || null].filter(Boolean).join(" · ") }}
              </span>
            </button>
          </div>

          <div v-if="hasMore" class="mt-5 flex justify-center">
            <button type="button" class="btn-ghost px-5 py-2 text-xs font-semibold" :disabled="loadingMore" @click="fetchDiscover(false)">
              {{ loadingMore ? "加载中…" : "加载更多" }}
            </button>
          </div>
        </template>
      </div>
    </template>

    <MangaReader
      v-if="activeChapter && selectedDetail"
      :manga-id="selectedDetail.manga.id"
      :manga-title="selectedDetail.manga.title"
      :chapter="activeChapter"
      :prev-chapter="prevChapter"
      :next-chapter="nextChapter"
      :initial-page="activeInitialPage"
      @close="activeChapter = null"
      @switch-chapter="startChapter($event, 0)"
      @progress="handleReaderProgress"
    />
  </section>
</template>

<style scoped>
.manga-card:hover img {
  transform: scale(1.03);
}

.manga-ch-btn {
  display: flex;
  align-items: center;
  gap: 8px;
  min-height: 40px;
  padding: 0 12px;
  border-radius: 10px;
  border: 1px solid var(--line);
  background: var(--card);
  color: var(--ink);
  font-size: 12.5px;
  transition: border-color 0.14s, background 0.14s;
}

.manga-ch-btn:hover {
  border-color: var(--accent-line);
  background: var(--tint-1);
}

.manga-ch-btn.is-current {
  border-color: var(--accent);
  background: var(--accent-soft);
  color: var(--accent-vivid);
}
</style>
