<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from "vue";

import type { DownloadJob, ExternalPlayerId, PlaybackSubtitleTrack } from "@shared/index";
import { sourceTypeLabel, type SourceType } from "@shared/index";

import {
  canPickDesktopDirectory,
  getCloudStatus,
  pickDesktopDirectory,
  getDownloadAssSubtitleUrl,
  getDownloadHlsPlaylistUrl,
  getDownloadStreamUrl,
  getDownloadSubtitleUrl,
  resolveDownloadArtworkUrl
} from "../api";
import { cloudArchiveLabel } from "../lib/cloudArchive";
import AppDialog from "../components/common/AppDialog.vue";
import SegmentedControl from "../components/common/SegmentedControl.vue";
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
  shortAnimeTitle,
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
import { settingsForm } from "../stores/session";
import { viewportWidth } from "../stores/ui";

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
/** 多选是低频操作：平时不摆复选框，点工具栏「选择」才出现（iOS 照片 / 文件 App 的做法） */
const selectMode = ref(false);
const manualOptionsOpen = ref(false);
const manualComposerOpen = ref(false);

// —— 添加任务：保存位置 ——
const SAVE_TO_OPTIONS = [
  { value: "local", label: "本机" },
  { value: "cloud", label: "Google Drive" }
] as const;
const cloudConnected = ref(false);
const canPickDirectory = canPickDesktopDirectory();

/** 每次打开对话框按设置里的默认位置起步；云盘没连上就先落本机 */
watch(manualComposerOpen, async (open) => {
  if (!open) {
    return;
  }
  manualDownloadForm.directory = "";
  manualDownloadForm.saveTo = "local";
  try {
    cloudConnected.value = (await getCloudStatus()).connected;
  } catch {
    cloudConnected.value = false;
  }
  if (settingsForm.defaultSaveTo === "cloud" && cloudConnected.value) {
    manualDownloadForm.saveTo = "cloud";
  }
});

async function pickManualDirectory(): Promise<void> {
  const picked = await pickDesktopDirectory();
  if (picked) {
    manualDownloadForm.directory = picked;
  }
}

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

const playbackEpisodeLabel = computed(
  () => playbackEpisodeOptions.value.find((option) => option.key === playbackEpisodeKey.value)?.label ?? null
);

/** 画面上方标题下那一行：第几集 · 字幕组 · 清晰度 · 播放方式 */
const playbackMetaLine = computed(() => {
  const playback = activePlayback.value;
  if (!playback) {
    return "";
  }
  const download = downloads.value.find((item) => item.gid === playback.gid);
  const parts = [playbackEpisodeLabel.value, download ? subgroupLabel(download) : null, download ? resolutionLabel(download) : null];
  if (playback.deliveryMode === "hls-transcode") {
    parts.push("本机 ffmpeg 转码");
  } else if (playback.deliveryMode === "hls-remux") {
    parts.push("本机 ffmpeg 转封装");
  }
  return parts.filter(Boolean).join(" · ");
});

const externalPlayerMenuRef = ref<HTMLDetailsElement | null>(null);
const externalPlayerOptions = computed<Array<{ id: ExternalPlayerId; label: string }>>(() => [
  { id: "system", label: "系统默认" },
  ...(activePlayback.value?.availablePlayers.filter((item) => item.id !== "system") ?? [])
]);

/** <details> 菜单点外面不会自己收起，补一个 */
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
  if (activePlayback.value) {
    void launchExternalPlayer(activePlayback.value.gid, playerId);
  }
}

/** 选集格子里只放集数：「第 04 集」→「4」，没有数字（SP、OVA）就原样显示 */
function episodeShortLabel(label: string): string {
  const match = label.match(/\d+(?:\.\d+)?/);
  return match ? String(Number(match[0])) : label;
}

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
      seriesGroup = { key: seriesKey, title: series || shortAnimeTitle(download), downloads: [], episodes: [] };
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
  selectMode.value = false;
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

/** 卡片第二行：单集写「第 04 集」，多集写「12 集」，后面跟字幕组、清晰度、总大小 */
function groupMetaLine(group: SeriesDownloadGroup): string {
  const primary = primaryDownloadForGroup(group);
  const parts = [group.episodes.length === 1 ? currentEpisodeLabel(group) : `${group.episodes.length} 集`];
  if (group.downloads.length > group.episodes.length) {
    parts.push(`${group.downloads.length} 个版本`);
  }
  for (const part of [subgroupLabel(primary), resolutionLabel(primary)]) {
    if (part) {
      parts.push(part);
    }
  }
  const total = groupTotalLength(group);
  if (total > 0) {
    parts.push(formatBytes(total));
  }
  if (primary.saveTo === "cloud" && !primary.cloudArchive) {
    parts.push("完成后存到云盘");
  }
  return parts.join(" · ");
}

/** 进度条右边那句：下载中给速度和剩余时间，其他状态给状态名 */
function groupProgressLine(group: SeriesDownloadGroup): string {
  const status = groupStatus(group);
  const progress = `${groupProgress(group)}%`;
  if (status === "active") {
    const speed = groupDownloadSpeed(group);
    const remaining = groupRemainingSeconds(group);
    return [progress, speed > 0 ? `${formatBytes(speed)}/s` : "连接中", remaining ? `剩余 ${formatDuration(remaining)}` : null]
      .filter(Boolean)
      .join(" · ");
  }
  return `${statusLabel(status)} · ${progress}`;
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

/**
 * 手机宽度：点任务进一个全屏详情页（顶上带返回），不在列表里就地展开。
 * 就地展开在窄屏上是缩进套缩进、竖线套竖线，一屏只剩半个面板。
 */
const narrowDownloads = computed(() => viewportWidth.value <= 640);
/** 宽屏（内容区放得下列表 + 22rem 侧栏）选中一部番时，分集显示在右侧栏而不是行下展开 */
const sideDownloads = computed(() => viewportWidth.value >= 1200);
const detailPanelGroup = computed(() =>
  (narrowDownloads.value || sideDownloads.value) && activeDownloadGroupKey.value
    ? groupedDownloads.value.find((group) => group.key === activeDownloadGroupKey.value) ?? null
    : null
);

/** 详情页标题用番名（「FX战士久留美」），番剧信息没回来之前才退回发布名 */
const detailPanelAnimeTitle = computed(() => {
  const group = detailPanelGroup.value;
  if (!group) {
    return "";
  }
  const belongs = group.downloads.some((item) => item.gid === selectedDownloadDetailGid.value);
  return (belongs && animeDetail.value?.anime.title) || group.title;
});

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
  <!-- 不再套一层带边框的卡片：首页直接铺在底色上，这页也一样，框里套框只是多一道线 -->
  <section class="grid h-full min-h-0 grid-cols-[minmax(0,1fr)] overflow-hidden">
    <!--
      播放页参考 B 站番剧播放页：左边大画面，下面是番名、当前集和播放方式；右边一栏选集格子。
      容器窄于 56rem（手机、窄窗口）时选集落到画面下方。
    -->
    <div v-if="activePlayback" class="@container min-h-0 overflow-auto p-3 sm:p-4">
      <!--
        标题放在画面上面（B 站 / YouTube 的做法）：一进来先知道在看哪部第几集，
        以前番名、集数、文件名全堆在画面下面，还隔着一排字幕按钮。
      -->
      <header class="mb-3 flex min-w-0 items-center gap-2.5">
        <button type="button" class="icon-btn shrink-0" aria-label="返回下载管理" title="返回下载管理" @click="closePlayback">
          <AppNavIcon name="chevron-left" />
        </button>
        <div class="min-w-0 flex-1">
          <h2 class="truncate text-base font-semibold leading-snug text-[var(--ink)]" :title="activePlayback.fileName">
            {{ playbackGroup?.title ?? activePlayback.fileName }}
          </h2>
          <p class="truncate text-xs text-[var(--muted)]">{{ playbackMetaLine }}</p>
        </div>
        <!-- 外部播放器是兜底手段，收进一个菜单，不再在画面下面摆一排按钮 -->
        <details ref="externalPlayerMenuRef" class="external-menu relative shrink-0">
          <summary class="btn-ghost inline-flex h-8 cursor-pointer list-none items-center gap-1 px-3 text-xs font-semibold">
            <AppNavIcon name="external" class="size-3.5" aria-hidden="true" />
            <span class="max-sm:sr-only">外部播放器</span>
          </summary>
          <div class="absolute right-0 top-full z-30 mt-1 grid min-w-[9rem] gap-0.5 rounded-lg border border-[var(--line)] bg-[var(--card)] p-1 shadow-[var(--strong-shadow)]">
            <button
              v-for="player in externalPlayerOptions"
              :key="player.id"
              type="button"
              class="rounded-md px-2.5 py-1.5 text-left text-xs font-semibold text-[var(--ink)] hover:bg-[var(--tint-2)]"
              @click="openExternalPlayer(player.id)"
            >
              {{ player.label }}
            </button>
          </div>
        </details>
      </header>

      <div class="grid content-start gap-4" :class="{ '@min-[56rem]:grid-cols-[minmax(0,1fr)_18rem]': playbackEpisodeOptions.length > 1 }">
        <div class="grid min-w-0 content-start gap-3">
          <ArtPlayerPane
            v-if="activePlayback.supportsInAppPlayback || activePlayback.supportsTranscodePlayback"
            :gid="activePlayback.gid"
            :stream-url="activePlaybackStreamUrl"
            :playback-key="`${activePlayback.filePath}:${activePlayback.deliveryMode}`"
            :expected-duration-seconds="activePlayback.durationSeconds"
            :hls="activePlayback.deliveryMode === 'hls-remux' || activePlayback.deliveryMode === 'hls-transcode'"
            :subtitle-tracks="activePlayback.subtitleTracks"
            :subtitle-url-for="playbackSubtitleUrl"
            @ended="playNextEpisode"
          />
          <div v-else class="grid aspect-video place-items-center rounded-xl bg-black px-6 text-center text-sm text-white/75">
            当前文件不支持内置播放器。常见的 MKV / HEVC / 高码率文件建议用右上角的外部播放器打开。
          </div>

        </div>

        <aside v-if="playbackEpisodeOptions.length > 1" class="grid content-start gap-3 rounded-xl border border-[var(--line)] bg-[var(--card)] p-3">
          <div class="flex items-baseline justify-between gap-2">
            <h3 class="text-sm font-semibold text-[var(--ink)]">选集</h3>
            <span class="text-xs text-[var(--muted)]">{{ playbackEpisodeOptions.filter((option) => option.playable).length }} / {{ playbackEpisodeOptions.length }} 集可播</span>
          </div>
          <div class="grid grid-cols-[repeat(auto-fill,minmax(2.75rem,1fr))] gap-1.5">
            <button
              v-for="option in playbackEpisodeOptions"
              :key="option.key"
              type="button"
              class="h-9 rounded-lg text-[13px] font-semibold tabular-nums transition disabled:cursor-not-allowed disabled:opacity-40"
              :class="
                option.key === playbackEpisodeKey
                  ? 'bg-[var(--accent)] text-[var(--accent-contrast)]'
                  : 'bg-[var(--panel)] text-[var(--ink-secondary)] hover:text-[var(--ink)]'
              "
              :disabled="!option.playable"
              :title="`${option.label} · ${option.statusLabel}`"
              :aria-label="`${option.label}，${option.playable ? '可播放' : option.statusLabel}`"
              :aria-current="option.key === playbackEpisodeKey"
              @click="option.key !== playbackEpisodeKey && playEpisodeOption(option)"
            >
              {{ episodeShortLabel(option.label) }}
            </button>
          </div>
          <p class="text-[11px] text-[var(--muted)]">播完自动播下一集已下载的分集。</p>
        </aside>
      </div>
    </div>

    <div v-else class="downloads-layout" :class="{ 'has-side': detailPanelGroup && !narrowDownloads }">
    <div v-if="!(narrowDownloads && detailPanelGroup)" class="grid min-h-0 grid-rows-[auto_minmax(0,1fr)_auto] overflow-hidden">
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
            <span class="inline-block min-w-[4.5rem] text-right max-sm:hidden">↑ {{ formatBytes(totalUploadSpeed) }}/s</span>
          </span>
        </div>

        <!-- 空列表时主操作交给空态卡片，不在工具栏摆一排永远禁用的按钮 -->
        <div v-if="downloads.length" class="toolbar-row flex-wrap">
          <!-- 手机上搜索、排序、添加挤在一行：一个任务配三行工具栏，比列表本身还高 -->
          <label class="search-field min-w-0 flex-1 basis-[8rem] sm:min-w-[14rem] sm:max-w-[22rem]">
            <AppNavIcon name="search" aria-hidden="true" />
            <input v-model="searchQuery" type="search" aria-label="搜索下载任务" placeholder="搜索番剧、集数、GID 或字幕组" />
          </label>
          <select v-model="sortMode" class="input-base h-[2.125rem] shrink-0 px-2.5 text-sm font-semibold max-sm:w-[5.75rem]" aria-label="排序方式">
            <option value="status">按状态</option>
            <option value="updated">按更新时间</option>
            <option value="progress">按进度</option>
          </select>
          <div class="ml-auto flex flex-wrap items-center justify-end gap-2 max-sm:contents">
            <button
              type="button"
              class="btn-ghost px-3 py-1.5 text-sm font-semibold max-sm:hidden"
              :aria-pressed="selectMode"
              @click="selectMode ? clearSelectedGroups() : (selectMode = true)"
            >
              {{ selectMode ? "完成" : "选择" }}
            </button>
            <button
              type="button"
              class="btn-ghost px-3 py-1.5 text-sm font-semibold max-sm:order-last disabled:hidden"
              :disabled="!downloads.some((download) => download.status === 'paused')"
              @click="resumeAllDownloads"
            >
              全部开始
            </button>
            <button
              type="button"
              class="btn-ghost px-3 py-1.5 text-sm font-semibold max-sm:order-last disabled:hidden"
              :disabled="!downloads.some((download) => ['active', 'waiting', 'queued'].includes(download.status))"
              @click="pauseAllDownloads"
            >
              全部暂停
            </button>
            <button
              type="button"
              class="btn-primary inline-flex shrink-0 items-center gap-1 px-3 py-1.5 text-sm font-semibold max-sm:px-2.5"
              aria-label="添加任务"
              @click="manualComposerOpen = true"
            >
              <AppNavIcon name="plus" class="size-4 sm:hidden" aria-hidden="true" />
              <span class="max-sm:sr-only">添加任务</span>
            </button>
          </div>
        </div>
      </div>

      <!--
        断点看的是这一列的实际宽度，不是视口宽度。并排详情栏拆掉之后这一列就是整个内容区，
        宽屏下能排满「番剧 / 状态 / 进度 / 速度 / 操作」五列，番剧列拿走剩下的全部份额。
      -->
      <div class="@container min-h-0 overflow-auto pb-2">
        <!-- 空态不摆表头：0 条任务时列标题和全选框没有指代对象 -->
        <div
          v-if="!filteredDownloads.length"
          class="empty-state h-full"
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

        <!--
          参考 Apple TV / Netflix 的「下载」页：不要表头和五列表格，一部番一张卡——
          竖版封面、番名、「第 N 集 · 字幕组 · 清晰度 · 大小」一行，只有没下完的才出进度条，
          右边是这部番此刻最该做的事（能看就是实心播放键）。删除这类低频操作悬停才出现。
        -->
        <div v-else class="grid gap-1 py-2">
          <div
            v-for="group in pagedDownloadGroups"
            :key="group.key"
            class="download-card"
            :class="{ 'is-open': activeDownloadGroupKey === group.key }"
          >
          <article class="download-row" @click="selectMode ? setGroupSelected(group.key, !selectedGroupKeys.has(group.key)) : toggleDownloadGroup(group.key)">
            <label v-if="selectMode" class="grid size-6 shrink-0 place-items-center" @click.stop>
              <input
                type="checkbox"
                class="size-4 accent-[var(--accent)]"
                :aria-label="`选择 ${group.title}`"
                :checked="selectedGroupKeys.has(group.key)"
                @change="setGroupSelected(group.key, ($event.target as HTMLInputElement).checked)"
              />
            </label>

            <div class="download-poster" :style="coverStyle(primaryDownloadForGroup(group).gid)">
              <img
                v-if="resolveDownloadArtworkUrl(primaryDownloadForGroup(group))"
                :src="resolveDownloadArtworkUrl(primaryDownloadForGroup(group)) ?? ''"
                :alt="group.title"
                class="absolute inset-0 size-full object-cover"
                loading="lazy"
              />
              <div v-else class="absolute inset-0 grid place-items-center px-1 text-center">
                <span class="text-xs font-semibold text-white/90">{{ coverLabel(group.title) }}</span>
              </div>
            </div>

            <div class="download-info grid min-w-0 gap-1">
              <strong class="truncate text-[15px] font-semibold leading-snug text-[var(--ink)]" :title="group.title">{{ group.title }}</strong>
              <p class="flex min-w-0 items-center gap-1.5 text-xs text-[var(--muted)]">
                <span v-if="groupStatus(group) === 'complete'" class="inline-flex shrink-0 items-center gap-0.5 font-semibold text-[var(--ok)]">
                  <AppNavIcon name="check" class="size-3.5" aria-hidden="true" />已下载
                </span>
                <span class="truncate">{{ groupMetaLine(group) }}</span>
              </p>
              <div v-if="groupStatus(group) !== 'complete'" class="flex max-w-md items-center gap-2.5">
                <span class="h-1 min-w-0 flex-1 overflow-hidden rounded-full bg-[var(--tint-4)]">
                  <span
                    class="block h-full rounded-full transition-[width] duration-300"
                    :class="groupStatus(group) === 'error' ? 'bg-[var(--err)]' : groupStatus(group) === 'paused' ? 'bg-[var(--muted)]' : 'bg-[var(--accent-vivid)]'"
                    :style="{ width: `${groupProgress(group)}%` }"
                  ></span>
                </span>
                <span class="shrink-0 text-xs tabular-nums" :class="groupStatus(group) === 'error' ? 'text-[var(--err)]' : 'text-[var(--muted)]'">
                  {{ groupProgressLine(group) }}
                </span>
              </div>
              <p
                v-if="downloadRawError(primaryDownloadForGroup(group))"
                class="truncate text-xs text-[var(--err)]"
                :title="downloadRawError(primaryDownloadForGroup(group)) ?? undefined"
              >
                {{ downloadRawError(primaryDownloadForGroup(group)) }}
              </p>
              <p v-if="primaryDownloadForGroup(group).cloudArchive" class="truncate text-xs text-[var(--muted)]"
                :title="primaryDownloadForGroup(group).cloudArchive?.error ?? undefined">
                {{ cloudArchiveLabel(primaryDownloadForGroup(group)) }}
                <span v-if="primaryDownloadForGroup(group).cloudArchive?.error" class="text-[var(--err)]"> · {{ primaryDownloadForGroup(group).cloudArchive?.error }}</span>
              </p>
            </div>

            <div v-if="!selectMode" class="flex shrink-0 items-center gap-1.5" @click.stop>
              <button
                type="button"
                class="icon-btn reveal-on-hover text-[var(--err)] hover:text-[var(--err)] max-sm:hidden"
                title="删除"
                aria-label="删除"
                @click="requestDelete(primaryDownloadForGroup(group))"
              >
                <AppNavIcon name="trash" />
              </button>
              <!-- 暂停 / 继续只在能用时出现，不再摆一个永远灰着的按钮 -->
              <button
                v-if="canPauseDownload(primaryDownloadForGroup(group)) || canResumeDownload(primaryDownloadForGroup(group))"
                type="button"
                class="icon-btn"
                :title="canResumeDownload(primaryDownloadForGroup(group)) ? '继续下载' : '暂停'"
                :aria-label="canResumeDownload(primaryDownloadForGroup(group)) ? '继续下载' : '暂停'"
                :disabled="isDownloadRowBusy(primaryDownloadForGroup(group).gid)"
                @click="updateDownloadState(canResumeDownload(primaryDownloadForGroup(group)) ? 'resume' : 'pause', primaryDownloadForGroup(group).gid)"
              >
                <AppNavIcon :name="canResumeDownload(primaryDownloadForGroup(group)) ? 'download' : 'pause'" />
              </button>
              <button
                v-if="playableDownloadForGroup(group)"
                type="button"
                class="play-btn"
                :title="`播放 ${playableEpisodeLabelForGroup(group) ?? ''}`"
                :aria-label="`播放 ${playableEpisodeLabelForGroup(group) ?? ''}`"
                :disabled="isDownloadRowBusy(playableDownloadForGroup(group)!.gid)"
                @click="openPlayback(playableDownloadForGroup(group)!)"
              >
                <AppNavIcon name="play" class="size-3.5" />
              </button>
              <AppNavIcon
                name="chevron-right"
                class="size-4 shrink-0 text-[var(--muted)] transition-transform duration-200"
                :class="{ 'rotate-90': activeDownloadGroupKey === group.key && !narrowDownloads && !sideDownloads }"
                aria-hidden="true"
              />
            </div>
          </article>

          <!-- 就地展开：分集/版本/日志直接长在这一行下面，拿整行全宽 -->
          <DownloadVersionPane
            v-if="activeDownloadGroupKey === group.key && !narrowDownloads && !sideDownloads"
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

      <!--
        只有一页、也没选中东西时整条底栏不出：没有可翻的页，也没有批量操作，
        留着就是一条空边框（分页器自己在单页时也不渲染）。
      -->
      <div
        v-if="filteredDownloads.length && (selectMode || downloadSeriesTotalPages > 1)"
        class="flex flex-wrap items-center gap-2 border-t border-[var(--line)] px-4 py-2"
      >
        <template v-if="selectMode">
          <span class="text-sm font-medium text-[var(--ink)]">已选 {{ selectedGroupKeys.size }} 项</span>
          <button type="button" class="btn-ghost h-8 px-3 text-sm" @click="togglePagedGroups(!allPagedGroupsSelected)">
            {{ allPagedGroupsSelected ? "取消全选" : "全选本页" }}
          </button>
          <button type="button" class="btn-ghost h-8 px-3 text-sm" :disabled="!selectedActionableDownloads.length" @click="runBatchTransfer('resume')">
            开始
          </button>
          <button type="button" class="btn-ghost h-8 px-3 text-sm" :disabled="!selectedActionableDownloads.length" @click="runBatchTransfer('pause')">
            暂停
          </button>
          <button
            type="button"
            class="h-8 rounded-md border border-[var(--err-soft)] bg-[var(--err-soft)] px-3 text-sm font-medium text-[var(--err)] transition hover:border-[var(--err)]"
            :disabled="!selectedGroupKeys.size"
            @click="requestBatchDelete"
          >
            删除
          </button>
        </template>
        <!-- 没选中任务时分页器居中；选中后左边是批量操作，分页器靠右 -->
        <div :class="selectMode ? 'ml-auto' : 'mx-auto'">
          <PagerBar
            plain
            class="!px-0"
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

    <!--
      选中一部番后的分集面板：手机上全屏（带返回），宽屏是右侧栏（像「邮件」「音乐」那样左列表右详情），
      中等宽度没地方放侧栏，就在列表里就地展开。
    -->
    <aside v-if="detailPanelGroup" class="download-detail" :class="{ 'is-side': !narrowDownloads }">
      <button
        v-if="narrowDownloads"
        type="button"
        class="btn-ghost inline-flex w-fit items-center gap-1 px-2.5 py-1.5 text-sm font-semibold"
        @click="collapseDownloadGroup"
      >
        <AppNavIcon name="chevron-left" class="size-4" aria-hidden="true" />
        下载管理
      </button>
      <header class="flex min-w-0 items-center gap-3">
        <div class="download-poster !w-14" :style="coverStyle(primaryDownloadForGroup(detailPanelGroup).gid)">
          <img
            v-if="resolveDownloadArtworkUrl(primaryDownloadForGroup(detailPanelGroup))"
            :src="resolveDownloadArtworkUrl(primaryDownloadForGroup(detailPanelGroup)) ?? ''"
            alt=""
            class="absolute inset-0 size-full object-cover"
          />
          <div v-else class="absolute inset-0 grid place-items-center text-sm font-semibold text-white/90">{{ coverLabel(detailPanelGroup.title) }}</div>
        </div>
        <div class="min-w-0">
          <h2 class="line-clamp-2 text-[15px] font-semibold leading-snug text-[var(--ink)]" :title="detailPanelGroup.title">{{ detailPanelAnimeTitle }}</h2>
          <p class="mt-0.5 truncate text-xs text-[var(--muted)]">
            {{ groupMetaLine(detailPanelGroup) }}
          </p>
        </div>
        <button
          v-if="!narrowDownloads"
          type="button"
          class="icon-btn ml-auto shrink-0 self-start"
          aria-label="关闭侧栏"
          title="关闭侧栏"
          @click="collapseDownloadGroup"
        >
          <AppNavIcon name="close" />
        </button>
      </header>
      <DownloadVersionPane
        flat
        :downloads="detailPanelGroup.downloads"
        :episodes="detailPanelGroup.episodes"
        :format-bytes="formatBytes"
        :download-display-title="displayTitle"
        :progress-percent="progressPercent"
        :download-raw-error="downloadRawError"
        :is-download-row-busy="isDownloadRowBusy"
        :anime-detail="animeDetail"
        :detail-belongs-to-group="detailPanelGroup.downloads.some((item) => item.gid === selectedDownloadDetailGid)"
        :detail-error="animeDetailError"
        :detail-loading="detailLoading"
        @conflict="handleDownloadConflict($event.gid, $event.strategy)"
        @playback="openPlayback"
        @pause="updateDownloadState('pause', $event)"
        @resume="updateDownloadState('resume', $event)"
        @remove="requestDelete"
      />
    </aside>
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

        <div class="grid gap-2 border-t border-[var(--line)] pt-3">
          <div class="flex flex-wrap items-center justify-between gap-2">
            <span class="text-sm font-semibold text-[var(--ink)]">保存到</span>
            <SegmentedControl v-model="manualDownloadForm.saveTo" :options="SAVE_TO_OPTIONS" aria-label="保存位置" />
          </div>
          <template v-if="manualDownloadForm.saveTo === 'local'">
            <div class="flex min-w-0 gap-2">
              <input
                v-model="manualDownloadForm.directory"
                class="input-base min-w-0 flex-1 rounded-lg px-3 py-2 text-sm"
                :placeholder="settingsForm.downloadDir || '下载目录'"
                aria-label="本机保存目录"
                spellcheck="false"
              />
              <button v-if="canPickDirectory" type="button" class="btn-ghost shrink-0 px-3 text-sm" @click="pickManualDirectory">选择…</button>
            </div>
            <p class="text-xs text-[var(--muted)]">留空存到设置里的下载目录；填写需为运行 Bangumi Cat 那台机器上的绝对路径。</p>
          </template>
          <template v-else>
            <p v-if="cloudConnected" class="text-xs leading-5 text-[var(--muted)]">
              先下载到本机，完成后上传到 Google Drive「{{ settingsForm.cloudDriveFolderId === "root" || !settingsForm.cloudDriveFolderId ? "我的云盘" : settingsForm.cloudDriveFolderId }}」；云端大小和 MD5 校验通过后删除本机副本。
            </p>
            <p v-else class="text-xs leading-5 text-[var(--warn)]">
              还没连接 Google Drive。
              <button type="button" class="font-semibold underline" @click="manualComposerOpen = false; selectTab('settings')">去设置 → 云盘归档</button>
            </p>
          </template>
        </div>
      </div>
      <template #footer>
        <button type="button" class="btn-ghost" @click="manualComposerOpen = false; manualDownloadForm.url = ''">取消</button>
        <button
          type="button"
          class="btn-primary"
          :disabled="isBusy('download:manual') || !manualDownloadForm.url.trim() || (manualDownloadForm.saveTo === 'cloud' && !cloudConnected)"
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

<style scoped>
/* 一部番一张卡：平时无边框，悬停 / 展开时浮出浅底，和视频 App 下载页一样轻 */
.download-card {
  border-radius: 14px;
  transition: background 0.15s;
}
.download-card:hover,
.download-card.is-open {
  background: var(--tint-1);
}
.download-row {
  display: flex;
  align-items: center;
  gap: 14px;
  padding: 10px;
  cursor: pointer;
}
/* 竖版海报而不是方形缩略图：番剧封面本来就是 2:3，裁成方的只剩半张脸 */
.download-poster {
  position: relative;
  flex-shrink: 0;
  width: 3.25rem;
  aspect-ratio: 2 / 3;
  overflow: hidden;
  border-radius: 8px;
  box-shadow: 0 0 0 1px var(--line);
}
.download-info {
  flex: 1;
}

.downloads-layout {
  display: grid;
  grid-template-columns: minmax(0, 1fr);
  min-height: 0;
  overflow: hidden;
}
.downloads-layout.has-side {
  grid-template-columns: minmax(0, 1fr) 22rem;
  gap: 20px;
}
.download-detail {
  display: grid;
  align-content: start;
  gap: 16px;
  min-height: 0;
  overflow: auto;
  padding: 12px;
}
/* 右侧栏：一块浅底圆角面板，和左边列表里「选中」那张卡同一个底色，看得出是它的详情 */
.download-detail.is-side {
  border-radius: 14px;
  background: var(--tint-1);
  padding: 16px;
}
@media (max-width: 639px) {
  .download-row {
    gap: 12px;
    padding: 8px 6px;
  }
  .download-poster {
    width: 2.75rem;
  }
}
</style>
