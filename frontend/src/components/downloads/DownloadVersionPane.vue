<script setup lang="ts">
import { computed, ref, watch } from "vue";

import type { AnimeDetail, DownloadConflictStrategy, DownloadJob } from "@shared/index";
import {
  canArchiveToCloud,
  canCancelCloudArchive,
  cloudArchiveLabel,
  cloudUploadProgress,
  isCloudFailed,
  isCloudStored,
  isCloudTransferring
} from "../../lib/cloudArchive";
import { extractResolutionLabel } from "../../lib/mikan";
import { computeDownloadsTotalProgress } from "../../lib/downloadGrouping";
import { formatSpeed } from "../../lib/format";
import AppNavIcon from "../layout/AppNavIcon.vue";
import { canStreamDownload } from "../../stores/streamPlay";

export interface DownloadEpisodeGroup {
  key: string;
  title: string;
  episodeLabel: string | null;
  downloads: DownloadJob[];
}

type DetailTab = "episodes" | "files" | "logs" | "history";

/**
 * 下载行的「就地展开」面板。
 *
 * 以前它是右侧常驻的并排详情栏，没选中条目时是空的却常年占掉约三分之一横向宽度。
 * 现在它长在被展开的那一行下面、拿整行全宽，所以内容按「表」排而不是竖着堆：
 * 集数 / 发布名 / 状态 / 进度 / 大小 / 操作 各占一列，发布名拿主要份额。
 */
const props = defineProps<{
  /** 手机上的独立详情页或右侧栏里用：不要缩进和左侧竖线，那是「挂在上面那一行下面」的视觉提示 */
  flat?: boolean;
  /** 是否显示顶部状态/速度/进度概览条（仅全屏详情页需要，右侧并排栏与左侧列表同屏无需重复） */
  showOverview?: boolean;
  downloads: DownloadJob[];
  episodes: DownloadEpisodeGroup[];
  animeDetail: AnimeDetail | null;
  /** animeDetail 是共享单例：只有它确实属于这一组时才敢拿来显示，否则会把上一行的简介贴到这一行 */
  detailBelongsToGroup: boolean;
  detailError: string | null;
  detailLoading: boolean;
  formatBytes: (value: number) => string;
  downloadDisplayTitle: (download: DownloadJob) => string;
  progressPercent: (download: DownloadJob) => number;
  downloadRawError: (download: DownloadJob) => string | null;
  isDownloadRowBusy: (gid: string) => boolean;
}>();

const emit = defineEmits<{
  (event: "conflict", payload: { gid: string; strategy: DownloadConflictStrategy }): void;
  (event: "playback", download: DownloadJob): void;
  (event: "pause", gid: string): void;
  (event: "resume", gid: string): void;
  (event: "remove", download: DownloadJob): void;
  (event: "archive", gid: string): void;
  (event: "cancel-archive", gid: string): void;
  (event: "archive-batch", gids: string[]): void;
  (event: "share-group"): void;
  (event: "share-episode", payload: { download: DownloadJob; episodeLabel: string | null }): void;
}>();

const activeTab = ref<DetailTab>("episodes");
/** 一集有多个版本时才需要再往下钻一层；同样只展开一个，展开区不至于无限长 */
const expandedEpisodeKey = ref<string | null>(null);

function rowPriority(item: DownloadJob): number {
  if (item.status === "complete") {
    if (isCloudTransferring(item)) return 0.5;
    if (isCloudFailed(item)) return 4;
  }
  const map: Record<DownloadJob["status"], number> = {
    active: 0,
    waiting: 1,
    queued: 2,
    paused: 3,
    error: 4,
    complete: 5,
    removed: 6
  };
  return map[item.status];
}

const primaryDownload = computed(() => primaryForDownloads(props.downloads));
const overviewSummary = computed(() => computeDownloadsTotalProgress(props.downloads));
const totalLength = computed(() =>
  overviewSummary.value.localTotalBytes > 0
    ? overviewSummary.value.localTotalBytes
    : overviewSummary.value.estimatedTotalBytes
);
const aggregateProgress = computed(() => overviewSummary.value.percent);
const aggregateProgressWidth = computed(() => overviewSummary.value.widthPercent);
const totalDownloadSpeed = computed(() => overviewSummary.value.downloadSpeed);
const totalUploadSpeed = computed(() => overviewSummary.value.uploadSpeed);
const totalCloudSpeed = computed(() => overviewSummary.value.cloudSpeed);
const remainingSeconds = computed(() => overviewSummary.value.etaSeconds);
const detailReady = computed(() => props.detailBelongsToGroup && !props.detailLoading);
const displaySummary = computed(() =>
  detailReady.value ? props.animeDetail?.summary || props.animeDetail?.intro || null : null
);
/** 全部下完且没有进行中/报错转存：概览只说「已完成 · 大小」 */
const allComplete = computed(
  () =>
    props.downloads.length > 0 &&
    overviewSummary.value.finishedTaskCount === props.downloads.length
);
const overviewStatusLabel = computed(() => {
  if (allComplete.value) {
    return overviewSummary.value.cloudStoredCount > 0 ? "已存云盘" : "已完成";
  }
  const hasActiveDl = props.downloads.some((d) => d.status === "active");
  const hasCloudActive = props.downloads.some(isCloudTransferring);
  if (hasActiveDl && hasCloudActive) {
    return "下载与转存中";
  }
  if (hasActiveDl) {
    return "下载中";
  }
  if (hasCloudActive) {
    return "转存云盘中";
  }
  if (props.downloads.some((d) => d.status === "error" || isCloudFailed(d))) {
    return "异常";
  }
  if (props.downloads.some((d) => d.status === "waiting" || d.status === "queued")) {
    return "等待中";
  }
  if (props.downloads.some((d) => d.status === "paused")) {
    return "暂停中";
  }
  return primaryDownload.value ? statusLabel(primaryDownload.value.status) : "等待中";
});
const overviewBytesLabel = computed(() => {
  const s = overviewSummary.value;
  const approx = s.hasUnresolvedSize ? "~" : "";
  if (s.hasCloudPipeline && s.cloudTotalBytes > 0) {
    if (!s.allLocalComplete) {
      return `下载 ${props.formatBytes(s.localCompletedBytes)} / ${approx}${props.formatBytes(s.estimatedTotalBytes)} · 云盘 ${props.formatBytes(s.cloudUploadedBytes)} / ${approx}${props.formatBytes(s.cloudTotalBytes)}`;
    }
    return `云盘 ${props.formatBytes(s.cloudUploadedBytes)} / ${props.formatBytes(s.cloudTotalBytes)}`;
  }
  if (s.estimatedTotalBytes > 0) {
    return `${props.formatBytes(s.localCompletedBytes)} / ${approx}${props.formatBytes(s.estimatedTotalBytes)}`;
  }
  return "";
});
const taskCompletionLabel = computed(() => {
  if (props.downloads.length <= 1) {
    return null;
  }
  return `已完成 ${overviewSummary.value.finishedTaskCount} / ${overviewSummary.value.totalTaskCount}`;
});
const resumableDownloads = computed(() => props.downloads.filter(canResume));
const pausableDownloads = computed(() => props.downloads.filter(canPause));
const archivableCompletedGids = computed(() =>
  props.downloads.filter((download) => download.status === "complete" && canArchiveToCloud(download)).map((d) => d.gid)
);
const shareableCount = computed(() =>
  props.downloads.filter((download) => download.canPlayExistingFile || download.status === "complete").length
);
const conflictDownloads = computed(() =>
  props.downloads.filter((download) => download.canResolveConflict && download.issueKind === "file-conflict")
);

const logEntries = computed(() =>
  props.downloads.flatMap((download) => {
    const message = download.cloudArchive?.error ?? props.downloadRawError(download) ?? download.issueMessage ?? download.errorMessage;
    if (!message) {
      return [];
    }
    return [
      {
        gid: download.gid,
        level: download.status === "error" || download.issueKind || isCloudFailed(download) ? "异常" : "提示",
        message,
        time: download.updatedAt
      }
    ];
  })
);
const historyEntries = computed(() =>
  props.downloads.flatMap((download) => [
    {
      key: `${download.gid}:created`,
      label: "创建任务",
      detail: props.downloadDisplayTitle(download),
      time: download.createdAt
    },
    {
      key: `${download.gid}:updated`,
      label: cloudArchiveLabel(download) || statusLabel(download.status),
      detail: `GID ${download.gid}`,
      time: download.updatedAt
    }
  ])
);
const detailTabs = computed(() => [
  { key: "episodes" as const, label: "分集", count: props.episodes.length },
  { key: "files" as const, label: "版本文件", count: props.downloads.length },
  { key: "logs" as const, label: "日志", count: logEntries.value.length },
  { key: "history" as const, label: "操作记录", count: historyEntries.value.length }
]);

const conflictStrategies: Array<{ value: DownloadConflictStrategy; label: string }> = [
  { value: "recheck", label: "重新校验" },
  { value: "overwrite", label: "覆盖续传" },
  { value: "delete-existing", label: "删除旧文件重下" }
];

// 换一组任务（轮询重建分组、或用户展开了别的行）时收起里层的分集展开
watch(
  () => props.episodes.map((episode) => episode.key).join("|"),
  () => {
    if (expandedEpisodeKey.value && !props.episodes.some((episode) => episode.key === expandedEpisodeKey.value)) {
      expandedEpisodeKey.value = null;
    }
  }
);

/** 「第 04 集」→「04」放进左边的集号方块；没有数字的（SP、合集）给个短标 */
function episodeIndex(label: string | null | undefined): string {
  return label?.match(/\d+(?:\.\d+)?/)?.[0] ?? "SP";
}

/** 点一集：多个版本先展开挑版本，只有一个版本且能播就直接播 */
function openEpisode(episode: DownloadEpisodeGroup): void {
  if (episode.downloads.length > 1) {
    toggleEpisode(episode.key);
    return;
  }
  const target = playTargetFor(episode.downloads);
  if (target) {
    emit("playback", target);
  }
}

function toggleEpisode(key: string): void {
  expandedEpisodeKey.value = expandedEpisodeKey.value === key ? null : key;
}

function statusLabel(status: DownloadJob["status"]): string {
  switch (status) {
    case "active":
      return "进行中";
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

function rowStatusPillClass(download: DownloadJob): string {
  if (isCloudTransferring(download)) {
    return "border-[var(--accent-line)] bg-[var(--accent-soft)] text-[var(--accent-vivid)]";
  }
  if (isCloudFailed(download)) {
    return "border-[var(--err-soft)] bg-[var(--err-soft)] text-[var(--err)]";
  }
  switch (download.status) {
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

/** 已完成且未在转存时用绿色；转存报错用红色；其余进行中进度一律走主色 */
function progressBarClass(downloads: DownloadJob[]): string {
  if (downloads.some((d) => d.status === "error" || isCloudFailed(d))) {
    return "bg-[var(--err)]";
  }
  if (downloads.some(isCloudTransferring)) {
    return "bg-[var(--accent-vivid)]";
  }
  return downloads.every((download) => download.status === "complete")
    ? "bg-[var(--ok)]"
    : "bg-[var(--accent-vivid)]";
}

function rowEffectivePercent(download: DownloadJob): number {
  if (download.status === "complete" && (isCloudTransferring(download) || isCloudFailed(download))) {
    return cloudUploadProgress(download).percent;
  }
  return props.progressPercent(download);
}

function rowEffectiveWidth(download: DownloadJob): number {
  if (download.status === "complete" && (isCloudTransferring(download) || isCloudFailed(download))) {
    const prog = cloudUploadProgress(download);
    return prog.total > 0 ? Math.max(0, Math.min(100, Math.round((prog.uploaded / prog.total) * 1000) / 10)) : 0;
  }
  if (!download.totalLength) {
    return 0;
  }
  return Math.max(0, Math.min(100, Math.round((download.completedLength / download.totalLength) * 1000) / 10));
}

function primaryForDownloads(downloads: DownloadJob[]): DownloadJob | undefined {
  let best: DownloadJob | undefined;
  for (const item of downloads) {
    if (
      !best ||
      rowPriority(item) < rowPriority(best) ||
      (rowPriority(item) === rowPriority(best) && item.updatedAt > best.updatedAt)
    ) {
      best = item;
    }
  }
  return best;
}

/** 一集有多个版本时，代表任务可能是正在下的那个；播放要挑真正能播的版本 */
function playableForDownloads(downloads: DownloadJob[]): DownloadJob | undefined {
  return downloads.find(canPlay);
}

function episodeCloudTarget(downloads: DownloadJob[]): DownloadJob | undefined {
  return downloads.find((d) => isCloudTransferring(d) || isCloudFailed(d));
}

function episodeRowPercent(downloads: DownloadJob[]): number {
  const cloudTarget = episodeCloudTarget(downloads);
  if (episodeComplete(downloads) && cloudTarget) {
    return cloudUploadProgress(cloudTarget).percent;
  }
  const total = downloads.reduce((sum, d) => sum + d.totalLength, 0);
  const completed = downloads.reduce((sum, d) => sum + d.completedLength, 0);
  if (total > 0) {
    return completed >= total ? 99 : Math.max(0, Math.min(99, Math.floor((completed / total) * 100)));
  }
  return Math.round(downloads.reduce((sum, d) => sum + props.progressPercent(d), 0) / Math.max(1, downloads.length));
}

function episodeRowWidth(downloads: DownloadJob[]): number {
  const cloudTarget = episodeCloudTarget(downloads);
  if (episodeComplete(downloads) && cloudTarget) {
    return rowEffectiveWidth(cloudTarget);
  }
  const total = downloads.reduce((sum, d) => sum + d.totalLength, 0);
  const completed = downloads.reduce((sum, d) => sum + d.completedLength, 0);
  if (total > 0) {
    return Math.max(0, Math.min(100, Math.round((completed / total) * 1000) / 10));
  }
  return episodeRowPercent(downloads);
}

function sizeForDownloads(downloads: DownloadJob[]): string {
  const total = downloads.reduce((sum, download) => sum + download.totalLength, 0);
  const completed = downloads.reduce((sum, download) => sum + download.completedLength, 0);
  return total > 0 ? `${props.formatBytes(completed)} / ${props.formatBytes(total)}` : props.formatBytes(completed);
}

/** 窄屏分集行第二行：字幕组 · 分辨率 · 大小 · 云盘归档状态 */
function releaseMeta(downloads: DownloadJob[]): string {
  const name = releaseName(downloads);
  const subgroup = name.match(/^\s*[[【]([^\]】]+)[\]】]/)?.[1]?.trim();
  const size = episodeComplete(downloads)
    ? props.formatBytes(downloads.reduce((sum, download) => sum + download.totalLength, 0))
    : sizeForDownloads(downloads);
  const primary = primaryForDownloads(downloads);
  const cloudNote =
    primary && isCloudStored(primary)
      ? primary.cloudArchive?.status === "archived"
        ? "已存云盘"
        : "云端已校验"
      : primary && primary.saveTo === "cloud" && !primary.cloudArchive && primary.status !== "complete"
        ? "完成后存云盘"
        : null;
  return [subgroup, extractResolutionLabel(name), size, cloudNote].filter(Boolean).join(" · ");
}

function episodeComplete(downloads: DownloadJob[]): boolean {
  return downloads.length > 0 && downloads.every((download) => download.status === "complete");
}

function releaseName(downloads: DownloadJob[]): string {
  const target = primaryForDownloads(downloads);
  return target ? props.downloadDisplayTitle(target) : "--";
}

function canPause(download: DownloadJob): boolean {
  return ["active", "waiting", "queued"].includes(download.status);
}

function canResume(download: DownloadJob): boolean {
  return ["paused", "error"].includes(download.status);
}

function canPlay(download: DownloadJob): boolean {
  return download.canPlayExistingFile || download.status === "complete" || canStreamDownload(download);
}

function playLabel(download: DownloadJob): string {
  return download.canPlayExistingFile || download.status === "complete" ? "播放" : "边下边播";
}

/** 一集里可以点播放的任务：先挑能直接播的，否则挑能边下边播的主任务（排队/暂停中也行）。 */
function playTargetFor(downloads: DownloadJob[]): DownloadJob | null {
  const playable = playableForDownloads(downloads);
  if (playable) return playable;
  const primary = primaryForDownloads(downloads);
  return primary && canStreamDownload(primary) ? primary : null;
}

function triggerTransfer(download: DownloadJob): void {
  if (canResume(download)) {
    emit("resume", download.gid);
    return;
  }
  if (canPause(download)) {
    emit("pause", download.gid);
  }
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

const dateTimeFormatter = new Intl.DateTimeFormat("zh-CN", {
  month: "2-digit",
  day: "2-digit",
  hour: "2-digit",
  minute: "2-digit"
});

function formatDateTime(value: string): string {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) {
    return value;
  }
  return dateTimeFormatter.format(date).replace(/\//g, "-");
}
</script>

<template>
  <!--
    左边那条竖线是展开区和它所属行的唯一视觉联系——行本身在上面，中间没有别的框，
    缩进 + 竖线一起把「这些分集属于上面那一行」说清楚。
  -->
  <section
    class="@container grid gap-2.5"
    :class="props.flat ? '' : 'motion-drop px-2 pb-3 @min-[640px]:pl-[4.75rem]'"
  >
    <div class="grid gap-2">
      <!-- 概览条：状态、总进度、分阶段字节统计、批量操作全部横排 -->
      <div v-if="primaryDownload && (props.showOverview ?? props.flat)" class="flex flex-wrap items-center gap-x-3 gap-y-2">
        <span class="shrink-0 rounded-full border px-2.5 py-0.5 text-xs font-semibold" :class="rowStatusPillClass(primaryDownload)">
          {{ overviewStatusLabel }}
        </span>
        <span v-if="allComplete" class="shrink-0 text-xs font-semibold tabular-nums text-[var(--muted)]">{{ props.formatBytes(totalLength) }}</span>
        <template v-else>
          <span class="progress-track w-32 shrink-0 sm:w-44">
            <span
              class="progress-fill"
              :class="progressBarClass(downloads)"
              :style="{ width: `${aggregateProgressWidth}%` }"
            ></span>
          </span>
          <span class="shrink-0 text-xs font-semibold tabular-nums text-[var(--ink)]">总进度 {{ aggregateProgress }}%</span>
          <span v-if="overviewBytesLabel" class="shrink-0 text-xs font-semibold tabular-nums text-[var(--muted)]">
            {{ overviewBytesLabel }}
          </span>
          <span v-if="totalDownloadSpeed > 0" class="shrink-0 text-xs font-semibold tabular-nums text-[var(--muted)]">↓ {{ formatSpeed(totalDownloadSpeed) }}</span>
          <span v-if="totalUploadSpeed > 0" class="shrink-0 text-xs font-semibold tabular-nums text-[var(--muted)]">↑ {{ formatSpeed(totalUploadSpeed) }}</span>
          <span v-if="totalCloudSpeed > 0" class="shrink-0 text-xs font-semibold tabular-nums text-[var(--accent-vivid)]">☁↑ {{ formatSpeed(totalCloudSpeed) }}</span>
          <span v-if="remainingSeconds" class="shrink-0 text-xs font-semibold tabular-nums text-[var(--muted)]">剩余 {{ formatDuration(remainingSeconds) }}</span>
        </template>
        <span v-if="taskCompletionLabel" class="shrink-0 text-xs font-semibold tabular-nums text-[var(--muted)]">{{ taskCompletionLabel }}</span>
        <span v-if="detailLoading" class="shrink-0 text-xs font-semibold text-[var(--muted)]">加载详情…</span>

        <span class="ml-auto flex shrink-0 flex-wrap gap-1.5">
          <button
            v-if="resumableDownloads.length"
            type="button"
            class="btn-ghost h-8 rounded-md px-3 text-xs font-semibold"
            @click="resumableDownloads.forEach((download) => emit('resume', download.gid))"
          >
            全部开始（{{ resumableDownloads.length }}）
          </button>
          <button
            v-if="pausableDownloads.length"
            type="button"
            class="btn-ghost h-8 rounded-md px-3 text-xs font-semibold"
            @click="pausableDownloads.forEach((download) => emit('pause', download.gid))"
          >
            全部暂停（{{ pausableDownloads.length }}）
          </button>
        </span>
      </div>

      <!-- 简介只留一行；要看全番信息走番剧页 -->
      <p v-if="displaySummary && props.flat" class="line-clamp-1 text-xs leading-5 text-[var(--muted)]" :title="displaySummary">
        {{ displaySummary }}
      </p>

      <!-- 文件冲突就地给出三种处理方式 -->
      <div
        v-for="download in conflictDownloads"
        :key="`conflict:${download.gid}`"
        class="flex flex-wrap items-center gap-2 rounded-md border border-[var(--warn-soft)] bg-[var(--warn-soft)] px-3 py-2 text-xs font-semibold text-[var(--warn)]"
      >
        <span class="min-w-0 flex-1 truncate">{{ download.issueMessage ?? "本地已存在同名文件" }}</span>
        <button
          v-for="strategy in conflictStrategies"
          :key="strategy.value"
          type="button"
          class="h-7 shrink-0 rounded-md border border-[var(--warn)] px-2.5 text-[11px] font-semibold text-[var(--warn)] transition hover:bg-[var(--card)]"
          :disabled="props.isDownloadRowBusy(download.gid)"
          @click="emit('conflict', { gid: download.gid, strategy: strategy.value })"
        >
          {{ strategy.label }}
        </button>
      </div>

      <!-- 分集标签栏 + 一键全部转存快捷入口 -->
      <div class="detail-tabs-wrap">
        <nav class="detail-tabs" aria-label="任务详情">
          <button
            v-for="tab in detailTabs"
            :key="tab.key"
            type="button"
            class="detail-tab"
            :class="{ 'is-active': activeTab === tab.key }"
            :aria-pressed="activeTab === tab.key"
            @click="activeTab = tab.key"
          >
            <span>{{ tab.label }}</span>
            <span class="tabular-nums opacity-70">{{ tab.count }}</span>
          </button>
        </nav>

        <div class="flex shrink-0 items-center gap-1.5">
          <button
            v-if="shareableCount > 0"
            type="button"
            class="cloud-batch-btn"
            title="生成在线播放分享链接"
            @click="emit('share-group')"
          >
            <AppNavIcon name="share" class="size-3.5" aria-hidden="true" />
            <span>{{ episodes.length > 1 ? "分享整部" : "分享" }}</span>
          </button>
          <button
            v-if="archivableCompletedGids.length > 1"
            type="button"
            class="cloud-batch-btn"
            title="将本番所有已完成本地分集并发转存至 Google Drive"
            @click="emit('archive-batch', archivableCompletedGids)"
          >
            <AppNavIcon name="cloud-upload" class="size-3.5" aria-hidden="true" />
            <span>全部转存 ({{ archivableCompletedGids.length }})</span>
          </button>
        </div>
      </div>

      <template v-if="activeTab === 'episodes'">
        <div v-for="episode in episodes" :key="episode.key" class="grid">
          <div
            class="episode-row"
            :class="{
              'is-open': expandedEpisodeKey === episode.key,
              'cursor-pointer': episode.downloads.length > 1 || playableForDownloads(episode.downloads)
            }"
            :title="releaseName(episode.downloads)"
            @click="openEpisode(episode)"
          >
            <span class="episode-index tabular-nums" :class="{ 'is-done': episodeComplete(episode.downloads) }">
              {{ episodeIndex(episode.episodeLabel) }}
            </span>

            <span class="grid min-w-0 gap-0.5">
              <span class="flex min-w-0 items-center gap-1.5 text-[13px] font-semibold text-[var(--ink)]">
                <span class="truncate">{{ episode.episodeLabel ?? "未知集数" }}</span>
                <span
                  v-if="episode.downloads.some(isCloudStored)"
                  class="inline-flex shrink-0 items-center gap-0.5 rounded-full border border-[var(--accent-line)] bg-[var(--accent-soft)] px-1.5 py-0.5 text-[10px] font-semibold text-[var(--accent-vivid)]"
                >
                  <AppNavIcon name="cloud" class="size-2.5" aria-hidden="true" />
                  云盘
                </span>
                <span v-if="episode.downloads.length > 1" class="shrink-0 text-[11px] font-medium text-[var(--muted)]">
                  {{ episode.downloads.length }} 个版本
                  <span class="inline-block transition-transform" :class="expandedEpisodeKey === episode.key ? 'rotate-90' : ''" aria-hidden="true">›</span>
                </span>
              </span>
              <span class="truncate text-[11px] tabular-nums text-[var(--muted)]">{{ releaseMeta(episode.downloads) }}</span>

              <!-- 下载中或转存云盘中都显示实时进度条 -->
              <span v-if="!episodeComplete(episode.downloads) || episodeCloudTarget(episode.downloads)" class="mt-1 flex min-w-0 items-center gap-2.5">
                <span class="progress-track w-28 shrink-0 sm:w-36">
                  <span
                    class="progress-fill"
                    :class="progressBarClass(episode.downloads)"
                    :style="{ width: `${episodeRowWidth(episode.downloads)}%` }"
                  ></span>
                </span>
                <span
                  class="min-w-0 truncate text-[11px] tabular-nums"
                  :class="episode.downloads.some(isCloudFailed) ? 'text-[var(--err)]' : 'text-[var(--muted)]'"
                >
                  <template v-if="episodeComplete(episode.downloads) && episodeCloudTarget(episode.downloads)">
                    {{ cloudArchiveLabel(episodeCloudTarget(episode.downloads)!) }}
                  </template>
                  <template v-else>
                    {{ statusLabel(primaryForDownloads(episode.downloads)?.status ?? "waiting") }} · {{ episodeRowPercent(episode.downloads) }}%
                  </template>
                </span>
              </span>
              <span
                v-if="episodeCloudTarget(episode.downloads)?.cloudArchive?.error"
                class="truncate text-[11px] text-[var(--err)]"
                :title="episodeCloudTarget(episode.downloads)?.cloudArchive?.error ?? undefined"
              >
                {{ episodeCloudTarget(episode.downloads)?.cloudArchive?.error }}
              </span>
            </span>

            <span class="flex shrink-0 items-center gap-1" @click.stop>
              <button
                v-if="playableForDownloads(episode.downloads)"
                type="button"
                class="icon-btn reveal-on-hover"
                :title="`分享 ${episode.episodeLabel ?? '该集'}`"
                :aria-label="`分享 ${episode.episodeLabel ?? '该集'}`"
                @click="emit('share-episode', { download: playableForDownloads(episode.downloads)!, episodeLabel: episode.episodeLabel })"
              >
                <AppNavIcon name="share" />
              </button>
              <button
                v-if="primaryForDownloads(episode.downloads)"
                type="button"
                class="icon-btn reveal-on-hover text-[var(--err)] hover:text-[var(--err)]"
                title="删除该集"
                aria-label="删除该集"
                @click="emit('remove', primaryForDownloads(episode.downloads)!)"
              >
                <AppNavIcon name="trash" />
              </button>

              <!-- 云盘转存操作：重试 / 取消 / 一键转存 -->
              <template v-if="primaryForDownloads(episode.downloads)">
                <button
                  v-if="isCloudFailed(primaryForDownloads(episode.downloads)!)"
                  type="button"
                  class="icon-btn text-[var(--warn)] hover:text-[var(--accent-vivid)]"
                  title="立即重试云盘转存"
                  aria-label="立即重试云盘转存"
                  :disabled="props.isDownloadRowBusy(primaryForDownloads(episode.downloads)!.gid)"
                  @click="emit('archive', primaryForDownloads(episode.downloads)!.gid)"
                >
                  <AppNavIcon name="refresh" />
                </button>
                <button
                  v-else-if="canArchiveToCloud(primaryForDownloads(episode.downloads)!)"
                  type="button"
                  class="icon-btn reveal-on-hover text-[var(--accent-vivid)]"
                  :title="primaryForDownloads(episode.downloads)!.status === 'complete' ? '转存至 Google Drive' : '完成后自动转存至 Google Drive'"
                  aria-label="转存至 Google Drive"
                  :disabled="props.isDownloadRowBusy(primaryForDownloads(episode.downloads)!.gid)"
                  @click="emit('archive', primaryForDownloads(episode.downloads)!.gid)"
                >
                  <AppNavIcon name="cloud-upload" />
                </button>

                <button
                  v-if="isCloudTransferring(primaryForDownloads(episode.downloads)!) || isCloudFailed(primaryForDownloads(episode.downloads)!)"
                  type="button"
                  class="icon-btn text-[var(--muted)] hover:text-[var(--err)]"
                  title="取消云盘转存"
                  aria-label="取消云盘转存"
                  :disabled="props.isDownloadRowBusy(primaryForDownloads(episode.downloads)!.gid)"
                  @click="emit('cancel-archive', primaryForDownloads(episode.downloads)!.gid)"
                >
                  <AppNavIcon name="close" />
                </button>
              </template>

              <button
                v-if="primaryForDownloads(episode.downloads) && (canPause(primaryForDownloads(episode.downloads)!) || canResume(primaryForDownloads(episode.downloads)!))"
                type="button"
                class="icon-btn"
                :title="canResume(primaryForDownloads(episode.downloads)!) ? '继续' : '暂停'"
                :aria-label="canResume(primaryForDownloads(episode.downloads)!) ? '继续' : '暂停'"
                :disabled="props.isDownloadRowBusy(primaryForDownloads(episode.downloads)!.gid)"
                @click="triggerTransfer(primaryForDownloads(episode.downloads)!)"
              >
                <AppNavIcon :name="canResume(primaryForDownloads(episode.downloads)!) ? 'download' : 'pause'" />
              </button>
              <button
                v-if="playTargetFor(episode.downloads)"
                type="button"
                class="play-btn"
                :title="`${playLabel(playTargetFor(episode.downloads)!)} ${episode.episodeLabel ?? '该集'}`"
                :aria-label="`${playLabel(playTargetFor(episode.downloads)!)} ${episode.episodeLabel ?? '该集'}`"
                @click="emit('playback', playTargetFor(episode.downloads)!)"
              >
                <AppNavIcon name="play" class="size-3.5" />
              </button>
            </span>
          </div>

          <!-- 同一集的多个版本：再缩进一层，标题仍旧占主列 -->
          <div v-if="expandedEpisodeKey === episode.key && episode.downloads.length > 1" class="grid gap-1 py-1 pl-4">
            <div
              v-for="download in episode.downloads"
              :key="download.gid"
              class="grid grid-cols-[minmax(0,1fr)_auto] items-center gap-x-3 gap-y-1 rounded-md border-l-2 border-[var(--line)] px-2 py-1.5 hover:bg-[var(--tint-2)] @min-[820px]:grid-cols-[minmax(10rem,1fr)_6.5rem_minmax(7rem,9rem)_7.5rem_auto]"
            >
              <span class="min-w-0 truncate text-xs text-[var(--muted)]" :title="props.downloadDisplayTitle(download)">
                {{ props.downloadDisplayTitle(download) }}
              </span>
              <span
                class="hidden w-fit max-w-full shrink-0 truncate rounded-md border px-2 py-0.5 text-[11px] font-semibold @min-[820px]:block"
                :class="rowStatusPillClass(download)"
              >
                {{ cloudArchiveLabel(download) || statusLabel(download.status) }}
              </span>
              <span class="col-span-2 flex min-w-0 items-center gap-2 @min-[820px]:col-span-1">
                <span class="h-1.5 min-w-0 flex-1 overflow-hidden rounded-full bg-[var(--tint-4)]">
                  <span
                    class="block h-full rounded-full transition-[width] duration-300 ease-linear"
                    :class="progressBarClass([download])"
                    :style="{ width: `${rowEffectiveWidth(download)}%` }"
                  ></span>
                </span>
                <span class="w-9 shrink-0 text-right text-[11px] font-semibold tabular-nums text-[var(--muted)]">
                  {{ rowEffectivePercent(download) }}%
                </span>
              </span>
              <span class="hidden truncate text-right text-[11px] font-semibold tabular-nums text-[var(--muted)] @min-[820px]:block">
                {{ props.formatBytes(download.completedLength) }} / {{ props.formatBytes(download.totalLength) }}
              </span>
              <span class="flex shrink-0 justify-end gap-1">
                <button
                  v-if="canArchiveToCloud(download)"
                  type="button"
                  class="icon-btn text-[var(--accent-vivid)]"
                  :title="isCloudFailed(download) ? '重试转存' : '转存至 Google Drive'"
                  :aria-label="isCloudFailed(download) ? '重试转存' : '转存至 Google Drive'"
                  :disabled="props.isDownloadRowBusy(download.gid)"
                  @click="emit('archive', download.gid)"
                >
                  <AppNavIcon :name="isCloudFailed(download) ? 'refresh' : 'cloud-upload'" />
                </button>
                <button
                  v-if="canCancelCloudArchive(download) && (isCloudTransferring(download) || isCloudFailed(download))"
                  type="button"
                  class="icon-btn"
                  title="取消转存"
                  aria-label="取消转存"
                  :disabled="props.isDownloadRowBusy(download.gid)"
                  @click="emit('cancel-archive', download.gid)"
                >
                  <AppNavIcon name="close" />
                </button>
                <button
                  type="button"
                  class="icon-btn"
                  :title="playLabel(download)"
                  :aria-label="playLabel(download)"
                  :disabled="!canPlay(download)"
                  @click="emit('playback', download)"
                >
                  <AppNavIcon name="play" />
                </button>
                <button
                  type="button"
                  class="icon-btn"
                  :title="canResume(download) ? '继续' : '暂停'"
                  :aria-label="canResume(download) ? '继续' : '暂停'"
                  :disabled="(!canPause(download) && !canResume(download)) || props.isDownloadRowBusy(download.gid)"
                  @click="triggerTransfer(download)"
                >
                  <AppNavIcon :name="canResume(download) ? 'download' : 'pause'" />
                </button>
                <button
                  type="button"
                  class="icon-btn text-[var(--err)] hover:text-[var(--err)]"
                  title="删除该版本"
                  aria-label="删除该版本"
                  @click="emit('remove', download)"
                >
                  <AppNavIcon name="trash" />
                </button>
              </span>
            </div>
          </div>
        </div>
      </template>

      <template v-else-if="activeTab === 'files'">
        <div
          class="hidden grid-cols-[minmax(10rem,1fr)_6.5rem_minmax(7rem,9rem)_7.5rem_9rem_auto] items-center gap-3 border-b border-[var(--line)] px-2 pb-1.5 text-[11px] font-semibold text-[var(--muted)] @min-[820px]:grid"
        >
          <span>文件 / 发布名</span>
          <span>状态</span>
          <span>进度</span>
          <span class="text-right">大小</span>
          <span>GID</span>
          <span class="text-right">操作</span>
        </div>
        <div
          v-for="download in downloads"
          :key="download.gid"
          class="grid grid-cols-[minmax(0,1fr)_auto] items-center gap-x-3 gap-y-1 rounded-md px-2 py-1.5 transition hover:bg-[var(--tint-2)] @min-[820px]:grid-cols-[minmax(10rem,1fr)_6.5rem_minmax(7rem,9rem)_7.5rem_9rem_auto]"
        >
          <span class="min-w-0 truncate text-xs font-semibold text-[var(--ink)]" :title="props.downloadDisplayTitle(download)">
            {{ props.downloadDisplayTitle(download) }}
          </span>
          <span
            class="hidden w-fit max-w-full shrink-0 truncate rounded-md border px-2 py-0.5 text-[11px] font-semibold @min-[820px]:block"
            :class="rowStatusPillClass(download)"
          >
            {{ cloudArchiveLabel(download) || statusLabel(download.status) }}
          </span>
          <span class="col-span-2 flex min-w-0 items-center gap-2 @min-[820px]:col-span-1">
            <span class="h-1.5 min-w-0 flex-1 overflow-hidden rounded-full bg-[var(--tint-4)]">
              <span
                class="block h-full rounded-full transition-[width] duration-300 ease-linear"
                :class="progressBarClass([download])"
                :style="{ width: `${rowEffectiveWidth(download)}%` }"
              ></span>
            </span>
            <span class="w-9 shrink-0 text-right text-[11px] font-semibold tabular-nums text-[var(--muted)]">
              {{ rowEffectivePercent(download) }}%
            </span>
          </span>
          <span class="hidden truncate text-right text-[11px] font-semibold tabular-nums text-[var(--muted)] @min-[820px]:block">
            {{ props.formatBytes(download.completedLength) }} / {{ props.formatBytes(download.totalLength) }}
          </span>
          <span class="hidden truncate text-[11px] font-semibold text-[var(--muted)] @min-[820px]:block">{{ download.gid }}</span>
          <span class="flex shrink-0 justify-end gap-1">
            <button
              v-if="canArchiveToCloud(download)"
              type="button"
              class="icon-btn text-[var(--accent-vivid)]"
              :title="isCloudFailed(download) ? '重试转存' : '转存至 Google Drive'"
              :aria-label="isCloudFailed(download) ? '重试转存' : '转存至 Google Drive'"
              :disabled="props.isDownloadRowBusy(download.gid)"
              @click="emit('archive', download.gid)"
            >
              <AppNavIcon :name="isCloudFailed(download) ? 'refresh' : 'cloud-upload'" />
            </button>
            <button
              v-if="canCancelCloudArchive(download) && (isCloudTransferring(download) || isCloudFailed(download))"
              type="button"
              class="icon-btn"
              title="取消转存"
              aria-label="取消转存"
              :disabled="props.isDownloadRowBusy(download.gid)"
              @click="emit('cancel-archive', download.gid)"
            >
              <AppNavIcon name="close" />
            </button>
            <button type="button" class="icon-btn" :title="playLabel(download)" :aria-label="playLabel(download)" :disabled="!canPlay(download)" @click="emit('playback', download)">
              <AppNavIcon name="play" />
            </button>
            <button
              type="button"
              class="icon-btn"
              :title="canResume(download) ? '继续' : '暂停'"
              :aria-label="canResume(download) ? '继续' : '暂停'"
              :disabled="(!canPause(download) && !canResume(download)) || props.isDownloadRowBusy(download.gid)"
              @click="triggerTransfer(download)"
            >
              <AppNavIcon :name="canResume(download) ? 'download' : 'pause'" />
            </button>
            <button
              type="button"
              class="icon-btn text-[var(--err)] hover:text-[var(--err)]"
              title="删除"
              aria-label="删除"
              @click="emit('remove', download)"
            >
              <AppNavIcon name="trash" />
            </button>
          </span>
        </div>
      </template>

      <template v-else-if="activeTab === 'logs'">
        <div
          v-if="detailError"
          class="rounded-md border border-[var(--warn-soft)] bg-[var(--warn-soft)] px-3 py-2 text-xs font-semibold leading-5 text-[var(--warn)]"
        >
          {{ detailError }}
        </div>
        <p v-if="!logEntries.length && !detailError" class="px-2 py-4 text-xs font-semibold text-[var(--muted)]">暂无日志。</p>
        <div
          v-for="entry in logEntries"
          :key="`${entry.gid}:${entry.time}:${entry.message}`"
          class="grid grid-cols-[minmax(0,1fr)_auto] items-baseline gap-x-3 gap-y-1 rounded-md px-2 py-1.5 @min-[820px]:grid-cols-[4rem_minmax(0,1fr)_11rem_7rem]"
        >
          <span
            class="shrink-0 text-[11px] font-semibold"
            :class="entry.level === '异常' ? 'text-[var(--err)]' : 'text-[var(--warn)]'"
          >
            {{ entry.level }}
          </span>
          <span class="min-w-0 text-xs leading-5 text-[var(--ink)]">{{ entry.message }}</span>
          <span class="hidden truncate text-[11px] font-semibold text-[var(--muted)] @min-[820px]:block">GID {{ entry.gid }}</span>
          <span class="shrink-0 text-right text-[11px] font-semibold tabular-nums text-[var(--muted)]">{{ formatDateTime(entry.time) }}</span>
        </div>
      </template>

      <template v-else>
        <div
          v-for="entry in historyEntries"
          :key="entry.key"
          class="grid grid-cols-[minmax(0,1fr)_auto] items-baseline gap-x-3 gap-y-1 rounded-md px-2 py-1.5 @min-[820px]:grid-cols-[7.5rem_minmax(0,1fr)_7rem]"
        >
          <strong class="shrink-0 truncate text-[11px] font-semibold text-[var(--ink)]">{{ entry.label }}</strong>
          <span class="min-w-0 truncate text-xs text-[var(--muted)]" :title="entry.detail">{{ entry.detail }}</span>
          <span class="shrink-0 text-right text-[11px] font-semibold tabular-nums text-[var(--muted)]">{{ formatDateTime(entry.time) }}</span>
        </div>
      </template>
    </div>
  </section>
</template>

<style scoped>
.detail-tabs-wrap {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
  border-bottom: 1px solid var(--line);
}
.detail-tabs {
  display: flex;
  min-width: 0;
  gap: 18px;
  overflow-x: auto;
  scrollbar-width: none;
}
.detail-tabs::-webkit-scrollbar {
  display: none;
}
.detail-tab {
  display: inline-flex;
  flex-shrink: 0;
  align-items: center;
  gap: 5px;
  margin-bottom: -1px;
  padding: 6px 0 7px;
  border-bottom: 2px solid transparent;
  color: var(--muted);
  font-size: 12px;
  font-weight: 600;
  white-space: nowrap;
  transition: color 0.15s, border-color 0.15s;
}
.detail-tab:hover {
  color: var(--ink);
}
.detail-tab.is-active {
  border-color: var(--accent);
  color: var(--ink);
}

.cloud-batch-btn {
  display: inline-flex;
  flex-shrink: 0;
  align-items: center;
  gap: 4px;
  height: 32px;
  margin-bottom: 3px;
  padding: 0 11px;
  border-radius: 999px;
  border: 1px solid var(--accent-line);
  background: var(--accent-soft);
  color: var(--accent-vivid);
  font-size: 11px;
  font-weight: 600;
  white-space: nowrap;
  transition: background 0.15s, border-color 0.15s, color 0.15s;
}
.cloud-batch-btn:hover {
  border-color: var(--accent);
  background: var(--accent);
  color: var(--accent-contrast);
}

.episode-row {
  display: grid;
  grid-template-columns: auto minmax(0, 1fr) auto;
  align-items: center;
  gap: 12px;
  padding: 8px 10px;
  border-radius: 12px;
  transition: background 0.15s;
}
.episode-row:hover,
.episode-row.is-open {
  background: var(--tint-2);
}
/* 集号方块：替代视频站分集缩略图的位置，下完的变成主题色 */
.episode-index {
  display: grid;
  place-items: center;
  width: 40px;
  height: 40px;
  border-radius: 12px;
  background: var(--panel);
  color: var(--ink-secondary);
  font-size: 14px;
  font-weight: 700;
}
.episode-index.is-done {
  background: var(--accent-soft);
  color: var(--accent);
}
</style>
