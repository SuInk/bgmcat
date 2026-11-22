<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from "vue";
import type { DownloadJob, ExternalPlayerId, MikanDownloadItem, Subscription } from "@shared/index";
import { resolveDownloadArtworkUrl, streamDownloadNow } from "../api";
import AppSelect from "../components/common/AppSelect.vue";
import DownloadDialogs from "../components/downloads/DownloadDialogs.vue";
import DownloadPlaybackSection from "../components/downloads/DownloadPlaybackSection.vue";
import DownloadVersionPane from "../components/downloads/DownloadVersionPane.vue";
import AppNavIcon from "../components/layout/AppNavIcon.vue";
import {
  canArchiveToCloud,
  cloudArchiveLabel,
  isCloudFailed,
  isCloudTransferring
} from "../lib/cloudArchive";
import {
  buildGroupedDownloads,
  cachedMetaLine,
  canPauseDownload,
  canPlayDownload,
  canResumeDownload,
  computeFilterCounts,
  coverLabel,
  coverStyle,
  groupArchivableGids,
  groupCloudStoredCount,
  groupCloudTransferSummary,
  groupMetaLine,
  groupProgress,
  groupProgressLine,
  groupProgressWidth,
  groupStatus,
  isGroupActiveOrTransferring,
  matchesDownloadFilter,
  resolutionLabel,
  subgroupLabel,
  type DownloadFilterKey,
  type DownloadSortMode,
  type SeriesDownloadGroup
} from "../lib/downloadGrouping";
import { openExternalLink } from "../lib/actions";
import { formatBytes, formatSpeed, progressPercent } from "../lib/format";
import { selectTab } from "../stores/boot";
import { isBusyMatching } from "../stores/busy";
import {
  animeDetail,
  animeDetailError,
  closeAnimeDetail,
  homepageAnime,
  openAnimeDetail,
  openDownloadAnimeDetail,
  queueMikanDownload
} from "../stores/discovery";
import {
  activePlayback,
  archiveBatchDownloads,
  cancelCloudArchive,
  closePlayback,
  displayTitle,
  downloadRawError,
  downloads,
  handleDownloadConflict,
  isDownloadRowBusy,
  launchExternalPlayer,
  openPlayback,
  pauseAllDownloads,
  removeBatchDownloads,
  resumeAllDownloads,
  selectedDownloadDetailGid,
  shortAnimeTitle,
  totalCloudUploadSpeed,
  totalDownloadSpeed,
  totalUploadSpeed,
  triggerCloudArchive,
  updateDownloadState
} from "../stores/downloads";
import { canStreamDownload, pendingStreamGid, streamDownloadJob } from "../stores/streamPlay";
import { subscriptions } from "../stores/subscriptions";
import { toastError } from "../stores/toasts";
import {
  SHARE_FEATURE_ENABLED,
  activeSharesCount,
  canShareSeriesGroup,
  openShareForDownload,
  openShareForSeriesGroup,
  openShareManager,
  refreshShares
} from "../stores/shares";
import {
  DOWNLOAD_SIDE_DEFAULT_WIDTH,
  DOWNLOAD_SIDE_MAX_WIDTH,
  DOWNLOAD_SIDE_MIN_WIDTH,
  downloadSideWidth,
  viewportWidth
} from "../stores/ui";

const filterKey = ref<DownloadFilterKey>("all");
const sortMode = ref<DownloadSortMode>("status");
const searchQuery = ref("");
const deleteCandidate = ref<DownloadJob | null>(null);
const batchDeleteCandidates = ref<DownloadJob[]>([]);
const downloadListPage = ref(1);
const downloadRowsPerPage = ref(16);
const downloadsScrollRef = ref<HTMLElement | null>(null);
const downloadsLoadMoreSentinelRef = ref<HTMLElement | null>(null);
let downloadsLoadMoreObserver: IntersectionObserver | null = null;

/**
 * 就地展开的那一行（同时只展开一行）。
 * 分组 key 由标题推导，订阅/种子元数据晚到会让它变形；用 gid 做锚点才不会把已展开的行收起来。
 */
const activeDownloadGroupKey = ref<string | null>(null);
const activeGroupAnchorGid = ref<string | null>(null);
const selectedGroupKeys = ref<Set<string>>(new Set());
/** 多选是低频操作：平时不摆复选框，点工具栏「选择」才出现（iOS 照片 / 文件 App 的做法） */
const selectMode = ref(false);
const manualComposerOpen = ref(false);

const detailLoading = computed(() => isBusyMatching((key) => key.startsWith("download-detail:")));

const subscriptionById = computed(() => {
  const map = new Map<number, Subscription>();
  for (const sub of subscriptions.value) {
    map.set(sub.id, sub);
  }
  return map;
});

const filteredDownloads = computed(() => {
  const query = searchQuery.value.trim().toLowerCase();
  return downloads.value.filter((download) => {
    if (!matchesDownloadFilter(download, filterKey.value)) {
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

const groupedDownloads = computed<SeriesDownloadGroup[]>(() =>
  buildGroupedDownloads(
    filteredDownloads.value,
    sortMode.value,
    displayTitle,
    shortAnimeTitle,
    subscriptionById.value
  )
);

const allGroupedDownloads = computed<SeriesDownloadGroup[]>(() =>
  buildGroupedDownloads(
    downloads.value,
    "status",
    displayTitle,
    shortAnimeTitle,
    subscriptionById.value
  )
);

/** 播放中的任务属于哪一组——选集面板与自动连播都要用（不受列表筛选条件影响） */
const playbackGroup = computed(() =>
  activePlayback.value
    ? allGroupedDownloads.value.find((group) =>
        group.downloads.some((download) => download.gid === activePlayback.value?.gid)
      ) ?? null
    : null
);

const activePlaybackDownload = computed(() =>
  activePlayback.value
    ? downloads.value.find((item) => item.gid === activePlayback.value?.gid) ?? null
    : null
);

const playbackDetailBelongs = computed(() => {
  if (!activePlayback.value || !selectedDownloadDetailGid.value) {
    return false;
  }
  if (selectedDownloadDetailGid.value === activePlayback.value.gid) {
    return true;
  }
  return Boolean(
    playbackGroup.value?.downloads.some((item) => item.gid === selectedDownloadDetailGid.value)
  );
});

const playbackAnimeDetail = computed(() =>
  playbackDetailBelongs.value ? animeDetail.value : null
);

const playbackDetailLoading = computed(
  () => detailLoading.value || Boolean(playbackAnimeDetail.value?.detailPending)
);

const pendingAutoPlayGid = ref<string | null>(null);
const queueingMikanId = ref<string | null>(null);

const activePlaybackSubgroup = computed(() =>
  activePlaybackDownload.value
    ? subgroupLabel(activePlaybackDownload.value, displayTitle, subscriptionById.value)
    : null
);

const activePlaybackResolution = computed(() =>
  activePlaybackDownload.value ? resolutionLabel(activePlaybackDownload.value, displayTitle) : null
);

function playDownloadByGid(gid: string): void {
  pendingAutoPlayGid.value = null;
  const target = downloads.value.find((item) => item.gid === gid);
  if (target) {
    void openPlayback(target);
  }
}

function selectDownloadingForAutoPlay(gid: string): void {
  const target = downloads.value.find((item) => item.gid === gid);
  if (target && canPlayDownload(target)) {
    pendingAutoPlayGid.value = null;
    void openPlayback(target);
    return;
  }
  pendingAutoPlayGid.value = pendingAutoPlayGid.value === gid ? null : gid;
  if (pendingAutoPlayGid.value && target) {
    void jumpQueueForPlayback(target);
  }
}

/** 播放页里等某集：排队/暂停中的 BT 任务立即开始并优先拉开头，别干等并发名额。 */
async function jumpQueueForPlayback(job: DownloadJob): Promise<void> {
  if (!canStreamDownload(job) || job.status === "active") return;
  try {
    await streamDownloadNow(job.gid);
  } catch (error) {
    toastError(error, "无法边下边播");
  }
}

async function handleQueueMikanEpisode(item: MikanDownloadItem): Promise<void> {
  if (queueingMikanId.value) return;
  queueingMikanId.value = item.id;
  try {
    const job = await queueMikanDownload(item);
    if (!job || !activePlayback.value) return;
    if (canPlayDownload(job)) {
      pendingAutoPlayGid.value = null;
      void openPlayback(job);
    } else {
      pendingAutoPlayGid.value = job.gid;
      void jumpQueueForPlayback(job);
    }
  } finally {
    queueingMikanId.value = null;
  }
}

function handleClosePlayback(): void {
  pendingAutoPlayGid.value = null;
  closePlayback();
}

function handleExternalPlayer(playerId: ExternalPlayerId): void {
  if (activePlayback.value) {
    void launchExternalPlayer(activePlayback.value.gid, playerId);
  }
}

function handlePlaybackShare(episodeLabel: string | null): void {
  if (activePlaybackDownload.value) {
    openShareForDownload(activePlaybackDownload.value, playbackGroup.value, episodeLabel);
  } else if (playbackGroup.value) {
    openShareForSeriesGroup(playbackGroup.value);
  }
}

const downloadPageItemCount = computed(() => Math.max(1, downloadRowsPerPage.value));
const downloadSeriesTotalPages = computed(() =>
  Math.max(1, Math.ceil(groupedDownloads.value.length / downloadPageItemCount.value))
);
const pagedDownloadGroups = computed(() =>
  groupedDownloads.value.slice(0, downloadListPage.value * downloadPageItemCount.value)
);
const hasMoreDownloadGroups = computed(
  () => pagedDownloadGroups.value.length < groupedDownloads.value.length
);

function maybeLoadMoreDownloads(): void {
  if (!hasMoreDownloadGroups.value) {
    return;
  }
  const scroller = downloadsScrollRef.value;
  if (!scroller) {
    return;
  }
  if (scroller.scrollHeight - scroller.scrollTop - scroller.clientHeight <= 280) {
    downloadListPage.value = Math.min(downloadSeriesTotalPages.value, downloadListPage.value + 1);
    void nextTick(maybeLoadMoreDownloads);
  }
}

function setupDownloadsLoadMoreObserver(): void {
  downloadsLoadMoreObserver?.disconnect();
  downloadsLoadMoreObserver = null;
  if (
    typeof IntersectionObserver === "undefined" ||
    !downloadsScrollRef.value ||
    !downloadsLoadMoreSentinelRef.value
  ) {
    return;
  }
  downloadsLoadMoreObserver = new IntersectionObserver(
    (entries) => {
      if (entries.some((entry) => entry.isIntersecting)) {
        maybeLoadMoreDownloads();
      }
    },
    { root: downloadsScrollRef.value, rootMargin: "280px 0px" }
  );
  downloadsLoadMoreObserver.observe(downloadsLoadMoreSentinelRef.value);
}

const filterCounts = computed(() => computeFilterCounts(downloads.value));
const filterTabs = computed(() => {
  const tabs: Array<{ key: DownloadFilterKey; label: string; count: number }> = [
    { key: "all", label: "全部", count: filterCounts.value.all },
    { key: "active", label: "下载中", count: filterCounts.value.active }
  ];
  if (filterCounts.value.transferring > 0 || filterKey.value === "transferring") {
    tabs.push({ key: "transferring", label: "转存中", count: filterCounts.value.transferring });
  }
  tabs.push(
    { key: "waiting", label: "等待中", count: filterCounts.value.waiting },
    { key: "paused", label: "暂停", count: filterCounts.value.paused },
    { key: "complete", label: "已完成", count: filterCounts.value.complete }
  );
  if (filterCounts.value.cloud > 0 || filterKey.value === "cloud") {
    tabs.push({ key: "cloud", label: "已存云盘", count: filterCounts.value.cloud });
  }
  tabs.push({ key: "issues", label: "异常", count: filterCounts.value.issues });
  return tabs;
});

/**
 * 统一采用海报封面墙排版：正在下载或转存云盘的番排在最前面，封面自带实时状态徽章与底部进度条，
 * 避免「下载一半」和「已缓存」被拆成两套割裂排版。
 */
const cachingGroups = computed(() => pagedDownloadGroups.value.filter(isGroupActiveOrTransferring));
const cachedGroups = computed(() => pagedDownloadGroups.value.filter((group) => !isGroupActiveOrTransferring(group)));
const unifiedDownloadGroups = computed(() => [...cachingGroups.value, ...cachedGroups.value]);

function cardStatusBadge(group: SeriesDownloadGroup): { label: string; kind: "err" | "cloud-up" | "active" | "paused" | "cloud" } | null {
  if (group.primary.status === "error" || isCloudFailed(group.primary)) {
    return { label: "异常", kind: "err" };
  }
  const cloud = groupCloudTransferSummary(group);
  if (cloud?.transferring || isCloudTransferring(group.primary)) {
    return { label: `转存 ${groupProgressWidth(group)}%`, kind: "cloud-up" };
  }
  if (isGroupActiveOrTransferring(group)) {
    const st = groupStatus(group);
    if (st === "paused") {
      return { label: `已暂停 ${groupProgressWidth(group)}%`, kind: "paused" };
    }
    return { label: `缓存 ${groupProgressWidth(group)}%`, kind: "active" };
  }
  if (groupCloudStoredCount(group) > 0) {
    return { label: "云盘", kind: "cloud" };
  }
  return null;
}

function resolveGroupArtworkUrl(group: SeriesDownloadGroup): string | null {
  for (const download of group.downloads) {
    if (download.feedItemImageUrl) {
      const url = resolveDownloadArtworkUrl(download);
      if (url) {
        return url;
      }
    }
  }
  for (const download of group.downloads) {
    const url = resolveDownloadArtworkUrl(download);
    if (url) {
      return url;
    }
  }
  const matchedHomepage = homepageAnime.value.find((item) => item.title === group.title);
  if (matchedHomepage?.imageUrl) {
    return matchedHomepage.imageUrl;
  }
  return null;
}

function formatGroupMetaLine(group: SeriesDownloadGroup): string {
  return groupMetaLine(group, displayTitle, subscriptionById.value);
}

function onGroupActivate(group: SeriesDownloadGroup): void {
  if (selectMode.value) {
    setGroupSelected(group.key, !selectedGroupKeys.value.has(group.key));
    return;
  }
  toggleDownloadGroup(group.key);
}

const selectedGroups = computed(() => groupedDownloads.value.filter((group) => selectedGroupKeys.value.has(group.key)));
const selectedDownloads = computed(() => selectedGroups.value.flatMap((group) => group.downloads));
const selectedActionableDownloads = computed(() =>
  selectedDownloads.value.filter((download) => download.status !== "complete" && download.status !== "removed")
);
const selectedArchivableGids = computed(() =>
  selectedDownloads.value.filter(canArchiveToCloud).map((download) => download.gid)
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
  void Promise.allSettled(
    targets.map((download) => updateDownloadState(action, download.gid, { silent: true, skipRefresh: true }))
  );
}

function runBatchCloudArchive(): void {
  if (!selectedArchivableGids.value.length) {
    return;
  }
  void archiveBatchDownloads(selectedArchivableGids.value);
  clearSelectedGroups();
}

function archiveCachedGroup(group: SeriesDownloadGroup): void {
  const gids = groupArchivableGids(group);
  if (gids.length === 1) {
    void triggerCloudArchive(gids[0]!);
  } else if (gids.length > 1) {
    void archiveBatchDownloads(gids);
  }
}

function requestDelete(download: DownloadJob): void {
  deleteCandidate.value = download;
  batchDeleteCandidates.value = [];
}

function requestBatchDelete(): void {
  batchDeleteCandidates.value = selectedDownloads.value;
  deleteCandidate.value = null;
}

function closeDeleteDialog(): void {
  deleteCandidate.value = null;
  batchDeleteCandidates.value = [];
}

function confirmDelete(deleteLocalFiles: boolean): void {
  if (batchDeleteCandidates.value.length) {
    const gids = batchDeleteCandidates.value.map((download) => download.gid);
    void removeBatchDownloads(gids, deleteLocalFiles);
    clearSelectedGroups();
    closeDeleteDialog();
    return;
  }

  const download = deleteCandidate.value;
  if (!download) {
    return;
  }
  void updateDownloadState("remove", download.gid, { deleteLocalFiles });
  closeDeleteDialog();
}

function toggleDownloadGroup(key: string): void {
  if (activeDownloadGroupKey.value === key) {
    collapseDownloadGroup();
    return;
  }
  activeDownloadGroupKey.value = key;
  const group = groupedDownloads.value.find((item) => item.key === key);
  if (group) {
    activeGroupAnchorGid.value = group.primary.gid;
    const detailTarget =
      group.downloads.find((item) => item.feedItemGuid?.startsWith("anime:") || item.feedItemImageUrl) ??
      group.primary;
    void openDownloadAnimeDetail(detailTarget);
  }
}

/** 宽屏（内容区放得下列表 + 22rem 侧栏）选中一部番时，分集显示在右侧栏而不是行下展开 */
const sideDownloads = computed(() => viewportWidth.value >= 1200);
/** 不够放侧栏时点开一部番就进整页分集（B 站「离线缓存」点进番剧那样） */
const fullPageDetail = computed(() => !sideDownloads.value);
const detailPanelGroup = computed(() =>
  activeDownloadGroupKey.value
    ? allGroupedDownloads.value.find((group) => group.key === activeDownloadGroupKey.value) ??
      groupedDownloads.value.find((group) => group.key === activeDownloadGroupKey.value) ??
      null
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

const detailBelongsToGroup = computed(() =>
  Boolean(
    detailPanelGroup.value &&
      animeDetail.value &&
      detailPanelGroup.value.downloads.some((item) => item.gid === selectedDownloadDetailGid.value)
  )
);

const detailMatchedAnime = computed(() =>
  detailBelongsToGroup.value ? animeDetail.value : null
);

const detailSourceLinks = computed(() => {
  const anime = detailMatchedAnime.value?.anime;
  if (!anime) {
    return [];
  }
  const links: Array<{ label: string; url: string }> = [];
  if (anime.bangumiId) {
    links.push({ label: "Bangumi", url: `https://bgm.tv/subject/${anime.bangumiId}` });
  }
  if (anime.mikanId) {
    links.push({ label: "蜜柑", url: `https://mikanani.me/Home/Bangumi/${anime.mikanId}` });
  }
  return links;
});

const detailPrimaryStreamingLink = computed(() =>
  detailMatchedAnime.value?.streamingLinks?.[0] ?? null
);

const detailRatingLabel = computed(() => {
  const value = detailMatchedAnime.value?.anime.rating;
  return typeof value === "number" && Number.isFinite(value) && value > 0 ? value.toFixed(1) : null;
});

function openDetailInDiscovery(): void {
  const matched = detailMatchedAnime.value?.anime;
  if (!matched) {
    return;
  }
  selectTab("home");
  void openAnimeDetail(matched);
}

function collapseDownloadGroup(): void {
  activeDownloadGroupKey.value = null;
  activeGroupAnchorGid.value = null;
  closeAnimeDetail();
}

function handleViewKeydown(event: KeyboardEvent): void {
  if (event.key === "Escape" && selectMode.value && !detailPanelGroup.value && !document.querySelector('[role="dialog"]')) {
    event.stopPropagation();
    clearSelectedGroups();
  }
}

const downloadsLayoutRef = ref<HTMLElement | null>(null);
const sideResizing = ref(false);
let sideResizeStartX = 0;
let sideResizeStartWidth = DOWNLOAD_SIDE_DEFAULT_WIDTH;

const downloadsLayoutStyle = computed(() => {
  if (!detailPanelGroup.value || fullPageDetail.value) {
    return undefined;
  }
  return {
    gridTemplateColumns: `minmax(0, 1fr) 16px min(${downloadSideWidth.value}px, max(22.5rem, 62%))`
  };
});

function maxDownloadSideWidth(): number {
  const layoutWidth = downloadsLayoutRef.value?.clientWidth ?? 0;
  if (layoutWidth <= 0) {
    return DOWNLOAD_SIDE_MAX_WIDTH;
  }
  return Math.min(DOWNLOAD_SIDE_MAX_WIDTH, Math.max(DOWNLOAD_SIDE_MIN_WIDTH, layoutWidth - 280));
}

function onSideResizeMove(event: PointerEvent): void {
  const delta = sideResizeStartX - event.clientX;
  const next = Math.min(maxDownloadSideWidth(), Math.max(DOWNLOAD_SIDE_MIN_WIDTH, Math.round(sideResizeStartWidth + delta)));
  downloadSideWidth.value = next;
}

function stopSideResize(): void {
  if (!sideResizing.value) return;
  sideResizing.value = false;
  document.body.style.userSelect = "";
  document.body.style.cursor = "";
  window.removeEventListener("pointermove", onSideResizeMove);
  window.removeEventListener("pointerup", stopSideResize);
  window.removeEventListener("pointercancel", stopSideResize);
}

function startSideResize(event: PointerEvent): void {
  if (fullPageDetail.value || !detailPanelGroup.value || event.button !== 0) return;
  event.preventDefault();
  sideResizeStartX = event.clientX;
  sideResizeStartWidth = downloadSideWidth.value;
  sideResizing.value = true;
  document.body.style.userSelect = "none";
  document.body.style.cursor = "col-resize";
  window.addEventListener("pointermove", onSideResizeMove);
  window.addEventListener("pointerup", stopSideResize);
  window.addEventListener("pointercancel", stopSideResize);
}

function resetSideWidth(): void {
  downloadSideWidth.value = DOWNLOAD_SIDE_DEFAULT_WIDTH;
}

function handleSideResizeKeydown(event: KeyboardEvent): void {
  if (event.key === "ArrowLeft") {
    event.preventDefault();
    downloadSideWidth.value = Math.min(maxDownloadSideWidth(), downloadSideWidth.value + 16);
  } else if (event.key === "ArrowRight") {
    event.preventDefault();
    downloadSideWidth.value = Math.max(DOWNLOAD_SIDE_MIN_WIDTH, downloadSideWidth.value - 16);
  } else if (event.key === "Home" || event.key === "0") {
    event.preventDefault();
    resetSideWidth();
  }
}

onMounted(() => {
  void refreshShares(true);
  window.addEventListener("keydown", handleViewKeydown);
  void nextTick(() => {
    setupDownloadsLoadMoreObserver();
    maybeLoadMoreDownloads();
  });
});
onBeforeUnmount(() => {
  stopSideResize();
  downloadsLoadMoreObserver?.disconnect();
  downloadsLoadMoreObserver = null;
  window.removeEventListener("keydown", handleViewKeydown);
});

watch([downloadSeriesTotalPages, downloadListPage], () => {
  const clamped = Math.min(Math.max(1, downloadListPage.value), downloadSeriesTotalPages.value);
  if (clamped !== downloadListPage.value) {
    downloadListPage.value = clamped;
  }
});

watch(
  [() => groupedDownloads.value.length, () => pagedDownloadGroups.value.length],
  () => {
    void nextTick(() => {
      setupDownloadsLoadMoreObserver();
      maybeLoadMoreDownloads();
    });
  }
);

watch([filterKey, searchQuery, sortMode], () => {
  downloadListPage.value = 1;
  activeDownloadGroupKey.value = null;
  activeGroupAnchorGid.value = null;
  downloadsScrollRef.value?.scrollTo({ top: 0 });
});

watch(
  [() => activePlayback.value?.gid, activePlaybackDownload],
  ([gid, download]) => {
    if (!gid || !download) return;
    if (!playbackDetailBelongs.value || (!animeDetail.value && !detailLoading.value && !animeDetailError.value)) {
      void openDownloadAnimeDetail(download);
    }
  },
  { immediate: true }
);

watch(downloads, (list) => {
  const waitingGid = pendingAutoPlayGid.value;
  if (!waitingGid || !activePlayback.value) {
    return;
  }
  const readyJob = list.find((item) => item.gid === waitingGid && canPlayDownload(item));
  if (readyJob) {
    pendingAutoPlayGid.value = null;
    void openPlayback(readyJob);
  }
});

watch(groupedDownloads, () => {
  if (activeDownloadGroupKey.value && !groupedDownloads.value.some((group) => group.key === activeDownloadGroupKey.value)) {
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
  if (selectedGroupKeys.value.size > 0) {
    const validKeys = new Set(groupedDownloads.value.map((group) => group.key));
    const filtered = Array.from(selectedGroupKeys.value).filter((key) => validKeys.has(key));
    if (filtered.length !== selectedGroupKeys.value.size) {
      selectedGroupKeys.value = new Set(filtered);
    }
  }
});
</script>

<template>
  <section class="grid h-full min-h-0 grid-cols-[minmax(0,1fr)] overflow-hidden">
    <DownloadPlaybackSection
      v-if="activePlayback"
      :playback="activePlayback"
      :active-download="activePlaybackDownload"
      :total-download-speed="totalDownloadSpeed"
      :total-upload-speed="totalUploadSpeed"
      :total-cloud-upload-speed="totalCloudUploadSpeed"
      :group="playbackGroup"
      :subgroup="activePlaybackSubgroup"
      :resolution="activePlaybackResolution"
      :anime-detail="playbackAnimeDetail"
      :detail-loading="playbackDetailLoading"
      :pending-auto-play-gid="pendingAutoPlayGid"
      :queueing-mikan-id="queueingMikanId"
      @close="handleClosePlayback"
      @play-gid="playDownloadByGid"
      @select-downloading="selectDownloadingForAutoPlay"
      @cancel-auto-play="pendingAutoPlayGid = null"
      @queue-mikan="handleQueueMikanEpisode"
      @external-player="handleExternalPlayer"
      @share="handlePlaybackShare"
    />

    <div
      v-else
      ref="downloadsLayoutRef"
      class="downloads-layout"
      :class="{ 'has-side': detailPanelGroup && !fullPageDetail }"
      :style="downloadsLayoutStyle"
    >
      <div v-if="!(fullPageDetail && detailPanelGroup)" class="motion-rise grid min-h-0 grid-rows-[auto_minmax(0,1fr)_auto] overflow-hidden">
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
            <span
              class="toolbar-meta inline-flex shrink-0 items-center gap-2.5 rounded-full border border-[var(--line)] bg-[var(--panel)] px-3 py-1 text-xs font-semibold tabular-nums"
              :title="`实时速率：下载 ${formatSpeed(totalDownloadSpeed)} · 上传 ${formatSpeed(totalUploadSpeed)}${totalCloudUploadSpeed > 0 ? ` · 云盘转存 ${formatSpeed(totalCloudUploadSpeed)}` : ''}`"
            >
              <span :class="totalDownloadSpeed > 0 ? 'text-[var(--ok)]' : 'text-[var(--muted)]'">
                ↓ <span :class="totalDownloadSpeed > 0 ? 'text-[var(--ink)]' : ''">{{ formatSpeed(totalDownloadSpeed) }}</span>
              </span>
              <span class="h-3 w-px bg-[var(--line)] max-sm:hidden" aria-hidden="true" />
              <span class="max-sm:hidden" :class="totalUploadSpeed > 0 ? 'text-[var(--accent)]' : 'text-[var(--muted)]'">
                ↑ <span :class="totalUploadSpeed > 0 ? 'text-[var(--ink)]' : ''">{{ formatSpeed(totalUploadSpeed) }}</span>
              </span>
              <template v-if="totalCloudUploadSpeed > 0">
                <span class="h-3 w-px bg-[var(--line)]" aria-hidden="true" />
                <span
                  class="inline-flex items-center gap-1 text-[var(--accent-vivid)]"
                  title="Google Drive 实时转存速率"
                >
                  <AppNavIcon name="cloud-upload" class="size-3.5 shrink-0" aria-hidden="true" />
                  <span>{{ formatSpeed(totalCloudUploadSpeed) }}</span>
                </span>
              </template>
            </span>
          </div>

          <div v-if="downloads.length" class="toolbar-row flex-wrap">
            <label class="search-field min-w-0 flex-1 basis-[8rem] sm:min-w-[14rem] sm:max-w-[22rem]">
              <AppNavIcon name="search" aria-hidden="true" />
              <input v-model="searchQuery" type="search" aria-label="搜索下载任务" placeholder="搜索下载…" />
            </label>
            <AppSelect
              :model-value="sortMode"
              aria-label="排序方式"
              :options="[
                { value: 'status', label: '按状态' },
                { value: 'updated', label: '按更新时间' },
                { value: 'progress', label: '按进度' }
              ]"
              @update:model-value="sortMode = $event as typeof sortMode"
            />
            <div class="ml-auto flex flex-wrap items-center justify-end gap-2 max-sm:contents">
              <button
                v-if="SHARE_FEATURE_ENABLED"
                type="button"
                class="btn-ghost inline-flex items-center gap-1.5 px-3 py-1.5 text-sm font-semibold max-sm:px-2.5"
                title="管理已分享的在线观看链接"
                @click="openShareManager"
              >
                <AppNavIcon name="share" class="size-3.5" aria-hidden="true" />
                <span>已分享</span>
                <span v-if="activeSharesCount > 0" class="rounded-full bg-[var(--accent-soft)] px-1.5 text-xs text-[var(--accent-vivid)] tabular-nums">
                  {{ activeSharesCount }}
                </span>
              </button>
              <button
                type="button"
                class="btn-ghost px-3 py-1.5 text-sm font-semibold max-sm:px-2.5"
                :aria-pressed="selectMode"
                @click="selectMode ? clearSelectedGroups() : (selectMode = true)"
              >
                {{ selectMode ? "完成" : "选择" }}
              </button>
              <button
                type="button"
                class="btn-ghost px-3 py-1.5 text-sm font-semibold max-sm:order-last disabled:hidden"
                :disabled="!filterCounts.paused"
                @click="resumeAllDownloads"
              >
                全部开始
              </button>
              <button
                type="button"
                class="btn-ghost px-3 py-1.5 text-sm font-semibold max-sm:order-last disabled:hidden"
                :disabled="!(filterCounts.active + filterCounts.waiting)"
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

        <div
          ref="downloadsScrollRef"
          class="@container min-h-0 overflow-auto pb-2 max-md:pb-[max(5.5rem,calc(env(safe-area-inset-bottom)+4.5rem))]"
          @scroll.passive="maybeLoadMoreDownloads"
        >
          <div v-if="!filteredDownloads.length" class="empty-state h-full">
            <div class="grid max-w-md gap-3">
              <span class="empty-icon"><AppNavIcon name="inbox" class="size-6" aria-hidden="true" /></span>
              <template v-if="downloads.length">
                <p class="text-sm font-semibold text-[var(--ink)]">当前筛选条件下没有任务</p>
                <button
                  type="button"
                  class="btn-ghost justify-self-center px-4 py-2 text-sm font-semibold"
                  @click="filterKey = 'all'; searchQuery = ''"
                >
                  清除筛选
                </button>
              </template>
              <template v-else>
                <p class="text-sm font-semibold text-[var(--ink)]">暂无下载任务</p>
                <div class="flex flex-wrap justify-center gap-2">
                  <button
                    type="button"
                    class="btn-primary px-4 py-2 text-sm font-semibold"
                    @click="manualComposerOpen = true"
                  >
                    添加链接
                  </button>
                  <button
                    type="button"
                    class="btn-ghost px-4 py-2 text-sm font-semibold"
                    @click="selectTab('home')"
                  >
                    发现番剧
                  </button>
                </div>
              </template>
            </div>
          </div>

          <div v-else class="grid gap-4 py-2">
            <section class="grid gap-3">
              <div class="cache-grid">
                <div
                  v-for="group in unifiedDownloadGroups"
                  :key="group.key"
                  class="cache-card"
                  :class="{
                    'is-open': activeDownloadGroupKey === group.key,
                    'is-selected': selectMode && selectedGroupKeys.has(group.key)
                  }"
                  role="button"
                  tabindex="0"
                  :aria-label="`${group.title}，${isGroupActiveOrTransferring(group) ? groupProgressLine(group) : cachedMetaLine(group)}`"
                  @click="onGroupActivate(group)"
                  @keydown.enter.prevent="onGroupActivate(group)"
                  @keydown.space.prevent="onGroupActivate(group)"
                >
                  <div class="cache-poster" :style="coverStyle(group.primary.gid)">
                    <img
                      v-if="resolveGroupArtworkUrl(group)"
                      :src="resolveGroupArtworkUrl(group) ?? ''"
                      :alt="group.title"
                      class="absolute inset-0 size-full object-cover"
                      loading="lazy"
                    />
                    <div v-else class="absolute inset-0 grid place-items-center px-2 text-center">
                      <span class="text-sm font-semibold text-white/90">{{ coverLabel(group.title) }}</span>
                    </div>

                    <span
                      v-if="!selectMode && cardStatusBadge(group)"
                      class="cache-cloud-badge"
                      :class="`is-${cardStatusBadge(group)!.kind}`"
                    >
                      <AppNavIcon
                        v-if="cardStatusBadge(group)!.kind === 'cloud' || cardStatusBadge(group)!.kind === 'cloud-up'"
                        name="cloud"
                        class="size-3"
                        aria-hidden="true"
                      />
                      <span
                        v-else-if="cardStatusBadge(group)!.kind === 'active'"
                        class="size-1.5 rounded-full bg-current animate-pulse"
                        aria-hidden="true"
                      />
                      <span class="tabular-nums">{{ cardStatusBadge(group)!.label }}</span>
                    </span>

                    <span v-if="!selectMode" class="cache-top-actions">
                      <button
                        v-if="SHARE_FEATURE_ENABLED && canShareSeriesGroup(group)"
                        type="button"
                        class="cache-cloud-btn"
                        :title="`分享 ${group.title}`"
                        :aria-label="`分享 ${group.title}`"
                        @click.stop="openShareForSeriesGroup(group)"
                      >
                        <AppNavIcon name="share" class="size-3.5" />
                      </button>
                      <button
                        v-if="isGroupActiveOrTransferring(group) && (canPauseDownload(group.primary) || canResumeDownload(group.primary))"
                        type="button"
                        class="cache-cloud-btn"
                        :title="canResumeDownload(group.primary) ? '继续下载' : '暂停下载'"
                        :aria-label="canResumeDownload(group.primary) ? '继续下载' : '暂停下载'"
                        :disabled="isDownloadRowBusy(group.primary.gid)"
                        @click.stop="updateDownloadState(canResumeDownload(group.primary) ? 'resume' : 'pause', group.primary.gid)"
                      >
                        <AppNavIcon :name="canResumeDownload(group.primary) ? 'download' : 'pause'" class="size-3.5" />
                      </button>
                      <button
                        v-else-if="groupArchivableGids(group).length > 0"
                        type="button"
                        class="cache-cloud-btn"
                        :title="`转存 ${group.title} 至 Google Drive`"
                        :aria-label="`转存 ${group.title} 至 Google Drive`"
                        @click.stop="archiveCachedGroup(group)"
                      >
                        <AppNavIcon name="cloud-upload" class="size-3.5" />
                      </button>
                    </span>

                    <span class="cache-badge tabular-nums">{{ group.episodes.length }} 集</span>
                    <span v-if="selectMode" class="cache-check" :class="{ 'is-on': selectedGroupKeys.has(group.key) }" aria-hidden="true">
                      <AppNavIcon name="check" class="size-3.5" />
                    </span>
                    <button
                      v-else-if="group.playable"
                      type="button"
                      class="play-btn cache-play"
                      :title="`${group.playable.status === 'complete' ? '播放' : '边下边播'} ${group.playableEpisodeLabel ?? ''}`.trim()"
                      :aria-label="`${group.playable.status === 'complete' ? '播放' : '边下边播'} ${group.playableEpisodeLabel ?? ''}`.trim()"
                      @click.stop="openPlayback(group.playable)"
                    >
                      <AppNavIcon name="play" class="size-3.5" />
                    </button>

                    <!-- 正在缓存 / 转存时在封面底边显示实时进度细条 -->
                    <span v-if="isGroupActiveOrTransferring(group)" class="cache-poster-progress" aria-hidden="true">
                      <span
                        class="cache-poster-progress-fill"
                        :class="{
                          'is-err': group.primary.status === 'error' || isCloudFailed(group.primary),
                          'is-paused': group.primary.status === 'paused'
                        }"
                        :style="{ width: `${groupProgressWidth(group)}%` }"
                      />
                    </span>
                  </div>

                  <strong class="mt-2 line-clamp-1 text-[13px] font-semibold text-[var(--ink)]" :title="group.title">{{ group.title }}</strong>
                  <span
                    v-if="isGroupActiveOrTransferring(group)"
                    class="line-clamp-2 text-[11.5px] leading-snug tabular-nums"
                    :class="
                      group.primary.status === 'error' || isCloudFailed(group.primary)
                        ? 'text-[var(--err)]'
                        : isCloudTransferring(group.primary)
                          ? 'text-[var(--accent-vivid)]'
                          : 'text-[var(--muted)]'
                    "
                    :title="groupProgressLine(group)"
                  >
                    {{ groupProgressLine(group) }}
                  </span>
                  <span v-else class="line-clamp-1 text-xs text-[var(--muted)]">{{ cachedMetaLine(group) }}</span>
                </div>
              </div>
            </section>
            <div
              v-if="hasMoreDownloadGroups"
              ref="downloadsLoadMoreSentinelRef"
              class="h-4"
              aria-hidden="true"
            />
          </div>
        </div>

        <div
          v-if="filteredDownloads.length && selectMode"
          class="flex flex-wrap items-center gap-2 border-t border-[var(--line)] px-4 py-2"
        >
          <span class="text-sm font-medium text-[var(--ink)]">已选 {{ selectedGroupKeys.size }} 项</span>
          <button type="button" class="btn-ghost h-8 px-3 text-sm" @click="togglePagedGroups(!allPagedGroupsSelected)">
            {{ allPagedGroupsSelected ? "取消全选" : "全选" }}
          </button>
          <button type="button" class="btn-ghost h-8 px-3 text-sm" :disabled="!selectedActionableDownloads.length" @click="runBatchTransfer('resume')">
            开始
          </button>
          <button type="button" class="btn-ghost h-8 px-3 text-sm" :disabled="!selectedActionableDownloads.length" @click="runBatchTransfer('pause')">
            暂停
          </button>
          <button
            type="button"
            class="inline-flex h-8 items-center gap-1 rounded-lg border border-[var(--accent-line)] bg-[var(--accent-soft)] px-3 text-sm font-medium text-[var(--accent-vivid)] transition hover:border-[var(--accent)] disabled:cursor-not-allowed disabled:opacity-45"
            :disabled="!selectedArchivableGids.length"
            @click="runBatchCloudArchive"
          >
            <AppNavIcon name="cloud-upload" class="size-3.5" aria-hidden="true" />
            转存云盘
          </button>
          <button
            type="button"
            class="h-8 rounded-lg border border-[var(--err-soft)] bg-[var(--err-soft)] px-3 text-sm font-medium text-[var(--err)] transition hover:border-[var(--err)]"
            :disabled="!selectedGroupKeys.size"
            @click="requestBatchDelete"
          >
            删除
          </button>
        </div>
      </div>

      <div
        v-if="detailPanelGroup && !fullPageDetail"
        class="split-resizer"
        :class="{ 'is-dragging': sideResizing }"
        role="separator"
        aria-orientation="vertical"
        aria-label="调整详情栏宽度"
        :aria-valuenow="downloadSideWidth"
        :aria-valuemin="DOWNLOAD_SIDE_MIN_WIDTH"
        :aria-valuemax="DOWNLOAD_SIDE_MAX_WIDTH"
        title="拖动调整左右栏宽度，双击重置"
        tabindex="0"
        @pointerdown="startSideResize"
        @dblclick="resetSideWidth"
        @keydown="handleSideResizeKeydown"
      >
        <span class="split-resizer-pill" aria-hidden="true" />
      </div>

      <aside v-if="detailPanelGroup" :key="detailPanelGroup.key" class="download-detail" :class="fullPageDetail ? 'motion-rise' : 'is-side motion-slide'">
        <!-- 顶部导航条：与首页番剧详情页对齐 -->
        <div class="detail-top-nav">
          <button
            type="button"
            class="detail-back-link"
            :aria-label="fullPageDetail ? '返回下载管理' : '关闭侧栏'"
            @click="collapseDownloadGroup"
          >
            <AppNavIcon :name="fullPageDetail ? 'chevron-left' : 'close'" class="size-3.5 shrink-0" aria-hidden="true" />
            <span>{{ fullPageDetail ? "返回" : "关闭" }}</span>
          </button>

          <div v-if="detailSourceLinks.length" class="flex flex-wrap items-center gap-1 text-xs font-medium text-[var(--muted)]">
            <button
              v-for="link in detailSourceLinks"
              :key="link.label"
              type="button"
              class="inline-flex min-h-8 items-center gap-1 rounded-lg px-2 py-1 transition hover:bg-[var(--panel)] hover:text-[var(--ink)]"
              :title="link.url"
              @click="openExternalLink(link.url)"
            >
              <span>{{ link.label }}</span>
              <AppNavIcon name="external" class="size-3 opacity-75" aria-hidden="true" />
            </button>
          </div>
        </div>

        <!-- 紧凑番剧头部：与首页详情页一致的 2:3 海报 + 标题评分 + 单行操作栏 -->
        <header class="detail-hero">
          <div class="detail-hero-poster" :style="coverStyle(detailPanelGroup.primary.gid)">
            <img
              v-if="resolveGroupArtworkUrl(detailPanelGroup)"
              :src="resolveGroupArtworkUrl(detailPanelGroup) ?? ''"
              alt=""
              class="block size-full object-cover"
            />
            <span v-else class="text-sm font-semibold text-white/90">{{ coverLabel(detailPanelGroup.title) }}</span>
          </div>

          <div class="flex min-w-0 flex-col gap-1.5">
            <div class="flex items-baseline justify-between gap-2">
              <h2 class="min-w-0 truncate text-[16px] font-bold leading-snug tracking-tight text-[var(--ink)] sm:text-[17px]" :title="detailPanelGroup.title">
                {{ detailPanelAnimeTitle }}
              </h2>
              <span
                v-if="detailRatingLabel"
                class="inline-flex shrink-0 items-baseline gap-0.5 text-xs font-bold tabular-nums text-[var(--warn)]"
              >
                ★ {{ detailRatingLabel }}
              </span>
            </div>

            <p class="text-xs leading-snug text-[var(--muted)]">
              {{ formatGroupMetaLine(detailPanelGroup) }}
            </p>

            <div class="mt-0.5 flex flex-wrap items-center gap-1.5">
              <button
                v-if="detailPanelGroup.playable"
                type="button"
                class="btn-primary inline-flex h-[30px] items-center gap-1.5 rounded-[7px] px-3 text-xs font-semibold"
                @click="openPlayback(detailPanelGroup.playable)"
              >
                <AppNavIcon name="play" class="size-3 shrink-0" aria-hidden="true" />
                <span>{{ detailPanelGroup.playable.status === "complete" ? "立即播放" : "边下边播" }}</span>
              </button>

              <button
                v-if="detailMatchedAnime"
                type="button"
                class="btn-ghost inline-flex h-[30px] items-center gap-1.5 rounded-[7px] px-2.5 text-xs font-semibold"
                title="前往番剧发现页查看全部剧集、在线片源与订阅"
                @click="openDetailInDiscovery"
              >
                <AppNavIcon name="home" class="size-3.5 shrink-0 opacity-85" aria-hidden="true" />
                <span>番剧详情 / 在线资源</span>
              </button>

              <button
                v-if="detailPrimaryStreamingLink"
                type="button"
                class="btn-ghost inline-flex h-[30px] items-center gap-1.5 rounded-[7px] px-2.5 text-xs font-semibold text-[var(--accent-vivid)]"
                :title="`前往 ${detailPrimaryStreamingLink.label} 在线观看`"
                @click="openExternalLink(detailPrimaryStreamingLink.url)"
              >
                <AppNavIcon name="play" class="size-3 shrink-0" aria-hidden="true" />
                <span>{{ detailPrimaryStreamingLink.label }}</span>
                <AppNavIcon name="external" class="size-3 shrink-0 opacity-75" aria-hidden="true" />
              </button>
            </div>
          </div>
        </header>

        <DownloadVersionPane
          flat
          :show-overview="true"
          :downloads="detailPanelGroup.downloads"
          :episodes="detailPanelGroup.episodes"
          :format-bytes="formatBytes"
          :download-display-title="displayTitle"
          :progress-percent="progressPercent"
          :download-raw-error="downloadRawError"
          :is-download-row-busy="isDownloadRowBusy"
          :anime-detail="animeDetail"
          :detail-belongs-to-group="detailBelongsToGroup"
          :detail-error="animeDetailError"
          :detail-loading="detailLoading"
          @conflict="handleDownloadConflict($event.gid, $event.strategy)"
          @playback="streamDownloadJob"
          @pause="updateDownloadState('pause', $event)"
          @resume="updateDownloadState('resume', $event)"
          @remove="requestDelete"
          @archive="triggerCloudArchive($event)"
          @cancel-archive="cancelCloudArchive($event)"
          @archive-batch="archiveBatchDownloads($event)"
          @share-group="openShareForSeriesGroup(detailPanelGroup)"
          @share-episode="openShareForDownload($event.download, detailPanelGroup, $event.episodeLabel)"
        />
      </aside>
    </div>

    <DownloadDialogs
      v-model:manual-composer-open="manualComposerOpen"
      :delete-candidate="deleteCandidate"
      :batch-delete-candidates="batchDeleteCandidates"
      :delete-candidate-title="deleteCandidate ? displayTitle(deleteCandidate) : ''"
      @close-delete="closeDeleteDialog"
      @confirm-delete="confirmDelete"
    />
  </section>
</template>

<style scoped>
/* 一部番一张卡：平时无边框，悬停 / 展开时浮出浅底，和视频 App 下载页一样轻 */
.download-card {
  border-radius: 16px;
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
  padding: 10px 12px;
  cursor: pointer;
}
/* 竖版海报而不是方形缩略图：番剧封面本来就是 2:3，裁成方的只剩半张脸 */
.download-poster {
  position: relative;
  flex-shrink: 0;
  width: 3.25rem;
  aspect-ratio: 2 / 3;
  overflow: hidden;
  border-radius: 12px;
  box-shadow: 0 0 0 1px var(--line);
}
.download-info {
  flex: 1;
}

.cache-section-title {
  display: flex;
  align-items: baseline;
  gap: 6px;
  color: var(--ink);
  font-size: 15px;
  font-weight: 700;
}
.cache-section-title > span {
  color: var(--muted);
  font-size: 12px;
  font-weight: 600;
}
/* 封面墙：和首页海报墙同一个尺寸档，一屏能看到很多部 */
.cache-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(8.5rem, 1fr));
  gap: 18px 14px;
}
.cache-card {
  display: grid;
  min-width: 0;
  cursor: pointer;
  outline: none;
}
.cache-poster {
  position: relative;
  aspect-ratio: 2 / 3;
  overflow: hidden;
  border-radius: 16px;
  box-shadow: inset 0 0 0 1px rgb(255 255 255 / 0.08), 0 2px 5px rgb(0 0 0 / 0.14), 0 8px 18px -8px rgb(0 0 0 / 0.35);
  transition: transform 0.2s ease, box-shadow 0.2s ease;
}
.cache-card:hover .cache-poster,
.cache-card:focus-visible .cache-poster {
  transform: translateY(-2px);
}
.cache-card.is-open .cache-poster,
.cache-card.is-selected .cache-poster,
.cache-card:focus-visible .cache-poster {
  box-shadow: 0 0 0 2.5px var(--accent-vivid);
}
/* 右下角集数角标：B 站离线缓存封面上的「已缓存 N 个」 */
.cache-badge {
  position: absolute;
  right: 8px;
  bottom: 8px;
  padding: 1px 8px;
  border-radius: 999px;
  background: rgb(15 19 32 / 0.62);
  color: #fff;
  font-size: 11px;
  font-weight: 600;
  line-height: 18px;
  backdrop-filter: blur(8px);
}
.cache-cloud-badge {
  position: absolute;
  top: 8px;
  left: 8px;
  display: inline-flex;
  align-items: center;
  gap: 3px;
  padding: 1px 7px;
  border-radius: 999px;
  border: 1px solid rgb(255 255 255 / 0.18);
  background: rgb(15 19 32 / 0.62);
  color: #fff;
  font-size: 10.5px;
  font-weight: 600;
  line-height: 17px;
  backdrop-filter: blur(8px);
}
.cache-top-actions {
  position: absolute;
  top: 8px;
  right: 8px;
  display: inline-flex;
  align-items: center;
  gap: 5px;
}
.cache-cloud-btn {
  display: grid;
  place-items: center;
  width: 34px;
  height: 34px;
  border-radius: 999px;
  border: 1px solid rgb(255 255 255 / 0.22);
  background: rgb(15 19 32 / 0.68);
  color: #fff;
  opacity: 0;
  backdrop-filter: blur(8px);
  transition: opacity 0.15s, background 0.15s, border-color 0.15s;
}
.cache-cloud-btn:hover {
  border-color: var(--accent);
  background: var(--accent);
  color: var(--accent-contrast);
}
.cache-card:hover .cache-cloud-btn,
.cache-cloud-btn:focus-visible {
  opacity: 1;
}
.cache-play {
  position: absolute;
  left: 8px;
  bottom: 8px;
  opacity: 0;
  transition: opacity 0.15s;
}
.cache-card:hover .cache-play,
.cache-play:focus-visible {
  opacity: 1;
}
@media (hover: none) {
  .cache-play {
    opacity: 1;
  }
}
.cache-check {
  position: absolute;
  top: 8px;
  left: 8px;
  display: grid;
  place-items: center;
  width: 26px;
  height: 26px;
  border-radius: 999px;
  border: 2px solid #fff;
  background: rgb(0 0 0 / 0.35);
  color: transparent;
}
.cache-check::after {
  content: "";
  position: absolute;
  inset: -6px;
}
.cache-check.is-on {
  border-color: var(--accent);
  background: var(--accent);
  color: var(--accent-contrast);
}

.downloads-layout {
  display: grid;
  grid-template-columns: minmax(0, 1fr);
  min-height: 0;
  overflow: hidden;
}
.downloads-layout.has-side {
  grid-template-columns: minmax(0, 1fr) 16px clamp(27rem, 42%, 34rem);
  gap: 0;
}
.downloads-layout.has-side .toolbar-row {
  flex-wrap: wrap;
}
.split-resizer {
  position: relative;
  display: flex;
  align-items: center;
  justify-content: center;
  cursor: col-resize;
  touch-action: none;
  user-select: none;
}
.split-resizer-pill {
  width: 3px;
  height: 36px;
  border-radius: 999px;
  background: var(--line);
  opacity: 0.65;
  transition: background 150ms ease, height 150ms ease, opacity 150ms ease;
}
.split-resizer:hover .split-resizer-pill,
.split-resizer.is-dragging .split-resizer-pill,
.split-resizer:focus-visible .split-resizer-pill {
  height: 56px;
  background: var(--accent);
  opacity: 1;
}
.split-resizer:focus-visible {
  outline: none;
}
.download-detail {
  display: grid;
  align-content: start;
  gap: 10px;
  min-height: 0;
  overflow-x: hidden;
  overflow-y: auto;
  padding: 10px 12px;
  scrollbar-width: none;
}
.download-detail::-webkit-scrollbar {
  width: 0;
  height: 0;
}
@media (max-width: 768px) {
  .download-detail {
    padding-bottom: max(5.5rem, calc(env(safe-area-inset-bottom) + 4.5rem));
  }
}
/* 右侧栏：一块浅底圆角面板，和左边列表里「选中」那张卡同一个底色，看得出是它的详情 */
.download-detail.is-side {
  border-radius: 18px;
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
.cache-cloud-badge.is-active,
.cache-cloud-badge.is-cloud-up {
  background: color-mix(in srgb, var(--accent) 82%, rgb(15 19 32));
  color: #fff;
}
.cache-cloud-badge.is-err {
  background: color-mix(in srgb, var(--err) 86%, rgb(15 19 32));
  color: #fff;
}
.cache-cloud-badge.is-paused {
  background: rgb(15 19 32 / 0.72);
  color: rgba(255, 255, 255, 0.85);
}
.cache-poster-progress {
  position: absolute;
  left: 0;
  right: 0;
  bottom: 0;
  height: 4px;
  background: rgb(0 0 0 / 0.45);
  overflow: hidden;
}
.cache-poster-progress-fill {
  display: block;
  height: 100%;
  background: var(--accent-vivid);
  transition: width 0.25s ease;
}
.cache-poster-progress-fill.is-err {
  background: var(--err);
}
.cache-poster-progress-fill.is-paused {
  background: var(--muted);
}
.detail-top-nav {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  border-bottom: 1px solid var(--line);
  padding-bottom: 6px;
}
.detail-back-link {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  min-height: 32px;
  padding: 0 8px 0 4px;
  border-radius: 8px;
  color: var(--muted);
  font-size: 13px;
  font-weight: 550;
  transition: background 0.14s ease, color 0.14s ease;
}
.detail-back-link:hover {
  background: var(--panel);
  color: var(--ink);
}
.detail-hero {
  display: grid;
  grid-template-columns: 60px minmax(0, 1fr);
  align-items: center;
  gap: 12px;
}
@media (min-width: 640px) {
  .detail-hero {
    grid-template-columns: 68px minmax(0, 1fr);
  }
}
.detail-hero-poster {
  position: relative;
  display: grid;
  place-items: center;
  width: 100%;
  aspect-ratio: 2 / 3;
  overflow: hidden;
  border-radius: 10px;
  box-shadow: 0 2px 10px rgb(0 0 0 / 0.18);
}
</style>
