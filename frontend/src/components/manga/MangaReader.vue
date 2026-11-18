<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from "vue";

import type { MangaChapter, MangaPagesResult } from "@shared/index";

import { getMangaChapterPages, resolveMangaImageUrl } from "../../api";
import { toastError } from "../../stores/toasts";
import AppNavIcon from "../layout/AppNavIcon.vue";

const props = defineProps<{
  mangaId: string;
  mangaTitle: string;
  chapter: MangaChapter;
  prevChapter: MangaChapter | null;
  nextChapter: MangaChapter | null;
  initialPage?: number;
}>();

const emit = defineEmits<{
  (event: "close"): void;
  (event: "switch-chapter", chapter: MangaChapter): void;
  (event: "progress", payload: { pageIndex: number; totalPages: number }): void;
}>();

const loading = ref(true);
const error = ref("");
const pagesData = ref<MangaPagesResult | null>(null);
const quality = ref<"data" | "saver">("data");
const readMode = ref<"scroll" | "page">("scroll");
const currentPage = ref(0);
const showHud = ref(true);
const scrollHostRef = ref<HTMLElement | null>(null);

const imageUrls = computed(() => {
  const data = pagesData.value;
  if (!data) {
    return [];
  }
  const raw = quality.value === "saver" && data.saverPages.length ? data.saverPages : data.pages;
  return raw.map((u) => resolveMangaImageUrl(u));
});

const totalPages = computed(() => imageUrls.value.length);

let progressTimer: number | null = null;
function scheduleProgressEmit(): void {
  if (progressTimer !== null) {
    window.clearTimeout(progressTimer);
  }
  progressTimer = window.setTimeout(() => {
    progressTimer = null;
    if (totalPages.value > 0) {
      emit("progress", { pageIndex: currentPage.value, totalPages: totalPages.value });
    }
  }, 450);
}

async function loadChapterPages(): Promise<void> {
  loading.value = true;
  error.value = "";
  pagesData.value = null;
  try {
    const res = await getMangaChapterPages(props.chapter.id);
    pagesData.value = res;
    const maxIdx = Math.max(0, (res.pages.length || res.saverPages.length) - 1);
    currentPage.value = Math.min(Math.max(0, props.initialPage ?? 0), maxIdx);
    scheduleProgressEmit();
  } catch (err) {
    error.value = err instanceof Error ? err.message : "加载章节图片失败";
    toastError(err, "加载章节图片失败");
  } finally {
    loading.value = false;
  }
}

function goPage(nextIdx: number): void {
  if (totalPages.value <= 0) {
    return;
  }
  if (nextIdx < 0) {
    if (props.prevChapter) {
      emit("switch-chapter", props.prevChapter);
    }
    return;
  }
  if (nextIdx >= totalPages.value) {
    if (props.nextChapter) {
      emit("switch-chapter", props.nextChapter);
    }
    return;
  }
  currentPage.value = nextIdx;
  scrollHostRef.value?.scrollTo({ top: 0 });
  scheduleProgressEmit();
}

function handlePageStageClick(event: MouseEvent): void {
  if (readMode.value === "scroll") {
    showHud.value = !showHud.value;
    return;
  }
  const target = event.currentTarget as HTMLElement | null;
  if (!target) {
    return;
  }
  const rect = target.getBoundingClientRect();
  const ratio = (event.clientX - rect.left) / Math.max(1, rect.width);
  if (ratio < 0.3) {
    goPage(currentPage.value - 1);
  } else if (ratio > 0.7) {
    goPage(currentPage.value + 1);
  } else {
    showHud.value = !showHud.value;
  }
}

function handleScrollTrack(): void {
  if (readMode.value !== "scroll" || !scrollHostRef.value || totalPages.value <= 0) {
    return;
  }
  const host = scrollHostRef.value;
  const imgs = host.querySelectorAll<HTMLElement>("[data-page-idx]");
  const midY = host.getBoundingClientRect().top + host.clientHeight * 0.35;
  let active = 0;
  for (const el of imgs) {
    const r = el.getBoundingClientRect();
    if (r.top <= midY) {
      active = Number(el.dataset.pageIdx || 0);
    } else {
      break;
    }
  }
  if (active !== currentPage.value) {
    currentPage.value = active;
    scheduleProgressEmit();
  }
}

function handleKeydown(event: KeyboardEvent): void {
  if (event.key === "Escape") {
    emit("close");
    return;
  }
  if (readMode.value === "page") {
    if (event.key === "ArrowLeft") {
      event.preventDefault();
      goPage(currentPage.value - 1);
    } else if (event.key === "ArrowRight" || event.key === " ") {
      event.preventDefault();
      goPage(currentPage.value + 1);
    }
  }
}

watch(
  () => props.chapter.id,
  () => {
    void loadChapterPages();
  },
  { immediate: true }
);

onMounted(() => {
  window.addEventListener("keydown", handleKeydown);
});

onBeforeUnmount(() => {
  window.removeEventListener("keydown", handleKeydown);
  if (progressTimer !== null) {
    window.clearTimeout(progressTimer);
  }
});
</script>

<template>
  <div class="manga-reader fixed inset-0 z-[180] grid grid-rows-[auto_minmax(0,1fr)_auto] bg-[#090b10] text-white">
    <header v-show="showHud" class="reader-bar reader-top">
      <button type="button" class="reader-icon-btn" aria-label="返回章节列表" title="返回章节列表" @click="emit('close')">
        <AppNavIcon name="chevron-left" class="size-5" aria-hidden="true" />
      </button>
      <div class="min-w-0 flex-1">
        <strong class="block truncate text-sm font-semibold text-white">{{ mangaTitle }}</strong>
        <span class="block truncate text-xs text-white/65">{{ chapter.label }}</span>
      </div>
      <div class="flex shrink-0 items-center gap-1.5">
        <button
          type="button"
          class="reader-pill"
          @click="readMode = readMode === 'scroll' ? 'page' : 'scroll'"
        >
          {{ readMode === "scroll" ? "条漫" : "翻页" }}
        </button>
        <button
          type="button"
          class="reader-pill"
          @click="quality = quality === 'data' ? 'saver' : 'data'"
        >
          {{ quality === "data" ? "原图" : "省流" }}
        </button>
      </div>
    </header>

    <div
      ref="scrollHostRef"
      class="relative min-h-0 overflow-y-auto overflow-x-hidden select-none"
      :class="!showHud ? 'row-span-3' : ''"
      @scroll.passive="handleScrollTrack"
      @click="handlePageStageClick"
    >
      <div v-if="loading" class="grid h-full min-h-[50vh] place-items-center text-sm text-white/70">
        正在加载漫画页面…
      </div>
      <div v-else-if="error" class="grid h-full min-h-[50vh] place-items-center px-6 text-center">
        <div class="grid gap-3">
          <p class="text-sm text-red-400">{{ error }}</p>
          <button type="button" class="reader-pill mx-auto" @click.stop="loadChapterPages">重试</button>
        </div>
      </div>

      <template v-else-if="readMode === 'scroll'">
        <div class="mx-auto grid max-w-[820px] gap-1 pb-20">
          <img
            v-for="(src, idx) in imageUrls"
            :key="`${chapter.id}:${idx}:${quality}`"
            :data-page-idx="idx"
            :src="src"
            :alt="`第 ${idx + 1} 页`"
            class="block w-full bg-black/40"
            loading="lazy"
            decoding="async"
          />
        </div>
      </template>

      <template v-else>
        <div class="grid h-full min-h-full place-items-center px-2 py-3">
          <img
            v-if="imageUrls[currentPage]"
            :src="imageUrls[currentPage]"
            :alt="`第 ${currentPage + 1} 页`"
            class="max-h-full max-w-full object-contain"
          />
        </div>
      </template>
    </div>

    <footer v-show="showHud" class="reader-bar reader-bottom">
      <button
        type="button"
        class="reader-pill"
        :disabled="!prevChapter"
        @click.stop="prevChapter && emit('switch-chapter', prevChapter)"
      >
        上一话
      </button>
      <span class="text-xs font-semibold tabular-nums text-white/85">
        {{ totalPages > 0 ? `${currentPage + 1} / ${totalPages}` : "—" }}
      </span>
      <button
        type="button"
        class="reader-pill"
        :disabled="!nextChapter"
        @click.stop="nextChapter && emit('switch-chapter', nextChapter)"
      >
        下一话
      </button>
    </footer>
  </div>
</template>

<style scoped>
.reader-bar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
  padding: 8px 14px;
  background: rgb(12 15 22 / 0.88);
  backdrop-filter: blur(10px);
  border-color: rgb(255 255 255 / 0.1);
}

.reader-top {
  border-bottom-width: 1px;
  padding-top: max(8px, env(safe-area-inset-top));
}

.reader-bottom {
  border-top-width: 1px;
  padding-bottom: max(10px, env(safe-area-inset-bottom));
}

.reader-icon-btn {
  display: inline-grid;
  place-items: center;
  width: 40px;
  height: 40px;
  flex-shrink: 0;
  border-radius: 10px;
  background: rgb(255 255 255 / 0.08);
  color: #fff;
}

.reader-pill {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  min-height: 36px;
  padding: 0 13px;
  border-radius: 999px;
  border: 1px solid rgb(255 255 255 / 0.16);
  background: rgb(255 255 255 / 0.08);
  color: #fff;
  font-size: 12.5px;
  font-weight: 600;
  white-space: nowrap;
}

.reader-pill:disabled {
  opacity: 0.35;
  cursor: not-allowed;
}
</style>
