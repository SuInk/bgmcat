<script setup lang="ts">
/**
 * 番剧详情：接管整个内容区，沿用 main 分支那版的左右分栏。
 *
 * - 左栏：紧凑头部（返回 / 封面 / 标题 / 元信息 / 收藏与订阅）+ 竖向剧集列表
 * - 右栏：当前选中剧集（默认「全部」）的资源
 *
 * 宽屏（≥1024px）两栏并排，点剧集只换右栏范围；窄屏放不下两栏，
 * 退回「剧集列表 → 点集看资源 → 返回」的逐层进入。
 * 简介 / 标签 / 别名 / 外链收进「简介与标签」默认折叠，别把剧集列表挤到折叠线以下。
 */
import { computed, ref, watch } from "vue";

import type { AnimeDetail, DownloadJob, MikanDownloadItem } from "@shared/index";

import { buildMikanCountOptions, extractResolutionLabel, providerIdOf, providerIdsOf } from "../../lib/mikan";
import { airDayLabel, broadcastLabel, broadcastWeekday } from "../../lib/format";
import { formatPlatformScore, metadataPlatformLabel } from "../../lib/metadataPlatform";
import { settings } from "../../stores/session";
import CuteLoader from "../common/CuteLoader.vue";
import RightDetailPane from "../common/RightDetailPane.vue";
import AppNavIcon from "../layout/AppNavIcon.vue";
import MikanResourcePane from "./MikanResourcePane.vue";

export interface MikanDownloadGroup {
  key: string;
  label: string;
  detail: string | null;
  order: number;
  downloads: MikanDownloadItem[];
}

const props = defineProps<{
  animeDetail: AnimeDetail | null;
  detailError: string | null;
  isFavorite: boolean;
  subscriptionId: number | null;
  filteredDownloads: MikanDownloadItem[];
  pagedDownloads: MikanDownloadItem[];
  selectedSubgroup: string;
  selectedResolution: string;
  detailSubgroupOptions: Array<{ key: string; label: string; count: number }>;
  detailResolutionOptions: Array<{ key: string; label: string; count: number }>;
  detailProviderOptions: Array<{ key: string; label: string; count: number }>;
  selectedProvider: string;
  busyKey: string | null;
  pagedDownloadGroups: MikanDownloadGroup[];
  isDownloadQueued: (item: MikanDownloadItem) => boolean;
  getQueuedDownload: (item: MikanDownloadItem) => DownloadJob | null;
  detailDownloadPageSize: number;
  downloadPage: number;
  downloadTotalPages: number;
  downloadPaginationItems: Array<number | "ellipsis">;
  /** 新版 store 传入的加载态；提供时优先于 busyKey 字符串前缀判断 */
  detailLoading?: boolean;
  /** 新版 store 传入的按条目忙碌判断；提供时优先于 busyKey */
  isDownloadBusy?: (item: MikanDownloadItem) => boolean;
}>();

const emit = defineEmits<{
  (event: "close"): void;
  (event: "favorite", payload: { anime: AnimeDetail["anime"]; mikanKeyword?: string }): void;
  (event: "remove-favorite", animeId: string): void;
  (event: "subscribe", payload: { anime: AnimeDetail["anime"]; mikanKeyword?: string; providerIds?: string[] }): void;
  (event: "select-subgroup", value: string): void;
  (event: "select-resolution", value: string): void;
  (event: "select-provider", value: string): void;
  (event: "update:downloadPage", value: number): void;
  (event: "queue-download", item: MikanDownloadItem): void;
  (event: "play-download", download: DownloadJob): void;
  (event: "copy-text", value: string): void;
  (event: "open-link", value: string): void;
}>();

const detailLoading = computed(() =>
  props.detailLoading ?? Boolean(props.busyKey?.startsWith("anime:") || props.busyKey?.startsWith("download-detail:"))
);
const showFullPaneLoading = computed(() => detailLoading.value && !props.animeDetail);
const showContentLoading = computed(() => detailLoading.value && Boolean(props.animeDetail));
const showFullPaneError = computed(() => (props.animeDetail ? null : props.detailError));
const showContentError = computed(() => (props.animeDetail ? props.detailError : null));

const detailScrollRef = ref<HTMLElement | null>(null);
const selectedDownloadGroupKey = ref<string | null>(null);
/** 只在窄屏有意义：宽屏两栏始终并排，这个值不影响显示。 */
const narrowView = ref<"episodes" | "resources">("episodes");
const infoExpanded = ref(false);

const animeAliases = computed(() => {
  const primary = props.animeDetail?.anime.title ?? "";
  return (props.animeDetail?.titles ?? []).filter((title) => title && title !== primary);
});
const animeSummary = computed(() => props.animeDetail?.summary?.trim() || null);
const animeTags = computed(() => props.animeDetail?.tags ?? []);
const sourceLinks = computed(() => {
  const detail = props.animeDetail;
  if (!detail) {
    return [];
  }
  const links: Array<{ label: string; url: string }> = [];
  if (detail.anime.bangumiId) {
    links.push({ label: "Bangumi", url: `https://bgm.tv/subject/${detail.anime.bangumiId}` });
  }
  if (detail.anime.mikanId) {
    const base = (settings.value?.mikanBaseUrl || "https://mikanani.me").replace(/\/+$/, "").replace(/^https?:\/\/mikanime\.tv$/, "https://mikanani.me");
    links.push({ label: "蜜柑", url: `${base}/Home/Bangumi/${detail.anime.mikanId}` });
  }
  if (detail.anime.officialSite) {
    links.push({ label: "官网", url: detail.anime.officialSite });
  }
  return links;
});
const hasExtraInfo = computed(() =>
  Boolean(animeSummary.value || animeTags.value.length || animeAliases.value.length > 1 || sourceLinks.value.length)
);

/**
 * 各信息平台的评分与链接。
 * **每个平台连同它自己的满分一起显示**，不折算、不平均——
 * Bangumi 的 8.5 和 AniList 的 90 不是同一把尺子上的数。
 */
const platformRows = computed(() =>
  (props.animeDetail?.platforms ?? []).map((platform) => ({
    id: platform.id,
    label: metadataPlatformLabel(platform.id) || platform.displayName,
    url: platform.url,
    score: formatPlatformScore(platform.score, platform.scoreMax),
    rank: typeof platform.rank === "number" && platform.rank > 0 ? platform.rank : null,
    scoreCount:
      typeof platform.scoreCount === "number" && platform.scoreCount > 0 ? platform.scoreCount : null
  }))
);

/** 这一轮没响应的信息平台。结果照出，只在展开区浅色提一句。 */
const platformFailureNotice = computed(() => {
  const failures = props.animeDetail?.platformFailures ?? [];
  if (!failures.length) {
    return "";
  }
  return `${failures.map((failure) => failure.displayName).join("、")} 这一轮没响应，评分里少了它们。`;
});

const animeStartDate = computed(() => props.animeDetail?.episodes.find((episode) => episode.airDate)?.airDate ?? null);
// 「开播日期」来自 Bangumi 剧集表，「周几」来自 bangumi-data 的放送时刻——
// 两个来源不一致就会写出「2026-07-02 · 周二」这种自相矛盾的行，所以星期与
// 时间统一从放送时刻推导，拿不到才退回服务端算好的 airDay。
const animeAirDayLabel = computed(() => {
  const weekday = broadcastWeekday(props.animeDetail?.anime.broadcastAt) ?? props.animeDetail?.anime.airDay;
  if (weekday === null || weekday === undefined) {
    return null;
  }
  return airDayLabel(weekday);
});
const animeBroadcastLabel = computed(() => broadcastLabel(props.animeDetail?.anime.broadcastAt));

/** 头部那一行元信息：只放判断「是不是这部、什么时候更」要用的东西。集数放在剧集列表标题上。 */
const metaParts = computed(() => {
  const detail = props.animeDetail;
  if (!detail) {
    return [];
  }
  const parts: string[] = [];
  if (animeStartDate.value) {
    parts.push(`${animeStartDate.value} 首播`);
  }
  if (animeBroadcastLabel.value) {
    parts.push(animeBroadcastLabel.value);
  } else if (animeAirDayLabel.value) {
    parts.push(animeAirDayLabel.value);
  }
  return parts;
});

const episodes = computed(() => props.animeDetail?.episodes ?? []);
const downloadGroupByKey = computed(() => {
  const groups = new Map<string, MikanDownloadGroup>();
  for (const group of props.pagedDownloadGroups) {
    groups.set(group.key, group);
  }
  return groups;
});
const unmatchedDownloadGroup = computed(() => downloadGroupByKey.value.get("unmatched") ?? null);
const selectedDownloadGroup = computed(() => {
  if (!selectedDownloadGroupKey.value) {
    return null;
  }
  return downloadGroupByKey.value.get(selectedDownloadGroupKey.value) ?? null;
});
const scopedDownloads = computed(() => selectedDownloadGroup.value?.downloads ?? props.animeDetail?.downloads ?? []);
const scopedFilteredDownloads = computed(() =>
  scopedDownloads.value.filter((item) => {
    if (props.selectedSubgroup !== "all" && (item.subgroup?.trim() || "__unknown") !== props.selectedSubgroup) {
      return false;
    }
    if (props.selectedResolution !== "all" && (extractResolutionLabel(item.title) || "__unknown") !== props.selectedResolution) {
      return false;
    }
    if (props.selectedProvider !== "all" && !providerIdsOf(item).includes(props.selectedProvider)) {
      return false;
    }
    return true;
  })
);
const scopedSubgroupOptions = computed(() => [
  {
    key: "all",
    label: "全部",
    count: scopedDownloads.value.length
  },
  ...buildMikanCountOptions(
    scopedDownloads.value,
    (item) => item.subgroup?.trim() || "__unknown",
    (key) => (key === "__unknown" ? "未知字幕组" : key)
  )
]);
const scopedResolutionOptions = computed(() => [
  {
    key: "all",
    label: "全部分辨率",
    count: scopedDownloads.value.length
  },
  ...buildMikanCountOptions(
    scopedDownloads.value,
    (item) => extractResolutionLabel(item.title) || "__unknown",
    (key) => (key === "__unknown" ? "分辨率未知" : key)
  )
]);
/**
 * 订阅时要挂哪些源。默认全选本次查过的源（即设置里启用的那些）。
 * 蜜柑拼不出地址（没有 bangumiId）之类的情况由后端跳过，不在这里预判。
 */
const subscribeProviders = ref<string[]>([]);
const subscribeProviderOptions = computed(() => props.animeDetail?.providerStatuses ?? []);
watch(
  subscribeProviderOptions,
  (options) => {
    subscribeProviders.value = options.map((option) => option.providerId);
  },
  { immediate: true }
);

function toggleSubscribeProvider(providerId: string): void {
  subscribeProviders.value = subscribeProviders.value.includes(providerId)
    ? subscribeProviders.value.filter((id) => id !== providerId)
    : [...subscribeProviders.value, providerId];
}

/** 只有蜜柑能建订阅时才需要 bangumiId；别的源按关键词搜，没有 id 也能订。 */
const canSubscribe = computed(() => {
  if (props.subscriptionId) {
    return false;
  }
  if (subscribeProviders.value.length === 0) {
    return false;
  }
  return subscribeProviders.value.some((id) => id !== "mikan") || Boolean(props.animeDetail?.anime.mikanId);
});
const showSubscribeProviders = computed(() => !props.subscriptionId && subscribeProviderOptions.value.length > 1);

const providerNames = computed<Record<string, string>>(() =>
  Object.fromEntries(
    (props.animeDetail?.providerStatuses ?? []).map((status) => [status.providerId, status.displayName])
  )
);
// pending 是「还在找」,不是「不可用」——混在一起会把「再等等就有」说成「这个源挂了」
const failedProviders = computed(() =>
  (props.animeDetail?.providerStatuses ?? []).filter((status) => !status.ok && !status.pending)
);
const pendingProviders = computed(() =>
  (props.animeDetail?.providerStatuses ?? []).filter((status) => status.pending)
);
const scopedProviderOptions = computed(() => [
  {
    key: "all",
    label: "全部来源",
    count: scopedDownloads.value.length
  },
  ...buildMikanCountOptions(
    scopedDownloads.value,
    (item) => providerIdOf(item) ?? "__unknown",
    (key) => providerNames.value[key] ?? (key === "__unknown" ? "未知来源" : key)
  )
]);

function getEpisodeDownloadCount(episode: AnimeDetail["episodes"][number]): number {
  return downloadGroupByKey.value.get(`episode:${episode.episodeId}`)?.downloads.length ?? 0;
}

/** Bangumi 没公布标题时常把标题写成「第 12 集」，和左边的集号重复，干脆不写。 */
function episodeTitleOf(episode: AnimeDetail["episodes"][number]): string | null {
  const title = episode.title?.trim();
  if (!title || /^第\s*\d+(\.\d+)?\s*[集话話]$/.test(title)) {
    return null;
  }
  return title;
}

/** 宽屏只是给右栏换范围；窄屏顺带切到资源视图。 */
function selectDownloadGroup(key: string | null): void {
  narrowView.value = "resources";
  // 窄屏整页一起滚：从列表中段点进来，资源视图要从顶上开始看
  detailScrollRef.value?.scrollTo({ top: 0 });
  if (selectedDownloadGroupKey.value === key) {
    return;
  }
  selectedDownloadGroupKey.value = key;
  emit("select-subgroup", "all");
  emit("select-resolution", "all");
}

watch(
  () => props.animeDetail?.anime.animeId,
  () => {
    detailScrollRef.value?.scrollTo({ top: 0 });
    selectedDownloadGroupKey.value = null;
    narrowView.value = "episodes";
    infoExpanded.value = false;
  }
);

watch(
  () => props.pagedDownloadGroups.map((group) => group.key).join("|"),
  () => {
    if (selectedDownloadGroupKey.value && !downloadGroupByKey.value.has(selectedDownloadGroupKey.value)) {
      selectedDownloadGroupKey.value = null;
    }
  }
);
</script>

<template>
  <RightDetailPane
    :loading="showFullPaneLoading"
    :error="showFullPaneError"
    empty-title="未选择番剧"
    empty-text="点击左侧番剧后，这里会显示详情与下载资源。"
  >
    <template #loading>
      <div class="grid place-items-center">
        <CuteLoader mode="loading" />
      </div>
    </template>

    <template v-if="animeDetail">
      <div class="relative grid h-full min-h-0 overflow-hidden">
        <!-- 刷新中：骨架与真实布局同形，切换时不跳 -->
        <div v-if="showContentLoading" class="detail-split">
          <div class="detail-side">
            <section class="grid gap-3 rounded-lg border border-[var(--line)] bg-[var(--card)] p-3">
              <div class="flex items-start gap-3">
                <button type="button" class="icon-btn shrink-0" aria-label="返回列表" title="返回列表" @click="emit('close')">
                  <AppNavIcon name="chevron-left" />
                </button>
                <div class="h-20 w-14 shrink-0 rounded bg-[var(--line)]/60 skeleton-pulse"></div>
                <div class="grid flex-1 content-start gap-2 pt-1">
                  <div class="h-5 w-4/5 rounded-full bg-[var(--line)]/70 skeleton-pulse"></div>
                  <div class="h-3 w-3/5 rounded-full bg-[var(--line)]/55 skeleton-pulse"></div>
                  <div class="h-3 w-2/5 rounded-full bg-[var(--line)]/55 skeleton-pulse"></div>
                </div>
              </div>
            </section>
            <section class="grid overflow-hidden rounded-lg border border-[var(--line)] bg-[var(--card)]">
              <div class="h-10 border-b border-[var(--line)]"></div>
              <div v-for="index in 8" :key="index" class="grid gap-1.5 border-b border-[var(--line)] px-3 py-2.5 last:border-b-0">
                <div class="h-3 w-3/5 rounded-full bg-[var(--line)]/70 skeleton-pulse"></div>
                <div class="h-3 w-1/4 rounded-full bg-[var(--line)]/55 skeleton-pulse"></div>
              </div>
            </section>
          </div>
          <section class="detail-main hidden content-start gap-3 rounded-lg border border-[var(--line)] bg-[var(--card)] p-3 lg:grid">
            <div class="flex flex-wrap gap-2">
              <div class="h-7 w-24 rounded-md bg-[var(--accent-soft)] skeleton-pulse"></div>
              <div class="h-7 w-28 rounded-md bg-[var(--line)]/60 skeleton-pulse"></div>
              <div class="h-7 w-20 rounded-md bg-[var(--line)]/60 skeleton-pulse"></div>
            </div>
            <div v-for="index in 6" :key="index" class="h-14 rounded-md bg-[var(--line)]/45 skeleton-pulse"></div>
          </section>
        </div>

        <div v-else-if="showContentError" class="grid min-h-full content-start gap-3 p-3">
          <button
            type="button"
            class="btn-ghost inline-flex items-center gap-1.5 justify-self-start px-3 py-1.5 text-sm font-semibold"
            @click="emit('close')"
          >
            <AppNavIcon name="chevron-left" class="size-4" />
            返回列表
          </button>
          <div class="grid max-w-sm gap-3 justify-self-center rounded-lg border border-[var(--err-soft)] bg-[var(--err-soft)] px-6 py-6 text-center text-[var(--err)]">
            <strong class="text-base font-semibold text-[var(--err)]">RSS 加载失败</strong>
            <p class="text-sm font-semibold leading-6 text-[var(--err)]">{{ showContentError }}</p>
          </div>
        </div>

        <template v-else>
          <div ref="detailScrollRef" class="detail-split">
            <!-- 左栏：头部 + 剧集。窄屏在资源视图时收起 -->
            <div class="detail-side" :class="{ 'narrow-hidden': narrowView === 'resources' }">
              <!-- 头部：返回、封面、标题、一行元信息、主操作。返回按钮就在这里，不再单独占一条栏 -->
              <header class="grid shrink-0 gap-2.5 rounded-lg border border-[var(--line)] bg-[var(--card)] p-3 shadow-[var(--soft-shadow)]">
                <!-- 左栏只有 19~23rem 宽：标题独占一行（返回按钮在它左边），封面与元信息在下一行，
                     别让返回按钮、封面、文字三列硬挤，文字列只剩 150px、每行都折。 -->
                <div class="flex items-start gap-2">
                  <button type="button" class="icon-btn shrink-0" aria-label="返回列表" title="返回列表" @click="emit('close')">
                    <AppNavIcon name="chevron-left" />
                  </button>
                  <h2 class="line-clamp-2 min-w-0 flex-1 pt-1.5 text-base font-semibold leading-snug text-[var(--ink)]" :title="animeDetail.anime.title">
                    {{ animeDetail.anime.title }}
                  </h2>
                </div>

                <div class="grid grid-cols-[3.5rem_minmax(0,1fr)] items-start gap-3">
                  <div class="grid h-20 w-14 place-items-center overflow-hidden rounded bg-[var(--panel)] text-lg font-semibold text-[var(--muted)]">
                    <img
                      v-if="animeDetail.anime.imageUrl"
                      :src="animeDetail.anime.imageUrl"
                      :alt="animeDetail.anime.title"
                      class="block h-full w-full object-cover object-top"
                    />
                    <span v-else>番</span>
                  </div>
                  <div class="grid min-w-0 content-start gap-1">
                    <p v-if="animeAliases.length" class="truncate text-xs text-[var(--muted)]" :title="animeAliases.join(' / ')">
                      {{ animeAliases[0] }}
                    </p>
                    <p v-if="metaParts.length" class="text-xs text-[var(--muted)]" :title="animeBroadcastLabel ? '放送时间为本地时间' : undefined">
                      {{ metaParts.join(" · ") }}
                    </p>
                    <!-- 各平台评分连同各自满分并排，点开去对应平台 -->
                    <div class="flex flex-wrap items-center gap-x-3 gap-y-0.5 text-xs">
                      <template v-if="platformRows.length">
                        <button
                          v-for="row in platformRows"
                          :key="row.id"
                          type="button"
                          class="inline-flex items-center gap-1 text-[var(--muted)] transition hover:text-[var(--ink)]"
                          :title="row.scoreCount ? `${row.label}：${row.scoreCount} 人评分` : row.url"
                          :aria-label="`在浏览器打开 ${row.label}`"
                          @click="emit('open-link', row.url)"
                        >
                          {{ row.label }}
                          <strong v-if="row.score" class="font-semibold text-accent">{{ row.score }}</strong>
                          <span v-else>暂无评分</span>
                          <span v-if="row.rank">#{{ row.rank }}</span>
                        </button>
                      </template>
                      <span v-else-if="animeDetail.anime.rating" class="text-[var(--muted)]">
                        <strong class="font-semibold text-accent">★ {{ animeDetail.anime.rating.toFixed(1) }}</strong>
                        <template v-if="animeDetail.rank"> · Bangumi #{{ animeDetail.rank }}</template>
                      </span>
                    </div>
                  </div>
                </div>

                <!-- 订阅是这页真正的主操作（自动追更），收藏只是记一笔，两者不同权重 -->
                <div class="grid grid-cols-[auto_minmax(0,1fr)] gap-2">
                    <button
                      v-if="!isFavorite"
                      type="button"
                      class="btn-ghost inline-flex min-h-9 items-center justify-center gap-1.5 whitespace-nowrap px-3 text-xs font-semibold"
                      @click="emit('favorite', { anime: animeDetail.anime, mikanKeyword: animeDetail.mikanKeyword })"
                    >
                      <AppNavIcon name="favorites" aria-hidden="true" />
                      收藏
                    </button>
                    <button
                      v-else
                      type="button"
                      class="inline-flex min-h-9 items-center justify-center gap-1.5 whitespace-nowrap rounded-md border border-[var(--accent)] bg-[var(--tint-2)] px-3 text-xs font-semibold text-[var(--accent)] transition"
                      :aria-label="`取消收藏 ${animeDetail.anime.title}`"
                      @click="emit('remove-favorite', animeDetail.anime.animeId)"
                    >
                      <AppNavIcon name="favorites" aria-hidden="true" />
                      已收藏
                    </button>
                    <button
                      type="button"
                      class="inline-flex min-h-9 items-center justify-center gap-1.5 whitespace-nowrap px-3 text-xs font-semibold"
                      :class="subscriptionId ? 'btn-ghost' : 'btn-primary'"
                      :disabled="!canSubscribe"
                      :title="subscriptionId ? '已在追更：新集数会自动加入下载队列，可在订阅页调整规则' : '订阅后持续抓取该番 RSS，新集数自动加入下载队列'"
                      @click="emit('subscribe', { anime: animeDetail.anime, mikanKeyword: animeDetail.mikanKeyword, providerIds: subscribeProviders })"
                    >
                      <AppNavIcon name="subscriptions" aria-hidden="true" />
                      {{ subscriptionId ? "已订阅" : "订阅并自动追更" }}
                    </button>
                </div>

                <!-- 次要一行：左边展开简介，右边选订阅挂哪些源。两样都没有就不占这一行 -->
                <div
                  v-if="hasExtraInfo || showSubscribeProviders"
                  class="flex flex-wrap items-center justify-between gap-x-4 gap-y-2 border-t border-[var(--line)] pt-2"
                >
                  <button
                    v-if="hasExtraInfo"
                    type="button"
                    class="inline-flex items-center gap-1 text-xs font-semibold text-[var(--muted)] transition hover:text-[var(--ink)]"
                    :aria-expanded="infoExpanded"
                    @click="infoExpanded = !infoExpanded"
                  >
                    <AppNavIcon
                      name="chevron-right"
                      class="size-3.5 transition-transform"
                      :class="{ 'rotate-90': infoExpanded }"
                      aria-hidden="true"
                    />
                    简介与标签
                  </button>
                  <span v-else></span>

                  <!-- 一次可以挂多个源：勾上的每个源各建一条订阅，新集数跨源择优只下一个 -->
                  <div v-if="showSubscribeProviders" class="flex flex-wrap items-center gap-1.5">
                    <span class="text-[11px] font-semibold text-[var(--muted)]">订阅来源</span>
                    <button
                      v-for="option in subscribeProviderOptions"
                      :key="option.providerId"
                      type="button"
                      class="rounded-md border px-2 py-0.5 text-[11px] font-semibold transition"
                      :class="subscribeProviders.includes(option.providerId)
                        ? 'border-[var(--accent)] bg-[var(--accent)] text-[var(--accent-contrast)]'
                        : 'border-[var(--line)] bg-[var(--card)] text-[var(--ink)] hover:border-[var(--accent)]'"
                      :aria-pressed="subscribeProviders.includes(option.providerId)"
                      @click="toggleSubscribeProvider(option.providerId)"
                    >
                      {{ option.displayName }}
                    </button>
                  </div>
                </div>

                <div v-if="infoExpanded" class="grid gap-2.5">
                  <p v-if="animeSummary" class="whitespace-pre-line text-xs leading-5 text-[var(--muted)]">{{ animeSummary }}</p>
                  <p v-if="animeAliases.length > 1" class="text-xs text-[var(--muted)]">
                    别名：{{ animeAliases.join(" / ") }}
                  </p>
                  <div v-if="animeTags.length" class="flex flex-wrap gap-1.5">
                    <span
                      v-for="tag in animeTags"
                      :key="tag"
                      class="rounded bg-[var(--tint-2)] px-1.5 py-0.5 text-[11px] font-semibold text-[var(--muted)]"
                    >
                      {{ tag }}
                    </span>
                  </div>
                  <div v-if="sourceLinks.length" class="flex flex-wrap items-center gap-x-3 gap-y-1 text-xs">
                    <button
                      v-for="link in sourceLinks"
                      :key="link.label"
                      type="button"
                      class="inline-flex items-center gap-1 font-semibold text-[var(--accent)] hover:underline"
                      :title="link.url"
                      :aria-label="`在浏览器打开 ${link.label}`"
                      @click="emit('open-link', link.url)"
                    >
                      {{ link.label }}
                      <AppNavIcon name="external" class="size-3" aria-hidden="true" />
                    </button>
                  </div>
                  <p v-if="platformFailureNotice" class="text-[11px] text-[var(--muted)]" role="status">
                    {{ platformFailureNotice }}
                  </p>
                </div>
              </header>

              <!-- 宽屏时头部不动、只有剧集列表自己滚，标题行始终看得见 -->
              <section class="episode-panel overflow-hidden rounded-lg border border-[var(--line)] bg-[var(--card)] shadow-[var(--soft-shadow)]">
                <div class="flex shrink-0 items-center justify-between gap-3 border-b border-[var(--line)] px-3 py-2">
                  <h3 class="text-sm font-semibold text-[var(--ink)]">剧集</h3>
                  <span v-if="animeDetail.totalEpisodes || episodes.length" class="text-xs font-semibold text-[var(--muted)]">
                    全 {{ animeDetail.totalEpisodes || episodes.length }} 集
                  </span>
                </div>

                <ul class="episode-list divide-y divide-[var(--line)]">
                  <li>
                    <button
                      type="button"
                      class="episode-row"
                      :class="selectedDownloadGroupKey === null ? 'episode-row-active' : 'hover:bg-[var(--tint-2)]'"
                      :aria-current="selectedDownloadGroupKey === null"
                      @click="selectDownloadGroup(null)"
                    >
                      <span class="grid min-w-0 gap-0.5">
                        <strong class="truncate text-[13px] font-semibold text-[var(--ink)]">全部 RSS</strong>
                        <span class="text-[11px] text-[var(--muted)]">不限剧集</span>
                      </span>
                      <span class="episode-count text-[var(--accent-vivid)]">{{ animeDetail.downloads.length }} 个资源</span>
                    </button>
                  </li>
                  <li v-for="episode in episodes" :key="episode.episodeId">
                    <button
                      type="button"
                      class="episode-row"
                      :class="[
                        selectedDownloadGroupKey === `episode:${episode.episodeId}` ? 'episode-row-active' : '',
                        getEpisodeDownloadCount(episode) > 0
                          ? (selectedDownloadGroupKey === `episode:${episode.episodeId}` ? '' : 'hover:bg-[var(--tint-2)]')
                          : 'cursor-not-allowed opacity-55'
                      ]"
                      :aria-current="selectedDownloadGroupKey === `episode:${episode.episodeId}`"
                      :disabled="getEpisodeDownloadCount(episode) === 0"
                      :title="episodeTitleOf(episode) ?? undefined"
                      @click="selectDownloadGroup(`episode:${episode.episodeId}`)"
                    >
                      <span class="grid min-w-0 gap-0.5">
                        <span class="truncate text-[13px] text-[var(--ink)]">
                          <strong class="font-semibold">{{ episode.episodeNumber ? `第 ${episode.episodeNumber} 集` : "SP" }}</strong>
                          <template v-if="episodeTitleOf(episode)"> · {{ episodeTitleOf(episode) }}</template>
                        </span>
                        <span class="text-[11px] tabular-nums text-[var(--muted)]">{{ episode.airDate ?? "未定档" }}</span>
                      </span>
                      <span
                        class="episode-count"
                        :class="getEpisodeDownloadCount(episode) > 0 ? 'text-[var(--accent-vivid)]' : 'text-[var(--muted)]'"
                      >
                        {{ getEpisodeDownloadCount(episode) > 0 ? `${getEpisodeDownloadCount(episode)} 个资源` : "暂无" }}
                      </span>
                    </button>
                  </li>
                  <li v-if="unmatchedDownloadGroup">
                    <button
                      type="button"
                      class="episode-row"
                      :class="selectedDownloadGroupKey === 'unmatched' ? 'episode-row-active' : 'hover:bg-[var(--tint-2)]'"
                      :aria-current="selectedDownloadGroupKey === 'unmatched'"
                      @click="selectDownloadGroup('unmatched')"
                    >
                      <span class="grid min-w-0 gap-0.5">
                        <strong class="truncate text-[13px] font-semibold text-[var(--ink)]">未匹配剧集</strong>
                        <span class="text-[11px] text-[var(--muted)]">没有匹配到 Bangumi 集数的资源</span>
                      </span>
                      <span class="episode-count text-[var(--accent-vivid)]">{{ unmatchedDownloadGroup.downloads.length }} 个资源</span>
                    </button>
                  </li>
                </ul>
              </section>
            </div>

            <!-- 右栏：选中剧集的资源。窄屏只在资源视图出现，顶上带返回 -->
            <div class="detail-main" :class="{ 'narrow-hidden': narrowView === 'episodes' }">
              <div class="flex lg:hidden">
                <button
                  type="button"
                  class="btn-ghost inline-flex shrink-0 items-center gap-1 px-2.5 py-1.5 text-xs font-semibold"
                  @click="narrowView = 'episodes'"
                >
                  <AppNavIcon name="chevron-left" class="size-3.5" aria-hidden="true" />
                  剧集列表
                </button>
              </div>
              <MikanResourcePane
                class="detail-resources"
                :selected-download-group="selectedDownloadGroup"
                :scoped-downloads="scopedDownloads"
                :scoped-filtered-downloads="scopedFilteredDownloads"
                :scoped-subgroup-options="scopedSubgroupOptions"
                :scoped-resolution-options="scopedResolutionOptions"
                :scoped-provider-options="scopedProviderOptions"
                :selected-subgroup="selectedSubgroup"
                :selected-resolution="selectedResolution"
                :selected-provider="selectedProvider"
                :failed-providers="failedProviders"
                :pending-providers="pendingProviders"
                :provider-names="providerNames"
                :busy-key="busyKey"
                :is-download-busy="isDownloadBusy"
                :is-download-queued="isDownloadQueued"
                @select-subgroup="emit('select-subgroup', $event)"
                @select-resolution="emit('select-resolution', $event)"
                @select-provider="emit('select-provider', $event)"
                @queue-download="emit('queue-download', $event)"
              />
            </div>
          </div>
        </template>
      </div>
    </template>
    <template v-else>
      <div class="grid h-full min-h-0 place-items-center p-8 text-center">
        <div class="grid max-w-xs place-items-center gap-2 text-[var(--muted)]">
          <AppNavIcon name="home" class="size-7" />
          <strong class="text-sm font-medium text-[var(--ink)]">未选择番剧</strong>
          <span class="text-sm leading-6">在左侧列表点开一部番剧，这里会显示简介、剧集与下载资源。</span>
        </div>
      </div>
    </template>
  </RightDetailPane>
</template>

<style scoped>
/**
 * 宽屏（≥1024px）：左栏固定宽度自己滚，右栏资源吃掉剩余宽高、在内部滚动（筛选头固定在上面）。
 * 窄屏：一次只显示一栏，整页一起滚——否则资源只剩一两行可视高度。
 */
.detail-split {
  display: grid;
  grid-template-columns: minmax(0, 1fr);
  align-content: start;
  gap: 0.75rem;
  height: 100%;
  min-height: 0;
  overflow: auto;
  padding: 0.75rem;
}

.detail-side,
.detail-main {
  display: flex;
  flex-direction: column;
  gap: 0.75rem;
  min-width: 0;
}

.detail-resources,
.episode-panel {
  flex: 0 0 auto;
}

/* 窄屏一次只显示一栏。写在 scoped 样式里：Tailwind 的 max-lg:hidden 压不过上面的 display:flex */
@media (max-width: 1023.98px) {
  .narrow-hidden {
    display: none;
  }
}

@media (min-width: 1024px) {
  .detail-split {
    /* 左栏随窗口伸缩：1024px 窗口里给右栏多留出一百多像素，宽屏也不超过 23rem */
    grid-template-columns: clamp(19rem, 34%, 23rem) minmax(0, 1fr);
    align-content: stretch;
    overflow: hidden;
  }

  .detail-side {
    min-height: 0;
    overflow: auto;
    scrollbar-width: thin;
  }

  .detail-main {
    min-height: 0;
  }

  .episode-panel {
    display: flex;
    flex: 1 1 auto;
    flex-direction: column;
    /* 展开简介后头部很高：剧集至少留几行，剩下交给左栏整体滚动 */
    min-height: 14rem;
  }

  .episode-list {
    min-height: 0;
    overflow: auto;
    scrollbar-width: thin;
  }

  .detail-resources {
    flex: 1 1 auto;
    min-height: 0;
  }
}

.episode-row {
  display: grid;
  grid-template-columns: minmax(0, 1fr) auto;
  align-items: center;
  gap: 0.75rem;
  width: 100%;
  padding: 0.5rem 0.75rem;
  text-align: left;
  transition: background-color 120ms;
}

.episode-row-active {
  background: var(--accent-soft);
  box-shadow: inset 3px 0 0 var(--accent);
}

.episode-count {
  white-space: nowrap;
  font-size: 0.75rem;
  font-weight: 600;
}

.skeleton-pulse {
  position: relative;
  overflow: hidden;
}

.skeleton-pulse::after {
  position: absolute;
  inset: 0;
  content: "";
  transform: translateX(-100%);
  background: linear-gradient(90deg, transparent, rgba(255, 255, 255, 0.14), transparent);
  animation: skeleton-shimmer 1.35s ease-in-out infinite;
}

@keyframes skeleton-shimmer {
  100% {
    transform: translateX(100%);
  }
}
</style>
