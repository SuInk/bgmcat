<script setup lang="ts">
import { computed, ref, watch } from "vue";

import type { AnimeDetail, DownloadConflictStrategy, DownloadJob } from "@shared/index";
import { cloudArchiveLabel } from "../../lib/cloudArchive";
import AppNavIcon from "../layout/AppNavIcon.vue";

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
  /** 手机上的独立详情页里用：不要缩进和左侧竖线，那是「挂在上面那一行下面」的视觉提示 */
  flat?: boolean;
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
}>();

const activeTab = ref<DetailTab>("episodes");
/** 一集有多个版本时才需要再往下钻一层；同样只展开一个，展开区不至于无限长 */
const expandedEpisodeKey = ref<string | null>(null);

const statusPriority: Record<DownloadJob["status"], number> = {
  active: 0,
  waiting: 1,
  queued: 2,
  paused: 3,
  error: 4,
  complete: 5,
  removed: 6
};

const primaryDownload = computed(() => primaryForDownloads(props.downloads));
const completedLength = computed(() => props.downloads.reduce((sum, download) => sum + download.completedLength, 0));
const totalLength = computed(() => props.downloads.reduce((sum, download) => sum + download.totalLength, 0));
const aggregateProgress = computed(() => progressForDownloads(props.downloads));
const totalDownloadSpeed = computed(() => props.downloads.reduce((sum, download) => sum + download.downloadSpeed, 0));
const totalUploadSpeed = computed(() => props.downloads.reduce((sum, download) => sum + download.uploadSpeed, 0));
const remainingSeconds = computed(() => {
  const remaining = Math.max(0, totalLength.value - completedLength.value);
  if (remaining <= 0 || totalDownloadSpeed.value <= 0) {
    return null;
  }
  return Math.ceil(remaining / totalDownloadSpeed.value);
});
const detailReady = computed(() => props.detailBelongsToGroup && !props.detailLoading);
const displaySummary = computed(() =>
  detailReady.value ? props.animeDetail?.summary || props.animeDetail?.intro || null : null
);
const sourceLabel = computed(() => {
  if (detailReady.value && props.animeDetail?.anime.mikanId) {
    return "Mikan RSS";
  }
  if (primaryDownload.value?.feedItemSubscriptionId) {
    return `订阅 #${primaryDownload.value.feedItemSubscriptionId}`;
  }
  return primaryDownload.value?.feedItemSourceUrl ? "RSS / Torrent" : "下载任务";
});
const resumableDownloads = computed(() => props.downloads.filter(canResume));
const pausableDownloads = computed(() => props.downloads.filter(canPause));
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
        level: download.status === "error" || download.issueKind ? "异常" : "提示",
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
      label: statusLabel(download.status),
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

/** 已完成才用绿色；其余进度一律走主色，避免「绿色=完成」的语义被稀释 */
function progressBarClass(downloads: DownloadJob[]): string {
  return downloads.every((download) => download.status === "complete")
    ? "bg-[var(--ok)]"
    : "bg-[var(--accent-vivid)]";
}

function primaryForDownloads(downloads: DownloadJob[]): DownloadJob | undefined {
  return [...downloads].sort(
    (left, right) => statusPriority[left.status] - statusPriority[right.status] || right.updatedAt.localeCompare(left.updatedAt)
  )[0];
}

/** 一集有多个版本时，代表任务可能是正在下的那个；播放要挑真正能播的版本 */
function playableForDownloads(downloads: DownloadJob[]): DownloadJob | undefined {
  return downloads.find(canPlay);
}

function progressForDownloads(downloads: DownloadJob[]): number {
  if (!downloads.length) {
    return 0;
  }
  const total = downloads.reduce((sum, download) => sum + download.totalLength, 0);
  const completed = downloads.reduce((sum, download) => sum + download.completedLength, 0);
  if (total > 0) {
    return Math.max(0, Math.min(100, Math.round((completed / total) * 100)));
  }
  return Math.max(...downloads.map(props.progressPercent), 0);
}

function sizeForDownloads(downloads: DownloadJob[]): string {
  const total = downloads.reduce((sum, download) => sum + download.totalLength, 0);
  const completed = downloads.reduce((sum, download) => sum + download.completedLength, 0);
  return total > 0 ? `${props.formatBytes(completed)} / ${props.formatBytes(total)}` : props.formatBytes(completed);
}

/** 展开区的主列：优先给完整发布名，只有拿不到才退回集数标题 */
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
  return download.canPlayExistingFile || download.status === "complete";
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

function formatDateTime(value: string): string {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) {
    return value;
  }
  return new Intl.DateTimeFormat("zh-CN", {
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit"
  })
    .format(date)
    .replace(/\//g, "-");
}
</script>

<template>
  <!--
    左边那条竖线是展开区和它所属行的唯一视觉联系——行本身在上面，中间没有别的框，
    缩进 + 竖线一起把「这些分集属于上面那一行」说清楚。
  -->
  <section
    class="@container grid gap-2.5"
    :class="props.flat ? '' : 'border-b border-[var(--line)] bg-[var(--tint-1)] py-3 pl-3 pr-3 @min-[640px]:pl-[3.25rem]'"
  >
    <div class="grid gap-2.5" :class="props.flat ? '' : 'border-l-2 border-[var(--accent-line)] pl-3'">
      <!-- 概览条：状态、进度、统计、批量操作全部横排，全宽下不必再竖着堆 -->
      <div v-if="primaryDownload" class="flex flex-wrap items-center gap-x-3 gap-y-2">
        <span class="shrink-0 rounded-md border px-2 py-0.5 text-xs font-semibold" :class="statusPillClass(primaryDownload.status)">
          {{ statusLabel(primaryDownload.status) }}
        </span>
        <span class="h-1.5 min-w-[8rem] flex-1 overflow-hidden rounded-full bg-[var(--tint-4)]">
          <span
            class="block h-full rounded-full transition-[width] duration-300"
            :class="progressBarClass(downloads)"
            :style="{ width: `${aggregateProgress}%` }"
          ></span>
        </span>
        <span class="shrink-0 text-xs font-semibold tabular-nums text-[var(--ink)]">{{ aggregateProgress }}%</span>
        <span class="shrink-0 text-xs font-semibold tabular-nums text-[var(--muted)]">
          {{ props.formatBytes(completedLength) }} / {{ props.formatBytes(totalLength) }}
        </span>
        <span class="shrink-0 text-xs font-semibold tabular-nums text-[var(--muted)]">↓ {{ props.formatBytes(totalDownloadSpeed) }}/s</span>
        <span class="shrink-0 text-xs font-semibold tabular-nums text-[var(--muted)]">↑ {{ props.formatBytes(totalUploadSpeed) }}/s</span>
        <span v-if="remainingSeconds" class="shrink-0 text-xs font-semibold text-[var(--muted)]">剩余 {{ formatDuration(remainingSeconds) }}</span>
        <span class="shrink-0 text-xs font-semibold text-[var(--muted)]">{{ sourceLabel }}</span>
        <span v-if="detailLoading" class="shrink-0 text-xs font-semibold text-[var(--muted)]">加载详情…</span>
        <span v-else-if="detailError" class="shrink-0 text-xs font-semibold text-[var(--warn)]">详情缺失</span>

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
      <p v-if="displaySummary" class="line-clamp-1 text-xs leading-5 text-[var(--muted)]" :title="displaySummary">
        {{ displaySummary }}
      </p>

      <!-- 文件冲突以前只有 toast 能触发，现在就地给出三种处理方式 -->
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

      <nav class="flex gap-1 overflow-x-auto [scrollbar-width:none] [&::-webkit-scrollbar]:hidden">
        <button
          v-for="tab in detailTabs"
          :key="tab.key"
          type="button"
          class="inline-flex h-8 shrink-0 items-center gap-1.5 whitespace-nowrap rounded-md px-3 text-xs font-semibold transition"
          :class="
            activeTab === tab.key
              ? 'bg-[var(--accent)] text-[var(--accent-contrast)]'
              : 'text-[var(--muted)] hover:bg-[var(--tint-2)] hover:text-[var(--ink)]'
          "
          @click="activeTab = tab.key"
        >
          <span>{{ tab.label }}</span>
          <span class="tabular-nums opacity-70">{{ tab.count }}</span>
        </button>
      </nav>

      <template v-if="activeTab === 'episodes'">
        <div
          class="hidden grid-cols-[5.5rem_minmax(10rem,1fr)_5rem_4.5rem_minmax(7rem,9rem)_7.5rem_6.75rem] items-center gap-3 border-b border-[var(--line)] px-2 pb-1.5 text-[11px] font-semibold text-[var(--muted)] @min-[820px]:grid"
        >
          <span>集数</span>
          <span>发布名</span>
          <span>状态</span>
          <span>版本</span>
          <span>进度</span>
          <span class="text-right">大小</span>
          <span class="text-right">操作</span>
        </div>

        <div v-for="episode in episodes" :key="episode.key" class="grid">
          <!-- 一集有多个版本时整行可点：窄屏没有「N 版」按钮的位置，得留一条同样的入口 -->
          <div
            class="grid grid-cols-[minmax(0,1fr)_auto] items-center gap-x-3 gap-y-1 rounded-md px-2 py-1.5 transition hover:bg-[var(--tint-2)] @min-[820px]:grid-cols-[5.5rem_minmax(10rem,1fr)_5rem_4.5rem_minmax(7rem,9rem)_7.5rem_6.75rem]"
            :class="[
              expandedEpisodeKey === episode.key ? 'bg-[var(--tint-2)]' : '',
              episode.downloads.length > 1 ? 'cursor-pointer' : ''
            ]"
            @click="episode.downloads.length > 1 && toggleEpisode(episode.key)"
          >
            <span class="order-1 hidden shrink-0 text-xs font-semibold text-[var(--ink)] @min-[820px]:block">
              {{ episode.episodeLabel ?? "未知集数" }}
            </span>

            <!-- 主列：完整发布名，全宽下单行省略，不再压成两行小字 -->
            <span class="order-2 min-w-0" :title="releaseName(episode.downloads)">
              <span class="block truncate text-xs font-semibold text-[var(--ink)] @min-[820px]:font-normal @min-[820px]:text-[var(--muted)]">
                <span class="font-semibold text-[var(--ink)] @min-[820px]:hidden">
                  <span
                    v-if="episode.downloads.length > 1"
                    class="inline-block transition-transform"
                    :class="expandedEpisodeKey === episode.key ? 'rotate-90' : ''"
                    aria-hidden="true"
                  >
                    ›
                  </span>
                  {{ episode.episodeLabel ?? "未知集数" }}<template v-if="episode.downloads.length > 1"> · {{ episode.downloads.length }} 版</template> ·
                </span>
                {{ releaseName(episode.downloads) }}
              </span>
            </span>

            <span
              class="order-4 hidden w-fit shrink-0 rounded-md border px-2 py-0.5 text-[11px] font-semibold @min-[820px]:order-3 @min-[820px]:block"
              :class="statusPillClass(primaryForDownloads(episode.downloads)?.status ?? 'waiting')"
            >
              {{ statusLabel(primaryForDownloads(episode.downloads)?.status ?? "waiting") }}
            </span>

            <button
              v-if="episode.downloads.length > 1"
              type="button"
              class="order-5 hidden w-fit items-center gap-1 rounded-md border border-[var(--line)] bg-[var(--card)] px-2 py-0.5 text-[11px] font-semibold text-[var(--ink)] transition hover:border-[var(--accent)] @min-[820px]:order-4 @min-[820px]:inline-flex"
              :aria-expanded="expandedEpisodeKey === episode.key"
              :title="expandedEpisodeKey === episode.key ? '收起版本' : '展开全部版本'"
              @click="toggleEpisode(episode.key)"
            >
              <span>{{ episode.downloads.length }} 版</span>
              <span
                class="inline-block transition-transform"
                :class="expandedEpisodeKey === episode.key ? 'rotate-90' : ''"
                aria-hidden="true"
              >
                ›
              </span>
            </button>
            <span v-else class="order-5 hidden text-[11px] text-[var(--muted)] @min-[820px]:order-4 @min-[820px]:block">1 版</span>

            <span class="order-3 col-span-2 flex min-w-0 items-center gap-2 @min-[820px]:order-5 @min-[820px]:col-span-1">
              <span class="h-1.5 min-w-0 flex-1 overflow-hidden rounded-full bg-[var(--tint-4)]">
                <span
                  class="block h-full rounded-full"
                  :class="progressBarClass(episode.downloads)"
                  :style="{ width: `${progressForDownloads(episode.downloads)}%` }"
                ></span>
              </span>
              <span class="w-9 shrink-0 text-right text-[11px] font-semibold tabular-nums text-[var(--muted)]">
                {{ progressForDownloads(episode.downloads) }}%
              </span>
            </span>

            <span class="order-6 hidden truncate text-right text-[11px] font-semibold tabular-nums text-[var(--muted)] @min-[820px]:block">
              {{ sizeForDownloads(episode.downloads) }}
            </span>

            <span class="order-7 flex shrink-0 justify-end gap-1.5" @click.stop>
              <button
                type="button"
                class="icon-btn"
                :title="`播放 ${episode.episodeLabel ?? '该集'}`"
                :aria-label="`播放 ${episode.episodeLabel ?? '该集'}`"
                :disabled="!playableForDownloads(episode.downloads)"
                @click="playableForDownloads(episode.downloads) && emit('playback', playableForDownloads(episode.downloads)!)"
              >
                <AppNavIcon name="play" />
              </button>
              <button
                v-if="primaryForDownloads(episode.downloads)"
                type="button"
                class="icon-btn"
                :title="canResume(primaryForDownloads(episode.downloads)!) ? '继续' : '暂停'"
                :aria-label="canResume(primaryForDownloads(episode.downloads)!) ? '继续' : '暂停'"
                :disabled="
                  (!canPause(primaryForDownloads(episode.downloads)!) && !canResume(primaryForDownloads(episode.downloads)!)) ||
                  props.isDownloadRowBusy(primaryForDownloads(episode.downloads)!.gid)
                "
                @click="triggerTransfer(primaryForDownloads(episode.downloads)!)"
              >
                <AppNavIcon :name="canResume(primaryForDownloads(episode.downloads)!) ? 'play' : 'pause'" />
              </button>
              <button
                v-if="primaryForDownloads(episode.downloads)"
                type="button"
                class="icon-btn text-[var(--err)] hover:text-[var(--err)]"
                title="删除该集"
                aria-label="删除该集"
                @click="emit('remove', primaryForDownloads(episode.downloads)!)"
              >
                <AppNavIcon name="trash" />
              </button>
            </span>
          </div>

          <!-- 同一集的多个版本：再缩进一层，标题仍旧占主列 -->
          <div v-if="expandedEpisodeKey === episode.key && episode.downloads.length > 1" class="grid gap-1 py-1 pl-4">
            <div
              v-for="download in episode.downloads"
              :key="download.gid"
              class="grid grid-cols-[minmax(0,1fr)_auto] items-center gap-x-3 gap-y-1 rounded-md border-l-2 border-[var(--line)] px-2 py-1.5 hover:bg-[var(--tint-2)] @min-[820px]:grid-cols-[minmax(10rem,1fr)_5rem_minmax(7rem,9rem)_7.5rem_auto]"
            >
              <span class="min-w-0 truncate text-xs text-[var(--muted)]" :title="props.downloadDisplayTitle(download)">
                {{ props.downloadDisplayTitle(download) }}
              </span>
              <span
                class="hidden w-fit shrink-0 rounded-md border px-2 py-0.5 text-[11px] font-semibold @min-[820px]:block"
                :class="statusPillClass(download.status)"
              >
                {{ cloudArchiveLabel(download) || statusLabel(download.status) }}
              </span>
              <span class="col-span-2 flex min-w-0 items-center gap-2 @min-[820px]:col-span-1">
                <span class="h-1.5 min-w-0 flex-1 overflow-hidden rounded-full bg-[var(--tint-4)]">
                  <span
                    class="block h-full rounded-full"
                    :class="progressBarClass([download])"
                    :style="{ width: `${props.progressPercent(download)}%` }"
                  ></span>
                </span>
                <span class="w-9 shrink-0 text-right text-[11px] font-semibold tabular-nums text-[var(--muted)]">
                  {{ props.progressPercent(download) }}%
                </span>
              </span>
              <span class="hidden truncate text-right text-[11px] font-semibold tabular-nums text-[var(--muted)] @min-[820px]:block">
                {{ props.formatBytes(download.completedLength) }} / {{ props.formatBytes(download.totalLength) }}
              </span>
              <span class="flex shrink-0 justify-end gap-1.5">
                <button
                  type="button"
                  class="icon-btn"
                  title="播放"
                  aria-label="播放"
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
                  <AppNavIcon :name="canResume(download) ? 'play' : 'pause'" />
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
          class="hidden grid-cols-[minmax(10rem,1fr)_5rem_minmax(7rem,9rem)_7.5rem_11rem_auto] items-center gap-3 border-b border-[var(--line)] px-2 pb-1.5 text-[11px] font-semibold text-[var(--muted)] @min-[820px]:grid"
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
          class="grid grid-cols-[minmax(0,1fr)_auto] items-center gap-x-3 gap-y-1 rounded-md px-2 py-1.5 transition hover:bg-[var(--tint-2)] @min-[820px]:grid-cols-[minmax(10rem,1fr)_5rem_minmax(7rem,9rem)_7.5rem_11rem_auto]"
        >
          <span class="min-w-0 truncate text-xs font-semibold text-[var(--ink)]" :title="props.downloadDisplayTitle(download)">
            {{ props.downloadDisplayTitle(download) }}
          </span>
          <span
            class="hidden w-fit shrink-0 rounded-md border px-2 py-0.5 text-[11px] font-semibold @min-[820px]:block"
            :class="statusPillClass(download.status)"
          >
            {{ cloudArchiveLabel(download) || statusLabel(download.status) }}
          </span>
          <span class="col-span-2 flex min-w-0 items-center gap-2 @min-[820px]:col-span-1">
            <span class="h-1.5 min-w-0 flex-1 overflow-hidden rounded-full bg-[var(--tint-4)]">
              <span
                class="block h-full rounded-full"
                :class="progressBarClass([download])"
                :style="{ width: `${props.progressPercent(download)}%` }"
              ></span>
            </span>
            <span class="w-9 shrink-0 text-right text-[11px] font-semibold tabular-nums text-[var(--muted)]">
              {{ props.progressPercent(download) }}%
            </span>
          </span>
          <span class="hidden truncate text-right text-[11px] font-semibold tabular-nums text-[var(--muted)] @min-[820px]:block">
            {{ props.formatBytes(download.completedLength) }} / {{ props.formatBytes(download.totalLength) }}
          </span>
          <span class="hidden truncate text-[11px] font-semibold text-[var(--muted)] @min-[820px]:block">{{ download.gid }}</span>
          <span class="flex shrink-0 justify-end gap-1.5">
            <button type="button" class="icon-btn" title="播放" aria-label="播放" :disabled="!canPlay(download)" @click="emit('playback', download)">
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
              <AppNavIcon :name="canResume(download) ? 'play' : 'pause'" />
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
          class="grid grid-cols-[minmax(0,1fr)_auto] items-baseline gap-x-3 gap-y-1 rounded-md px-2 py-1.5 @min-[820px]:grid-cols-[5.5rem_minmax(0,1fr)_7rem]"
        >
          <strong class="shrink-0 text-[11px] font-semibold text-[var(--ink)]">{{ entry.label }}</strong>
          <span class="min-w-0 truncate text-xs text-[var(--muted)]" :title="entry.detail">{{ entry.detail }}</span>
          <span class="shrink-0 text-right text-[11px] font-semibold tabular-nums text-[var(--muted)]">{{ formatDateTime(entry.time) }}</span>
        </div>
      </template>
    </div>
  </section>
</template>
