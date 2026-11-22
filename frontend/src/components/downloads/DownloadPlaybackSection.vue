<script setup lang="ts">
import { computed, defineAsyncComponent, onBeforeUnmount, onMounted, ref } from "vue";
import type {
  AnimeDetail,
  DownloadJob,
  DownloadPlaybackInfo,
  ExternalPlayerId,
  MikanDownloadItem,
  PlaybackSubtitleTrack
} from "@shared/index";
import { extractEpisodeCandidates, extractEpisodeNumber, extractSubgroup, isBatchRelease } from "@shared/episode";
import {
  getDownloadAssSubtitleUrl,
  getDownloadHlsPlaylistUrl,
  getDownloadStreamUrl,
  getDownloadSubtitleUrl,
  resolveDownloadArtworkUrl
} from "../../api";
import { playableDownloadForEpisode, statusLabel, type SeriesDownloadGroup } from "../../lib/downloadGrouping";
import { formatSpeed, progressPercent } from "../../lib/format";
import { extractResolutionLabel } from "../../lib/mikan";
import type { PlaybackHistoryMeta } from "../../lib/playbackHistory";
import { SHARE_FEATURE_ENABLED } from "../../stores/shares";
import AppNavIcon from "../layout/AppNavIcon.vue";

// 按需加载播放器核心（artplayer / hls.js / assjs），未进入播放态时不阻塞下载管理列表
import ArtPlayerPlaceholder from "./ArtPlayerPlaceholder.vue";

const ArtPlayerPane = defineAsyncComponent({
  loader: () => import("./ArtPlayerPane.vue"),
  loadingComponent: ArtPlayerPlaceholder,
  delay: 0
});

export interface UnifiedPlaybackEpisode {
  key: string;
  episodeNumber: number | null;
  shortLabel: string;
  fullLabel: string;
  episodeTitle: string | null;
  airDate: string | null;
  state: "playable" | "downloading" | "available" | "upcoming";
  playableGid: string | null;
  downloadingGid: string | null;
  downloadProgress: number | null;
  statusText: string;
  mikanItems: MikanDownloadItem[];
  bestMikanItem: MikanDownloadItem | null;
}

const props = defineProps<{
  playback: DownloadPlaybackInfo;
  activeDownload?: DownloadJob | null;
  totalDownloadSpeed?: number;
  totalUploadSpeed?: number;
  totalCloudUploadSpeed?: number;
  group: SeriesDownloadGroup | null;
  subgroup: string | null;
  resolution: string | null;
  animeDetail: AnimeDetail | null;
  detailLoading: boolean;
  pendingAutoPlayGid: string | null;
  queueingMikanId: string | null;
}>();

const emit = defineEmits<{
  close: [];
  playGid: [gid: string];
  selectDownloading: [gid: string];
  cancelAutoPlay: [];
  queueMikan: [item: MikanDownloadItem];
  externalPlayer: [playerId: ExternalPlayerId];
  share: [episodeLabel: string | null];
}>();

const streamUrl = computed(() =>
  props.playback.deliveryMode === "direct"
    ? getDownloadStreamUrl(props.playback.gid)
    : props.playback.deliveryMode === "hls-remux" || props.playback.deliveryMode === "hls-transcode"
      ? getDownloadHlsPlaylistUrl(props.playback.gid)
      : ""
);

/** ass/ssa 走直出端点交给 ass 插件；其余轨仍旧转 WebVTT */
function playbackSubtitleUrl(track: PlaybackSubtitleTrack): string {
  const gid = props.playback.gid;
  const codec = track.codec?.toLowerCase() ?? "";
  return codec === "ass" || codec === "ssa"
    ? getDownloadAssSubtitleUrl(gid, track.id)
    : getDownloadSubtitleUrl(gid, track.id);
}

function parsePositiveEpisodeNumber(raw: string | null | undefined): number | null {
  if (!raw) return null;
  const n = Number.parseInt(raw, 10);
  return Number.isFinite(n) && n > 0 ? n : null;
}

/** 根据当前正在看的字幕组、清晰度与浏览器直播兼容性（MP4 优先）给候选资源打分 */
function scoreMikanCandidate(item: MikanDownloadItem): number {
  let score = 0;
  const sub = (item.subgroup?.trim() || extractSubgroup(item.title) || "").toLowerCase();
  const targetSub = (props.subgroup || "").trim().toLowerCase();
  if (targetSub && sub && (sub === targetSub || sub.includes(targetSub) || targetSub.includes(sub))) {
    score += 100;
  }
  const res = (extractResolutionLabel(item.title) || "").toUpperCase();
  const targetRes = (props.resolution || "1080P").toUpperCase();
  if (res && res === targetRes) {
    score += 40;
  } else if (res === "1080P") {
    score += 20;
  }
  if (/\.mp4(?:\s|$|\]|\))/i.test(item.title) || /\bmp4\b/i.test(item.title)) {
    score += 12;
  }
  if (/\b(?:chs|gb|简|简日|简繁)\b/i.test(item.title)) {
    score += 6;
  }
  return score;
}

function pickBestMikanItem(items: MikanDownloadItem[]): MikanDownloadItem | null {
  if (!items.length) return null;
  let best = items[0]!;
  let bestScore = scoreMikanCandidate(best);
  for (let i = 1; i < items.length; i += 1) {
    const cand = items[i]!;
    const s = scoreMikanCandidate(cand);
    if (s > bestScore) {
      best = cand;
      bestScore = s;
    }
  }
  return best;
}

/** 融合本地/云盘已下载分集 + Bangumi 剧集表 + Mikan 在线可播资源 */
const unifiedEpisodes = computed<UnifiedPlaybackEpisode[]>(() => {
  const detail = props.animeDetail;
  const bgmEpisodes = detail?.episodes ?? [];
  const knownNumbers = new Set<number>();
  const bgmByNumber = new Map<number, AnimeDetail["episodes"][number]>();

  for (const ep of bgmEpisodes) {
    const num = parsePositiveEpisodeNumber(ep.episodeNumber);
    if (num !== null && !bgmByNumber.has(num)) {
      bgmByNumber.set(num, ep);
      knownNumbers.add(num);
    }
  }

  // 按集数归拢在线 RSS 单集资源（跳过整季合集包）
  const mikanByNumber = new Map<number, MikanDownloadItem[]>();
  for (const item of detail?.downloads ?? []) {
    if ((!item.torrentUrl && !item.magnetUrl) || isBatchRelease(item.title)) {
      continue;
    }
    let matchedNum: number | null = null;
    if (knownNumbers.size > 0) {
      for (const cand of extractEpisodeCandidates(item.title)) {
        if (knownNumbers.has(cand)) {
          matchedNum = cand;
          break;
        }
      }
    }
    if (matchedNum === null) {
      matchedNum = extractEpisodeNumber(item.title);
    }
    if (matchedNum !== null && matchedNum > 0 && matchedNum <= 999) {
      const bucket = mikanByNumber.get(matchedNum) ?? [];
      bucket.push(item);
      mikanByNumber.set(matchedNum, bucket);
    }
  }

  // 本地已加入队列的分集（数字集 + SP 等非数字集）
  const localByNumber = new Map<number, NonNullable<SeriesDownloadGroup["episodes"][number]>>();
  const localSpecials: Array<NonNullable<SeriesDownloadGroup["episodes"][number]>> = [];
  for (const localEp of props.group?.episodes ?? []) {
    if (localEp.episodeNumber !== null && localEp.episodeNumber > 0) {
      localByNumber.set(localEp.episodeNumber, localEp);
    } else {
      localSpecials.push(localEp);
    }
  }

  const allNumbers = new Set<number>([
    ...bgmByNumber.keys(),
    ...mikanByNumber.keys(),
    ...localByNumber.keys()
  ]);

  const sortedNumbers = Array.from(allNumbers).sort((a, b) => a - b);
  const result: UnifiedPlaybackEpisode[] = [];

  for (const num of sortedNumbers) {
    const pad = String(num).padStart(2, "0");
    const localEp = localByNumber.get(num);
    const bgmEp = bgmByNumber.get(num);
    const mikanItems = mikanByNumber.get(num) ?? [];
    const bestMikan = pickBestMikanItem(mikanItems);
    const fullLabel = localEp?.episodeLabel ?? `第 ${pad} 集`;
    const episodeTitle = bgmEp?.title?.trim() || null;
    const airDate = bgmEp?.airDate ?? null;

    if (localEp) {
      const playable = playableDownloadForEpisode(localEp);
      const primary = localEp.downloads[0]!;
      if (playable) {
        result.push({
          key: `ep:${num}`,
          episodeNumber: num,
          shortLabel: String(num),
          fullLabel,
          episodeTitle,
          airDate,
          state: "playable",
          playableGid: playable.gid,
          downloadingGid: null,
          downloadProgress: null,
          statusText: playable.status === "complete" ? "已缓存" : "边下边播",
          mikanItems,
          bestMikanItem: bestMikan
        });
      } else {
        const pct = progressPercent(primary);
        result.push({
          key: `ep:${num}`,
          episodeNumber: num,
          shortLabel: String(num),
          fullLabel,
          episodeTitle,
          airDate,
          state: "downloading",
          playableGid: null,
          downloadingGid: primary.gid,
          downloadProgress: pct,
          statusText: `${statusLabel(primary.status)} ${pct}%`,
          mikanItems,
          bestMikanItem: bestMikan
        });
      }
      continue;
    }

    if (bestMikan) {
      result.push({
        key: `ep:${num}`,
        episodeNumber: num,
        shortLabel: String(num),
        fullLabel,
        episodeTitle,
        airDate,
        state: "available",
        playableGid: null,
        downloadingGid: null,
        downloadProgress: null,
        statusText: "点击在线缓冲播放",
        mikanItems,
        bestMikanItem: bestMikan
      });
    } else {
      result.push({
        key: `ep:${num}`,
        episodeNumber: num,
        shortLabel: String(num),
        fullLabel,
        episodeTitle,
        airDate,
        state: "upcoming",
        playableGid: null,
        downloadingGid: null,
        downloadProgress: null,
        statusText: airDate ? `${airDate} 播出` : "暂无片源",
        mikanItems: [],
        bestMikanItem: null
      });
    }
  }

  for (const sp of localSpecials) {
    const playable = playableDownloadForEpisode(sp);
    const primary = sp.downloads[0]!;
    const rawLabel = sp.episodeLabel && sp.episodeLabel !== "未知集数" ? sp.episodeLabel : "单集";
    const match = rawLabel.match(/\d+(?:\.\d+)?/);
    const shortLabel = match ? String(Number(match[0])) : rawLabel.slice(0, 4);
    const pct = progressPercent(primary);
    result.push({
      key: `local:${sp.key}`,
      episodeNumber: null,
      shortLabel,
      fullLabel: rawLabel,
      episodeTitle: null,
      airDate: null,
      state: playable ? "playable" : "downloading",
      playableGid: playable?.gid ?? null,
      downloadingGid: playable ? null : primary.gid,
      downloadProgress: playable ? null : pct,
      statusText: playable ? (playable.status === "complete" ? "已缓存" : "边下边播") : `${statusLabel(primary.status)} ${pct}%`,
      mikanItems: [],
      bestMikanItem: null
    });
  }

  // 兜底：若刚从地址栏恢复播放且 downloads 尚未匹配出分组，至少展示当前这一集
  if (!result.length) {
    const fallbackNum = extractEpisodeNumber(props.playback.fileName ?? "");
    result.push({
      key: `current:${props.playback.gid}`,
      episodeNumber: fallbackNum,
      shortLabel: fallbackNum ? String(fallbackNum) : "1",
      fullLabel: fallbackNum ? `第 ${String(fallbackNum).padStart(2, "0")} 集` : "当前视频",
      episodeTitle: null,
      airDate: null,
      state: "playable",
      playableGid: props.playback.gid,
      downloadingGid: null,
      downloadProgress: null,
      statusText: "正在播放",
      mikanItems: [],
      bestMikanItem: null
    });
  }

  return result;
});

const activeEpisode = computed<UnifiedPlaybackEpisode | null>(() => {
  const byPlayableGid = unifiedEpisodes.value.find((ep) => ep.playableGid === props.playback.gid);
  if (byPlayableGid) return byPlayableGid;
  const groupMatch = props.group?.episodes.find((ep) => ep.downloads.some((d) => d.gid === props.playback.gid));
  if (groupMatch?.episodeNumber) {
    return unifiedEpisodes.value.find((ep) => ep.episodeNumber === groupMatch.episodeNumber) ?? null;
  }
  return unifiedEpisodes.value[0] ?? null;
});

const pendingEpisode = computed<UnifiedPlaybackEpisode | null>(() => {
  if (!props.pendingAutoPlayGid) return null;
  return (
    unifiedEpisodes.value.find(
      (ep) => ep.downloadingGid === props.pendingAutoPlayGid || ep.playableGid === props.pendingAutoPlayGid
    ) ?? null
  );
});

const playbackEpisodeLabel = computed(() => activeEpisode.value?.fullLabel ?? null);

const playbackHistoryMeta = computed<PlaybackHistoryMeta>(() => ({
  kind: "download",
  title: props.animeDetail?.anime.title || props.group?.title || undefined,
  episodeLabel: playbackEpisodeLabel.value,
  subgroup: props.subgroup,
  resolution: props.resolution,
  imageUrl:
    props.animeDetail?.anime.imageUrl ??
    (props.group?.primary ? resolveDownloadArtworkUrl(props.group.primary) : null),
  animeId: props.animeDetail?.anime.animeId ?? null
}));

/** 画面上方标题下那一行：第几集 · 话标题 · 字幕组 · 清晰度 */
const playbackMetaLine = computed(() => {
  const parts: Array<string | null | undefined> = [
    playbackEpisodeLabel.value,
    activeEpisode.value?.episodeTitle,
    props.subgroup,
    props.resolution
  ];
  return parts.filter(Boolean).join(" · ");
});

const liveDownloadSpeed = computed(() => props.activeDownload?.downloadSpeed || props.totalDownloadSpeed || 0);
const liveUploadSpeed = computed(() => (props.activeDownload?.uploadSpeed || props.totalUploadSpeed || 0) + (props.totalCloudUploadSpeed || 0));
const isStreamingActiveDownload = computed(
  () => Boolean(props.activeDownload && props.activeDownload.status !== "complete")
);
const activeDownloadPercent = computed(() =>
  props.activeDownload ? progressPercent(props.activeDownload) : null
);
const showPlaybackSpeedPill = computed(
  () => isStreamingActiveDownload.value || liveDownloadSpeed.value > 0 || liveUploadSpeed.value > 0
);

const episodeSummaryStats = computed(() => {
  const list = unifiedEpisodes.value;
  const watchable = list.filter((ep) => ep.state !== "upcoming").length;
  if (props.detailLoading) {
    return "";
  }
  if (list.length > watchable) {
    return `${watchable} / ${list.length}`;
  }
  return `${list.length} 话`;
});

function isCurrentEpisode(ep: UnifiedPlaybackEpisode): boolean {
  return activeEpisode.value?.key === ep.key;
}

function isPendingEpisode(ep: UnifiedPlaybackEpisode): boolean {
  if (props.pendingAutoPlayGid && (ep.downloadingGid === props.pendingAutoPlayGid || ep.playableGid === props.pendingAutoPlayGid)) {
    return true;
  }
  if (props.queueingMikanId && ep.bestMikanItem?.id === props.queueingMikanId) {
    return true;
  }
  return false;
}

function episodeTooltip(ep: UnifiedPlaybackEpisode): string {
  const head = ep.episodeTitle ? `${ep.fullLabel} · ${ep.episodeTitle}` : ep.fullLabel;
  if (ep.state === "available" && ep.bestMikanItem) {
    const sub = ep.bestMikanItem.subgroup?.trim() || extractSubgroup(ep.bestMikanItem.title);
    const res = extractResolutionLabel(ep.bestMikanItem.title);
    const tag = [sub, res].filter(Boolean).join(" · ");
    return `${head}\n点击自动下载并播放${tag ? `（${tag}）` : ""}`;
  }
  return `${head} · ${ep.statusText}`;
}

function handleEpisodeClick(ep: UnifiedPlaybackEpisode): void {
  if (isCurrentEpisode(ep)) {
    return;
  }
  if (ep.state === "playable" && ep.playableGid) {
    emit("playGid", ep.playableGid);
    return;
  }
  if (ep.state === "downloading" && ep.downloadingGid) {
    emit("selectDownloading", ep.downloadingGid);
    return;
  }
  if (ep.state === "available" && ep.bestMikanItem) {
    emit("queueMikan", ep.bestMikanItem);
  }
}

const externalPlayerMenuRef = ref<HTMLDetailsElement | null>(null);
const externalPlayerOptions = computed<Array<{ id: ExternalPlayerId; label: string }>>(() => [
  { id: "system", label: "系统默认" },
  ...props.playback.availablePlayers.filter((item) => item.id !== "system")
]);

function closeExternalPlayerMenu(event: PointerEvent): void {
  const menu = externalPlayerMenuRef.value;
  if (menu?.open && !menu.contains(event.target as Node)) {
    menu.open = false;
  }
}

onMounted(() => window.addEventListener("pointerdown", closeExternalPlayerMenu));
onBeforeUnmount(() => window.removeEventListener("pointerdown", closeExternalPlayerMenu));

function openExternalPlayer(playerId: ExternalPlayerId): void {
  if (externalPlayerMenuRef.value) {
    externalPlayerMenuRef.value.open = false;
  }
  emit("externalPlayer", playerId);
}

function playNextEpisode(): void {
  const list = unifiedEpisodes.value;
  const index = list.findIndex((ep) => ep.key === activeEpisode.value?.key);
  if (index < 0) return;
  const next = list.slice(index + 1).find((ep) => ep.state === "playable" || ep.state === "available");
  if (!next) return;
  if (next.state === "playable" && next.playableGid) {
    emit("playGid", next.playableGid);
  } else if (next.state === "available" && next.bestMikanItem) {
    emit("queueMikan", next.bestMikanItem);
  }
}

function formatMikanOptionLabel(item: MikanDownloadItem): string {
  const sub = item.subgroup?.trim() || extractSubgroup(item.title) || "资源";
  const res = extractResolutionLabel(item.title);
  const ext = /\.mp4(?:\s|$|\]|\))/i.test(item.title) ? "MP4" : /\.mkv(?:\s|$|\]|\))/i.test(item.title) ? "MKV" : null;
  return [sub, res, ext].filter(Boolean).join(" · ");
}
</script>

<template>
  <div class="motion-rise @container min-h-0 overflow-auto p-3 sm:p-4 max-md:pb-[max(5.5rem,calc(env(safe-area-inset-bottom)+4.5rem))]">
    <header class="mb-3 flex min-w-0 items-center gap-2.5">
      <button
        type="button"
        class="playback-back-area group flex min-w-0 flex-1 items-center gap-2.5 rounded-xl px-2 py-1.5 -ml-2 text-left transition hover:bg-[var(--panel)]"
        aria-label="返回下载管理"
        title="点击返回下载管理"
        @click="emit('close')"
      >
        <span class="grid size-8 shrink-0 place-items-center rounded-lg text-[var(--muted)] transition group-hover:text-[var(--ink)]">
          <AppNavIcon name="chevron-left" class="size-4.5 transition-transform group-hover:-translate-x-0.5" />
        </span>
        <span class="min-w-0 flex-1">
          <span class="block truncate text-base font-semibold leading-snug text-[var(--ink)]" :title="playback.fileName">
            {{ animeDetail?.anime.title || group?.title || playback.fileName }}
          </span>
          <span class="block truncate text-xs text-[var(--muted)]">{{ playbackMetaLine }}</span>
        </span>
      </button>
      <span
        v-if="showPlaybackSpeedPill"
        class="inline-flex shrink-0 items-center gap-2 rounded-full border border-[var(--line)] bg-[var(--panel)] px-3 py-1.5 text-xs font-semibold tabular-nums"
        :title="`边下边播实时速率：下载 ${formatSpeed(liveDownloadSpeed)} · 上传 ${formatSpeed(liveUploadSpeed)}`"
      >
        <span v-if="isStreamingActiveDownload && activeDownloadPercent !== null" class="text-[var(--accent-vivid)]">
          缓冲 {{ activeDownloadPercent }}%
        </span>
        <span :class="liveDownloadSpeed > 0 ? 'text-[var(--ok)]' : 'text-[var(--muted)]'">
          ↓ <span :class="liveDownloadSpeed > 0 ? 'text-[var(--ink)]' : ''">{{ formatSpeed(liveDownloadSpeed) }}</span>
        </span>
        <span v-if="liveUploadSpeed > 0" class="max-sm:hidden text-[var(--accent)]">
          ↑ <span class="text-[var(--ink)]">{{ formatSpeed(liveUploadSpeed) }}</span>
        </span>
      </span>
      <button
        v-if="SHARE_FEATURE_ENABLED"
        type="button"
        class="btn-ghost inline-flex h-9 min-w-[2.5rem] shrink-0 items-center justify-center gap-1.5 px-3 text-xs font-semibold"
        title="生成在线播放分享链接"
        @click="emit('share', playbackEpisodeLabel)"
      >
        <AppNavIcon name="share" class="size-3.5" aria-hidden="true" />
        <span class="max-sm:sr-only">分享</span>
      </button>
      <details ref="externalPlayerMenuRef" class="external-menu relative shrink-0">
        <summary class="btn-ghost inline-flex h-9 min-w-[2.5rem] cursor-pointer list-none items-center justify-center gap-1.5 px-3 text-xs font-semibold">
          <AppNavIcon name="external" class="size-3.5" aria-hidden="true" />
          <span class="max-sm:sr-only">外部播放器</span>
        </summary>
        <div class="absolute right-0 top-full z-30 mt-1 grid min-w-[9.5rem] gap-0.5 rounded-lg border border-[var(--line)] bg-[var(--card)] p-1 shadow-[var(--strong-shadow)]">
          <button
            v-for="player in externalPlayerOptions"
            :key="player.id"
            type="button"
            class="rounded-md px-3 py-2 text-left text-xs font-semibold text-[var(--ink)] hover:bg-[var(--tint-2)]"
            @click="openExternalPlayer(player.id)"
          >
            {{ player.label }}
          </button>
        </div>
      </details>
    </header>

    <div class="grid content-start gap-3.5 @min-[56rem]:grid-cols-[minmax(0,1fr)_19rem]">
      <div class="grid min-w-0 content-start gap-3">
        <ArtPlayerPane
          v-if="playback.supportsInAppPlayback || playback.supportsTranscodePlayback"
          :gid="playback.gid"
          :stream-url="streamUrl"
          :playback-key="`${playback.filePath}:${playback.deliveryMode}`"
          :expected-duration-seconds="playback.durationSeconds"
          :hls="playback.deliveryMode === 'hls-remux' || playback.deliveryMode === 'hls-transcode'"
          :subtitle-tracks="playback.subtitleTracks"
          :subtitle-url-for="playbackSubtitleUrl"
          :history-meta="playbackHistoryMeta"
          @ended="playNextEpisode"
        />
        <div v-else class="grid aspect-video place-items-center rounded-xl bg-black px-6 text-center text-sm text-white/75">
          当前文件不支持内置播放器。常见的 MKV / HEVC / 高码率文件建议用右上角的外部播放器打开。
        </div>
      </div>

      <!-- 剧集面板（宽屏居右，窄屏紧贴画面下方） -->
      <aside class="grid content-start gap-2.5 rounded-xl border border-[var(--line)] bg-[var(--card)] p-3">
        <div class="flex items-center justify-between gap-2">
          <div class="flex items-center gap-1.5">
            <h3 class="text-[13px] font-semibold text-[var(--ink)]">剧集</h3>
            <AppNavIcon
              v-if="detailLoading"
              name="refresh"
              class="size-3 animate-spin text-[var(--muted)]"
              aria-hidden="true"
            />
          </div>
          <span class="text-[11px] text-[var(--muted)] tabular-nums">{{ episodeSummaryStats }}</span>
        </div>

        <!-- 等待缓冲自动切集的实时状态条 -->
        <div
          v-if="pendingEpisode"
          class="flex items-center justify-between gap-2 rounded-lg border border-[var(--accent-line)] bg-[var(--accent-soft)] px-2.5 py-1.5 text-xs"
        >
          <span class="min-w-0 truncate font-medium text-[var(--ink)]">
            缓冲 {{ pendingEpisode.fullLabel }}
            <span v-if="pendingEpisode.downloadProgress !== null" class="tabular-nums text-[var(--accent-vivid)]">
              · {{ pendingEpisode.downloadProgress }}%
            </span>
          </span>
          <button
            type="button"
            class="shrink-0 text-[11px] font-semibold text-[var(--muted)] hover:text-[var(--ink)]"
            @click="emit('cancelAutoPlay')"
          >
            取消
          </button>
        </div>

        <!-- 选集宫格 -->
        <div class="grid max-h-[20rem] grid-cols-[repeat(auto-fill,minmax(2.85rem,1fr))] gap-1.5 overflow-y-auto pr-0.5">
          <button
            v-for="ep in unifiedEpisodes"
            :key="ep.key"
            type="button"
            class="ep-cell tabular-nums"
            :class="{
              'is-current': isCurrentEpisode(ep),
              'is-pending': !isCurrentEpisode(ep) && isPendingEpisode(ep),
              'is-playable': !isCurrentEpisode(ep) && ep.state === 'playable',
              'is-downloading': !isCurrentEpisode(ep) && ep.state === 'downloading',
              'is-available': !isCurrentEpisode(ep) && ep.state === 'available',
              'is-upcoming': ep.state === 'upcoming'
            }"
            :disabled="ep.state === 'upcoming' || Boolean(queueingMikanId && ep.bestMikanItem?.id === queueingMikanId)"
            :title="episodeTooltip(ep)"
            :aria-label="`${ep.fullLabel}，${ep.statusText}`"
            :aria-current="isCurrentEpisode(ep)"
            @click="handleEpisodeClick(ep)"
          >
            <span>{{ ep.shortLabel }}</span>
            <span
              v-if="!isCurrentEpisode(ep) && ep.state === 'playable'"
              class="ep-dot"
              aria-hidden="true"
            />
            <span
              v-else-if="!isCurrentEpisode(ep) && ep.state === 'downloading'"
              class="ep-sub"
              aria-hidden="true"
            >
              {{ ep.downloadProgress ?? 0 }}%
            </span>
            <span
              v-else-if="!isCurrentEpisode(ep) && ep.state === 'available'"
              class="ep-dl-mark"
              aria-hidden="true"
            >
              ↓
            </span>
          </button>
        </div>

        <!-- 当前集其它字幕组片源切换（有多片源时可按需展开） -->
        <details
          v-if="activeEpisode && activeEpisode.mikanItems.length > 1"
          class="group border-t border-[var(--line)] pt-2 text-xs"
        >
          <summary class="flex cursor-pointer list-none items-center justify-between py-1 text-[11.5px] font-medium text-[var(--ink-secondary)] hover:text-[var(--ink)]">
            <span>其他片源 ({{ activeEpisode.mikanItems.length }})</span>
            <AppNavIcon name="chevron-right" class="size-3.5 transition-transform group-open:rotate-90" aria-hidden="true" />
          </summary>
          <div class="mt-1.5 grid max-h-36 gap-1 overflow-y-auto pr-0.5">
            <button
              v-for="item in activeEpisode.mikanItems"
              :key="item.id"
              type="button"
              class="flex min-h-[36px] items-center justify-between gap-2 rounded-md bg-[var(--panel)] px-2.5 py-2 text-left text-[11px] text-[var(--ink)] hover:bg-[var(--tint-2)]"
              :title="item.title"
              @click="emit('queueMikan', item)"
            >
              <span class="min-w-0 flex-1 truncate font-medium">{{ formatMikanOptionLabel(item) }}</span>
              <AppNavIcon name="play" class="size-3 shrink-0 text-[var(--accent-vivid)]" aria-hidden="true" />
            </button>
          </div>
        </details>
      </aside>
    </div>
  </div>
</template>

<style scoped>
.playback-back-btn {
  width: 2.75rem;
  height: 2.75rem;
  border-radius: 12px;
}

.ep-cell {
  position: relative;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  min-height: 42px;
  border-radius: 8px;
  border: 1px solid transparent;
  font-size: 13px;
  font-weight: 600;
  line-height: 1.1;
  transition: background-color 0.12s ease, border-color 0.12s ease, color 0.12s ease;
}

.ep-cell.is-current {
  background: var(--accent);
  color: var(--accent-contrast);
}

.ep-cell.is-playable {
  background: var(--panel);
  border-color: var(--accent-line);
  color: var(--ink);
}

.ep-cell.is-playable:hover {
  border-color: var(--accent);
}

.ep-cell.is-downloading,
.ep-cell.is-pending {
  background: var(--accent-soft);
  border-color: var(--accent-vivid);
  color: var(--accent-vivid);
}

.ep-cell.is-available {
  background: var(--panel);
  color: var(--ink-secondary);
}

.ep-cell.is-available:hover:not(:disabled) {
  border-color: var(--line);
  background: var(--tint-2);
  color: var(--ink);
}

.ep-cell.is-upcoming {
  background: var(--tint-1);
  color: var(--muted);
  opacity: 0.42;
  cursor: not-allowed;
}

.ep-dot {
  position: absolute;
  top: 4px;
  right: 4px;
  width: 5px;
  height: 5px;
  border-radius: 999px;
  background: var(--ok);
}

.ep-dl-mark {
  position: absolute;
  top: 2px;
  right: 4px;
  font-size: 9px;
  font-weight: 700;
  line-height: 1;
  color: var(--muted);
}

.ep-sub {
  font-size: 9px;
  font-weight: 600;
  opacity: 0.85;
}
</style>
