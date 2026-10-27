<script setup lang="ts">
/**
 * 番剧详情：接管整个内容区，任何宽度都是一栏从上往下：
 *
 * - 头部：返回 / 封面 / 标题 / 元信息 / 简介 / 收藏与订阅
 * - 选集格子：「全部」+ 每集一格，没资源的集变灰
 * - 资源：当前选中剧集（默认「全部」）的资源
 *
 * 以前宽屏是左右两栏：左栏 20rem 塞头部和竖排剧集列表，右栏放资源。
 * 资源少时右边是一大块空框，左边又挤，所以改成和窄屏同一套。
 * 简介 / 标签 / 别名 / 外链收进「简介与标签」默认折叠。
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

/** 头部大号评分：第一个有分的平台（通常是 Bangumi），其余平台在下面一行小字列出 */
const primaryScore = computed(() => {
  const platform = (props.animeDetail?.platforms ?? []).find(
    (item) => typeof item.score === "number" && Number.isFinite(item.score) && item.score > 0
  );
  if (platform) {
    const max = platform.scoreMax ?? 10;
    return {
      label: metadataPlatformLabel(platform.id) || platform.displayName,
      value: max === 100 ? String(Math.round(platform.score as number)) : (platform.score as number).toFixed(1),
      max,
      rank: typeof platform.rank === "number" && platform.rank > 0 ? platform.rank : props.animeDetail?.rank ?? null,
      id: platform.id
    };
  }
  const rating = props.animeDetail?.anime.rating;
  if (typeof rating === "number" && rating > 0) {
    return { label: "Bangumi", value: rating.toFixed(1), max: 10, rank: props.animeDetail?.rank ?? null, id: "bangumi" };
  }
  return null;
});
const secondaryPlatformRows = computed(() => platformRows.value.filter((row) => row.id !== primaryScore.value?.id));

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

/**
 * Go 后端对所有源（含蜜柑）都按关键词建搜索 RSS，不需要蜜柑番剧 ID。
 * 以前这里要求蜜柑必须有 mikanId，而后端从不填它，默认只开蜜柑时订阅按钮永远是灰的。
 */
const canSubscribe = computed(() => !props.subscriptionId && subscribeProviders.value.length > 0);
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
                <div class="detail-poster shrink-0 skeleton-pulse"></div>
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
            <!-- 左栏：头部 + 剧集。窄屏只留头部，剧集改成资源上方的一排横向切换 -->
            <div class="detail-side">
              <!--
                头部：封面放大做主角，背后铺一层同图的模糊底，评分用大号数字。
                以前封面只有 56×80、简介默认收起，整块看着像一张表单。
              -->
              <header class="detail-hero shrink-0">
                <div
                  v-if="animeDetail.anime.imageUrl"
                  class="detail-hero-backdrop"
                  :style="{ backgroundImage: `url(${animeDetail.anime.imageUrl})` }"
                  aria-hidden="true"
                ></div>

                <div class="relative grid gap-3 p-3 sm:p-4">
                  <div class="flex items-start gap-2">
                    <button type="button" class="icon-btn shrink-0" aria-label="返回列表" title="返回列表" @click="emit('close')">
                      <AppNavIcon name="chevron-left" />
                    </button>
                    <h2 class="line-clamp-2 min-w-0 flex-1 pt-1 text-[17px] font-bold leading-snug text-[var(--ink)]" :title="animeDetail.anime.title">
                      {{ animeDetail.anime.title }}
                    </h2>
                  </div>

                  <div class="grid grid-cols-[auto_minmax(0,1fr)] items-start gap-3 sm:gap-4">
                    <div class="detail-poster">
                      <img
                        v-if="animeDetail.anime.imageUrl"
                        :src="animeDetail.anime.imageUrl"
                        :alt="animeDetail.anime.title"
                        class="block size-full object-cover"
                      />
                      <span v-else>番</span>
                    </div>

                    <div class="grid min-w-0 content-start gap-2">
                      <p v-if="animeAliases.length" class="line-clamp-2 text-xs leading-snug text-[var(--muted)]" :title="animeAliases.join(' / ')">
                        {{ animeAliases[0] }}
                      </p>

                      <div v-if="metaParts.length" class="flex flex-wrap gap-1" :title="animeBroadcastLabel ? '放送时间为本地时间' : undefined">
                        <span v-for="part in metaParts" :key="part" class="detail-chip">{{ part }}</span>
                      </div>

                      <button
                        v-if="primaryScore"
                        type="button"
                        class="grid justify-items-start gap-0.5 text-left"
                        :title="`在浏览器打开 ${primaryScore.label}`"
                        @click="platformRows.find((row) => row.id === primaryScore?.id) && emit('open-link', platformRows.find((row) => row.id === primaryScore?.id)!.url)"
                      >
                        <span class="flex items-baseline gap-1">
                          <strong class="text-2xl font-bold leading-none tabular-nums text-[var(--accent-vivid)]">{{ primaryScore.value }}</strong>
                          <span class="text-xs font-semibold text-[var(--muted)]">/ {{ primaryScore.max }}</span>
                        </span>
                        <span class="text-[11px] font-medium text-[var(--muted)]">
                          {{ primaryScore.label }}<template v-if="primaryScore.rank"> · 排名 #{{ primaryScore.rank }}</template>
                        </span>
                      </button>
                      <span v-else class="text-xs text-[var(--muted)]">暂无评分</span>

                      <div v-if="secondaryPlatformRows.length" class="flex flex-wrap gap-x-3 gap-y-0.5 text-[11px]">
                        <button
                          v-for="row in secondaryPlatformRows"
                          :key="row.id"
                          type="button"
                          class="inline-flex items-center gap-1 text-[var(--muted)] transition hover:text-[var(--ink)]"
                          :title="row.scoreCount ? `${row.label}：${row.scoreCount} 人评分` : row.url"
                          :aria-label="`在浏览器打开 ${row.label}`"
                          @click="emit('open-link', row.url)"
                        >
                          {{ row.label }}
                          <strong v-if="row.score" class="font-semibold text-[var(--ink)]">{{ row.score }}</strong>
                          <span v-else>暂无</span>
                        </button>
                      </div>
                    </div>
                  </div>

                  <!-- 简介默认露三行：番剧讲什么是打开详情最想先看到的，不该藏在折叠里 -->
                  <div v-if="animeSummary || animeTags.length" class="grid gap-2">
                    <p
                      v-if="animeSummary"
                      class="whitespace-pre-line text-xs leading-5 text-[var(--ink-secondary)]"
                      :class="{ 'line-clamp-3': !infoExpanded }"
                    >{{ animeSummary }}</p>
                    <div v-if="infoExpanded && animeTags.length" class="flex flex-wrap gap-1">
                      <span v-for="tag in animeTags" :key="tag" class="detail-chip">{{ tag }}</span>
                    </div>
                    <div v-if="infoExpanded" class="grid gap-1.5">
                      <p v-if="animeAliases.length > 1" class="text-[11px] text-[var(--muted)]">别名：{{ animeAliases.join(" / ") }}</p>
                      <div v-if="sourceLinks.length" class="flex flex-wrap items-center gap-x-3 gap-y-1 text-xs">
                        <button
                          v-for="link in sourceLinks"
                          :key="link.label"
                          type="button"
                          class="inline-flex items-center gap-1 font-semibold text-[var(--accent-vivid)] hover:underline"
                          :title="link.url"
                          :aria-label="`在浏览器打开 ${link.label}`"
                          @click="emit('open-link', link.url)"
                        >
                          {{ link.label }}
                          <AppNavIcon name="external" class="size-3" aria-hidden="true" />
                        </button>
                      </div>
                      <p v-if="platformFailureNotice" class="text-[11px] text-[var(--muted)]" role="status">{{ platformFailureNotice }}</p>
                    </div>
                    <button
                      v-if="hasExtraInfo"
                      type="button"
                      class="inline-flex items-center gap-1 justify-self-start text-xs font-semibold text-[var(--muted)] transition hover:text-[var(--ink)]"
                      :aria-expanded="infoExpanded"
                      @click="infoExpanded = !infoExpanded"
                    >
                      {{ infoExpanded ? "收起" : "展开简介与标签" }}
                      <AppNavIcon name="chevron-right" class="size-3.5 transition-transform" :class="infoExpanded ? '-rotate-90' : 'rotate-90'" aria-hidden="true" />
                    </button>
                  </div>

                  <!-- 订阅是这页真正的主操作（自动追更），收藏只是记一笔，两者不同权重 -->
                  <!-- 宽屏不让主按钮横贯整页：一千多像素宽的按钮读起来像横幅，不像按钮 -->
                  <div class="grid grid-cols-[auto_minmax(0,1fr)] gap-2 sm:max-w-md">
                    <button
                      v-if="!isFavorite"
                      type="button"
                      class="btn-ghost inline-flex h-9 items-center justify-center gap-1.5 whitespace-nowrap px-3 text-[13px] font-semibold"
                      @click="emit('favorite', { anime: animeDetail.anime, mikanKeyword: animeDetail.mikanKeyword })"
                    >
                      <AppNavIcon name="favorites" class="size-4" aria-hidden="true" />
                      收藏
                    </button>
                    <button
                      v-else
                      type="button"
                      class="inline-flex h-9 items-center justify-center gap-1.5 whitespace-nowrap rounded-lg border border-[var(--accent)] bg-[var(--accent-soft)] px-3 text-[13px] font-semibold text-[var(--accent-vivid)] transition"
                      :aria-label="`取消收藏 ${animeDetail.anime.title}`"
                      @click="emit('remove-favorite', animeDetail.anime.animeId)"
                    >
                      <AppNavIcon name="favorites" class="size-4" aria-hidden="true" />
                      已收藏
                    </button>
                    <button
                      type="button"
                      class="inline-flex h-9 items-center justify-center gap-1.5 whitespace-nowrap px-3 text-[13px] font-semibold"
                      :class="subscriptionId ? 'btn-ghost' : 'btn-primary'"
                      :disabled="!canSubscribe"
                      :title="subscriptionId ? '已在追更：新集数会自动加入下载队列，可在订阅页调整规则' : '订阅后持续抓取该番 RSS，新集数自动加入下载队列'"
                      @click="emit('subscribe', { anime: animeDetail.anime, mikanKeyword: animeDetail.mikanKeyword, providerIds: subscribeProviders })"
                    >
                      <AppNavIcon name="subscriptions" class="size-4" aria-hidden="true" />
                      {{ subscriptionId ? "已订阅" : "订阅并自动追更" }}
                    </button>
                  </div>

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
              </header>

            </div>

            <!--
              右栏：选中剧集的资源。窄屏紧跟在头部下面——资源才是这一页要找的东西，
              以前窄屏得先在剧集列表里点一下才看得到，很多人根本没发现。
            -->
            <div class="detail-main">
              <div class="episode-chips" role="group" aria-label="按剧集筛选资源">
                <button
                  type="button"
                  class="episode-chip episode-chip-wide"
                  :class="{ 'is-active': selectedDownloadGroupKey === null }"
                  :aria-pressed="selectedDownloadGroupKey === null"
                  @click="selectDownloadGroup(null)"
                >
                  全部 <span class="tabular-nums opacity-70">{{ animeDetail.downloads.length }}</span>
                </button>
                <button
                  v-for="episode in episodes"
                  :key="episode.episodeId"
                  type="button"
                  class="episode-chip"
                  :class="{ 'is-active': selectedDownloadGroupKey === `episode:${episode.episodeId}` }"
                  :aria-pressed="selectedDownloadGroupKey === `episode:${episode.episodeId}`"
                  :disabled="getEpisodeDownloadCount(episode) === 0"
                  :title="`${episode.episodeNumber ? `第 ${episode.episodeNumber} 集` : 'SP'}${episodeTitleOf(episode) ? ` · ${episodeTitleOf(episode)}` : ''} · ${getEpisodeDownloadCount(episode) ? `${getEpisodeDownloadCount(episode)} 个资源` : '暂无资源'}`"
                  :aria-label="`${episode.episodeNumber ? `第 ${episode.episodeNumber} 集` : 'SP'}，${getEpisodeDownloadCount(episode) ? `${getEpisodeDownloadCount(episode)} 个资源` : '暂无资源'}`"
                  @click="selectDownloadGroup(`episode:${episode.episodeId}`)"
                >
                  {{ episode.episodeNumber ?? "SP" }}
                </button>
                <button
                  v-if="unmatchedDownloadGroup"
                  type="button"
                  class="episode-chip episode-chip-wide"
                  :class="{ 'is-active': selectedDownloadGroupKey === 'unmatched' }"
                  :aria-pressed="selectedDownloadGroupKey === 'unmatched'"
                  @click="selectDownloadGroup('unmatched')"
                >
                  未匹配 <span class="tabular-nums opacity-70">{{ unmatchedDownloadGroup.downloads.length }}</span>
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
 * 一栏整页滚动：头部、选集、资源依次往下排。
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

.detail-resources {
  flex: 0 0 auto;
}

/*
 * 选集面板：像视频站那样一格一个集数、自动换行，一屏看到全部剧集。
 * 以前是横向滚动的「第 N 集 n」胶囊，手机上一次只露出三集。资源数放进 title / aria-label。
 */
.episode-chips {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(2.75rem, 1fr));
  gap: 6px;
}
.episode-chip {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 4px;
  height: 34px;
  padding: 0 6px;
  border-radius: 8px;
  background: var(--panel);
  color: var(--ink-secondary);
  font-size: 12.5px;
  font-weight: 550;
  white-space: nowrap;
  transition: background 0.15s, color 0.15s;
}
.episode-chip:hover:not(:disabled):not(.is-active) {
  color: var(--ink);
}
.episode-chip.is-active {
  background: var(--accent);
  color: var(--accent-contrast);
}
.episode-chip:disabled {
  cursor: not-allowed;
  opacity: 0.4;
}
/* 「全部 N」「未匹配 N」比集数格子宽，占两格 */
.episode-chip-wide {
  grid-column: span 2;
}

/* 头部：封面做主角，背后同图模糊铺底，像海报墙那样带出这部番的配色 */
.detail-hero {
  position: relative;
  overflow: hidden;
  border-radius: 0.75rem;
  border: 1px solid var(--line);
  background: var(--card);
  box-shadow: var(--soft-shadow);
}

.detail-hero-backdrop {
  position: absolute;
  inset: -2rem;
  background-position: center;
  background-size: cover;
  filter: blur(36px) saturate(1.3);
  opacity: 0.28;
  pointer-events: none;
}

.detail-hero-backdrop::after {
  position: absolute;
  inset: 0;
  content: "";
  background: linear-gradient(to bottom, transparent 0%, var(--card) 85%);
}

.detail-poster {
  display: grid;
  place-items: center;
  width: 7rem;
  aspect-ratio: 3 / 4;
  overflow: hidden;
  border-radius: 0.625rem;
  background: var(--panel);
  box-shadow: 0 8px 24px -10px rgb(0 0 0 / 0.55), 0 0 0 1px var(--line);
  color: var(--muted);
  font-size: 1.5rem;
  font-weight: 600;
}

/* 窄屏单栏时横向空间足，封面再大一号 */
@media (min-width: 640px) and (max-width: 1023.98px) {
  .detail-poster {
    width: 9rem;
  }
}

@media (min-width: 1280px) {
  .detail-poster {
    width: 7.5rem;
  }
}

.detail-chip {
  border-radius: 999px;
  background: var(--tint-3);
  padding: 0.0625rem 0.5rem;
  color: var(--ink-secondary);
  font-size: 11px;
  font-weight: 600;
  line-height: 1.25rem;
  white-space: nowrap;
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
