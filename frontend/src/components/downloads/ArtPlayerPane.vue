<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from "vue";

import type { PlaybackSubtitleTrack } from "@shared/index";

import { getDownloadPlaybackProgress, readApiToken } from "../../api";

export interface PlayerEpisodeOption {
  key: string;
  label: string;
  gid: string;
  statusLabel: string;
  playable: boolean;
}

const props = defineProps<{
  gid: string;
  streamUrl: string;
  playbackKey: string;
  expectedDurationSeconds?: number | null;
  hls?: boolean;
  /** 字幕轨列表；ass/ssa 走原始字节直出，其余走 WebVTT */
  subtitleTracks?: PlaybackSubtitleTrack[];
  subtitleUrlFor?: (track: PlaybackSubtitleTrack) => string;
  episodes?: PlayerEpisodeOption[];
  activeEpisodeKey?: string | null;
}>();

const emit = defineEmits<{
  (event: "select-episode", option: PlayerEpisodeOption): void;
  (event: "ended"): void;
}>();

const PLAYBACK_HISTORY_KEY = "anime-rss-playback-history";
const PLAYBACK_RATE_KEY = "anime-rss-playback-rate";
const SAVE_INTERVAL_SECONDS = 5;
const RESUME_MIN_SECONDS = 8;
const RESUME_END_GAP_SECONDS = 20;
const PLAYBACK_RATES = [0.5, 0.75, 1, 1.25, 1.5, 1.75, 2];

type PlaybackHistory = Record<string, { currentTime: number; duration: number; updatedAt: number }>;
type ArtPlayerInstance = InstanceType<typeof import("artplayer").default>;

const containerRef = ref<HTMLDivElement | null>(null);
const episodePanelOpen = ref(false);
const resumePrompt = ref<{ seconds: number } | null>(null);
const streamError = ref<string | null>(null);
const bufferedSeconds = ref<number | null>(null);
const waitingForStream = ref(false);
const activeSubtitleId = ref<string | null>(null);

let player: ArtPlayerInstance | null = null;
let hlsInstance: import("hls.js").default | null = null;
/**
 * ASS 字幕直接用 assjs 渲染，不走 artplayer-plugin-assjs：Artplayer 的插件按名字注册、注册后删不掉，
 * 换一次字幕轨就报「already has the same name」，而且插件在报错前已经建好了第二个渲染器，
 * 简体和繁体字幕会叠在一起永远不消失。
 */
let assRenderer: { destroy: () => void } | null = null;
/** 快速连点换轨时，只认最后一次请求，晚到的旧字幕直接丢掉 */
let subtitleRequest = 0;
let lastSavedTime = 0;
let restoredForKey: string | null = null;
let progressTimer: number | null = null;

const subtitleOptions = computed(() => props.subtitleTracks ?? []);
const hasEpisodePanel = computed(() => (props.episodes?.length ?? 0) > 1);

function formatPlaybackTime(seconds: number): string {
  const safeSeconds = Math.max(0, Math.floor(seconds));
  const hours = Math.floor(safeSeconds / 3600);
  const minutes = Math.floor((safeSeconds % 3600) / 60);
  const rest = safeSeconds % 60;
  if (hours > 0) {
    return `${hours}:${String(minutes).padStart(2, "0")}:${String(rest).padStart(2, "0")}`;
  }
  return `${String(minutes).padStart(2, "0")}:${String(rest).padStart(2, "0")}`;
}

function isAssTrack(track: PlaybackSubtitleTrack): boolean {
  const codec = track.codec?.toLowerCase() ?? "";
  return codec === "ass" || codec === "ssa";
}

/* ---------- 进度记忆（与播放器实现无关，整段从 DPlayerPane 搬过来） ---------- */

function readPlaybackHistory(): PlaybackHistory {
  try {
    const raw = window.localStorage.getItem(PLAYBACK_HISTORY_KEY);
    if (!raw) {
      return {};
    }
    const parsed = JSON.parse(raw) as PlaybackHistory;
    return parsed && typeof parsed === "object" ? parsed : {};
  } catch {
    return {};
  }
}

function writePlaybackHistory(nextHistory: PlaybackHistory): void {
  try {
    window.localStorage.setItem(PLAYBACK_HISTORY_KEY, JSON.stringify(nextHistory));
  } catch {
    // 隐私模式下写不进去就算了，不该因此中断播放
  }
}

function clearPlaybackProgress(): void {
  if (!props.playbackKey) {
    return;
  }
  const history = readPlaybackHistory();
  if (!(props.playbackKey in history)) {
    return;
  }
  delete history[props.playbackKey];
  writePlaybackHistory(history);
}

function playbackDuration(): number {
  const expected = props.expectedDurationSeconds;
  if (typeof expected === "number" && Number.isFinite(expected) && expected > 0) {
    return expected;
  }
  return player?.video.duration ?? 0;
}

function savePlaybackProgress(force = false): void {
  if (!player || !props.playbackKey) {
    return;
  }

  // 续播浮层还挂着的时候，播放器正从 0 开始放；这会儿存进度会把用户上次的位置
  // 直接覆盖掉，等他点「继续播放」时已经没得可续了。
  if (resumePrompt.value) {
    return;
  }

  const currentTime = player.video.currentTime ?? 0;
  const duration = playbackDuration();
  if (!Number.isFinite(currentTime) || currentTime <= 0 || !Number.isFinite(duration) || duration <= 0) {
    return;
  }

  if (duration - currentTime <= RESUME_END_GAP_SECONDS) {
    clearPlaybackProgress();
    lastSavedTime = currentTime;
    return;
  }

  if (!force && Math.abs(currentTime - lastSavedTime) < SAVE_INTERVAL_SECONDS) {
    return;
  }

  const history = readPlaybackHistory();
  history[props.playbackKey] = { currentTime, duration, updatedAt: Date.now() };
  writePlaybackHistory(history);
  lastSavedTime = currentTime;
}

/** 不再静默 seek：把上次的位置摆成一个浮层，用户自己决定是续播还是从头看 */
function offerResume(): void {
  if (!props.playbackKey || restoredForKey === props.playbackKey) {
    return;
  }
  restoredForKey = props.playbackKey;

  const record = readPlaybackHistory()[props.playbackKey];
  const duration = playbackDuration();
  if (!record || !Number.isFinite(duration) || duration <= 0) {
    return;
  }

  const resumeTime = Math.min(record.currentTime, Math.max(0, duration - RESUME_END_GAP_SECONDS));
  if (resumeTime < RESUME_MIN_SECONDS) {
    return;
  }
  resumePrompt.value = { seconds: resumeTime };
}

function acceptResume(): void {
  const seconds = resumePrompt.value?.seconds;
  resumePrompt.value = null;
  if (player && typeof seconds === "number") {
    player.currentTime = seconds;
    lastSavedTime = seconds;
  }
}

function dismissResume(): void {
  resumePrompt.value = null;
}

/* ---------- 倍速记忆 ---------- */

/** 播放器主题色跟随全站点缀色（--accent-vivid），换主题色后重新初始化即生效。 */
function readAccentVivid(): string {
  const value = getComputedStyle(document.documentElement).getPropertyValue("--accent-vivid").trim();
  return value || "#0041fd";
}

function readStoredRate(): number {
  const raw = Number(window.localStorage.getItem(PLAYBACK_RATE_KEY) ?? "");
  return PLAYBACK_RATES.includes(raw) ? raw : 1;
}

function storeRate(rate: number): void {
  try {
    window.localStorage.setItem(PLAYBACK_RATE_KEY, String(rate));
  } catch {
    // 同上，存不下不影响播放
  }
}

/* ---------- 字幕 ---------- */

async function applySubtitleTrack(track: PlaybackSubtitleTrack | null): Promise<void> {
  if (!player) {
    return;
  }

  const request = ++subtitleRequest;
  assRenderer?.destroy();
  assRenderer = null;
  activeSubtitleId.value = track?.id ?? null;

  if (!track || !props.subtitleUrlFor) {
    player.subtitle.show = false;
    player.subtitle.url = "";
    return;
  }

  const url = props.subtitleUrlFor(track);
  if (isAssTrack(track)) {
    // ASS 走插件直接渲染原始字节，WebVTT 的 <track> 完全表达不了 ASS 的排版
    player.subtitle.show = false;
    player.subtitle.url = "";
    try {
      const [{ default: ASS }, content] = await Promise.all([
        import("assjs"),
        fetch(url).then((response) => {
          if (!response.ok) {
            throw new Error(`HTTP ${response.status}`);
          }
          return response.text();
        })
      ]);
      if (!player || request !== subtitleRequest) {
        return;
      }
      const video = player.video;
      assRenderer = new ASS(content, video, { container: video.parentElement ?? undefined, resampling: "video_width" });
    } catch (error) {
      if (request !== subtitleRequest) {
        return;
      }
      streamError.value = `字幕加载失败：${error instanceof Error ? error.message : String(error)}`;
    }
    return;
  }

  player.subtitle.url = url;
  player.subtitle.show = true;
}

/* ---------- HLS 转码进度 ---------- */

function stopProgressPolling(): void {
  if (progressTimer !== null) {
    window.clearInterval(progressTimer);
    progressTimer = null;
  }
}

async function pollProgressOnce(): Promise<void> {
  if (!props.hls || !props.gid) {
    return;
  }
  try {
    const progress = await getDownloadPlaybackProgress(props.gid);
    bufferedSeconds.value = progress.bufferedSeconds;
    if (progress.error) {
      streamError.value = `转码失败：${progress.error}`;
    }
    if (progress.finished) {
      stopProgressPolling();
    }
  } catch {
    // 进度查询失败不该冒泡成播放错误，下一轮再试
  }
}

function startProgressPolling(): void {
  stopProgressPolling();
  if (!props.hls) {
    bufferedSeconds.value = null;
    return;
  }
  void pollProgressOnce();
  progressTimer = window.setInterval(() => void pollProgressOnce(), 2000);
}

/* ---------- 播放器 ---------- */

function destroyPlayer(): void {
  subtitleRequest += 1;
  assRenderer?.destroy();
  assRenderer = null;
  hlsInstance?.destroy();
  hlsInstance = null;
  player?.destroy(false);
  player = null;
}

async function mountPlayer(): Promise<void> {
  if (!containerRef.value || !props.streamUrl) {
    return;
  }

  const { default: Artplayer } = await import("artplayer");
  const Hls = props.hls ? (await import("hls.js")).default : null;

  savePlaybackProgress(true);
  destroyPlayer();
  lastSavedTime = 0;
  restoredForKey = null;
  resumePrompt.value = null;
  streamError.value = null;
  waitingForStream.value = Boolean(props.hls);

  const storedRate = readStoredRate();

  player = new Artplayer({
    container: containerRef.value,
    url: props.streamUrl,
    type: props.hls ? "m3u8" : "",
    autoplay: true,
    pip: true,
    // 只留真全屏：网页全屏在这个页面里只是把播放器铺满内容区，
    // 而播放器下面只有字幕轨/选集/外部播放器三行，铺满前后差别极小，
    // 用户点了会以为没反应，反倒是多了一个要分辨的按钮。
    fullscreen: true,
    screenshot: true,
    setting: true,
    playbackRate: true,
    hotkey: false, // 自己接管，避免与内置 ←→ 叠加成 10 秒
    lang: "zh-cn",
    // ArtPlayer 只吃字符串颜色，读不了 CSS 变量，所以在这里取一次当前的点缀色
    theme: readAccentVivid(),
    volume: 1,
    playsInline: true,
    moreVideoAttr: { playsInline: true },
    customType: props.hls
      ? {
          m3u8(video: HTMLVideoElement, url: string) {
            if (video.canPlayType("application/vnd.apple.mpegurl")) {
              video.src = url;
              return;
            }
            if (!Hls?.isSupported()) {
              video.src = url;
              return;
            }
            hlsInstance = new Hls({
              // 播放流带 token：hls.js 自己发的 XHR 不走 api.ts 的封装，得手动补上
              xhrSetup(xhr) {
                const token = readApiToken();
                if (token) {
                  xhr.setRequestHeader("Authorization", `Bearer ${token}`);
                }
              }
            });
            hlsInstance.on(Hls.Events.ERROR, (_event, data) => {
              if (!data.fatal) {
                return;
              }
              // 原来 hls.js 出错是完全静默的，用户只看到永远转不出来的黑屏
              streamError.value =
                data.type === Hls.ErrorTypes.NETWORK_ERROR
                  ? "播放流中断：分片请求失败，可能是转码进程已退出。"
                  : `播放失败：${data.details}`;
            });
            hlsInstance.loadSource(url);
            hlsInstance.attachMedia(video);
          }
        }
      : {}
  });

  player.on("video:loadedmetadata", () => {
    waitingForStream.value = false;
    if (player) {
      player.playbackRate = storedRate;
    }
    offerResume();
  });
  player.on("video:timeupdate", () => {
    waitingForStream.value = false;
    savePlaybackProgress();
  });
  player.on("video:pause", () => savePlaybackProgress(true));
  player.on("video:ratechange", () => {
    if (player) {
      storeRate(player.playbackRate);
    }
  });
  player.on("video:ended", () => {
    clearPlaybackProgress();
    emit("ended");
  });
  player.on("video:error", () => {
    waitingForStream.value = false;
    streamError.value = "播放失败：浏览器无法解码这个流，试试外部播放器。";
  });

  const initialTrack =
    subtitleOptions.value.find((track) => track.id === activeSubtitleId.value) ?? subtitleOptions.value[0] ?? null;
  await applySubtitleTrack(initialTrack);

  startProgressPolling();
}

/* ---------- 快捷键 ---------- */

function isTypingTarget(target: EventTarget | null): boolean {
  const element = target as HTMLElement | null;
  if (!element) {
    return false;
  }
  return ["INPUT", "TEXTAREA", "SELECT"].includes(element.tagName) || element.isContentEditable;
}

function handleKeydown(event: KeyboardEvent): void {
  if (!player || isTypingTarget(event.target) || event.metaKey || event.ctrlKey || event.altKey) {
    return;
  }

  const duration = player.video.duration || playbackDuration();

  // 空格在部分输入法/布局下 event.key 不是 " "，用 code 兜一层
  if (event.key === " " || event.key === "Spacebar" || event.code === "Space") {
    player.toggle();
    event.preventDefault();
    return;
  }

  switch (event.key) {
    case "ArrowLeft":
      player.currentTime = Math.max(0, player.currentTime - 5);
      break;
    case "ArrowRight":
      player.currentTime = Math.min(duration || Number.MAX_SAFE_INTEGER, player.currentTime + 5);
      break;
    case "ArrowUp":
      player.volume = Math.min(1, player.volume + 0.1);
      break;
    case "ArrowDown":
      player.volume = Math.max(0, player.volume - 0.1);
      break;
    case "f":
    case "F":
      player.fullscreen = !player.fullscreen;
      break;
    case "m":
    case "M":
      player.muted = !player.muted;
      break;
    default: {
      // 数字键跳到对应百分比
      if (!/^[0-9]$/.test(event.key) || !duration) {
        return;
      }
      player.currentTime = (duration * Number(event.key)) / 10;
      break;
    }
  }

  event.preventDefault();
}

watch(
  () => [props.streamUrl, props.playbackKey, props.hls],
  () => {
    void mountPlayer();
  }
);

watch(
  () => props.subtitleTracks,
  (tracks) => {
    if (!player) {
      return;
    }
    const next = tracks?.find((track) => track.id === activeSubtitleId.value) ?? tracks?.[0] ?? null;
    void applySubtitleTrack(next);
  }
);

onMounted(() => {
  void mountPlayer();
  window.addEventListener("keydown", handleKeydown);
});

onBeforeUnmount(() => {
  window.removeEventListener("keydown", handleKeydown);
  savePlaybackProgress(true);
  stopProgressPolling();
  destroyPlayer();
});
</script>

<template>
  <div class="grid min-h-0 gap-2">
    <div class="relative min-h-0 rounded-lg border border-[var(--line)] bg-black/20 p-3">
      <div ref="containerRef" class="art-host h-full min-h-[360px] overflow-hidden rounded-lg"></div>

      <!-- 转码是边转边播的，黑屏期至少要让用户知道进度在动 -->
      <div
        v-if="waitingForStream && !streamError"
        class="pointer-events-none absolute inset-3 z-20 grid place-items-center rounded-lg bg-black/55 text-center"
      >
        <div class="grid gap-1 px-6 text-sm font-semibold text-white">
          <span>正在本机转码，准备播放…</span>
          <span v-if="bufferedSeconds !== null" class="text-xs font-medium text-white/75 tabular-nums">
            已缓冲到 {{ formatPlaybackTime(bufferedSeconds) }}
          </span>
        </div>
      </div>

      <div
        v-if="streamError"
        class="absolute inset-x-3 bottom-3 z-20 rounded-lg border border-[var(--err-soft)] bg-[var(--card)] px-4 py-3 text-sm font-semibold text-[var(--err)]"
      >
        {{ streamError }}
      </div>

      <!-- 续播浮层取代静默 seek：不告诉用户就跳走，会让人以为进度条坏了 -->
      <div
        v-if="resumePrompt"
        class="absolute bottom-16 left-6 z-20 flex items-center gap-3 rounded-lg bg-black/75 px-4 py-2.5 text-sm font-semibold text-white shadow-[var(--strong-shadow)]"
      >
        <span class="tabular-nums">上次看到 {{ formatPlaybackTime(resumePrompt.seconds) }}</span>
        <button type="button" class="rounded-md bg-[var(--accent)] px-3 py-1.5 text-xs font-semibold text-[var(--accent-contrast)]" @click="acceptResume">
          继续播放
        </button>
        <button type="button" class="rounded-md px-2 py-1.5 text-xs font-semibold text-white/70 hover:text-white" @click="dismissResume">
          从头看
        </button>
      </div>
    </div>

    <div v-if="subtitleOptions.length > 1 || hasEpisodePanel" class="flex flex-wrap items-center gap-2">
      <template v-if="subtitleOptions.length > 1">
        <span class="text-xs font-semibold text-[var(--muted)]">字幕</span>
        <button
          v-for="track in subtitleOptions"
          :key="track.id"
          type="button"
          class="chip h-8 rounded-md px-3 text-xs font-semibold"
          :class="{ 'chip-active': activeSubtitleId === track.id }"
          @click="applySubtitleTrack(track)"
        >
          {{ track.label }}
        </button>
        <button
          type="button"
          class="chip h-8 rounded-md px-3 text-xs font-semibold"
          :class="{ 'chip-active': activeSubtitleId === null }"
          @click="applySubtitleTrack(null)"
        >
          关闭
        </button>
      </template>

      <button
        v-if="hasEpisodePanel"
        type="button"
        class="chip ml-auto h-8 rounded-md px-3 text-xs font-semibold"
        :class="{ 'chip-active': episodePanelOpen }"
        @click="episodePanelOpen = !episodePanelOpen"
      >
        选集（{{ episodes?.length }}）
      </button>
    </div>

    <div v-if="hasEpisodePanel && episodePanelOpen" class="grid gap-1 rounded-lg border border-[var(--line)] bg-[var(--card)] p-2">
      <button
        v-for="episode in episodes"
        :key="episode.key"
        type="button"
        class="flex items-center gap-3 rounded-md px-3 py-2 text-left text-sm font-semibold transition disabled:opacity-40"
        :class="activeEpisodeKey === episode.key ? 'bg-[var(--accent-soft)] text-[var(--accent)]' : 'text-[var(--ink)] hover:bg-[var(--tint-2)]'"
        :disabled="!episode.playable"
        @click="emit('select-episode', episode)"
      >
        <span class="min-w-0 flex-1 truncate">{{ episode.label }}</span>
        <span class="shrink-0 text-xs font-medium text-[var(--muted)]">{{ episode.statusLabel }}</span>
      </button>
    </div>
  </div>
</template>

<style scoped>
.art-host :deep(.art-video-player) {
  height: 100%;
  background: #000;
}
</style>
