<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from "vue";

import type { PlaybackSubtitleTrack } from "@shared/index";

import { getDownloadPlaybackProgress, readApiToken } from "../../api";
import AppNavIcon from "../layout/AppNavIcon.vue";
import {
  readPlaybackHistory,
  RESUME_END_GAP_SECONDS,
  RESUME_MIN_SECONDS,
  writePlaybackHistory,
  type PlaybackHistoryMeta
} from "../../lib/playbackHistory";

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
  /** 自定义 HLS 转码进度加载器（公开分享页不持有管理员 gid 时传入） */
  progressLoader?: () => Promise<{
    bufferedSeconds?: number | null;
    producedSeconds?: number | null;
    finished: boolean;
    error?: string | null;
  }>;
  /** 禁止原生右键菜单与下载控件（仅允许在线播放的分享页启用） */
  disableDownload?: boolean;
  /** 写入观看历史时的展示快照（番名、集数、封面、本地/云盘文件来源等） */
  historyMeta?: PlaybackHistoryMeta;
}>();

// 选集不在播放器里了：播放页右侧（窄屏在下方）有 B 站式的选集格子
const emit = defineEmits<{
  (event: "ended"): void;
}>();

const PLAYBACK_RATE_KEY = "bgmcat.playback-rate";
const LEGACY_PLAYBACK_RATE_KEY = "anime-rss-playback-rate";
const SAVE_INTERVAL_SECONDS = 5;
const PLAYBACK_RATES = [0.5, 0.75, 1, 1.25, 1.5, 1.75, 2];

type ArtPlayerInstance = InstanceType<typeof import("artplayer").default>;
type ControlOption = Parameters<ArtPlayerInstance["controls"]["update"]>[0];

const SUBTITLE_CONTROL = "subtitle-track";
const SUBTITLE_OFF = "__off";
/** 续播提示挂这么久没人理，就当是从头看，不一直压在画面上 */
const RESUME_PROMPT_MS = 8000;

const containerRef = ref<HTMLDivElement | null>(null);
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
let activeSession: {
  key: string;
  gid: string;
  expectedDuration: number | null | undefined;
  meta: PlaybackHistoryMeta | undefined;
} | null = null;

const subtitleOptions = computed(() => props.subtitleTracks ?? []);

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

/* ---------- 进度与观看历史记忆 ---------- */

function mergeHistoryMeta(
  existing: ReturnType<typeof readPlaybackHistory>[string] | undefined,
  meta: PlaybackHistoryMeta | undefined
): PlaybackHistoryMeta {
  return {
    ...(existing?.kind ? { kind: existing.kind } : {}),
    ...(existing?.title ? { title: existing.title } : {}),
    ...(existing?.episodeLabel ? { episodeLabel: existing.episodeLabel } : {}),
    ...(existing?.subgroup ? { subgroup: existing.subgroup } : {}),
    ...(existing?.resolution ? { resolution: existing.resolution } : {}),
    ...(existing?.imageUrl ? { imageUrl: existing.imageUrl } : {}),
    ...(existing?.animeId ? { animeId: existing.animeId } : {}),
    ...(existing?.folderLabel ? { folderLabel: existing.folderLabel } : {}),
    ...(existing?.fileSource ? { fileSource: existing.fileSource } : {}),
    ...(existing?.fileEntry ? { fileEntry: existing.fileEntry } : {}),
    ...(meta
      ? Object.fromEntries(Object.entries(meta).filter(([, v]) => v !== undefined && v !== null && v !== ""))
      : {})
  };
}

function clearPlaybackProgress(targetKey = activeSession?.key ?? props.playbackKey): void {
  if (!targetKey) {
    return;
  }
  const history = readPlaybackHistory();
  if (!(targetKey in history)) {
    return;
  }
  delete history[targetKey];
  writePlaybackHistory(history);
}

function playbackDuration(expectedOverride?: number | null): number {
  const expected = expectedOverride !== undefined ? expectedOverride : props.expectedDurationSeconds;
  if (typeof expected === "number" && Number.isFinite(expected) && expected > 0) {
    return expected;
  }
  return player?.video.duration ?? 0;
}

function markPlaybackCompleted(forceWrite = false): void {
  const session = activeSession;
  const key = session?.key ?? props.playbackKey;
  if (!key) {
    return;
  }
  if (key.startsWith("share:")) {
    clearPlaybackProgress(key);
    return;
  }
  const duration = playbackDuration(session?.expectedDuration);
  if (!Number.isFinite(duration) || duration <= 0) {
    return;
  }
  const history = readPlaybackHistory();
  const existing = history[key];
  if (existing?.completed && !forceWrite) {
    return;
  }
  const gid = session?.gid ?? props.gid;
  const meta = mergeHistoryMeta(existing, session?.meta ?? props.historyMeta);
  history[key] = {
    ...meta,
    currentTime: duration,
    duration,
    updatedAt: Date.now(),
    ...(gid ? { gid } : {}),
    completed: true
  };
  writePlaybackHistory(history);
}

function savePlaybackProgress(force = false): void {
  const session = activeSession;
  const key = session?.key ?? props.playbackKey;
  if (!player || !key) {
    return;
  }

  // 续播浮层还挂着的时候，播放器正从 0 开始放；这会儿存进度会把用户上次的位置
  // 直接覆盖掉，等他点「继续播放」时已经没得可续了。
  if (resumePrompt.value) {
    return;
  }

  const currentTime = player.video.currentTime ?? 0;
  const duration = playbackDuration(session?.expectedDuration);
  if (!Number.isFinite(currentTime) || currentTime < 1 || !Number.isFinite(duration) || duration <= 0) {
    return;
  }

  if (duration - currentTime <= RESUME_END_GAP_SECONDS) {
    markPlaybackCompleted(false);
    lastSavedTime = currentTime;
    return;
  }

  const history = readPlaybackHistory();
  const existing = history[key];

  // 已看完的剧集若只是点开瞄了几秒（未满续播阈值），保留「已看完」状态，不误退回未看完
  if (existing?.completed && currentTime < RESUME_MIN_SECONDS) {
    return;
  }

  const isFirstRecord = !existing && lastSavedTime === 0;
  if (!force && !isFirstRecord && Math.abs(currentTime - lastSavedTime) < SAVE_INTERVAL_SECONDS) {
    return;
  }

  const gid = session?.gid ?? props.gid;
  const meta = mergeHistoryMeta(existing, session?.meta ?? props.historyMeta);
  history[key] = {
    ...meta,
    currentTime,
    duration,
    updatedAt: Date.now(),
    ...(gid ? { gid } : {}),
    completed: false
  };
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
  if (!record || record.completed || !Number.isFinite(duration) || duration <= 0) {
    return;
  }
  if (duration - record.currentTime <= RESUME_END_GAP_SECONDS) {
    return;
  }

  const resumeTime = Math.min(record.currentTime, Math.max(0, duration - RESUME_END_GAP_SECONDS));
  if (resumeTime < RESUME_MIN_SECONDS) {
    return;
  }
  resumePrompt.value = { seconds: resumeTime };
  clearResumeTimer();
  resumeTimer = window.setTimeout(dismissResume, RESUME_PROMPT_MS);
}

let resumeTimer: number | null = null;

function clearResumeTimer(): void {
  if (resumeTimer !== null) {
    window.clearTimeout(resumeTimer);
    resumeTimer = null;
  }
}

function acceptResume(): void {
  clearResumeTimer();
  const seconds = resumePrompt.value?.seconds;
  resumePrompt.value = null;
  if (player && typeof seconds === "number") {
    player.currentTime = seconds;
    lastSavedTime = seconds;
  }
}

function dismissResume(): void {
  clearResumeTimer();
  resumePrompt.value = null;
}

/* ---------- 倍速记忆 ---------- */

/** 播放器主题色跟随全站点缀色（--accent-vivid），换主题色后重新初始化即生效。 */
function readAccentVivid(): string {
  const value = getComputedStyle(document.documentElement).getPropertyValue("--accent-vivid").trim();
  return value || "#0041fd";
}

function readStoredRate(): number {
  const raw = Number(window.localStorage.getItem(PLAYBACK_RATE_KEY) ?? window.localStorage.getItem(LEGACY_PLAYBACK_RATE_KEY) ?? "");
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
        fetch(url, { credentials: "same-origin" }).then((response) => {
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

/**
 * 字幕轨放进播放器控制条（B 站的「字幕」按钮那样），不再在画面下面摆一排胶囊：
 * 全屏时那排胶囊根本看不到，平时又把番名挤到了画面下面。
 */
function subtitleControlOption(): ControlOption {
  return {
    name: SUBTITLE_CONTROL,
    position: "right",
    index: 15,
    html: "字幕",
    tooltip: "字幕",
    style: { padding: "0 8px", fontSize: "13px", fontWeight: "600" },
    selector: [
      ...subtitleOptions.value.map((track) => ({ html: track.label, value: track.id, default: track.id === activeSubtitleId.value })),
      { html: "关闭", value: SUBTITLE_OFF, default: activeSubtitleId.value === null }
    ],
    onSelect(item) {
      const track = subtitleOptions.value.find((option) => option.id === item.value) ?? null;
      void applySubtitleTrack(track);
      return "字幕";
    }
  };
}

function syncSubtitleControl(): void {
  if (!player) {
    return;
  }
  if (subtitleOptions.value.length > 1) {
    player.controls.update(subtitleControlOption());
  } else if (player.controls[SUBTITLE_CONTROL as keyof typeof player.controls]) {
    player.controls.remove(SUBTITLE_CONTROL);
  }
}

/* ---------- HLS 转码进度 ---------- */

function stopProgressPolling(): void {
  if (progressTimer !== null) {
    window.clearInterval(progressTimer);
    progressTimer = null;
  }
}

async function pollProgressOnce(): Promise<void> {
  if (!props.hls || (!props.progressLoader && !props.gid)) {
    return;
  }
  try {
    const progress = props.progressLoader
      ? await props.progressLoader()
      : await getDownloadPlaybackProgress(props.gid);
    const secs =
      progress.bufferedSeconds ??
      ("producedSeconds" in progress ? progress.producedSeconds : null) ??
      null;
    bufferedSeconds.value = typeof secs === "number" && Number.isFinite(secs) ? secs : null;
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
  activeSession = null;
}

async function mountPlayer(): Promise<void> {
  if (!containerRef.value || !props.streamUrl) {
    return;
  }

  savePlaybackProgress(true);
  destroyPlayer();

  const { default: Artplayer } = await import("artplayer");
  const Hls = props.hls ? (await import("hls.js")).default : null;
  if (!containerRef.value || !props.streamUrl) {
    return;
  }

  lastSavedTime = 0;
  restoredForKey = null;
  resumePrompt.value = null;
  streamError.value = null;
  waitingForStream.value = Boolean(props.hls);
  activeSession = {
    key: props.playbackKey,
    gid: props.gid,
    expectedDuration: props.expectedDurationSeconds,
    meta: props.historyMeta
  };

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
    icons: {
      state: `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" aria-hidden="true"><path fill="currentColor" d="M9.2 7.25c0-.86.94-1.39 1.68-.94l7.65 4.75c.7.43.7 1.45 0 1.88l-7.65 4.75c-.74.45-1.68-.08-1.68-.94V7.25Z"/></svg>`
    },
    volume: 1,
    playsInline: true,
    ...(props.disableDownload ? { contextmenu: [] } : {}),
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
              xhrSetup(xhr, requestUrl) {
                if (requestUrl.includes("/api/public/shares/")) {
                  xhr.withCredentials = true;
                  return;
                }
                const token = readApiToken();
                if (token) {
                  xhr.setRequestHeader("Authorization", `Bearer ${token}`);
                }
              }
            });
            let mediaRecoverAttempts = 0;
            hlsInstance.on(Hls.Events.ERROR, (_event, data) => {
              if (!data.fatal) {
                return;
              }
              if (data.type === Hls.ErrorTypes.MEDIA_ERROR && mediaRecoverAttempts < 2) {
                mediaRecoverAttempts += 1;
                hlsInstance?.recoverMediaError();
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

  if (props.disableDownload && player?.video) {
    player.video.setAttribute("controlslist", "nodownload");
  }

  player.on("video:loadedmetadata", () => {
    waitingForStream.value = false;
    streamError.value = null;
    if (player) {
      player.playbackRate = storedRate;
    }
    offerResume();
  });
  player.on("video:canplay", () => {
    waitingForStream.value = false;
    streamError.value = null;
  });
  player.on("video:playing", () => {
    waitingForStream.value = false;
    streamError.value = null;
  });
  player.on("video:timeupdate", () => {
    waitingForStream.value = false;
    if (streamError.value && player && !player.video.paused && player.video.readyState >= 2) {
      streamError.value = null;
    }
    savePlaybackProgress();
  });
  player.on("video:pause", () => savePlaybackProgress(true));
  player.on("video:ratechange", () => {
    if (player) {
      storeRate(player.playbackRate);
    }
  });
  player.on("video:ended", () => {
    markPlaybackCompleted(true);
    emit("ended");
  });
  // 浏览器 / 外壳拒绝真全屏时（iPhone Safari、权限被拦），退到铺满窗口，别让按钮点了没反应
  player.on("fullscreenError", () => {
    if (player) {
      player.fullscreenWeb = true;
    }
  });
  player.on("video:error", () => {
    // 正在正常播放或只是切换流/轨道期间的瞬态事件，不应把报错条钉在画面上
    if (player && (!player.video.error || (!player.video.paused && player.video.readyState >= 2))) {
      return;
    }
    waitingForStream.value = false;
    streamError.value = "播放失败：浏览器无法解码这个流，试试外部播放器。";
  });

  const initialTrack =
    subtitleOptions.value.find((track) => track.id === activeSubtitleId.value) ?? subtitleOptions.value[0] ?? null;
  await applySubtitleTrack(initialTrack);
  syncSubtitleControl();

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
  () => props.historyMeta,
  (meta) => {
    if (activeSession && activeSession.key === props.playbackKey) {
      activeSession.meta = meta;
    }
    if (!meta || !props.playbackKey || props.playbackKey.startsWith("share:")) {
      return;
    }
    const history = readPlaybackHistory();
    const existing = history[props.playbackKey];
    if (!existing) {
      return;
    }
    const merged = mergeHistoryMeta(existing, meta);
    const changed = (Object.keys(merged) as Array<keyof PlaybackHistoryMeta>).some(
      (k) => merged[k] !== existing[k]
    );
    if (changed) {
      history[props.playbackKey] = {
        ...existing,
        ...merged
      };
      writePlaybackHistory(history);
    }
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
    syncSubtitleControl();
  }
);

onMounted(() => {
  void mountPlayer();
  window.addEventListener("keydown", handleKeydown);
});

onBeforeUnmount(() => {
  window.removeEventListener("keydown", handleKeydown);
  clearResumeTimer();
  savePlaybackProgress(true);
  stopProgressPolling();
  destroyPlayer();
});
</script>

<template>
  <div class="grid min-h-0 gap-2">
    <!-- 播放器本体：16:9 黑底、无边框，像视频站那样让画面做主角 -->
    <div
      class="@container relative aspect-video w-full overflow-hidden rounded-xl bg-black"
      @contextmenu="disableDownload && $event.preventDefault()"
    >
      <div ref="containerRef" class="art-host size-full"></div>

      <!-- 转码是边转边播的，黑屏期至少要让用户知道进度在动 -->
      <div
        v-if="waitingForStream && !streamError"
        class="pointer-events-none absolute inset-0 z-20 grid place-items-center bg-black/55 text-center"
      >
        <div class="grid gap-1 px-6 text-sm font-semibold text-white">
          <span>正在本机转码，准备播放…</span>
          <span v-if="bufferedSeconds !== null && Number.isFinite(bufferedSeconds)" class="text-xs font-medium text-white/75 tabular-nums">
            已缓冲到 {{ formatPlaybackTime(bufferedSeconds) }}
          </span>
        </div>
      </div>

      <div
        v-if="streamError"
        class="absolute inset-x-3 bottom-14 z-20 flex items-center justify-between gap-3 rounded-lg border border-[var(--err-soft)] bg-[var(--card)]/95 px-3.5 py-2.5 text-xs font-semibold text-[var(--err)] backdrop-blur-sm"
      >
        <span class="min-w-0 flex-1">{{ streamError }}</span>
        <button
          type="button"
          class="grid size-6 shrink-0 place-items-center rounded-md text-[var(--muted)] transition hover:bg-[var(--panel)] hover:text-[var(--ink)]"
          aria-label="关闭错误提示"
          @click="streamError = null"
        >
          <AppNavIcon name="close" class="size-3.5" aria-hidden="true" />
        </button>
      </div>

      <!--
        续播提示学 B 站：左下角一行小字「上次看到 16:51 · 跳转播放」，几秒后自己消失。
        以前是压在画面中间的一大块深色条，还要求用户必须二选一。
      -->
      <Transition name="resume-toast">
        <div v-if="resumePrompt" class="resume-toast">
          <span class="tabular-nums">上次看到 {{ formatPlaybackTime(resumePrompt.seconds) }}</span>
          <button type="button" class="font-semibold text-[var(--accent-vivid)] hover:underline" @click="acceptResume">跳转播放</button>
          <button type="button" class="-mr-1 grid size-5 place-items-center text-white/60 hover:text-white" aria-label="关闭续播提示" @click="dismissResume">
            <AppNavIcon name="close" class="size-3.5" aria-hidden="true" />
          </button>
        </div>
      </Transition>
    </div>
  </div>
</template>

<style scoped>
.art-host:not(:has(.art-fullscreen-web)) {
  position: relative;
  z-index: 1;
  isolation: isolate;
}

.art-host :deep(.art-video-player) {
  --art-state-size: 44px;
  --art-state-opacity: 1;
  --art-control-height: 38px;
  --art-control-icon-size: 30px;
  --art-control-icon-scale: 1;
  --art-bottom-height: 76px;
  --art-bottom-offset: 12px;
  height: 100%;
  background: #000;
}

.art-host :deep(.art-mask .art-state) {
  width: var(--art-state-size);
  height: var(--art-state-size);
  border-radius: 999px;
  border: 1px solid rgb(255 255 255 / 0.18);
  background: rgb(10 12 18 / 0.62);
  color: #fff;
  backdrop-filter: blur(8px);
  box-shadow: 0 6px 20px rgb(0 0 0 / 0.36);
  transform: scale(0.92);
  transition:
    opacity 0.16s ease,
    transform 0.16s ease,
    background-color 0.16s ease,
    border-color 0.16s ease;
}

.art-host :deep(.art-video-player.art-mask-show .art-state) {
  opacity: 1;
  transform: scale(1);
}

.art-host :deep(.art-mask .art-state:hover) {
  background: rgb(10 12 18 / 0.78);
  border-color: rgb(255 255 255 / 0.32);
  transform: scale(1.05);
}

.art-host :deep(.art-mask .art-state svg) {
  width: 18px;
  height: 18px;
}

/*
 * assjs 把字幕层挂在播放器容器里，但不给 z-index；Artplayer 的 .art-video 是 z-index 10，
 * 于是整层 ASS 字幕被视频盖在下面，DOM 里有字、画面上没有。放到和 Artplayer 自带字幕同一层（20），
 * 在控制条（60）之下；点击要穿透给播放器（暂停、双击全屏）。
 */
.resume-toast {
  position: absolute;
  bottom: 54px;
  left: 14px;
  z-index: 30;
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 6px 12px;
  border-radius: 8px;
  background: rgb(0 0 0 / 0.62);
  color: #fff;
  font-size: 12.5px;
  backdrop-filter: blur(8px);
}
.resume-toast-enter-active,
.resume-toast-leave-active {
  transition: opacity 0.2s, transform 0.2s;
}
.resume-toast-enter-from,
.resume-toast-leave-to {
  opacity: 0;
  transform: translateY(6px);
}

@container (max-width: 520px) {
  .art-host :deep(.art-video-player) {
    --art-state-size: 38px;
    --art-control-height: 34px;
    --art-control-icon-size: 26px;
    --art-bottom-height: 64px;
  }

  .art-host :deep(.art-mask .art-state svg) {
    width: 15px;
    height: 15px;
  }

  .resume-toast {
    bottom: 48px;
  }
}

.art-host :deep(.ASS-box) {
  z-index: 20;
  pointer-events: none;
}
</style>
