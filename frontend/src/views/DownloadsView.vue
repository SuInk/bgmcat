<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from "vue";
import type { DownloadJob, ExternalPlayerId, MikanDownloadItem, Subscription } from "@shared/index";
import { resolveDownloadArtworkUrl } from "../api";
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
import { formatBytes, formatSpeed, progressPercent } from "../lib/format";
import { selectTab } from "../stores/boot";
import { isBusyMatching } from "../stores/busy";
import {
  animeDetail,
  animeDetailError,
  closeAnimeDetail,
  homepageAnime,
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
import { subscriptions } from "../stores/subscriptions";
import {
  activeSharesCount,
  canShareSeriesGroup,
  openShareForDownload,
  openShareForSeriesGroup,
  openShareManager,
  refreshShares
} from "../stores/shares";
import { viewportWidth } from "../stores/ui";

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
 * 按 B 站「离线缓存」分两段：上面「正在缓存 / 转存」包含仍在下载、正在转存云盘或转存报错的番（带实时进度与重试/取消操作），
 * 下面「已缓存」是已全部就绪的番，排成封面墙，点进去看分集。
 */
const cachingGroups = computed(() => pagedDownloadGroups.value.filter(isGroupActiveOrTransferring));
const cachedGroups = computed(() => pagedDownloadGroups.value.filter((group) => !isGroupActiveOrTransferring(group)));
const activeSectionTitle = computed(() => {
  const hasDownloading = cachingGroups.value.some((group) => groupStatus(group) !== "complete");
  const hasTransferring = cachingGroups.value.some((group) => Boolean(groupCloudTransferSummary(group)));
  if (hasDownloading && hasTransferring) return "正在缓存 / 转存";
  if (hasTransferring) return "正在转存云盘";
  return "正在缓存";
});

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

onMounted(() => {
  void refreshShares(true);
  window.addEventListener("keydown", handleViewKeydown);
  void nextTick(() => {
    setupDownloadsLoadMoreObserver();
    maybeLoadMoreDownloads();
  });
});
onBeforeUnmount(() => {
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

    <div v-else class="downloads-layout" :class="{ 'has-side': detailPanelGroup && !fullPageDetail }">
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
            <span v-if="totalDownloadSpeed > 0 || totalUploadSpeed > 0 || totalCloudUploadSpeed > 0" class="toolbar-meta flex shrink-0 items-center gap-3 tabular-nums">
              <span v-if="totalDownloadSpeed > 0" class="inline-block min-w-[5.25rem] text-right text-[var(--ink)]">↓ {{ formatSpeed(totalDownloadSpeed) }}</span>
              <span v-if="totalUploadSpeed > 0" class="inline-block min-w-[5.25rem] text-right max-sm:hidden">↑ {{ formatSpeed(totalUploadSpeed) }}</span>
              <span
                v-if="totalCloudUploadSpeed > 0"
                class="inline-flex min-w-[5.75rem] items-center justify-end gap-1 text-right font-semibold text-[var(--accent-vivid)]"
                title="Google Drive 实时转存速率"
              >
                <AppNavIcon name="cloud-upload" class="size-3.5 shrink-0" aria-hidden="true" />
                <span>{{ formatSpeed(totalCloudUploadSpeed) }}</span>
              </span>
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

          <div v-else class="grid gap-6 py-2">
            <section v-if="cachingGroups.length" class="grid gap-2">
              <h3 class="cache-section-title">{{ activeSectionTitle }} <span class="tabular-nums">{{ cachingGroups.length }}</span></h3>
              <div class="grid gap-1">
                <div
                  v-for="group in cachingGroups"
                  :key="group.key"
                  class="download-card"
                  :class="{ 'is-open': activeDownloadGroupKey === group.key }"
                >
                  <article class="download-row" @click="onGroupActivate(group)">
                    <label v-if="selectMode" class="grid size-6 shrink-0 place-items-center" @click.stop>
                      <input
                        type="checkbox"
                        class="size-4 accent-[var(--accent)]"
                        :aria-label="`选择 ${group.title}`"
                        :checked="selectedGroupKeys.has(group.key)"
                        @change="setGroupSelected(group.key, ($event.target as HTMLInputElement).checked)"
                      />
                    </label>

                    <div class="download-poster" :style="coverStyle(group.primary.gid)">
                      <img
                        v-if="resolveGroupArtworkUrl(group)"
                        :src="resolveGroupArtworkUrl(group) ?? ''"
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
                        <span class="truncate">{{ formatGroupMetaLine(group) }}</span>
                      </p>
                      <div class="flex min-w-0 items-center gap-2.5">
                        <span class="progress-track w-36 shrink-0 sm:w-48">
                          <span
                            class="progress-fill"
                            :class="
                              group.primary.status === 'error' || isCloudFailed(group.primary)
                                ? '!bg-[var(--err)]'
                                : group.primary.status === 'paused'
                                  ? '!bg-[var(--muted)]'
                                  : ''
                            "
                            :style="{ width: `${groupProgressWidth(group)}%` }"
                          ></span>
                        </span>
                        <span
                          class="min-w-0 truncate text-xs tabular-nums"
                          :class="
                            group.primary.status === 'error' || isCloudFailed(group.primary)
                              ? 'text-[var(--err)]'
                              : isCloudTransferring(group.primary)
                                ? 'font-semibold text-[var(--accent-vivid)]'
                                : 'text-[var(--muted)]'
                          "
                        >
                          {{ groupProgressLine(group) }}
                        </span>
                      </div>
                      <p
                        v-if="downloadRawError(group.primary)"
                        class="truncate text-xs text-[var(--err)]"
                        :title="downloadRawError(group.primary) ?? undefined"
                      >
                        {{ downloadRawError(group.primary) }}
                      </p>
                      <p
                        v-if="group.primary.cloudArchive?.error"
                        class="truncate text-xs text-[var(--err)]"
                        :title="group.primary.cloudArchive.error ?? undefined"
                      >
                        {{ cloudArchiveLabel(group.primary) }} · {{ group.primary.cloudArchive.error }}
                      </p>
                      <p
                        v-else-if="group.primary.cloudArchive && groupStatus(group) !== 'complete'"
                        class="truncate text-xs text-[var(--muted)]"
                      >
                        {{ cloudArchiveLabel(group.primary) }}
                      </p>
                    </div>

                    <div v-if="!selectMode" class="flex shrink-0 items-center gap-1.5" @click.stop>
                      <button
                        v-if="canShareSeriesGroup(group)"
                        type="button"
                        class="icon-btn reveal-on-hover max-sm:hidden"
                        :title="`分享 ${group.title}`"
                        :aria-label="`分享 ${group.title}`"
                        @click="openShareForSeriesGroup(group)"
                      >
                        <AppNavIcon name="share" />
                      </button>
                      <button
                        type="button"
                        class="icon-btn reveal-on-hover text-[var(--err)] hover:text-[var(--err)] max-sm:hidden"
                        title="删除"
                        aria-label="删除"
                        @click="requestDelete(group.primary)"
                      >
                        <AppNavIcon name="trash" />
                      </button>
                      <button
                        v-if="isCloudFailed(group.primary)"
                        type="button"
                        class="icon-btn text-[var(--warn)] hover:text-[var(--accent-vivid)]"
                        title="立即重试云盘转存"
                        aria-label="立即重试云盘转存"
                        :disabled="isDownloadRowBusy(group.primary.gid)"
                        @click="triggerCloudArchive(group.primary.gid)"
                      >
                        <AppNavIcon name="refresh" />
                      </button>
                      <button
                        v-else-if="canArchiveToCloud(group.primary)"
                        type="button"
                        class="icon-btn reveal-on-hover text-[var(--accent-vivid)] max-sm:hidden"
                        :title="group.primary.status === 'complete' ? '转存至 Google Drive' : '完成后自动转存至 Google Drive'"
                        aria-label="转存至 Google Drive"
                        :disabled="isDownloadRowBusy(group.primary.gid)"
                        @click="triggerCloudArchive(group.primary.gid)"
                      >
                        <AppNavIcon name="cloud-upload" />
                      </button>
                      <button
                        v-if="isCloudTransferring(group.primary) || isCloudFailed(group.primary)"
                        type="button"
                        class="icon-btn text-[var(--muted)] hover:text-[var(--err)]"
                        title="取消云盘转存"
                        aria-label="取消云盘转存"
                        :disabled="isDownloadRowBusy(group.primary.gid)"
                        @click="cancelCloudArchive(group.primary.gid)"
                      >
                        <AppNavIcon name="close" />
                      </button>
                      <button
                        v-if="canPauseDownload(group.primary) || canResumeDownload(group.primary)"
                        type="button"
                        class="icon-btn"
                        :title="canResumeDownload(group.primary) ? '继续下载' : '暂停'"
                        :aria-label="canResumeDownload(group.primary) ? '继续下载' : '暂停'"
                        :disabled="isDownloadRowBusy(group.primary.gid)"
                        @click="updateDownloadState(canResumeDownload(group.primary) ? 'resume' : 'pause', group.primary.gid)"
                      >
                        <AppNavIcon :name="canResumeDownload(group.primary) ? 'download' : 'pause'" />
                      </button>
                      <button
                        v-if="group.playable"
                        type="button"
                        class="play-btn"
                        :title="`${group.playable.status === 'complete' ? '播放' : '边下边播'} ${group.playableEpisodeLabel ?? ''}`.trim()"
                        :aria-label="`${group.playable.status === 'complete' ? '播放' : '边下边播'} ${group.playableEpisodeLabel ?? ''}`.trim()"
                        :disabled="isDownloadRowBusy(group.playable.gid)"
                        @click="openPlayback(group.playable)"
                      >
                        <AppNavIcon name="play" class="size-3.5" />
                      </button>
                      <AppNavIcon
                        name="chevron-right"
                        class="size-4 shrink-0 text-[var(--muted)] transition-transform duration-200"
                        aria-hidden="true"
                      />
                    </div>
                  </article>
                </div>
              </div>
            </section>

            <section v-if="cachedGroups.length" class="grid gap-3">
              <h3 class="cache-section-title">已缓存 <span class="tabular-nums">{{ cachedGroups.length }}</span></h3>
              <div class="cache-grid">
                <div
                  v-for="group in cachedGroups"
                  :key="group.key"
                  class="cache-card"
                  :class="{ 'is-open': activeDownloadGroupKey === group.key, 'is-selected': selectMode && selectedGroupKeys.has(group.key) }"
                  role="button"
                  tabindex="0"
                  :aria-label="`${group.title}，${cachedMetaLine(group)}`"
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
                    <span v-if="!selectMode && groupCloudStoredCount(group) > 0" class="cache-cloud-badge">
                      <AppNavIcon name="cloud" class="size-3" aria-hidden="true" />
                      <span>云盘</span>
                    </span>
                    <span v-if="!selectMode" class="cache-top-actions">
                      <button
                        v-if="canShareSeriesGroup(group)"
                        type="button"
                        class="cache-cloud-btn"
                        :title="`分享 ${group.title}`"
                        :aria-label="`分享 ${group.title}`"
                        @click.stop="openShareForSeriesGroup(group)"
                      >
                        <AppNavIcon name="share" class="size-3.5" />
                      </button>
                      <button
                        v-if="groupArchivableGids(group).length > 0"
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
                      :title="`播放 ${group.playableEpisodeLabel ?? ''}`"
                      :aria-label="`播放 ${group.playableEpisodeLabel ?? ''}`"
                      @click.stop="openPlayback(group.playable)"
                    >
                      <AppNavIcon name="play" class="size-3.5" />
                    </button>
                  </div>
                  <strong class="mt-2 line-clamp-1 text-[13px] font-semibold text-[var(--ink)]" :title="group.title">{{ group.title }}</strong>
                  <span class="line-clamp-1 text-xs text-[var(--muted)]">{{ cachedMetaLine(group) }}</span>
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

      <aside v-if="detailPanelGroup" :key="detailPanelGroup.key" class="download-detail" :class="fullPageDetail ? 'motion-rise' : 'is-side motion-slide'">
        <button
          v-if="fullPageDetail"
          type="button"
          class="btn-ghost inline-flex min-h-[40px] w-fit items-center gap-1.5 px-3.5 py-2 text-sm font-semibold"
          @click="collapseDownloadGroup"
        >
          <AppNavIcon name="chevron-left" class="size-4" aria-hidden="true" />
          下载管理
        </button>
        <header class="flex min-w-0 items-center gap-3">
          <div class="download-poster !w-14" :style="coverStyle(detailPanelGroup.primary.gid)">
            <img
              v-if="resolveGroupArtworkUrl(detailPanelGroup)"
              :src="resolveGroupArtworkUrl(detailPanelGroup) ?? ''"
              alt=""
              class="absolute inset-0 size-full object-cover"
            />
            <div v-else class="absolute inset-0 grid place-items-center text-sm font-semibold text-white/90">{{ coverLabel(detailPanelGroup.title) }}</div>
          </div>
          <div class="min-w-0">
            <h2 class="line-clamp-2 text-[15px] font-semibold leading-snug text-[var(--ink)]" :title="detailPanelGroup.title">{{ detailPanelAnimeTitle }}</h2>
            <p class="mt-0.5 truncate text-xs text-[var(--muted)]">
              {{ formatGroupMetaLine(detailPanelGroup) }}
            </p>
          </div>
          <button
            v-if="!fullPageDetail"
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
          :show-overview="true"
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
</style>
