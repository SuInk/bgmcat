<script setup lang="ts">
import { computed, ref, watch } from "vue";

import type { DownloadJob, PlaybackSubtitleTrack } from "@shared/index";
import { sourceTypeLabel, type SourceType } from "@shared/index";

import {
  getDownloadAssSubtitleUrl,
  getDownloadHlsPlaylistUrl,
  getDownloadStreamUrl,
  getDownloadSubtitleUrl,
  resolveDownloadArtworkUrl
} from "../api";
import { cloudArchiveLabel } from "../lib/cloudArchive";
import AppDialog from "../components/common/AppDialog.vue";
import PagerBar from "../components/common/PagerBar.vue";
import AppNavIcon from "../components/layout/AppNavIcon.vue";
import ArtPlayerPane from "../components/downloads/ArtPlayerPane.vue";
import type { PlayerEpisodeOption } from "../components/downloads/ArtPlayerPane.vue";
import DownloadVersionPane from "../components/downloads/DownloadVersionPane.vue";
import type { DownloadEpisodeGroup } from "../components/downloads/DownloadVersionPane.vue";
import { extractEpisodeNumberFromTitle } from "@shared/episode";
import { trimTitleSeparators } from "../lib/downloadTitle";
import { buildPaginationItems, formatBytes, progressPercent } from "../lib/format";
import { isBusy, isBusyMatching } from "../stores/busy";
import { animeDetail, animeDetailError, closeAnimeDetail, openDownloadAnimeDetail } from "../stores/discovery";
import {
  activePlayback,
  closePlayback,
  displayTitle,
  downloadRawError,
  downloads,
  handleDownloadConflict,
  isDownloadRowBusy,
  launchExternalPlayer,
  manualDownloadForm,
  inferredManualSourceType,
  openPlayback,
  pauseAllDownloads,
  resumeAllDownloads,
  selectedDownloadDetailGid,
  submitManualDownload,
  totalDownloadSpeed,
  totalUploadSpeed,
  updateDownloadState
} from "../stores/downloads";
import { subscriptions } from "../stores/subscriptions";
import { selectTab } from "../stores/boot";

type DownloadFilterKey = "all" | "active" | "waiting" | "paused" | "complete" | "issues";
type DownloadSortMode = "status" | "updated" | "progress";
type EpisodeDownloadGroup = DownloadEpisodeGroup & {
  episodeNumber: number | null;
};

type SeriesDownloadGroup = {
  key: string;
  title: string;
  downloads: DownloadJob[];
  episodes: EpisodeDownloadGroup[];
};

const DOWNLOAD_STATUS_PRIORITY: Record<DownloadJob["status"], number> = {
  active: 0,
  waiting: 1,
  queued: 2,
  paused: 3,
  error: 4,
  complete: 5,
  removed: 6
};

const filterKey = ref<DownloadFilterKey>("all");
const sortMode = ref<DownloadSortMode>("status");
const searchQuery = ref("");
const deleteCandidate = ref<DownloadJob | null>(null);
const batchDeleteCandidates = ref<DownloadJob[]>([]);
const deleteLocalFiles = ref(true);
const downloadListPage = ref(1);

const downloadRowsPerPage = ref(8);
/**
 * 就地展开的那一行（同时只展开一行）。
 *
 * 展开区里有分集、版本、日志三块，纵向不矮；允许多行同时展开会把刚点的那行顶出视口，
 * 而且番剧详情（openDownloadAnimeDetail）本来就是单目标的，多行展开只会互相抢同一份数据。
 * 要横向比对版本请走番剧页。
 */
const activeDownloadGroupKey = ref<string | null>(null);
/** 分组 key 由标题推导，订阅/种子元数据晚到会让它变形；用 gid 做锚点才不会把已展开的行收起来 */
const activeGroupAnchorGid = ref<string | null>(null);
const selectedGroupKeys = ref<Set<string>>(new Set());
const manualOptionsOpen = ref(false);
const manualComposerOpen = ref(false);

const detailLoading = computed(() => isBusyMatching((key) => key.startsWith("download-detail:")));

const activePlaybackStreamUrl = computed(() =>
  activePlayback.value
    ? activePlayback.value.deliveryMode === "direct"
      ? getDownloadStreamUrl(activePlayback.value.gid)
      : activePlayback.value.deliveryMode === "hls-remux" || activePlayback.value.deliveryMode === "hls-transcode"
        ? getDownloadHlsPlaylistUrl(activePlayback.value.gid)
        : ""
    : ""
);

/** ass/ssa 走直出端点交给 ass 插件；其余轨仍旧转 WebVTT */
function playbackSubtitleUrl(track: PlaybackSubtitleTrack): string {
  const gid = activePlayback.value?.gid;
  if (!gid) {
    return "";
  }
  const codec = track.codec?.toLowerCase() ?? "";
  return codec === "ass" || codec === "ssa"
    ? getDownloadAssSubtitleUrl(gid, track.id)
    : getDownloadSubtitleUrl(gid, track.id);
}

/** 播放中的任务属于哪一组、哪一集——选集面板与自动连播都要用 */
const playbackGroup = computed(() =>
  activePlayback.value
    ? groupedDownloads.value.find((group) =>
        group.downloads.some((download) => download.gid === activePlayback.value?.gid)
      ) ?? null
    : null
);

const playbackEpisodeKey = computed(
  () =>
    playbackGroup.value?.episodes.find((episode) =>
      episode.downloads.some((download) => download.gid === activePlayback.value?.gid)
    )?.key ?? null
);

const playbackEpisodeOptions = computed<PlayerEpisodeOption[]>(() =>
  (playbackGroup.value?.episodes ?? []).map((episode) => {
    const playable = playableDownloadForEpisode(episode);
    return {
      key: episode.key,
      label: episode.episodeLabel ?? episode.title,
      gid: (playable ?? episode.downloads[0]).gid,
      statusLabel: statusLabel(primaryDownloadForDownloads(episode.downloads).status),
      playable: Boolean(playable)
    };
  })
);

function playEpisodeOption(option: PlayerEpisodeOption): void {
  const download = downloads.value.find((item) => item.gid === option.gid);
  if (download) {
    void openPlayback(download);
  }
}

/** 播完自动进下一集：只跳到下一个能播的集，播完最后一集就停 */
function playNextEpisode(): void {
  const options = playbackEpisodeOptions.value;
  const index = options.findIndex((option) => option.key === playbackEpisodeKey.value);
  const next = index >= 0 ? options.slice(index + 1).find((option) => option.playable) : undefined;
  if (next) {
    playEpisodeOption(next);
  }
}

function matchesFilter(download: DownloadJob, currentFilter: DownloadFilterKey): boolean {
  switch (currentFilter) {
    case "active":
      return download.status === "active";
    case "waiting":
      return download.status === "waiting" || download.status === "queued";
    case "paused":
      return download.status === "paused";
    case "issues":
      return download.status === "error" || Boolean(download.issueKind);
    case "complete":
      return download.status === "complete";
    default:
      return true;
  }
}

const filteredDownloads = computed(() => {
  const query = searchQuery.value.trim().toLowerCase();
  return downloads.value.filter((download) => {
    if (!matchesFilter(download, filterKey.value)) {
      return false;
    }
    if (!query) {
      return true;
    }
    const title = displayTitle(download).toLowerCase();
    const gid = download.gid.toLowerCase();
    return title.includes(query) || gid.includes(query);
  });
});

function compareDownloads(left: DownloadJob, right: DownloadJob): number {
  if (sortMode.value === "updated") {
    return right.updatedAt.localeCompare(left.updatedAt);
  }

  const leftProgress = progressPercent(left);
  const rightProgress = progressPercent(right);
  if (sortMode.value === "progress") {
    return rightProgress - leftProgress || right.updatedAt.localeCompare(left.updatedAt);
  }

  const statusDelta = DOWNLOAD_STATUS_PRIORITY[left.status] - DOWNLOAD_STATUS_PRIORITY[right.status];
  if (statusDelta !== 0) {
    return statusDelta;
  }
  if (leftProgress !== rightProgress) {
    return leftProgress - rightProgress;
  }
  return right.updatedAt.localeCompare(left.updatedAt);
}

function aggregateGroupProgress(groupDownloads: DownloadJob[]): number {
  const total = groupDownloads.reduce((sum, download) => sum + download.totalLength, 0);
  const completed = groupDownloads.reduce((sum, download) => sum + download.completedLength, 0);
  if (total > 0) {
    return Math.max(0, Math.min(100, Math.round((completed / total) * 100)));
  }
  return Math.max(...groupDownloads.map(progressPercent), 0);
}

function bestGroupStatusPriority(groupDownloads: DownloadJob[]): number {
  return Math.min(...groupDownloads.map((download) => DOWNLOAD_STATUS_PRIORITY[download.status]));
}

const groupedDownloads = computed<SeriesDownloadGroup[]>(() => {
  const seriesGroups = new Map<string, SeriesDownloadGroup>();
  for (const download of filteredDownloads.value) {
    const title = displayTitle(download);
    const episode = extractEpisodeNumber(title);
    const bangumiId = resolveDownloadBangumiId(download);
    const series = normalizeSeriesTitle(title, episode);
    const seriesKey = bangumiId ? `bangumi:${bangumiId}` : `series:${series || title}`;
    const episodeKey = `${seriesKey}::episode:${episode ?? "unknown"}`;

    let seriesGroup = seriesGroups.get(seriesKey);
    if (!seriesGroup) {
      seriesGroup = { key: seriesKey, title: series || title, downloads: [], episodes: [] };
      seriesGroups.set(seriesKey, seriesGroup);
    }

    seriesGroup.downloads.push(download);
    let episodeGroup = seriesGroup.episodes.find((group) => group.key === episodeKey);
    if (!episodeGroup) {
      episodeGroup = {
        key: episodeKey,
        title: series || title,
        episodeLabel: episode ? `第 ${episode.padStart(2, "0")} 集` : "未知集数",
        episodeNumber: episode ? Number(episode) : null,
        downloads: []
      };
      seriesGroup.episodes.push(episodeGroup);
    }
    episodeGroup.downloads.push(download);
  }

  return Array.from(seriesGroups.values())
    .map((seriesGroup) => ({
      ...seriesGroup,
      downloads: [...seriesGroup.downloads].sort(compareDownloads),
      episodes: seriesGroup.episodes
        .map((episodeGroup) => ({
          ...episodeGroup,
          downloads: [...episodeGroup.downloads].sort(compareDownloads)
        }))
        .sort((left, right) => {
          if (left.episodeNumber !== null && right.episodeNumber !== null) {
            return left.episodeNumber - right.episodeNumber;
          }
          if (left.episodeNumber !== null) {
            return -1;
          }
          if (right.episodeNumber !== null) {
            return 1;
          }
          return right.downloads[0]?.updatedAt.localeCompare(left.downloads[0]?.updatedAt ?? "") ?? 0;
        })
    }))
    .sort((left, right) => {
      if (sortMode.value === "updated") {
        return (right.downloads[0]?.updatedAt ?? "").localeCompare(left.downloads[0]?.updatedAt ?? "");
      }
      if (sortMode.value === "progress") {
        return aggregateGroupProgress(right.downloads) - aggregateGroupProgress(left.downloads);
      }
      const statusDelta = bestGroupStatusPriority(left.downloads) - bestGroupStatusPriority(right.downloads);
      if (statusDelta !== 0) {
        return statusDelta;
      }
      const progressDelta = aggregateGroupProgress(left.downloads) - aggregateGroupProgress(right.downloads);
      if (progressDelta !== 0) {
        return progressDelta;
      }
      return left.title.localeCompare(right.title, "zh-CN");
    });
});

const downloadPageSizeOptions = [6, 8, 12, 18];
const downloadPageItemCount = computed(() => Math.max(1, downloadRowsPerPage.value));
const downloadSeriesTotalPages = computed(() =>
  Math.max(1, Math.ceil(groupedDownloads.value.length / downloadPageItemCount.value))
);
const pagedDownloadGroups = computed(() => {
  const start = (downloadListPage.value - 1) * downloadPageItemCount.value;
  return groupedDownloads.value.slice(start, start + downloadPageItemCount.value);
});
const downloadListPaginationItems = computed(() =>
  buildPaginationItems(downloadListPage.value, downloadSeriesTotalPages.value)
);
const filterTabs = computed(() => [
  { key: "all" as const, label: "全部", count: downloads.value.length },
  { key: "active" as const, label: "下载中", count: downloads.value.filter((d) => d.status === "active").length },
  {
    key: "waiting" as const,
    label: "等待中",
    count: downloads.value.filter((d) => d.status === "waiting" || d.status === "queued").length
  },
  { key: "paused" as const, label: "暂停", count: downloads.value.filter((d) => d.status === "paused").length },
  { key: "complete" as const, label: "已完成", count: downloads.value.filter((d) => d.status === "complete").length },
  {
    key: "issues" as const,
    label: "异常",
    count: downloads.value.filter((d) => d.status === "error" || Boolean(d.issueKind)).length
  }
]);

const selectedGroups = computed(() => groupedDownloads.value.filter((group) => selectedGroupKeys.value.has(group.key)));
const selectedDownloads = computed(() => selectedGroups.value.flatMap((group) => group.downloads));
const selectedActionableDownloads = computed(() =>
  selectedDownloads.value.filter((download) => download.status !== "complete" && download.status !== "removed")
);
const allPagedGroupsSelected = computed(
  () => pagedDownloadGroups.value.length > 0 && pagedDownloadGroups.value.every((group) => selectedGroupKeys.value.has(group.key))
);

function setGroupSelected(key: string, selected: boolean): void {
  const next = new Set(selectedGroupKeys.value);
  if (selected) {
    next.add(key);
  } else {
    next.delete(key);
  }
  selectedGroupKeys.value = next;
}

function togglePagedGroups(selected: boolean): void {
  const next = new Set(selectedGroupKeys.value);
  for (const group of pagedDownloadGroups.value) {
    if (selected) {
      next.add(group.key);
    } else {
      next.delete(group.key);
    }
  }
  selectedGroupKeys.value = next;
}

function clearSelectedGroups(): void {
  selectedGroupKeys.value = new Set();
}

function runBatchTransfer(action: "pause" | "resume"): void {
  const statuses = action === "pause" ? ["active", "waiting", "queued"] : ["paused", "error"];
  const targets = selectedDownloads.value.filter((download) => statuses.includes(download.status));
  for (const download of targets) {
    void updateDownloadState(action, download.gid, { silent: true });
  }
}

function requestDelete(download: DownloadJob): void {
  deleteCandidate.value = download;
  batchDeleteCandidates.value = [];
  deleteLocalFiles.value = true;
}

function requestBatchDelete(): void {
  batchDeleteCandidates.value = selectedDownloads.value;
  deleteCandidate.value = null;
  deleteLocalFiles.value = true;
}

function closeDeleteDialog(): void {
  deleteCandidate.value = null;
  batchDeleteCandidates.value = [];
  deleteLocalFiles.value = true;
}

function confirmDelete(): void {
  if (batchDeleteCandidates.value.length) {
    for (const download of batchDeleteCandidates.value) {
      void updateDownloadState("remove", download.gid, { deleteLocalFiles: deleteLocalFiles.value });
    }
    clearSelectedGroups();
    closeDeleteDialog();
    return;
  }

  const download = deleteCandidate.value;
  if (!download) {
    return;
  }
  void updateDownloadState("remove", download.gid, { deleteLocalFiles: deleteLocalFiles.value });
  closeDeleteDialog();
}

function primaryDownloadForGroup(group: SeriesDownloadGroup): DownloadJob {
  return [...group.downloads].sort(compareDownloads)[0] ?? group.downloads[0];
}

function primaryDownloadForDownloads(groupDownloads: DownloadJob[]): DownloadJob {
  return [...groupDownloads].sort(compareDownloads)[0] ?? groupDownloads[0];
}

/**
 * 行级播放键指向「最新一个能播的集」：同番有新集在下载时，代表任务是那个活跃的新集，
 * 但用户想点的是已经下完的最后一集。按集数取最大，集数未知时退到最近更新的那条。
 */
function playableDownloadForGroup(group: SeriesDownloadGroup): DownloadJob | null {
  let best: { download: DownloadJob; episodeNumber: number | null } | null = null;
  for (const episode of group.episodes) {
    for (const download of episode.downloads) {
      if (!canPlayDownload(download)) {
        continue;
      }
      if (!best) {
        best = { download, episodeNumber: episode.episodeNumber };
        continue;
      }
      const leftEpisode = episode.episodeNumber;
      const rightEpisode = best.episodeNumber;
      if (leftEpisode !== null && rightEpisode !== null) {
        if (leftEpisode > rightEpisode) {
          best = { download, episodeNumber: leftEpisode };
        }
        continue;
      }
      if (leftEpisode !== null && rightEpisode === null) {
        best = { download, episodeNumber: leftEpisode };
        continue;
      }
      if (leftEpisode === null && rightEpisode === null && download.updatedAt > best.download.updatedAt) {
        best = { download, episodeNumber: null };
      }
    }
  }
  return best?.download ?? null;
}

function playableEpisodeLabelForGroup(group: SeriesDownloadGroup): string | null {
  const target = playableDownloadForGroup(group);
  if (!target) {
    return null;
  }
  return group.episodes.find((episode) => episode.downloads.some((item) => item.gid === target.gid))?.episodeLabel ?? null;
}

function playableDownloadForEpisode(episode: EpisodeDownloadGroup): DownloadJob | null {
  return episode.downloads.find(canPlayDownload) ?? null;
}

function groupStatus(group: SeriesDownloadGroup): DownloadJob["status"] {
  return primaryDownloadForGroup(group).status;
}

function statusLabel(status: DownloadJob["status"]): string {
  switch (status) {
    case "active":
      return "下载中";
    case "waiting":
    case "queued":
      return "等待中";
    case "paused":
      return "暂停中";
    case "complete":
      return "已完成";
    case "error":
      return "异常";
    case "removed":
      return "已删除";
    default:
      return status;
  }
}

function statusPillClass(status: DownloadJob["status"]): string {
  switch (status) {
    case "active":
      return "border-[var(--accent-line)] bg-[var(--accent-soft)] text-[var(--accent)]";
    case "complete":
      return "border-[var(--ok-soft)] bg-[var(--ok-soft)] text-[var(--ok)]";
    case "waiting":
    case "queued":
      return "border-[var(--warn-soft)] bg-[var(--warn-soft)] text-[var(--warn)]";
    case "paused":
      return "border-[var(--line)] bg-[var(--panel)] text-[var(--muted)]";
    case "error":
      return "border-[var(--err-soft)] bg-[var(--err-soft)] text-[var(--err)]";
    default:
      return "border-[var(--line)] bg-[var(--tint-2)] text-[var(--muted)]";
  }
}

function canPauseDownload(download: DownloadJob): boolean {
  return ["active", "waiting", "queued"].includes(download.status);
}

function canResumeDownload(download: DownloadJob): boolean {
  return ["paused", "error"].includes(download.status);
}

function canPlayDownload(download: DownloadJob): boolean {
  return download.canPlayExistingFile || download.status === "complete";
}

function groupProgress(group: SeriesDownloadGroup): number {
  return aggregateGroupProgress(group.downloads);
}

function groupCompletedLength(group: SeriesDownloadGroup): number {
  return group.downloads.reduce((sum, download) => sum + download.completedLength, 0);
}

function groupTotalLength(group: SeriesDownloadGroup): number {
  return group.downloads.reduce((sum, download) => sum + download.totalLength, 0);
}

function groupDownloadSpeed(group: SeriesDownloadGroup): number {
  return group.downloads.reduce((sum, download) => sum + download.downloadSpeed, 0);
}

function groupUploadSpeed(group: SeriesDownloadGroup): number {
  return group.downloads.reduce((sum, download) => sum + download.uploadSpeed, 0);
}

function groupRemainingSeconds(group: SeriesDownloadGroup): number | null {
  const remaining = Math.max(0, groupTotalLength(group) - groupCompletedLength(group));
  const speed = groupDownloadSpeed(group);
  if (remaining <= 0 || speed <= 0) {
    return null;
  }
  return Math.ceil(remaining / speed);
}

function formatDuration(seconds: number | null): string {
  if (!seconds || seconds <= 0) {
    return "--";
  }
  const hours = Math.floor(seconds / 3600);
  const minutes = Math.floor((seconds % 3600) / 60);
  const rest = seconds % 60;
  if (hours > 0) {
    return `${hours}h ${String(minutes).padStart(2, "0")}m`;
  }
  if (minutes > 0) {
    return `${minutes}m ${String(rest).padStart(2, "0")}s`;
  }
  return `${rest}s`;
}

function currentEpisodeLabel(group: SeriesDownloadGroup): string {
  const activeEpisode = group.episodes.find((episode) =>
    episode.downloads.some((download) => ["active", "waiting", "queued", "paused"].includes(download.status))
  );
  return activeEpisode?.episodeLabel ?? group.episodes[0]?.episodeLabel ?? "--";
}

function resolutionLabel(download: DownloadJob): string | null {
  const match = displayTitle(download).match(/\b(2160p|1080p|720p|480p|4k)\b/i);
  return match?.[1]?.toUpperCase() ?? null;
}

function subgroupLabel(download: DownloadJob): string | null {
  const title = displayTitle(download);
  const bracket = title.match(/^\[([^\]]+)]/);
  if (bracket?.[1]) {
    return bracket[1];
  }
  return download.feedItemSubscriptionId
    ? subscriptions.value.find((subscription) => subscription.id === download.feedItemSubscriptionId)?.name ?? null
    : null;
}

function coverLabel(title: string): string {
  const cleaned = title
    .replace(/\[[^\]]+]/g, " ")
    .replace(/\([^\)]+\)/g, " ")
    .replace(/\.torrent$/i, "")
    .trim();
  const characters = cleaned.match(/[\p{Script=Han}\p{Script=Hiragana}\p{Script=Katakana}A-Za-z0-9]/gu);
  return characters?.slice(0, 2).join("").toUpperCase() ?? "番";
}

function coverStyle(seed: string): Record<string, string> {
  const hue = seed.split("").reduce((total, char) => total + char.charCodeAt(0), 0) % 360;
  return {
    background: `hsl(${hue} 14% 36%)`
  };
}

function resolveDownloadBangumiId(download: DownloadJob): string | null {
  const subscriptionUrl = download.feedItemSubscriptionId
    ? subscriptions.value.find((subscription) => subscription.id === download.feedItemSubscriptionId)?.url ?? null
    : null;
  const candidates = [download.feedItemGuid, download.feedItemLink, download.feedItemSourceUrl, subscriptionUrl];

  for (const candidate of candidates) {
    const id = extractBangumiId(candidate);
    if (id) {
      return id;
    }
  }
  return null;
}

function extractBangumiId(value: string | null | undefined): string | null {
  const raw = value?.trim();
  if (!raw) {
    return null;
  }

  if (raw.startsWith("anime:")) {
    return raw.slice("anime:".length).trim() || null;
  }

  try {
    const url = new URL(raw);
    const queryId = url.searchParams.get("bangumiId") ?? url.searchParams.get("BangumiId");
    if (queryId?.trim()) {
      return queryId.trim();
    }
    const pathMatch = url.pathname.match(/\/(?:Home\/)?Bangumi\/([^/?#]+)/i);
    if (pathMatch?.[1]) {
      return decodeURIComponent(pathMatch[1]);
    }
  } catch {
    const queryMatch = raw.match(/[?&]bangumiId=([^&#]+)/i);
    if (queryMatch?.[1]) {
      return decodeURIComponent(queryMatch[1]);
    }
    const pathMatch = raw.match(/\/(?:Home\/)?Bangumi\/([^/?#]+)/i);
    if (pathMatch?.[1]) {
      return decodeURIComponent(pathMatch[1]);
    }
  }

  return null;
}

function extractEpisodeNumber(title: string): string | null {
  return extractEpisodeNumberFromTitle(title);
}

function normalizeSeriesTitle(title: string, episode: string | null): string {
  let normalized = title
    .replace(/\.(?:mkv|mp4|avi|mov|webm|flv|ass|srt|torrent)$/i, " ")
    .replace(/\[[^\]]+]/g, " ")
    .replace(/【[^】]+】/g, " ")
    .replace(/\([^)]+\)/g, " ");

  if (episode) {
    normalized = normalized
      .replace(new RegExp(`(?:第\\s*)?0*${episode}(?:話|话|集).*`, "i"), " ")
      .replace(new RegExp(`\\b(?:ep?|e)0*${episode}\\b.*`, "i"), " ")
      .replace(new RegExp(`(?:^|[\\s._-])0*${episode}(?:v\\d+)?(?:[\\]\\)\\s._-]|$).*`, "i"), " ");
  }

  const cleaned = normalized
    .replace(/\bS\d{1,2}(?:\s*E\d{1,3})?\b.*$/i, " ")
    .replace(/\bSeason\s*\d+\b.*$/i, " ")
    .replace(/第\s*\d+\s*季.*$/i, " ")
    .replace(/\b(?:1080p|720p|2160p|4k|hevc|avc|x26[45]|aac|flac|web-?rip|baha|gb|big5|chs|cht|mp4|mkv)\b/gi, " ")
    .replace(/(?:日英双语|多国字幕|内嵌|外挂|字幕组|简日|繁日|简体|繁体|合集|先行|正式)/g, " ")
    .replace(/\s+/g, " ")
    .trim();

  // 剥离集数后常留下「尼古喵喵 -」这种连接符尾巴，分组标题要把它清掉
  return trimTitleSeparators(cleaned);
}

/** 点行即展开，再点同一行收起——表格本身拿全宽，不再切出一栏 */
function toggleDownloadGroup(key: string): void {
  if (activeDownloadGroupKey.value === key) {
    collapseDownloadGroup();
    return;
  }
  activeDownloadGroupKey.value = key;
  const group = groupedDownloads.value.find((item) => item.key === key);
  if (group) {
    const primary = primaryDownloadForGroup(group);
    activeGroupAnchorGid.value = primary.gid;
    void openDownloadAnimeDetail(primary);
  }
}

function collapseDownloadGroup(): void {
  activeDownloadGroupKey.value = null;
  activeGroupAnchorGid.value = null;
  closeAnimeDetail();
}

const effectiveManualSourceType = computed(
  () => manualDownloadForm.sourceTypeOverride ?? inferredManualSourceType.value
);

const manualSourceOptions: Array<{ value: SourceType | null; label: string; description: string }> = [
  { value: null, label: "自动识别", description: "按地址内容自动判断来源类型" },
  { value: "direct", label: "HTTP(S)", description: "普通直链下载地址" },
  { value: "magnet", label: "Magnet", description: "磁力链接" },
  { value: "torrent-url", label: "Torrent URL", description: ".torrent 文件地址" }
];

async function submitManual(): Promise<void> {
  await submitManualDownload();
  manualComposerOpen.value = false;
}

watch([downloadSeriesTotalPages, downloadListPage], () => {
  const clamped = Math.min(Math.max(1, downloadListPage.value), downloadSeriesTotalPages.value);
  if (clamped !== downloadListPage.value) {
    downloadListPage.value = clamped;
  }
});

watch([filterKey, searchQuery, sortMode], () => {
  downloadListPage.value = 1;
  activeDownloadGroupKey.value = null;
  activeGroupAnchorGid.value = null;
});

watch(groupedDownloads, () => {
  if (activeDownloadGroupKey.value && !groupedDownloads.value.some((group) => group.key === activeDownloadGroupKey.value)) {
    // 轮询重建分组后 key 可能变形（标题拿到新元数据、番剧 id 补齐）。
    // 只要锚点任务还在，就跟着它换到新 key，而不是把展开区收起来。
    const anchored = activeGroupAnchorGid.value
      ? groupedDownloads.value.find((group) => group.downloads.some((item) => item.gid === activeGroupAnchorGid.value))
      : undefined;
    if (anchored) {
      activeDownloadGroupKey.value = anchored.key;
    } else {
      activeDownloadGroupKey.value = null;
      activeGroupAnchorGid.value = null;
    }
  }
  selectedGroupKeys.value = new Set(
    Array.from(selectedGroupKeys.value).filter((key) => groupedDownloads.value.some((group) => group.key === key))
  );
});

watch(
  () => manualDownloadForm.url,
  (value) => {
    if (!value.trim()) {
      manualComposerOpen.value = false;
    }
  }
);

</script>

<template>
  <section class="grid h-full min-h-0 grid-cols-[minmax(0,1fr)] overflow-hidden rounded-lg border border-[var(--line)] bg-[var(--table-bg)]">
    <div v-if="activePlayback" class="grid min-h-0 grid-rows-[auto_auto] content-start gap-3 overflow-auto p-4">
      <section class="grid gap-4 rounded-lg border border-[var(--line)] bg-[var(--card)] p-5">
        <div class="flex flex-wrap items-start justify-between gap-4">
          <div class="min-w-0">
            <h2 class="break-words text-lg font-semibold text-[var(--ink)]">{{ activePlayback.fileName }}</h2>
          </div>
          <button type="button" class="btn-ghost px-4 py-2 text-sm font-semibold" @click="closePlayback">关闭</button>
        </div>
      </section>

      <div class="grid gap-3">
        <ArtPlayerPane
          v-if="activePlayback.supportsInAppPlayback || activePlayback.supportsTranscodePlayback"
          :gid="activePlayback.gid"
          :stream-url="activePlaybackStreamUrl"
          :playback-key="`${activePlayback.filePath}:${activePlayback.deliveryMode}`"
          :expected-duration-seconds="activePlayback.durationSeconds"
          :hls="activePlayback.deliveryMode === 'hls-remux' || activePlayback.deliveryMode === 'hls-transcode'"
          :subtitle-tracks="activePlayback.subtitleTracks"
          :subtitle-url-for="playbackSubtitleUrl"
          :episodes="playbackEpisodeOptions"
          :active-episode-key="playbackEpisodeKey"
          @select-episode="playEpisodeOption"
          @ended="playNextEpisode"
        />
        <div v-else class="grid h-full place-items-center rounded-lg border border-dashed border-[var(--line)] p-6 text-sm text-[var(--muted)]">
          当前文件不支持内置播放器。常见的 MKV / HEVC / 高码率文件建议直接调用 VLC、mpv、PotPlayer 或系统默认播放器。
        </div>
        <p
          v-if="activePlayback.deliveryMode === 'hls-remux' || activePlayback.deliveryMode === 'hls-transcode'"
          class="rounded-lg border border-dashed border-[var(--line)] bg-[var(--card)] px-4 py-3 text-center text-sm font-semibold text-[var(--muted)]"
        >
          {{ activePlayback.deliveryMode === "hls-remux" ? "当前文件正在通过本机 ffmpeg 转封装为 HLS 播放。" : "当前文件正在通过本机 ffmpeg 转码为浏览器兼容流。" }}
        </p>

        <div class="flex flex-wrap justify-center gap-2">
          <button type="button" class="btn-ghost px-3 py-2 text-sm font-semibold" @click="launchExternalPlayer(activePlayback.gid, 'system')">
            系统默认
          </button>
          <button
            v-for="player in activePlayback.availablePlayers.filter((item) => item.id !== 'system')"
            :key="player.id"
            type="button"
            class="btn-ghost px-3 py-2 text-sm font-semibold"
            @click="launchExternalPlayer(activePlayback.gid, player.id)"
          >
            {{ player.label }}
          </button>
        </div>
      </div>
    </div>

    <div v-else class="grid min-h-0 grid-rows-[auto_minmax(0,1fr)_auto] overflow-hidden">
      <!-- 与其他页同一套工具栏：第一行状态筛选 + 右侧总速度，第二行搜索 / 排序 / 批量与主操作 -->
      <div class="page-toolbar">
        <div class="toolbar-row">
          <div class="toolbar-chips" role="group" aria-label="按状态筛选">
            <button
              v-for="tab in filterTabs"
              :key="tab.key"
              type="button"
              class="chip"
              :class="{ 'chip-active': filterKey === tab.key }"
              :aria-pressed="filterKey === tab.key"
              @click="filterKey = tab.key"
            >
              {{ tab.label }}
              <span class="chip-count tabular-nums">{{ tab.count }}</span>
            </button>
          </div>
          <!-- 速度每 3 秒变一次；给数字固定最小宽度，不然会推着左边的 chip 来回跳 -->
          <span v-if="downloads.length" class="toolbar-meta flex items-center gap-3 tabular-nums">
            <span class="inline-block min-w-[4.5rem] text-right text-[var(--ink)]">↓ {{ formatBytes(totalDownloadSpeed) }}/s</span>
            <span class="inline-block min-w-[4.5rem] text-right">↑ {{ formatBytes(totalUploadSpeed) }}/s</span>
          </span>
        </div>

        <!-- 空列表时主操作交给空态卡片，不在工具栏摆一排永远禁用的按钮 -->
        <div v-if="downloads.length" class="toolbar-row flex-wrap">
          <label class="search-field w-full sm:w-auto sm:min-w-[14rem]">
            <AppNavIcon name="search" aria-hidden="true" />
            <input v-model="searchQuery" type="search" aria-label="搜索下载任务" placeholder="搜索番剧、集数、GID 或字幕组" />
          </label>
          <select v-model="sortMode" class="input-base h-[2.125rem] shrink-0 px-2.5 text-sm font-semibold" aria-label="排序方式">
            <option value="status">按状态</option>
            <option value="updated">按更新时间</option>
            <option value="progress">按进度</option>
          </select>
          <span
            v-if="detailLoading"
            class="status-pill status-pill-accent shrink-0"
          >
            <span class="size-1.5 animate-pulse rounded-full bg-current"></span>
            正在查询番剧详情…
          </span>
          <div class="ml-auto flex flex-wrap items-center justify-end gap-2">
            <button
              type="button"
              class="btn-ghost px-3 py-1.5 text-sm font-semibold"
              :disabled="!downloads.some((download) => download.status === 'paused')"
              @click="resumeAllDownloads"
            >
              全部开始
            </button>
            <button
              type="button"
              class="btn-ghost px-3 py-1.5 text-sm font-semibold"
              :disabled="!downloads.some((download) => ['active', 'waiting', 'queued'].includes(download.status))"
              @click="pauseAllDownloads"
            >
              全部暂停
            </button>
            <button type="button" class="btn-primary px-3 py-1.5 text-sm font-semibold" @click="manualComposerOpen = true">
              添加任务
            </button>
          </div>
        </div>
      </div>

      <!--
        断点看的是这一列的实际宽度，不是视口宽度。并排详情栏拆掉之后这一列就是整个内容区，
        宽屏下能排满「番剧 / 状态 / 进度 / 速度 / 操作」五列，番剧列拿走剩下的全部份额。
      -->
      <div class="@container min-h-0 overflow-auto px-4 pb-2">
        <!-- 空态不摆表头：0 条任务时列标题和全选框没有指代对象 -->
        <div
          v-if="!filteredDownloads.length"
          class="grid place-items-center rounded-lg border border-dashed border-[var(--line)] px-6 py-14 text-center"
        >
          <div class="grid max-w-md gap-3">
            <AppNavIcon name="inbox" class="mx-auto size-8 text-[var(--muted)]" aria-hidden="true" />
            <template v-if="downloads.length">
              <p class="text-sm font-semibold text-[var(--ink)]">当前筛选条件下没有任务</p>
              <button
                type="button"
                class="btn-ghost justify-self-center rounded-md px-4 py-2 text-sm font-semibold"
                @click="filterKey = 'all'; searchQuery = ''"
              >
                清除筛选
              </button>
            </template>
            <template v-else>
              <p class="text-sm font-semibold text-[var(--ink)]">还没有下载任务</p>
              <p class="text-sm text-[var(--muted)]">从首页点开番剧挑资源，或直接粘贴磁力／直链。</p>
              <div class="flex flex-wrap justify-center gap-2">
                <button
                  type="button"
                  class="btn-primary rounded-md px-4 py-2 text-sm font-semibold"
                  @click="manualComposerOpen = true"
                >
                  粘贴磁力/直链
                </button>
                <button
                  type="button"
                  class="btn-ghost rounded-md px-4 py-2 text-sm font-semibold"
                  @click="selectTab('home')"
                >
                  去首页找番剧
                </button>
              </div>
            </template>
          </div>
        </div>

        <div v-else>
          <!-- 表头和行的最后一列（操作）必须同宽：以前都写 auto，表头算出 24px、行算出 108px，中间几列整体错位 84px -->
          <div class="hidden grid-cols-[minmax(0,1fr)_auto] items-center gap-3 border-b border-[var(--line)] px-2 py-2.5 @min-[640px]:grid text-xs font-medium text-[var(--muted)] @min-[640px]:grid-cols-[2rem_minmax(9rem,1fr)_6rem_minmax(7rem,10rem)_6.75rem] @min-[1000px]:grid-cols-[2rem_minmax(14rem,1fr)_6.5rem_minmax(11rem,15rem)_6.5rem_6.75rem]">
            <label class="hidden size-6 place-items-center @min-[640px]:grid">
              <input
                type="checkbox"
                class="size-4 accent-[var(--accent)]"
                aria-label="全选本页任务"
                :checked="allPagedGroupsSelected"
                @change="togglePagedGroups(($event.target as HTMLInputElement).checked)"
              />
            </label>
            <span>番剧</span>
            <span class="hidden @min-[640px]:block">状态</span>
            <span class="hidden @min-[640px]:block">进度</span>
            <span class="hidden @min-[1000px]:block">速度</span>
            <span class="text-right">操作</span>
          </div>

          <div v-for="group in pagedDownloadGroups" :key="group.key">
          <article
            class="grid cursor-pointer grid-cols-[minmax(0,1fr)_auto] items-center gap-3 border-b px-2 py-2.5 transition @min-[640px]:grid-cols-[2rem_minmax(9rem,1fr)_6rem_minmax(7rem,10rem)_6.75rem] @min-[1000px]:grid-cols-[2rem_minmax(14rem,1fr)_6.5rem_minmax(11rem,15rem)_6.5rem_6.75rem]"
            :class="activeDownloadGroupKey === group.key ? 'border-[var(--accent)] bg-[var(--tint-2)]' : 'border-[var(--line)] hover:bg-[var(--tint-1)]'"
            @click="toggleDownloadGroup(group.key)"
          >
            <!-- 手机上没地方放多选框：批量操作留给宽屏，窄屏点行展开逐条处理 -->
            <label class="hidden size-6 place-items-center @min-[640px]:grid" @click.stop>
              <input
                type="checkbox"
                class="size-4 accent-[var(--accent)]"
                :aria-label="`选择 ${group.title}`"
                :checked="selectedGroupKeys.has(group.key)"
                @change="setGroupSelected(group.key, ($event.target as HTMLInputElement).checked)"
              />
            </label>

            <div class="flex min-w-0 items-center gap-3">
              <!-- 展开指示：收起时指右、展开时转向下。整行也能点，但键盘语义落在这个真按钮上 -->
              <button
                type="button"
                class="hidden size-5 shrink-0 place-items-center rounded text-sm leading-none transition-transform duration-200 @min-[640px]:grid"
                :class="activeDownloadGroupKey === group.key ? 'rotate-90 text-[var(--accent)]' : 'text-[var(--muted)]'"
                :aria-expanded="activeDownloadGroupKey === group.key"
                :aria-label="`${activeDownloadGroupKey === group.key ? '收起' : '展开'} ${group.title} 的分集`"
                @click.stop="toggleDownloadGroup(group.key)"
              >
                ›
              </button>
              <div class="relative size-10 shrink-0 overflow-hidden rounded-md @min-[640px]:size-12" :style="coverStyle(primaryDownloadForGroup(group).gid)">
                <img
                  v-if="resolveDownloadArtworkUrl(primaryDownloadForGroup(group))"
                  :src="resolveDownloadArtworkUrl(primaryDownloadForGroup(group)) ?? ''"
                  :alt="group.title"
                  class="absolute inset-0 size-full object-cover"
                  loading="lazy"
                />
                <div v-else class="absolute inset-0 grid place-items-center px-1 text-center">
                  <span class="text-sm font-semibold text-white/90">{{ coverLabel(group.title) }}</span>
                </div>
              </div>
              <div class="min-w-0 flex-1">
                <strong class="block truncate text-sm font-semibold text-[var(--ink)]" :title="group.title">{{ group.title }}</strong>
                <p class="mt-0.5 truncate text-xs text-[var(--muted)]">
                  {{ group.episodes.length }} 集 · {{ group.downloads.length }} 个版本 · {{ currentEpisodeLabel(group) }}<template v-if="resolutionLabel(primaryDownloadForGroup(group))"> · {{ resolutionLabel(primaryDownloadForGroup(group)) }}</template><template v-if="subgroupLabel(primaryDownloadForGroup(group))"> · {{ subgroupLabel(primaryDownloadForGroup(group)) }}</template>
                </p>
                <p
                  v-if="downloadRawError(primaryDownloadForGroup(group))"
                  class="mt-0.5 truncate text-xs text-[var(--err)]"
                  :title="downloadRawError(primaryDownloadForGroup(group)) ?? undefined"
                >
                  {{ downloadRawError(primaryDownloadForGroup(group)) }}
                </p>
                <p v-if="primaryDownloadForGroup(group).cloudArchive" class="mt-0.5 text-xs text-[var(--muted)]"
                  :title="primaryDownloadForGroup(group).cloudArchive?.error ?? undefined">
                  {{ cloudArchiveLabel(primaryDownloadForGroup(group)) }}
                  <span v-if="primaryDownloadForGroup(group).cloudArchive?.error" class="text-[var(--err)]"> · {{ primaryDownloadForGroup(group).cloudArchive?.error }}</span>
                </p>
                <!-- 窄屏内联进度 -->
                <div class="mt-1.5 flex items-center gap-2 @min-[640px]:hidden">
                  <div class="h-1.5 flex-1 overflow-hidden rounded-full bg-[var(--tint-4)]">
                    <div class="h-full rounded-full bg-[var(--accent-vivid)] transition-[width] duration-300" :style="{ width: `${groupProgress(group)}%` }"></div>
                  </div>
                  <span class="shrink-0 text-xs text-[var(--muted)]">{{ groupProgress(group) }}%</span>
                </div>
              </div>
            </div>

            <div class="hidden min-w-0 gap-1 @min-[640px]:grid">
              <span class="w-fit rounded-md border px-2 py-0.5 text-xs font-medium" :class="statusPillClass(groupStatus(group))">
                {{ statusLabel(groupStatus(group)) }}
              </span>
              <!-- 速度在宽屏有独立一列，这里只在中等宽度补一句，别两处重复 -->
              <span v-if="groupDownloadSpeed(group) > 0" class="truncate text-xs tabular-nums text-[var(--muted)] @min-[1000px]:hidden">
                ↓ {{ formatBytes(groupDownloadSpeed(group)) }}/s
              </span>
              <span v-else-if="groupRemainingSeconds(group)" class="truncate text-xs text-[var(--muted)] @min-[1000px]:hidden">
                剩余 {{ formatDuration(groupRemainingSeconds(group)) }}
              </span>
            </div>

            <!-- 宽屏下进度条拿到 12–18rem，数字和进度条并排而不是叠成两行 -->
            <div class="hidden items-center gap-2.5 @min-[640px]:flex">
              <span class="h-1.5 min-w-0 flex-1 overflow-hidden rounded-full bg-[var(--tint-4)]">
                <span
                  class="block h-full rounded-full transition-[width] duration-300"
                  :class="groupStatus(group) === 'complete' ? 'bg-[var(--ok)]' : 'bg-[var(--accent-vivid)]'"
                  :style="{ width: `${groupProgress(group)}%` }"
                ></span>
              </span>
              <span class="shrink-0 text-xs font-medium tabular-nums text-[var(--ink)] @min-[1000px]:hidden">{{ groupProgress(group) }}%</span>
              <span class="hidden shrink-0 text-right text-xs tabular-nums text-[var(--muted)] @min-[1000px]:block">
                {{ formatBytes(groupCompletedLength(group)) }} / {{ formatBytes(groupTotalLength(group)) }}
              </span>
            </div>

            <div class="hidden min-w-0 @min-[1000px]:block">
              <span v-if="groupDownloadSpeed(group) > 0" class="block truncate text-xs tabular-nums text-[var(--ink)]">
                ↓ {{ formatBytes(groupDownloadSpeed(group)) }}/s
              </span>
              <span v-else-if="groupRemainingSeconds(group)" class="block truncate text-xs text-[var(--muted)]">
                剩余 {{ formatDuration(groupRemainingSeconds(group)) }}
              </span>
              <span v-else class="block text-xs text-[var(--muted)]">—</span>
            </div>

            <div class="flex justify-end gap-1.5" @click.stop>
              <!-- 播放键指向最新一个能播的集，不再跟着「正在下载的代表任务」一起变灰 -->
              <button
                type="button"
                class="icon-btn"
                :title="playableEpisodeLabelForGroup(group) ? `播放 ${playableEpisodeLabelForGroup(group)}` : '暂无可播放的分集'"
                :aria-label="playableEpisodeLabelForGroup(group) ? `播放 ${playableEpisodeLabelForGroup(group)}` : '暂无可播放的分集'"
                :disabled="!playableDownloadForGroup(group) || isDownloadRowBusy(playableDownloadForGroup(group)!.gid)"
                @click="playableDownloadForGroup(group) && openPlayback(playableDownloadForGroup(group)!)"
              >
                <AppNavIcon name="play" />
              </button>
              <button
                type="button"
                class="icon-btn"
                :title="canResumeDownload(primaryDownloadForGroup(group)) ? '继续' : '暂停'"
                :aria-label="canResumeDownload(primaryDownloadForGroup(group)) ? '继续' : '暂停'"
                :disabled="(!canPauseDownload(primaryDownloadForGroup(group)) && !canResumeDownload(primaryDownloadForGroup(group))) || isDownloadRowBusy(primaryDownloadForGroup(group).gid)"
                @click="updateDownloadState(canResumeDownload(primaryDownloadForGroup(group)) ? 'resume' : 'pause', primaryDownloadForGroup(group).gid)"
              >
                <AppNavIcon :name="canResumeDownload(primaryDownloadForGroup(group)) ? 'play' : 'pause'" />
              </button>
              <button
                type="button"
                class="icon-btn hidden text-[var(--err)] hover:text-[var(--err)] @min-[640px]:inline-grid"
                title="删除"
                aria-label="删除"
                @click="requestDelete(primaryDownloadForGroup(group))"
              >
                <AppNavIcon name="trash" />
              </button>
            </div>
          </article>

          <!-- 就地展开：分集/版本/日志直接长在这一行下面，拿整行全宽 -->
          <DownloadVersionPane
            v-if="activeDownloadGroupKey === group.key"
            :downloads="group.downloads"
            :episodes="group.episodes"
            :format-bytes="formatBytes"
            :download-display-title="displayTitle"
            :progress-percent="progressPercent"
            :download-raw-error="downloadRawError"
            :is-download-row-busy="isDownloadRowBusy"
            :anime-detail="animeDetail"
            :detail-belongs-to-group="group.downloads.some((item) => item.gid === selectedDownloadDetailGid)"
            :detail-error="animeDetailError"
            :detail-loading="detailLoading"
            @conflict="handleDownloadConflict($event.gid, $event.strategy)"
            @playback="openPlayback"
            @pause="updateDownloadState('pause', $event)"
            @resume="updateDownloadState('resume', $event)"
            @remove="requestDelete"
          />
          </div>
        </div>
      </div>

      <!-- 0 条任务时不渲染分页器：没有可翻的页 -->
      <div
        v-if="filteredDownloads.length"
        class="flex flex-wrap items-center gap-2 border-t border-[var(--line)] px-4 py-2"
      >
        <template v-if="selectedGroupKeys.size > 0">
          <span class="text-sm font-medium text-[var(--ink)]">已选 {{ selectedGroupKeys.size }} 项</span>
          <button type="button" class="btn-ghost h-8 px-3 text-sm" :disabled="!selectedActionableDownloads.length" @click="runBatchTransfer('resume')">
            开始
          </button>
          <button type="button" class="btn-ghost h-8 px-3 text-sm" :disabled="!selectedActionableDownloads.length" @click="runBatchTransfer('pause')">
            暂停
          </button>
          <button
            type="button"
            class="h-8 rounded-md border border-[var(--err-soft)] bg-[var(--err-soft)] px-3 text-sm font-medium text-[var(--err)] transition hover:border-[var(--err)]"
            @click="requestBatchDelete"
          >
            删除
          </button>
          <button type="button" class="btn-ghost h-8 px-3 text-sm text-[var(--muted)]" @click="clearSelectedGroups">
            取消选择
          </button>
        </template>
        <div class="ml-auto">
          <PagerBar
            :current-page="downloadListPage"
            :total-pages="downloadSeriesTotalPages"
            :pagination-items="downloadListPaginationItems"
            :row-count="downloadRowsPerPage"
            :row-count-options="downloadPageSizeOptions"
            row-label="每页"
            @update:page="downloadListPage = $event"
            @update:row-count="downloadRowsPerPage = $event"
          />
        </div>
      </div>
    </div>

    <AppDialog
      v-if="manualComposerOpen"
      title="添加下载链接"
      description="支持磁力、.torrent 地址和普通 HTTP(S) 直链。"
      @close="manualComposerOpen = false"
    >
      <div class="grid gap-3">
        <textarea
          v-model="manualDownloadForm.url"
          class="input-base min-h-32 w-full rounded-lg px-4 py-3"
          placeholder="粘贴磁力、torrent URL 或直链后添加到队列"
        ></textarea>
        <div class="flex flex-wrap items-center gap-3">
          <button
            type="button"
            class="rounded-full border border-[var(--line)] bg-[var(--tint-2)] px-3 py-2 text-sm font-semibold text-[var(--accent)] transition hover:border-[var(--accent)]"
            @click="manualOptionsOpen = true"
          >
            下载选项 · {{ sourceTypeLabel(effectiveManualSourceType) }}
          </button>
          <span class="text-sm font-semibold text-[var(--muted)]">当前识别：{{ sourceTypeLabel(inferredManualSourceType) }}</span>
        </div>
      </div>
      <template #footer>
        <button type="button" class="btn-ghost" @click="manualComposerOpen = false; manualDownloadForm.url = ''">取消</button>
        <button
          type="button"
          class="btn-primary"
          :disabled="isBusy('download:manual') || !manualDownloadForm.url.trim()"
          @click="submitManual"
        >
          {{ isBusy("download:manual") ? "添加中…" : "确认添加" }}
        </button>
      </template>
    </AppDialog>

    <AppDialog
      v-if="manualOptionsOpen"
      title="更多下载选项"
      :description="`当前识别：${sourceTypeLabel(inferredManualSourceType)}`"
      @close="manualOptionsOpen = false"
    >
      <div class="grid gap-3">
        <button
          v-for="option in manualSourceOptions"
          :key="option.label"
          type="button"
          class="grid gap-1 rounded-lg border px-4 py-3 text-left transition"
          :class="manualDownloadForm.sourceTypeOverride === option.value ? 'border-[var(--accent)] bg-[var(--tint-2)]' : 'border-[var(--line)] bg-[var(--tint-2)] hover:border-[var(--accent)]'"
          @click="manualDownloadForm.sourceTypeOverride = option.value"
        >
          <div class="flex items-center justify-between gap-3">
            <span class="text-sm font-semibold text-[var(--ink)]">{{ option.label }}</span>
            <span v-if="manualDownloadForm.sourceTypeOverride === option.value" class="rounded-full bg-[var(--accent)] px-2 py-0.5 text-[11px] font-semibold text-[var(--accent-contrast)]">
              当前
            </span>
          </div>
          <p class="text-xs font-semibold text-[var(--muted)]">{{ option.description }}</p>
        </button>
      </div>
      <div class="mt-3 rounded-lg border border-[var(--line)] bg-[var(--tint-2)] px-4 py-3 text-xs font-semibold text-[var(--muted)]">
        支持粘贴磁力链接、.torrent 地址和普通 HTTP(S) 直链。多数情况下保持自动识别即可，只有识别不准时再手动切换。
      </div>
    </AppDialog>

    <AppDialog
      v-if="deleteCandidate || batchDeleteCandidates.length"
      :title="batchDeleteCandidates.length ? `删除 ${batchDeleteCandidates.length} 个下载任务` : '删除下载任务'"
      :description="deleteCandidate ? displayTitle(deleteCandidate) : '将删除当前选中的分组任务。'"
      @close="closeDeleteDialog"
    >
      <label class="flex items-start gap-3 rounded-lg border border-[var(--line)] bg-[var(--tint-2)] p-4 text-sm text-[var(--ink)]">
        <input v-model="deleteLocalFiles" type="checkbox" class="mt-1 size-5 accent-[var(--accent)]" />
        <span class="grid gap-1">
          <span class="font-semibold">同时删除本地文件</span>
          <span class="text-[var(--muted)]">取消勾选则只删除任务记录，保留已下载的文件。</span>
        </span>
      </label>
      <template #footer>
        <button type="button" class="btn-ghost" @click="closeDeleteDialog">取消</button>
        <button type="button" class="btn-danger" @click="confirmDelete">
          {{ deleteLocalFiles ? "删除任务和文件" : "仅删除任务" }}
        </button>
      </template>
    </AppDialog>
  </section>
</template>
