<script setup lang="ts">
import { computed, defineAsyncComponent, onMounted, ref, watch } from "vue";

import type { DownloadPlaybackInfo, PlaybackSubtitleTrack } from "@shared/index";

import {
  ApiRequestError,
  getPublicShare,
  getPublicShareAssSubtitleUrl,
  getPublicShareDownloadUrl,
  getPublicShareHlsUrl,
  getPublicShareItemPlayback,
  getPublicShareItemProgress,
  getPublicShareStreamUrl,
  getPublicShareSubtitleUrl,
  unlockPublicShare,
  type PublicShareView,
  type ShareItemSummary
} from "../api";
import BrandLogo from "../components/common/BrandLogo.vue";
import AppNavIcon from "../components/layout/AppNavIcon.vue";
import { formatBytes } from "../lib/format";
import { setThemeMode, themeMode } from "../stores/theme";

const ArtPlayerPane = defineAsyncComponent(() => import("../components/downloads/ArtPlayerPane.vue"));

const props = defineProps<{
  code: string;
}>();

const loading = ref(true);
const pageError = ref<{ code?: string; message: string } | null>(null);
const share = ref<PublicShareView | null>(null);
const unlockToken = ref("");
const passcodeInput = ref("");
const unlocking = ref(false);
const unlockError = ref("");

const selectedItemId = ref<string>("");
const playback = ref<DownloadPlaybackInfo | null>(null);
const playbackLoading = ref(false);
const playbackError = ref("");

const storageTokenKey = computed(() => `bgmcat-share-st:${props.code}`);

function readCachedUnlockToken(): string {
  try {
    return window.sessionStorage.getItem(storageTokenKey.value) ?? "";
  } catch {
    return "";
  }
}

function saveCachedUnlockToken(token: string): void {
  unlockToken.value = token;
  try {
    if (token) {
      window.sessionStorage.setItem(storageTokenKey.value, token);
    } else {
      window.sessionStorage.removeItem(storageTokenKey.value);
    }
  } catch {
    // ignore storage restrictions
  }
}

function cycleTheme(): void {
  const isDark = document.documentElement.dataset.theme === "dark";
  setThemeMode(isDark ? "light" : "dark");
}

const isDarkTheme = computed(() => {
  void themeMode.value;
  return typeof document !== "undefined" && document.documentElement.dataset.theme === "dark";
});

const items = computed<ShareItemSummary[]>(() => share.value?.items ?? []);
const playableItems = computed(() => items.value.filter((item) => item.playable));
const nonPlayableItems = computed(() => items.value.filter((item) => !item.playable));

const selectedItem = computed<ShareItemSummary | null>(
  () => items.value.find((item) => item.id === selectedItemId.value) ?? null
);

const allItemsEpisodeLike = computed(
  () =>
    playableItems.value.length > 1 &&
    playableItems.value.every((item) => /^第\s*\d+(?:\.\d+)?\s*[集话話]$/u.test(item.label) || /^\d+(?:\.\d+)?$/u.test(item.label))
);

function episodeShortLabel(label: string): string {
  const match = label.match(/\d+(?:\.\d+)?/u);
  return match ? String(Number(match[0])) : label;
}

const expirySummary = computed(() => {
  const exp = share.value?.expiresAt;
  if (!exp) return "永久有效";
  const diffMs = new Date(exp).getTime() - Date.now();
  if (!Number.isFinite(diffMs) || diffMs <= 0) return "已过期";
  const hours = Math.ceil(diffMs / 3_600_000);
  if (hours < 24) return `${hours} 小时后到期`;
  return `${Math.ceil(hours / 24)} 天后到期`;
});

const shareMetaLine = computed(() => {
  if (!share.value) return "";
  const parts: string[] = [];
  if (share.value.itemCount > 1) {
    parts.push(`共 ${share.value.itemCount} 项`);
  } else if (selectedItem.value && selectedItem.value.size > 0) {
    parts.push(formatBytes(selectedItem.value.size));
  }
  if (share.value.itemCount > 1 && share.value.totalSize > 0) {
    parts.push(formatBytes(share.value.totalSize));
  }
  parts.push(expirySummary.value);
  if (!share.value.allowDownload) {
    parts.push("仅限在线观看");
  }
  return parts.join(" · ");
});

const streamUrl = computed(() => {
  const item = selectedItem.value;
  const pb = playback.value;
  if (!item || !pb) return "";
  if (pb.deliveryMode === "direct") {
    return getPublicShareStreamUrl(props.code, item.id, unlockToken.value);
  }
  if (pb.deliveryMode === "hls-remux" || pb.deliveryMode === "hls-transcode") {
    return getPublicShareHlsUrl(props.code, item.id, unlockToken.value);
  }
  return "";
});

function subtitleUrlFor(track: PlaybackSubtitleTrack): string {
  const item = selectedItem.value;
  if (!item) return "";
  const codec = track.codec?.toLowerCase() ?? "";
  return codec === "ass" || codec === "ssa"
    ? getPublicShareAssSubtitleUrl(props.code, item.id, track.id, unlockToken.value)
    : getPublicShareSubtitleUrl(props.code, item.id, track.id, unlockToken.value);
}

async function loadProgress() {
  const item = selectedItem.value;
  if (!item) {
    return { finished: true };
  }
  return getPublicShareItemProgress(props.code, item.id, unlockToken.value);
}

function downloadUrlForItem(item: ShareItemSummary): string {
  return getPublicShareDownloadUrl(props.code, item.id, unlockToken.value);
}

let playbackSeq = 0;

async function selectItem(item: ShareItemSummary): Promise<void> {
  selectedItemId.value = item.id;
  playbackError.value = "";
  playback.value = null;
  if (!item.playable) {
    playbackLoading.value = false;
    return;
  }
  const seq = ++playbackSeq;
  playbackLoading.value = true;
  try {
    const info = await getPublicShareItemPlayback(props.code, item.id, unlockToken.value);
    if (seq !== playbackSeq) return;
    playback.value = info;
  } catch (error) {
    if (seq !== playbackSeq) return;
    playbackError.value = error instanceof Error && error.message ? error.message : "加载播放信息失败";
  } finally {
    if (seq === playbackSeq) {
      playbackLoading.value = false;
    }
  }
}

function handlePlayEnded(): void {
  const list = playableItems.value;
  const idx = list.findIndex((item) => item.id === selectedItemId.value);
  const next = idx >= 0 ? list[idx + 1] : undefined;
  if (next) {
    void selectItem(next);
  }
}

function applyUnlockedShare(data: PublicShareView): void {
  share.value = data;
  if (data.unlockToken) {
    saveCachedUnlockToken(data.unlockToken);
  }
  document.title = data.title ? `${data.title} · 猫追番分享` : "猫追番 · 分享";
  if (!data.locked && data.items.length > 0) {
    const firstPlayable = data.items.find((it) => it.playable) ?? data.items[0]!;
    void selectItem(firstPlayable);
  }
}

async function loadShare(): Promise<void> {
  loading.value = true;
  pageError.value = null;
  unlockError.value = "";
  const cached = readCachedUnlockToken();
  unlockToken.value = cached;
  try {
    const data = await getPublicShare(props.code, cached, false);
    applyUnlockedShare(data);
  } catch (error) {
    if (error instanceof ApiRequestError) {
      pageError.value = { code: error.code, message: error.message };
    } else {
      pageError.value = { message: error instanceof Error ? error.message : "加载分享内容失败" };
    }
  } finally {
    loading.value = false;
  }
}

async function submitUnlock(): Promise<void> {
  const codeText = passcodeInput.value.trim();
  if (!codeText) {
    unlockError.value = "请输入提取码";
    return;
  }
  unlocking.value = true;
  unlockError.value = "";
  try {
    const data = await unlockPublicShare(props.code, codeText);
    applyUnlockedShare(data);
  } catch (error) {
    unlockError.value = error instanceof Error && error.message ? error.message : "提取码校验失败";
  } finally {
    unlocking.value = false;
  }
}

function fileIconName(item: ShareItemSummary): string {
  switch (item.kind) {
    case "video":
      return "file-video";
    case "audio":
      return "file-audio";
    case "subtitle":
      return "file-text";
    case "archive":
      return "file-archive";
    default:
      return "file";
  }
}

watch(
  () => props.code,
  () => {
    void loadShare();
  }
);

onMounted(() => {
  void loadShare();
});
</script>

<template>
  <div class="share-shell">
    <!-- 顶部极简栏 -->
    <header class="share-topbar">
      <div class="mx-auto flex h-12 w-full max-w-[78rem] items-center justify-between gap-3 px-3 sm:px-5">
        <div class="flex min-w-0 items-center gap-2.5">
          <BrandLogo :size="26" />
          <div class="min-w-0">
            <h1 class="truncate text-sm font-semibold text-[var(--ink)]">
              {{ share?.title || "猫追番 · 分享" }}
            </h1>
            <p v-if="share && !share.locked" class="truncate text-[11px] text-[var(--muted)] tabular-nums">
              {{ shareMetaLine }}
            </p>
          </div>
        </div>

        <div class="flex shrink-0 items-center gap-1.5">
          <a
            v-if="share && !share.locked && share.allowDownload && selectedItem"
            :href="downloadUrlForItem(selectedItem)"
            class="btn-ghost inline-flex h-8 items-center gap-1.5 px-2.5 text-xs font-semibold"
            download
          >
            <AppNavIcon name="download" class="size-3.5" aria-hidden="true" />
            <span>下载当前文件</span>
          </a>
          <button
            type="button"
            class="icon-btn"
            :title="isDarkTheme ? '切换浅色外观' : '切换深色外观'"
            :aria-label="isDarkTheme ? '切换浅色外观' : '切换深色外观'"
            @click="cycleTheme"
          >
            <AppNavIcon :name="isDarkTheme ? 'sun' : 'moon'" class="size-4" aria-hidden="true" />
          </button>
        </div>
      </div>
    </header>

    <!-- 主内容区 -->
    <main class="mx-auto w-full max-w-[78rem] flex-1 px-3 py-4 sm:px-5 sm:py-5">
      <!-- 1. 加载中 -->
      <div v-if="loading" class="grid min-h-[56dvh] place-items-center">
        <p class="text-xs font-medium text-[var(--muted)]">正在加载分享内容…</p>
      </div>

      <!-- 2. 失效或不存在 -->
      <div v-else-if="pageError" class="grid min-h-[56dvh] place-items-center">
        <div class="w-full max-w-sm rounded-xl border border-[var(--line)] bg-[var(--card)] p-5 text-center shadow-[var(--soft-shadow)]">
          <div class="mx-auto mb-2.5 grid size-9 place-items-center rounded-lg bg-[var(--panel)] text-[var(--muted)]">
            <AppNavIcon name="link" class="size-4" aria-hidden="true" />
          </div>
          <h2 class="text-sm font-semibold text-[var(--ink)]">
            {{ pageError.code === "SHARE_EXPIRED" ? "该分享已过期" : "分享链接不可用" }}
          </h2>
          <p class="mt-1 text-xs leading-relaxed text-[var(--muted)]">{{ pageError.message }}</p>
        </div>
      </div>

      <!-- 3. 提取码门禁 -->
      <div v-else-if="share?.locked" class="grid min-h-[58dvh] place-items-center">
        <form
          class="grid w-full max-w-[22rem] gap-3.5 rounded-xl border border-[var(--line)] bg-[var(--card)] p-5 shadow-[var(--soft-shadow)]"
          @submit.prevent="submitUnlock"
        >
          <div class="flex items-center gap-2.5">
            <div class="grid size-8 shrink-0 place-items-center rounded-lg bg-[var(--accent-soft)] text-[var(--accent-vivid)]">
              <AppNavIcon name="lock" class="size-4" aria-hidden="true" />
            </div>
            <div class="min-w-0">
              <h2 class="truncate text-sm font-semibold text-[var(--ink)]">{{ share.title || "受保护的分享" }}</h2>
              <p class="text-[11px] text-[var(--muted)] tabular-nums">
                {{ share.itemCount > 1 ? `共 ${share.itemCount} 个条目` : "单文件分享" }}
                <template v-if="share.totalSize > 0"> · {{ formatBytes(share.totalSize) }}</template>
              </p>
            </div>
          </div>

          <label class="grid gap-1.5">
            <span class="text-xs font-medium text-[var(--ink-secondary)]">请输入提取码查看内容</span>
            <input
              v-model="passcodeInput"
              type="text"
              autofocus
              autocomplete="off"
              maxlength="32"
              placeholder="输入提取码"
              class="h-9 w-full rounded-lg border border-[var(--line)] bg-[var(--input-bg)] px-3 font-mono text-sm text-[var(--ink)] outline-none focus:border-[var(--accent-vivid)]"
            />
          </label>

          <p v-if="unlockError" class="text-xs font-medium text-[var(--err)]">{{ unlockError }}</p>

          <button type="submit" class="btn-primary h-9 w-full justify-center text-xs" :disabled="unlocking">
            {{ unlocking ? "验证中…" : "确认访问" }}
          </button>
        </form>
      </div>

      <!-- 4. 已解锁内容 -->
      <div v-else-if="share" class="@container grid gap-4">
        <!-- 含可播放音视频：播放器 + 选集面板 -->
        <div
          v-if="playableItems.length > 0"
          class="grid content-start gap-4"
          :class="{ '@min-[56rem]:grid-cols-[minmax(0,1fr)_18.5rem]': items.length > 1 }"
        >
          <div class="grid min-w-0 content-start gap-2.5">
            <div
              v-if="playbackLoading"
              class="grid aspect-video w-full place-items-center rounded-xl bg-black text-xs font-medium text-white/75"
            >
              正在准备播放流…
            </div>
            <div
              v-else-if="playbackError"
              class="grid aspect-video w-full place-items-center rounded-xl bg-black px-6 text-center text-xs font-medium text-[var(--err)]"
            >
              {{ playbackError }}
            </div>
            <ArtPlayerPane
              v-else-if="playback && selectedItem && (playback.supportsInAppPlayback || playback.supportsTranscodePlayback)"
              gid=""
              :stream-url="streamUrl"
              :playback-key="`share:${code}:${selectedItem.id}:${playback.deliveryMode}`"
              :expected-duration-seconds="playback.durationSeconds"
              :hls="playback.deliveryMode === 'hls-remux' || playback.deliveryMode === 'hls-transcode'"
              :subtitle-tracks="playback.subtitleTracks"
              :subtitle-url-for="subtitleUrlFor"
              :progress-loader="loadProgress"
              :disable-download="!share.allowDownload"
              @ended="handlePlayEnded"
            />
            <div
              v-else-if="selectedItem"
              class="grid aspect-video w-full place-items-center rounded-xl bg-black px-6 text-center text-xs text-white/75"
            >
              <div class="grid gap-2">
                <span>当前文件暂不支持网页直接解码播放。</span>
                <a
                  v-if="share.allowDownload"
                  :href="downloadUrlForItem(selectedItem)"
                  download
                  class="mx-auto inline-flex h-8 items-center gap-1.5 rounded-lg bg-white/15 px-3 text-xs font-semibold text-white hover:bg-white/25"
                >
                  <AppNavIcon name="download" class="size-3.5" aria-hidden="true" />
                  下载原文件到本地播放
                </a>
              </div>
            </div>

            <!-- 当前播放条目的文件名与详情小字 -->
            <div v-if="selectedItem" class="flex flex-wrap items-center justify-between gap-2 px-0.5 text-xs text-[var(--muted)]">
              <span class="min-w-0 truncate" :title="selectedItem.fileName">
                正在播放：<strong class="font-semibold text-[var(--ink)]">{{ selectedItem.label }}</strong>
                <span v-if="selectedItem.fileName && selectedItem.fileName !== selectedItem.label" class="ml-1.5 opacity-80">
                  ({{ selectedItem.fileName }})
                </span>
              </span>
              <span v-if="selectedItem.size > 0" class="shrink-0 tabular-nums">{{ formatBytes(selectedItem.size) }}</span>
            </div>
          </div>

          <!-- 右侧选集 / 文件列表 -->
          <aside
            v-if="items.length > 1"
            class="grid content-start gap-2.5 rounded-xl border border-[var(--line)] bg-[var(--card)] p-3"
          >
            <div class="flex items-baseline justify-between gap-2">
              <h2 class="text-xs font-semibold text-[var(--ink)]">
                {{ allItemsEpisodeLike ? "选集" : "分享目录" }}
              </h2>
              <span class="text-[11px] text-[var(--muted)] tabular-nums">
                {{ playableItems.length }} 集可播<template v-if="nonPlayableItems.length > 0"> · 另含 {{ nonPlayableItems.length }} 个附件</template>
              </span>
            </div>

            <!-- 数字选集宫格（整部番剧场景） -->
            <div
              v-if="allItemsEpisodeLike"
              class="grid max-h-[24rem] grid-cols-[repeat(auto-fill,minmax(2.75rem,1fr))] gap-1.5 overflow-y-auto pr-0.5"
            >
              <button
                v-for="ep in playableItems"
                :key="ep.id"
                type="button"
                class="h-8.5 rounded-lg text-[13px] font-semibold tabular-nums transition"
                :class="
                  ep.id === selectedItemId
                    ? 'bg-[var(--accent)] text-[var(--accent-contrast)]'
                    : 'bg-[var(--panel)] text-[var(--ink-secondary)] hover:text-[var(--ink)]'
                "
                :title="`${ep.label} · ${ep.fileName}`"
                @click="ep.id !== selectedItemId && selectItem(ep)"
              >
                {{ episodeShortLabel(ep.label) }}
              </button>
            </div>

            <!-- 混合文件名列表（文件夹/多视频场景） -->
            <div v-else class="grid max-h-[24rem] gap-1 overflow-y-auto pr-0.5">
              <div
                v-for="ep in playableItems"
                :key="ep.id"
                class="flex items-center gap-1.5 rounded-lg px-2 py-1.5 text-xs transition"
                :class="
                  ep.id === selectedItemId
                    ? 'bg-[var(--accent-soft)] text-[var(--accent-vivid)] font-semibold'
                    : 'hover:bg-[var(--tint-2)] text-[var(--ink)]'
                "
              >
                <button
                  type="button"
                  class="flex min-w-0 flex-1 items-center gap-1.5 text-left"
                  @click="ep.id !== selectedItemId && selectItem(ep)"
                >
                  <AppNavIcon name="play" class="size-3.5 shrink-0" aria-hidden="true" />
                  <span class="truncate" :title="ep.fileName">{{ ep.label }}</span>
                </button>
                <a
                  v-if="share.allowDownload"
                  :href="downloadUrlForItem(ep)"
                  download
                  class="grid size-6 shrink-0 place-items-center rounded text-[var(--muted)] hover:bg-[var(--panel)] hover:text-[var(--ink)]"
                  title="下载此文件"
                >
                  <AppNavIcon name="download" class="size-3.5" aria-hidden="true" />
                </a>
              </div>
            </div>

            <!-- 同目录附加文件（如外挂字幕包/特典等，仅允许下载时提供下载） -->
            <div
              v-if="nonPlayableItems.length > 0 && share.allowDownload"
              class="grid gap-1 border-t border-[var(--line)] pt-2"
            >
              <span class="text-[11px] font-medium text-[var(--muted)]">附加文件</span>
              <a
                v-for="att in nonPlayableItems"
                :key="att.id"
                :href="downloadUrlForItem(att)"
                download
                class="flex items-center justify-between gap-2 rounded-md px-2 py-1 text-xs text-[var(--ink-secondary)] hover:bg-[var(--tint-2)] hover:text-[var(--ink)]"
              >
                <span class="min-w-0 flex-1 truncate" :title="att.fileName">{{ att.fileName }}</span>
                <span class="shrink-0 text-[11px] text-[var(--muted)] tabular-nums">{{ formatBytes(att.size) }}</span>
              </a>
            </div>

            <p v-if="playableItems.length > 1" class="text-[11px] text-[var(--muted)]">
              当前集播放完毕后将自动连播下一集。
            </p>
          </aside>
        </div>

        <!-- 纯文件列表分享（无音视频条目时） -->
        <div v-else class="rounded-xl border border-[var(--line)] bg-[var(--card)] overflow-hidden">
          <div class="flex items-center justify-between border-b border-[var(--line)] bg-[var(--panel)] px-3.5 py-2 text-xs font-semibold text-[var(--ink-secondary)]">
            <span>文件名 ({{ items.length }})</span>
            <span>大小 / 操作</span>
          </div>
          <div class="divide-y divide-[var(--line)]">
            <div
              v-for="item in items"
              :key="item.id"
              class="flex items-center justify-between gap-3 px-3.5 py-2.5 text-xs hover:bg-[var(--tint-1)]"
            >
              <div class="flex min-w-0 items-center gap-2">
                <AppNavIcon :name="fileIconName(item)" class="size-4 shrink-0 text-[var(--muted)]" aria-hidden="true" />
                <span class="truncate font-medium text-[var(--ink)]" :title="item.fileName">{{ item.fileName }}</span>
              </div>
              <div class="flex shrink-0 items-center gap-3">
                <span class="text-[var(--muted)] tabular-nums">{{ formatBytes(item.size) }}</span>
                <a
                  v-if="share.allowDownload"
                  :href="downloadUrlForItem(item)"
                  download
                  class="btn-ghost inline-flex h-7 items-center gap-1 px-2.5 text-xs font-semibold"
                >
                  <AppNavIcon name="download" class="size-3.5" aria-hidden="true" />
                  下载
                </a>
              </div>
            </div>
          </div>
        </div>
      </div>
    </main>
  </div>
</template>

<style scoped>
.share-shell {
  display: flex;
  min-height: 100dvh;
  flex-direction: column;
  background: var(--app-bg);
  color: var(--ink);
}

.share-topbar {
  position: sticky;
  top: 0;
  z-index: 20;
  border-bottom: 1px solid var(--line);
  background: var(--topbar-bg);
}
</style>
