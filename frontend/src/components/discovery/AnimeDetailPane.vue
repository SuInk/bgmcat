<script setup lang="ts">
/**
 * 番剧详情页（主流媒体单流式上下布局 — 对齐 Apple TV / B站番剧页 / Infuse）：
 * - 通栏影视级头部：柔和海报氛围背光、一行完整大标题、圆点分隔元信息、两行高可读简介、右侧豆瓣/B站式无框星级评分
 * - 干净操作区：「订阅自动追更」+「收藏」+ 收纳式「订阅源下拉菜单」（告别满屏胶囊块）
 * - 选集优先横向条：默认聚焦最新一话（带「最新」角标），一键切「全部」，未开播剧集折叠在右侧
 * - 下方衔接单行筛选工具栏与清爽细分割线资源表
 */
import { computed, ref, watch } from "vue";

import type {
  AnimeDetail,
  AnimeStreamingLink,
  AnimeSubscriptionOptions,
  DownloadJob,
  MikanDownloadItem
} from "@shared/index";

import { getAnimeStreamingLinks } from "../../api";
import { airDayLabel, broadcastLabel, broadcastWeekday } from "../../lib/format";
import { formatPlatformScore, metadataPlatformLabel } from "../../lib/metadataPlatform";
import {
  buildMikanCountOptions,
  extractResolutionLabel,
  type MikanDownloadGroup,
  providerIdOf,
  providerIdsOf,
  resolveSubgroupKey
} from "../../lib/mikan";
import { settings } from "../../stores/session";
import { subscriptions } from "../../stores/subscriptions";
import CuteLoader from "../common/CuteLoader.vue";
import RightDetailPane from "../common/RightDetailPane.vue";
import AppNavIcon from "../layout/AppNavIcon.vue";
import AnimeSubscribeDialog from "./AnimeSubscribeDialog.vue";
import MikanResourcePane from "./MikanResourcePane.vue";

export type { MikanDownloadGroup };

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
  detailLoading?: boolean;
  isDownloadBusy?: (item: MikanDownloadItem) => boolean;
  isSubscribeBusy?: boolean;
  isHidden?: boolean;
}>();

const emit = defineEmits<{
  (event: "close"): void;
  (event: "favorite", payload: { anime: AnimeDetail["anime"]; mikanKeyword?: string }): void;
  (event: "remove-favorite", animeId: string): void;
  (event: "toggle-hidden", anime: AnimeDetail["anime"]): void;
  (
    event: "subscribe",
    payload: {
      anime: AnimeDetail["anime"];
      mikanKeyword?: string;
      providerIds?: string[];
      options?: AnimeSubscriptionOptions;
    }
  ): void;
  (event: "unsubscribe", animeId: string): void;
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
const showContentLoading = computed(() => detailLoading.value && !props.animeDetail?.anime.title);
const showFullPaneError = computed(() => (props.animeDetail ? null : props.detailError));
const showContentError = computed(() => (props.animeDetail ? props.detailError : null));

const detailScrollRef = ref<HTMLElement | null>(null);
const selectedDownloadGroupKey = ref<string | null>(null);
const userManuallyPickedScope = ref(false);
const subscribeDialogOpen = ref(false);

const allAnimeTitles = computed(() => {
  const primary = props.animeDetail?.anime.title ?? "";
  return [primary, ...(props.animeDetail?.titles ?? [])].filter(Boolean);
});

const animeAliases = computed(() => {
  const primary = props.animeDetail?.anime.title ?? "";
  return (props.animeDetail?.titles ?? []).filter((title) => title && title !== primary);
});
const animeSummary = computed(() => props.animeDetail?.summary?.trim() || null);
const animeTags = computed(() => props.animeDetail?.tags ?? []);

const resolvedStreamingState = ref<{ animeId: string; links: AnimeStreamingLink[] }>({
  animeId: "",
  links: []
});

watch(
  () => props.animeDetail?.anime.animeId ?? "",
  (animeId) => {
    if (!animeId) {
      resolvedStreamingState.value = { animeId: "", links: [] };
      return;
    }
    if (resolvedStreamingState.value.animeId !== animeId) {
      resolvedStreamingState.value = { animeId, links: [] };
    }
    void getAnimeStreamingLinks(animeId)
      .then((links) => {
        if ((props.animeDetail?.anime.animeId ?? "") === animeId) {
          resolvedStreamingState.value = { animeId, links };
        }
      })
      .catch(() => {
        // 静默降级为详情自身已提取的播放链接
      });
  },
  { immediate: true }
);

const activeStreamingLinks = computed<AnimeStreamingLink[]>(() => {
  const detail = props.animeDetail;
  if (!detail) {
    return [];
  }
  const resolved =
    resolvedStreamingState.value.animeId === detail.anime.animeId ? resolvedStreamingState.value.links : [];
  if (resolved.length) {
    return resolved;
  }
  return detail.streamingLinks ?? [];
});

const heroStreamingButtons = computed<AnimeStreamingLink[]>(() => {
  if (activeStreamingLinks.value.length) {
    return activeStreamingLinks.value;
  }
  const title = props.animeDetail?.anime.title?.trim() ?? "";
  if (!title) {
    return [];
  }
  return [
    {
      id: "bilibili-search",
      label: "B 站搜索",
      url: `https://search.bilibili.com/all?keyword=${encodeURIComponent(title)}`,
      direct: false
    }
  ];
});

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
    const base = (settings.value?.mikanBaseUrl || "https://mikanani.me")
      .replace(/\/+$/, "")
      .replace(/^https?:\/\/mikanime\.tv$/, "https://mikanani.me");
    links.push({ label: "蜜柑", url: `${base}/Home/Bangumi/${detail.anime.mikanId}` });
  }
  if (detail.anime.officialSite) {
    links.push({ label: "官网", url: detail.anime.officialSite });
  }
  return links;
});

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

const primaryScore = computed(() => {
  const platform = (props.animeDetail?.platforms ?? []).find(
    (item) => typeof item.score === "number" && Number.isFinite(item.score) && item.score > 0
  );
  if (platform) {
    const max = platform.scoreMax ?? 10;
    const raw = platform.score as number;
    return {
      label: metadataPlatformLabel(platform.id) || platform.displayName,
      value: max === 100 ? String(Math.round(raw)) : raw.toFixed(1),
      numeric: raw,
      max,
      rank: typeof platform.rank === "number" && platform.rank > 0 ? platform.rank : props.animeDetail?.rank ?? null,
      id: platform.id
    };
  }
  const rating = props.animeDetail?.anime.rating;
  if (typeof rating === "number" && rating > 0) {
    return {
      label: "Bangumi",
      value: rating.toFixed(1),
      numeric: rating,
      max: 10,
      rank: props.animeDetail?.rank ?? null,
      id: "bangumi"
    };
  }
  return null;
});

/** 折算为 5 颗星的填充百分比数组 */
const starFillPercents = computed(() => {
  if (!primaryScore.value) {
    return [0, 0, 0, 0, 0];
  }
  const fiveScale = (primaryScore.value.numeric / primaryScore.value.max) * 5;
  return [1, 2, 3, 4, 5].map((index) => {
    const ratio = Math.max(0, Math.min(1, fiveScale - (index - 1)));
    return Math.round(ratio * 100);
  });
});

const secondaryPlatformRows = computed(() => platformRows.value.filter((row) => row.id !== primaryScore.value?.id));

const platformFailureNotice = computed(() => {
  const failures = props.animeDetail?.platformFailures ?? [];
  if (!failures.length) {
    return "";
  }
  return `${failures.map((failure) => failure.displayName).join("、")} 这一轮没响应，评分里少了它们。`;
});

const animeStartDate = computed(() => props.animeDetail?.episodes.find((episode) => episode.airDate)?.airDate ?? null);
const meaningfulTags = computed(() =>
  animeTags.value.filter((tag) => {
    const t = tag.trim();
    if (!t) return false;
    // 首播日期已单独显示时，剔除纯年份/年月重复标签（如 2026 / 2026年10月）
    if (animeStartDate.value && (/^\d{4}(?:年(?:\d{1,2}月)?)?$/u.test(t) || /^\d{1,2}月$/u.test(t))) {
      return false;
    }
    return true;
  })
);
const hasBottomIntro = computed(() =>
  Boolean(
    animeSummary.value ||
      animeAliases.value.length > 0 ||
      meaningfulTags.value.length > 3 ||
      platformFailureNotice.value
  )
);
const animeAirDayLabel = computed(() => {
  const weekday = broadcastWeekday(props.animeDetail?.anime.broadcastAt) ?? props.animeDetail?.anime.airDay;
  if (weekday === null || weekday === undefined) {
    return null;
  }
  return airDayLabel(weekday);
});
const animeBroadcastLabel = computed(() => broadcastLabel(props.animeDetail?.anime.broadcastAt));

const episodes = computed(() => props.animeDetail?.episodes ?? []);
const downloadGroupByKey = computed(() => {
  const groups = new Map<string, MikanDownloadGroup>();
  for (const group of props.pagedDownloadGroups) {
    groups.set(group.key, group);
  }
  return groups;
});
const batchDownloadGroup = computed(() => downloadGroupByKey.value.get("batch") ?? null);
const unmatchedDownloadGroup = computed(() => downloadGroupByKey.value.get("unmatched") ?? null);
const otherSeasonDownloadGroup = computed(() => downloadGroupByKey.value.get("other-season") ?? null);
const syntheticEpisodeGroups = computed(() =>
  props.pagedDownloadGroups.filter((group) => group.kind === "synthetic")
);

/**
 * 「全部」视图默认聚焦本季相关资源（正片 + 合集 + 番外），把混入的其他季度隔离到独立胶囊；
 * 若所有资源都在其他季度（极端情况），则回退展示全部以免空白。
 */
const currentSeasonDownloads = computed(() => {
  const all = props.animeDetail?.downloads ?? [];
  const other = otherSeasonDownloadGroup.value;
  if (!other || other.downloads.length === 0 || other.downloads.length >= all.length) {
    return all;
  }
  const otherIds = new Set(other.downloads.map((item) => item.id));
  return all.filter((item) => !otherIds.has(item.id));
});

const selectedDownloadGroup = computed(() => {
  if (!selectedDownloadGroupKey.value) {
    return null;
  }
  return downloadGroupByKey.value.get(selectedDownloadGroupKey.value) ?? null;
});
const scopedDownloads = computed(() => selectedDownloadGroup.value?.downloads ?? currentSeasonDownloads.value);
const scopedFilteredDownloads = computed(() =>
  scopedDownloads.value.filter((item) => {
    if (props.selectedSubgroup !== "all" && resolveSubgroupKey(item) !== props.selectedSubgroup) {
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
    (item) => resolveSubgroupKey(item),
    (key) => (key === "__unknown" ? "未标注字幕组" : key)
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

function getEpisodeDownloadCount(episode: AnimeDetail["episodes"][number]): number {
  return downloadGroupByKey.value.get(`episode:${episode.episodeId}`)?.downloads.length ?? 0;
}

function numericEp(value: string | null | undefined): number | null {
  if (!value) return null;
  const n = Number.parseInt(value, 10);
  return Number.isFinite(n) && n > 0 ? n : null;
}

const episodesWithDownloads = computed(() => episodes.value.filter((episode) => getEpisodeDownloadCount(episode) > 0));
const emptyEpisodes = computed(() => episodes.value.filter((episode) => getEpisodeDownloadCount(episode) === 0));

const latestEpisodeInfo = computed(() => {
  let bestKey: string | null = null;
  let bestNum = -1;
  for (const ep of episodesWithDownloads.value) {
    const n = numericEp(ep.episodeNumber) ?? 0;
    if (n >= bestNum) {
      bestNum = n;
      bestKey = `episode:${ep.episodeId}`;
    }
  }
  for (const syn of syntheticEpisodeGroups.value) {
    const n = syn.episodeNumber ?? 0;
    if (n > bestNum) {
      bestNum = n;
      bestKey = syn.key;
    }
  }
  return {
    key: bestKey,
    num: bestNum > 0 ? bestNum : null
  };
});
const latestAvailableEpisodeKey = computed(() => latestEpisodeInfo.value.key);
const latestEpisodeNumber = computed(() => latestEpisodeInfo.value.num);

const latestResourcePublishedLabel = computed(() => {
  const downloads = props.animeDetail?.downloads ?? [];
  let bestTs = 0;
  for (const item of downloads) {
    if (!item.publishedAt) continue;
    const ts = Date.parse(item.publishedAt);
    if (Number.isFinite(ts) && ts > bestTs) {
      bestTs = ts;
    }
  }
  if (!bestTs) {
    return null;
  }
  const date = new Date(bestTs);
  const now = new Date();
  const sameDay =
    date.getFullYear() === now.getFullYear() &&
    date.getMonth() === now.getMonth() &&
    date.getDate() === now.getDate();
  const hh = String(date.getHours()).padStart(2, "0");
  const mm = String(date.getMinutes()).padStart(2, "0");
  if (sameDay) {
    return `今天 ${hh}:${mm} 发布`;
  }
  const mo = String(date.getMonth() + 1).padStart(2, "0");
  const da = String(date.getDate()).padStart(2, "0");
  return `${mo}-${da} ${hh}:${mm} 发布`;
});

/** 头部绿点连载进度文案：如「周日更新 · 更新至第 6 话」 */
const airingStatusText = computed(() => {
  const day = animeAirDayLabel.value;
  const latest = latestEpisodeNumber.value;
  if (day && latest) {
    return `${day}更新 · 更新至第 ${latest} 话`;
  }
  if (latest) {
    return `更新至第 ${latest} 话`;
  }
  if (animeBroadcastLabel.value) {
    return animeBroadcastLabel.value;
  }
  if (day) {
    return `${day}更新`;
  }
  return null;
});

/** 圆点分隔的头部纯文本元信息段 */
const heroMetaTextSegments = computed(() => {
  const segments: string[] = [];
  const epCount = Math.max(
    episodes.value.length,
    props.animeDetail?.totalEpisodes ?? 0,
    props.animeDetail?.anime.totalEpisodes ?? 0,
    latestEpisodeNumber.value ?? 0
  );
  if (epCount > 0) {
    segments.push(`全 ${epCount} 话`);
  }
  if (latestResourcePublishedLabel.value) {
    segments.push(`最新源 ${latestResourcePublishedLabel.value}`);
  }
  const startDate = animeStartDate.value || props.animeDetail?.anime.airDate?.trim() || null;
  if (startDate) {
    segments.push(`${startDate} 首播`);
  }
  if (meaningfulTags.value.length > 0) {
    segments.push(meaningfulTags.value.slice(0, 3).join(" / "));
  }
  return segments;
});

const activeAnimeSubscriptions = computed(() => {
  const aid = props.animeDetail?.anime.animeId;
  if (!aid) {
    return [];
  }
  return subscriptions.value.filter((sub) => sub.animeId === aid);
});

const isAnimeSubscribed = computed(
  () => Boolean(props.subscriptionId) || activeAnimeSubscriptions.value.length > 0
);

const subscribedButtonLabel = computed(() => {
  if (!isAnimeSubscribed.value) {
    return "订阅追更";
  }
  const primary = activeAnimeSubscriptions.value[0];
  const locked = primary?.rule.lockedSubgroup?.trim() || primary?.rule.preferredSubgroups?.[0]?.trim();
  return locked ? `已追更 · ${locked}` : "已追更";
});

function handleConfirmSubscribe(payload: {
  anime: AnimeDetail["anime"];
  mikanKeyword?: string;
  providerIds: string[];
  options: AnimeSubscriptionOptions;
}): void {
  emit("subscribe", payload);
  subscribeDialogOpen.value = false;
}

function handleConfirmUnsubscribe(): void {
  if (!props.animeDetail) {
    return;
  }
  emit("unsubscribe", props.animeDetail.anime.animeId);
  subscribeDialogOpen.value = false;
}

const providerNames = computed<Record<string, string>>(() =>
  Object.fromEntries(
    (props.animeDetail?.providerStatuses ?? []).map((status) => [status.providerId, status.displayName])
  )
);
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

/**
 * 横向选集条（长篇番剧超过 100 话时支持按百话分段切换）
 */
const EPISODE_PAGE_SIZE = 100;
const showAllEpisodes = ref(false);
const episodeRangeIndex = ref(-1);
const canCollapseEpisodes = computed(
  () => episodesWithDownloads.value.length > 0 && emptyEpisodes.value.length > 0
);
const hasEpisodeRanges = computed(() => episodes.value.length > EPISODE_PAGE_SIZE);
const episodeRanges = computed(() => {
  const all = episodes.value;
  const ranges: Array<{ index: number; label: string; hasDownloads: boolean }> = [];
  for (let i = 0; i * EPISODE_PAGE_SIZE < all.length; i += 1) {
    const slice = all.slice(i * EPISODE_PAGE_SIZE, (i + 1) * EPISODE_PAGE_SIZE);
    const first = numericEp(slice[0]?.episodeNumber) ?? i * EPISODE_PAGE_SIZE + 1;
    const last = numericEp(slice[slice.length - 1]?.episodeNumber) ?? i * EPISODE_PAGE_SIZE + slice.length;
    ranges.push({
      index: i,
      label: `${first}–${last}`,
      hasDownloads: slice.some((ep) => getEpisodeDownloadCount(ep) > 0)
    });
  }
  return ranges;
});
const activeEpisodeRangeIndex = computed(() => {
  const totalRanges = episodeRanges.value.length;
  if (totalRanges === 0) {
    return 0;
  }
  if (episodeRangeIndex.value >= 0 && episodeRangeIndex.value < totalRanges) {
    return episodeRangeIndex.value;
  }
  for (let i = totalRanges - 1; i >= 0; i -= 1) {
    if (episodeRanges.value[i]?.hasDownloads) {
      return i;
    }
  }
  return totalRanges - 1;
});
const visibleEpisodes = computed(() => {
  if (canCollapseEpisodes.value && !showAllEpisodes.value) {
    return episodes.value.filter(
      (episode) =>
        getEpisodeDownloadCount(episode) > 0 || selectedDownloadGroupKey.value === `episode:${episode.episodeId}`
    );
  }
  if (hasEpisodeRanges.value) {
    const start = activeEpisodeRangeIndex.value * EPISODE_PAGE_SIZE;
    return episodes.value.slice(start, start + EPISODE_PAGE_SIZE);
  }
  return episodes.value;
});
const visibleSyntheticEpisodeGroups = computed(() => {
  if (
    !hasEpisodeRanges.value ||
    (canCollapseEpisodes.value && !showAllEpisodes.value) ||
    activeEpisodeRangeIndex.value === episodeRanges.value.length - 1
  ) {
    return syntheticEpisodeGroups.value;
  }
  return [];
});

function selectEpisodeRange(index: number): void {
  episodeRangeIndex.value = index;
}

function toggleShowAllEpisodes(): void {
  showAllEpisodes.value = !showAllEpisodes.value;
  if (showAllEpisodes.value && episodeRangeIndex.value < 0) {
    episodeRangeIndex.value = activeEpisodeRangeIndex.value;
  }
}

const collapsedEmptyLabel = computed(() => {
  const nums = emptyEpisodes.value
    .map((ep) => numericEp(ep.episodeNumber))
    .filter((n): n is number => n !== null)
    .sort((a, b) => a - b);
  if (nums.length >= 2) {
    const first = nums[0]!;
    const last = nums[nums.length - 1]!;
    const isTrailingUnaired = (latestEpisodeNumber.value ?? 0) > 0 && first > (latestEpisodeNumber.value ?? 0);
    return isTrailingUnaired
      ? `第 ${first}–${last} 话尚未开播`
      : `其余 ${emptyEpisodes.value.length} 话暂无资源`;
  }
  return `其余 ${emptyEpisodes.value.length} 话暂无资源`;
});

const showEpisodeStrip = computed(
  () =>
    episodesWithDownloads.value.length > 0 ||
    syntheticEpisodeGroups.value.length > 0 ||
    Boolean(batchDownloadGroup.value) ||
    Boolean(unmatchedDownloadGroup.value) ||
    Boolean(otherSeasonDownloadGroup.value)
);

function episodeGroupOf(episode: AnimeDetail["episodes"][number]): MikanDownloadGroup | null {
  return downloadGroupByKey.value.get(`episode:${episode.episodeId}`) ?? null;
}

function episodePillLabel(episode: AnimeDetail["episodes"][number]): string {
  const n = numericEp(episode.episodeNumber);
  const hasDual = Boolean(episodeSeasonHint(episode));
  if (n !== null) {
    return hasDual ? `${String(n).padStart(2, "0")} 话` : `第 ${String(n).padStart(2, "0")} 话`;
  }
  return episode.episodeNumber ? `第 ${episode.episodeNumber} 话` : "SP";
}

function episodeSeasonHint(episode: AnimeDetail["episodes"][number]): string | null {
  return episodeGroupOf(episode)?.seasonSubLabel ?? null;
}

function episodeTitleOf(episode: AnimeDetail["episodes"][number]): string | null {
  const title = episode.title?.trim();
  if (!title || /^第\s*\d+(\.\d+)?\s*[集话話]$/.test(title)) {
    return null;
  }
  return title;
}

function episodeTooltipOf(episode: AnimeDetail["episodes"][number]): string {
  const group = episodeGroupOf(episode);
  const parts = [episodePillLabel(episode)];
  if (group?.seasonSubLabel) {
    parts.push(`(${group.seasonSubLabel})`);
  }
  const title = episodeTitleOf(episode);
  if (title) {
    parts.push(title);
  }
  if (episode.airDate) {
    parts.push(`首播 ${episode.airDate}`);
  }
  const cnt = group?.downloads.length ?? 0;
  if (cnt > 0) {
    const sgCnt = group?.subgroupCount ?? 0;
    parts.push(sgCnt > 0 ? `${sgCnt} 个字幕组 / ${cnt} 条资源` : `${cnt} 条资源`);
  } else {
    parts.push("暂无资源");
  }
  return parts.join(" · ");
}

function selectDownloadGroup(key: string | null): void {
  userManuallyPickedScope.value = true;
  if (selectedDownloadGroupKey.value === key) {
    return;
  }
  selectedDownloadGroupKey.value = key;
  emit("select-subgroup", "all");
  emit("select-resolution", "all");
}

/**
 * 默认聚焦最新一话（带「最新」角标），若暂无可用分集资源则退回展示「全部」。
 */
function applyDefaultEpisodeFocus(): void {
  if (userManuallyPickedScope.value) {
    return;
  }
  selectedDownloadGroupKey.value = latestAvailableEpisodeKey.value ?? null;
}

watch(
  () => props.animeDetail?.anime.animeId,
  () => {
    detailScrollRef.value?.scrollTo({ top: 0 });
    userManuallyPickedScope.value = false;
    subscribeDialogOpen.value = false;
    showAllEpisodes.value = false;
    episodeRangeIndex.value = -1;
    applyDefaultEpisodeFocus();
  },
  { immediate: true }
);

watch(
  () => props.pagedDownloadGroups.map((group) => group.key).join("|"),
  () => {
    if (!userManuallyPickedScope.value) {
      applyDefaultEpisodeFocus();
      return;
    }
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
      <div class="relative flex h-full min-h-0 flex-col overflow-hidden">
        <!-- 骨架态（紧凑单流布局） -->
        <div v-if="showContentLoading" class="flex h-full flex-col gap-3 overflow-hidden">
          <div class="flex items-center justify-between border-b border-[var(--line)] pb-1.5">
            <button type="button" class="back-link" @click="emit('close')">
              <AppNavIcon name="chevron-left" class="size-3.5" />
              返回
            </button>
            <div class="h-3.5 w-28 rounded bg-[var(--line)]/60 skeleton-pulse"></div>
          </div>
          <div class="grid grid-cols-[84px_minmax(0,1fr)] items-start gap-3.5 sm:grid-cols-[96px_minmax(0,1fr)]">
            <div class="aspect-[2/3] w-full rounded-lg bg-[var(--line)]/65 skeleton-pulse"></div>
            <div class="grid gap-2">
              <div class="h-5 w-3/5 rounded bg-[var(--line)]/75 skeleton-pulse"></div>
              <div class="h-3.5 w-2/5 rounded bg-[var(--line)]/55 skeleton-pulse"></div>
              <div class="h-3.5 w-4/5 rounded bg-[var(--line)]/45 skeleton-pulse"></div>
              <div class="flex gap-1.5 pt-0.5">
                <div class="h-[30px] w-24 rounded-[7px] bg-[var(--accent-soft)] skeleton-pulse"></div>
                <div class="h-[30px] w-16 rounded-[7px] bg-[var(--line)]/55 skeleton-pulse"></div>
              </div>
            </div>
          </div>
          <div class="flex gap-1.5 border-y border-[var(--line)] py-2">
            <div v-for="i in 6" :key="i" class="h-[26px] w-14 rounded-[6px] bg-[var(--line)]/50 skeleton-pulse"></div>
          </div>
          <div class="grid gap-1.5 rounded-lg border border-[var(--line)] p-3">
            <div v-for="i in 5" :key="i" class="h-9 rounded bg-[var(--line)]/40 skeleton-pulse"></div>
          </div>
        </div>

        <!-- 错误态 -->
        <div v-else-if="showContentError" class="grid min-h-full content-start gap-3">
          <div class="top-nav">
            <button type="button" class="back-link" @click="emit('close')">
              <AppNavIcon name="chevron-left" class="size-3.5" />
              返回
            </button>
          </div>
          <div class="grid max-w-sm gap-2 justify-self-center rounded-lg border border-[var(--err-soft)] bg-[var(--err-soft)] px-4 py-4 text-center text-[var(--err)]">
            <strong class="text-sm font-semibold text-[var(--err)]">资源加载失败</strong>
            <p class="text-xs font-medium leading-5 text-[var(--err)]">{{ showContentError }}</p>
          </div>
        </div>

        <!-- 紧凑高密度单流式详情：头部 -> 选集与下载资源 -> 底部番剧简介 -->
        <template v-else>
          <div
            ref="detailScrollRef"
            class="detail-canvas"
          >
            <!-- 1. 顶部紧凑导航条 -->
            <div class="top-nav">
              <button type="button" class="back-link" aria-label="返回列表" @click="emit('close')">
                <AppNavIcon name="chevron-left" class="size-3.5 shrink-0" />
                <span>返回</span>
              </button>

              <div v-if="sourceLinks.length" class="flex flex-wrap items-center gap-1.5 text-xs font-medium text-[var(--muted)]">
                <button
                  v-for="link in sourceLinks"
                  :key="link.label"
                  type="button"
                  class="inline-flex min-h-8 items-center gap-1 rounded-lg px-2 py-1 transition hover:bg-[var(--panel)] hover:text-[var(--ink)]"
                  :title="link.url"
                  @click="emit('open-link', link.url)"
                >
                  <span>{{ link.label }}</span>
                  <AppNavIcon name="external" class="size-3 opacity-75" aria-hidden="true" />
                </button>
              </div>
            </div>

            <!-- 2. 紧凑头部：顶对齐小尺寸海报 + 单行元信息 + 单行操作条 -->
            <header class="hero-section">
              <div class="hero-poster">
                <img
                  v-if="animeDetail.anime.imageUrl"
                  :src="animeDetail.anime.imageUrl"
                  :alt="animeDetail.anime.title"
                  class="block size-full object-cover"
                />
                <span v-else>番</span>
              </div>

              <div class="flex min-w-0 flex-col gap-1.5">
                <div class="flex items-baseline justify-between gap-2">
                  <h2
                    class="min-w-0 text-[16px] font-bold leading-snug tracking-tight text-[var(--ink)] sm:truncate sm:text-[17px]"
                    :title="animeDetail.anime.title"
                  >
                    {{ animeDetail.anime.title }}
                  </h2>
                  <span
                    v-if="primaryScore"
                    class="inline-flex shrink-0 items-baseline gap-0.5 text-xs font-bold tabular-nums text-[var(--warn)] lg:hidden"
                    :title="`${primaryScore.label} 评分`"
                  >
                    ★ {{ primaryScore.value }}
                  </span>
                </div>

                <!-- 圆点分隔的紧凑元信息（圆点跟随后项，换行不悬空） -->
                <div class="meta-line">
                  <span v-if="airingStatusText" class="airing-badge">{{ airingStatusText }}</span>
                  <span
                    v-for="(seg, idx) in heroMetaTextSegments"
                    :key="seg"
                    class="meta-seg"
                    :class="{ 'has-dot': idx > 0 || Boolean(airingStatusText) }"
                  >{{ seg }}</span>
                </div>

                <!-- 紧凑单行操作栏 -->
                <div class="mt-1 flex flex-wrap items-center gap-1.5">
                  <button
                    type="button"
                    class="btn-cta"
                    :class="isAnimeSubscribed ? 'btn-ghost' : 'btn-primary'"
                    :disabled="isSubscribeBusy"
                    :title="isAnimeSubscribed ? '调整字幕组或取消追更' : '选择字幕组并开启自动追更'"
                    @click="subscribeDialogOpen = true"
                  >
                    <AppNavIcon name="subscriptions" class="size-3.5 shrink-0" aria-hidden="true" />
                    <span class="max-w-[11rem] truncate">{{ subscribedButtonLabel }}</span>
                  </button>

                  <button
                    v-if="!isFavorite"
                    type="button"
                    class="btn-fav-secondary"
                    @click="emit('favorite', { anime: animeDetail.anime, mikanKeyword: animeDetail.mikanKeyword })"
                  >
                    <AppNavIcon name="favorites" class="size-3.5 shrink-0" aria-hidden="true" />
                    <span>收藏</span>
                  </button>
                  <button
                    v-else
                    type="button"
                    class="btn-fav-secondary is-favorited"
                    :aria-label="`取消收藏 ${animeDetail.anime.title}`"
                    @click="emit('remove-favorite', animeDetail.anime.animeId)"
                  >
                    <AppNavIcon name="favorites" class="size-3.5 shrink-0" aria-hidden="true" />
                    <span>已收藏</span>
                  </button>

                  <button
                    type="button"
                    class="btn-fav-secondary"
                    :class="{ 'is-favorited': isHidden }"
                    :title="isHidden ? '恢复在首页显示' : '在首页隐藏'"
                    :aria-label="isHidden ? `恢复在首页显示 ${animeDetail.anime.title}` : `在首页隐藏 ${animeDetail.anime.title}`"
                    @click="emit('toggle-hidden', animeDetail.anime)"
                  >
                    <AppNavIcon :name="isHidden ? 'eye' : 'eye-off'" class="size-3.5 shrink-0" aria-hidden="true" />
                    <span>{{ isHidden ? "已隐藏" : "隐藏" }}</span>
                  </button>

                  <template v-if="heroStreamingButtons.length">
                    <span class="mx-0.5 hidden h-4 w-px bg-[var(--line)] sm:inline-block" aria-hidden="true" />
                    <button
                      v-for="stream in heroStreamingButtons"
                      :key="stream.id"
                      type="button"
                      class="btn-stream-link"
                      :class="{ 'is-direct': Boolean(stream.direct) }"
                      :title="stream.direct ? `直达 ${stream.label} 播放：${stream.url}` : `前往 ${stream.label}：${stream.url}`"
                      @click="emit('open-link', stream.url)"
                    >
                      <AppNavIcon name="play" class="size-3 shrink-0" aria-hidden="true" />
                      <span>{{ stream.label }}</span>
                      <AppNavIcon name="external" class="size-3 shrink-0 opacity-70" aria-hidden="true" />
                    </button>
                  </template>
                </div>

                <AnimeSubscribeDialog
                  v-if="subscribeDialogOpen"
                  :anime="animeDetail.anime"
                  :mikan-keyword="animeDetail.mikanKeyword"
                  :downloads="animeDetail.downloads"
                  :provider-statuses="animeDetail.providerStatuses"
                  :known-latest-episode="latestEpisodeNumber"
                  :initial-subgroup="selectedSubgroup"
                  :submitting="isSubscribeBusy"
                  @close="subscribeDialogOpen = false"
                  @confirm="handleConfirmSubscribe"
                  @unsubscribe="handleConfirmUnsubscribe"
                />
              </div>

              <!-- 桌面端右侧紧凑评分块 -->
              <div v-if="primaryScore" class="rating-panel">
                <button
                  type="button"
                  class="grid justify-items-start gap-0.5 text-left transition hover:opacity-80"
                  :title="`在浏览器打开 ${primaryScore.label}`"
                  @click="platformRows.find((row) => row.id === primaryScore?.id) && emit('open-link', platformRows.find((row) => row.id === primaryScore?.id)!.url)"
                >
                  <span class="text-[11px] font-medium text-[var(--muted)]">{{ primaryScore.label }}</span>
                  <span class="flex items-baseline gap-1">
                    <strong class="text-[24px] font-extrabold leading-none tabular-nums text-[var(--ink)]">{{ primaryScore.value }}</strong>
                    <span class="text-[11px] font-medium text-[var(--muted)]">/ {{ primaryScore.max }}</span>
                  </span>

                  <span class="flex items-center gap-0.5 pt-0.5" aria-hidden="true">
                    <span v-for="(pct, idx) in starFillPercents" :key="idx" class="relative inline-block size-3">
                      <svg class="size-3 text-[var(--tint-4)]" viewBox="0 0 24 24" fill="currentColor">
                        <path d="M12 2l3.09 6.26L22 9.27l-5 4.87 1.18 6.88L12 17.77l-6.18 3.25L7 14.14 2 9.27l6.91-1.01L12 2z" />
                      </svg>
                      <svg
                        v-if="pct > 0"
                        class="star-fill absolute inset-0 size-3"
                        :style="{ clipPath: `inset(0 ${100 - pct}% 0 0)` }"
                        viewBox="0 0 24 24"
                        fill="currentColor"
                      >
                        <path d="M12 2l3.09 6.26L22 9.27l-5 4.87 1.18 6.88L12 17.77l-6.18 3.25L7 14.14 2 9.27l6.91-1.01L12 2z" />
                      </svg>
                    </span>
                  </span>

                  <span v-if="primaryScore.rank" class="text-[11px] text-[var(--muted)]">#{{ primaryScore.rank }}</span>
                </button>

                <div v-if="secondaryPlatformRows.length" class="mt-0.5 flex flex-wrap gap-x-2 gap-y-0.5 text-[11px] text-[var(--muted)]">
                  <button
                    v-for="row in secondaryPlatformRows"
                    :key="row.id"
                    type="button"
                    class="transition hover:text-[var(--ink)]"
                    @click="emit('open-link', row.url)"
                  >
                    {{ row.label }} <strong v-if="row.score" class="font-semibold text-[var(--ink-secondary)]">{{ row.score }}</strong>
                  </button>
                </div>
              </div>
            </header>

            <!-- 3. 选集与资源工作区 -->
            <div class="workspace">
              <!-- 横向单排选集条：清晰区分正片各话、季度合集、番外特典与其他季度 -->
              <div v-if="showEpisodeStrip" class="episode-strip" role="group" aria-label="按剧集筛选资源">
                <div
                  v-if="hasEpisodeRanges && (showAllEpisodes || !canCollapseEpisodes)"
                  class="ep-ranges"
                  role="group"
                  aria-label="选择话数区间"
                >
                  <button
                    v-for="range in episodeRanges"
                    :key="range.index"
                    type="button"
                    class="ep-range-btn"
                    :class="{
                      'is-active': activeEpisodeRangeIndex === range.index,
                      'has-downloads': range.hasDownloads
                    }"
                    :aria-pressed="activeEpisodeRangeIndex === range.index"
                    @click="selectEpisodeRange(range.index)"
                  >
                    {{ range.label }}
                  </button>
                </div>

                <div class="ep-list">
                  <button
                    type="button"
                    class="ep-item"
                    :class="{ 'is-active': selectedDownloadGroupKey === null }"
                    :aria-pressed="selectedDownloadGroupKey === null"
                    title="显示本季全部剧集与字幕组"
                    @click="selectDownloadGroup(null)"
                  >
                    <span>全部</span>
                    <span class="ep-cnt">{{ currentSeasonDownloads.length }}</span>
                  </button>

                  <button
                    v-for="episode in visibleEpisodes"
                    :key="episode.episodeId"
                    type="button"
                    class="ep-item"
                    :class="{ 'is-active': selectedDownloadGroupKey === `episode:${episode.episodeId}` }"
                    :aria-pressed="selectedDownloadGroupKey === `episode:${episode.episodeId}`"
                    :disabled="getEpisodeDownloadCount(episode) === 0"
                    :title="episodeTooltipOf(episode)"
                    @click="selectDownloadGroup(`episode:${episode.episodeId}`)"
                  >
                    <span>{{ episodePillLabel(episode) }}</span>
                    <span v-if="episodeSeasonHint(episode)" class="ep-sub-hint">{{ episodeSeasonHint(episode) }}</span>
                    <span
                      v-if="latestAvailableEpisodeKey === `episode:${episode.episodeId}`"
                      class="badge-new"
                    >最新</span>
                    <span v-else-if="getEpisodeDownloadCount(episode) > 0" class="ep-cnt">{{ getEpisodeDownloadCount(episode) }}</span>
                  </button>

                  <button
                    v-for="synGroup in visibleSyntheticEpisodeGroups"
                    :key="synGroup.key"
                    type="button"
                    class="ep-item"
                    :class="{ 'is-active': selectedDownloadGroupKey === synGroup.key }"
                    :aria-pressed="selectedDownloadGroupKey === synGroup.key"
                    :title="`${synGroup.label} · ${synGroup.subgroupCount} 个字幕组 / ${synGroup.downloads.length} 条资源`"
                    @click="selectDownloadGroup(synGroup.key)"
                  >
                    <span>{{ synGroup.shortLabel }}</span>
                    <span v-if="latestAvailableEpisodeKey === synGroup.key" class="badge-new">最新</span>
                    <span v-else class="ep-cnt">{{ synGroup.downloads.length }}</span>
                  </button>

                  <button
                    v-if="batchDownloadGroup"
                    type="button"
                    class="ep-item ep-item-special"
                    :class="{ 'is-active': selectedDownloadGroupKey === 'batch' }"
                    :aria-pressed="selectedDownloadGroupKey === 'batch'"
                    :title="`季度合集与全集打包 · ${batchDownloadGroup.subgroupCount} 个字幕组 / ${batchDownloadGroup.downloads.length} 条资源`"
                    @click="selectDownloadGroup('batch')"
                  >
                    <span>合集</span>
                    <span class="ep-cnt">{{ batchDownloadGroup.downloads.length }}</span>
                  </button>

                  <button
                    v-if="unmatchedDownloadGroup"
                    type="button"
                    class="ep-item ep-item-special"
                    :class="{ 'is-active': selectedDownloadGroupKey === 'unmatched' }"
                    :aria-pressed="selectedDownloadGroupKey === 'unmatched'"
                    :title="`番外、特典或未标注集数资源 · ${unmatchedDownloadGroup.downloads.length} 条`"
                    @click="selectDownloadGroup('unmatched')"
                  >
                    <span>番外/其他</span>
                    <span class="ep-cnt">{{ unmatchedDownloadGroup.downloads.length }}</span>
                  </button>

                  <button
                    v-if="otherSeasonDownloadGroup"
                    type="button"
                    class="ep-item ep-item-muted"
                    :class="{ 'is-active': selectedDownloadGroupKey === 'other-season' }"
                    :aria-pressed="selectedDownloadGroupKey === 'other-season'"
                    :title="`搜索关联到的其他季度资源 · ${otherSeasonDownloadGroup.downloads.length} 条`"
                    @click="selectDownloadGroup('other-season')"
                  >
                    <span>其他季度</span>
                    <span class="ep-cnt">{{ otherSeasonDownloadGroup.downloads.length }}</span>
                  </button>

                  <button
                    v-if="canCollapseEpisodes"
                    type="button"
                    class="ep-more"
                    :aria-expanded="showAllEpisodes"
                    @click="toggleShowAllEpisodes"
                  >
                    {{ showAllEpisodes ? "收起未播剧集 ▴" : `${collapsedEmptyLabel} ▾` }}
                  </button>
                </div>
              </div>

              <!-- 字幕组与剧集双维精细工作区 -->
              <MikanResourcePane
                :selected-download-group="selectedDownloadGroup"
                :download-groups="pagedDownloadGroups"
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
                :anime-titles="allAnimeTitles"
                :anime-title="animeDetail.anime.title"
                :streaming-links="activeStreamingLinks"
                @select-subgroup="emit('select-subgroup', $event)"
                @select-resolution="emit('select-resolution', $event)"
                @select-provider="emit('select-provider', $event)"
                @select-download-group="selectDownloadGroup"
                @queue-download="emit('queue-download', $event)"
                @open-link="emit('open-link', $event)"
              />
            </div>

            <!-- 4. 底部番剧简介、别名与完整标签 -->
            <section v-if="hasBottomIntro" class="intro-section" aria-label="番剧简介">
              <h3 class="intro-title">番剧简介</h3>
              <p v-if="animeSummary" class="intro-summary">{{ animeSummary }}</p>
              <div
                v-if="animeAliases.length || meaningfulTags.length > 3 || platformFailureNotice"
                class="intro-meta"
              >
                <p v-if="animeAliases.length">
                  <span class="text-[var(--muted)]">原名 / 别名：</span>{{ animeAliases.join(" / ") }}
                </p>
                <div v-if="meaningfulTags.length > 3" class="flex flex-wrap items-center gap-1">
                  <span class="text-[var(--muted)]">标签：</span>
                  <span
                    v-for="tag in meaningfulTags"
                    :key="tag"
                    class="intro-tag"
                  >{{ tag }}</span>
                </div>
                <p v-if="platformFailureNotice" role="status">{{ platformFailureNotice }}</p>
              </div>
            </section>
          </div>
        </template>
      </div>
    </template>

    <template v-else>
      <div class="grid h-full min-h-0 place-items-center p-8 text-center">
        <div class="grid max-w-xs place-items-center gap-2.5 text-[var(--muted)]">
          <span class="empty-icon"><AppNavIcon name="home" class="size-6" /></span>
          <strong class="text-sm font-medium text-[var(--ink)]">未选择番剧</strong>
          <span class="text-sm leading-6">在左侧列表点开一部番剧，这里会显示简介、剧集与下载资源。</span>
        </div>
      </div>
    </template>
  </RightDetailPane>
</template>

<style scoped>
.detail-canvas {
  display: flex;
  flex-direction: column;
  gap: 8px;
  height: 100%;
  min-height: 0;
  overflow-x: hidden;
  overflow-y: auto;
  padding-bottom: 12px;
}
@media (max-width: 768px) {
  .detail-canvas {
    padding-bottom: max(5.5rem, calc(env(safe-area-inset-bottom) + 4.5rem));
  }
}

/* 顶部紧凑导航条（与内容区左右齐平，不再套外层卡片内边距） */
.top-nav {
  display: flex;
  flex-shrink: 0;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  padding: 0 0 6px;
  border-bottom: 1px solid var(--line);
}

.back-link {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  min-height: 38px;
  margin-left: -8px;
  padding: 6px 14px 6px 10px;
  border-radius: 10px;
  background: transparent;
  color: var(--ink-secondary);
  font-size: 13px;
  font-weight: 600;
  transition: color 0.15s, background 0.15s;
}
.back-link:hover {
  background: var(--panel);
  color: var(--ink);
}

/* 紧凑顶对齐头部（移走简介后海报同步收小，与标题+元信息+按钮高度对齐） */
.hero-section {
  display: grid;
  flex-shrink: 0;
  grid-template-columns: 68px minmax(0, 1fr);
  align-items: center;
  gap: 12px;
  padding: 2px 0 10px;
  border-bottom: 1px solid var(--line);
}
@media (min-width: 640px) {
  .hero-section {
    grid-template-columns: 76px minmax(0, 1fr);
    gap: 14px;
  }
}
@media (min-width: 1024px) {
  .hero-section {
    grid-template-columns: 76px minmax(0, 1fr) auto;
    gap: 16px;
  }
}

.hero-poster {
  display: grid;
  place-items: center;
  width: 100%;
  aspect-ratio: 2 / 3;
  overflow: hidden;
  border-radius: 8px;
  border: 1px solid var(--line);
  background: var(--panel);
  color: var(--muted);
  font-size: 1.25rem;
  font-weight: 600;
}

.meta-line {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 2px 6px;
  font-size: 12px;
  line-height: 1.45;
  color: var(--muted);
}

.meta-seg.has-dot::before {
  content: "·";
  margin-right: 6px;
  opacity: 0.45;
}

.airing-badge {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  color: var(--ok);
  font-weight: 600;
}
.airing-badge::before {
  content: "";
  width: 5px;
  height: 5px;
  border-radius: 999px;
  background: var(--ok);
}

/* 紧凑单行操作按钮（统一 30px 高度、7px 圆角） */
.btn-cta {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  height: 30px;
  padding: 0 11px;
  border-radius: 7px;
  font-size: 12.5px;
  font-weight: 600;
  white-space: nowrap;
}

.btn-fav-secondary {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  height: 30px;
  padding: 0 10px;
  border-radius: 7px;
  border: 1px solid var(--line);
  background: var(--card);
  color: var(--ink);
  font-size: 12.5px;
  font-weight: 600;
  white-space: nowrap;
  transition: border-color 0.15s, background 0.15s, color 0.15s;
}
.btn-fav-secondary:hover {
  border-color: var(--accent-line);
  background: var(--tint-1);
}
.btn-fav-secondary.is-favorited {
  border-color: var(--accent-line);
  background: var(--accent-soft);
  color: var(--accent-vivid);
}

.btn-stream-link {
  display: inline-flex;
  align-items: center;
  gap: 4.5px;
  height: 30px;
  padding: 0 10px;
  border-radius: 7px;
  border: 1px solid var(--accent-line);
  background: color-mix(in srgb, var(--accent-soft) 55%, var(--card));
  color: var(--accent-vivid);
  font-size: 12px;
  font-weight: 600;
  white-space: nowrap;
  transition: border-color 0.15s, background 0.15s, color 0.15s;
}
.btn-stream-link:hover {
  border-color: var(--accent);
  background: var(--accent);
  color: var(--accent-contrast);
}
@media (pointer: coarse) {
  .btn-cta,
  .btn-fav-secondary,
  .btn-stream-link {
    min-height: 36px;
    padding: 0 12px;
  }
}

/* 订阅源收纳下拉框 */
.source-dropdown-btn {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  height: 30px;
  padding: 0 9px;
  border-radius: 7px;
  border: 1px solid var(--line);
  background: var(--card);
  color: var(--muted);
  font-size: 12px;
  font-weight: 500;
  white-space: nowrap;
  transition: border-color 0.15s, background 0.15s;
}
.source-dropdown-btn:hover {
  border-color: var(--accent-line);
  background: var(--tint-1);
}

.source-popover {
  position: absolute;
  top: calc(100% + 4px);
  right: 0;
  z-index: 30;
  width: min(14.5rem, calc(100vw - 2rem));
  border-radius: 8px;
  border: 1px solid var(--line);
  background: var(--card);
  padding: 8px 10px;
  box-shadow: var(--strong-shadow);
}
@media (min-width: 640px) {
  .source-popover {
    left: 0;
    right: auto;
  }
}

.source-popover-item {
  display: flex;
  width: 100%;
  align-items: center;
  gap: 7px;
  border-radius: 6px;
  padding: 5px 6px;
  color: var(--ink);
  font-size: 12px;
  font-weight: 500;
  text-align: left;
  transition: background 0.12s;
}
.source-popover-item:hover {
  background: var(--tint-1);
}

.source-checkbox {
  display: inline-grid;
  place-items: center;
  width: 14px;
  height: 14px;
  flex-shrink: 0;
  border-radius: 4px;
  border: 1px solid var(--line);
  background: var(--card);
  color: var(--accent-contrast);
}
.source-checkbox.is-checked {
  border-color: var(--accent);
  background: var(--accent);
}

/* 右侧评分块 */
.rating-panel {
  display: none;
  min-width: 7.25rem;
  flex-direction: column;
  justify-content: flex-start;
  border-left: 1px solid var(--line);
  padding-left: 16px;
}
@media (min-width: 1024px) {
  .rating-panel {
    display: flex;
  }
}

.star-fill {
  color: var(--warn);
}

/* 选集与资源工作区 */
.workspace {
  display: flex;
  flex: none;
  flex-direction: column;
  gap: 8px;
}

/* 底部番剧简介区 */
.intro-section {
  display: grid;
  flex: none;
  gap: 6px;
  margin-top: 2px;
  border-top: 1px solid var(--line);
  padding: 10px 0 4px;
}

.intro-title {
  color: var(--ink);
  font-size: 13px;
  font-weight: 650;
  line-height: 1.4;
}

.intro-summary {
  max-width: 78ch;
  color: var(--ink-secondary);
  font-size: 12.5px;
  line-height: 1.65;
  white-space: pre-line;
}

.intro-meta {
  display: grid;
  gap: 4px;
  color: var(--ink-secondary);
  font-size: 12px;
  line-height: 1.5;
}

.intro-tag {
  border-radius: 4px;
  background: var(--panel);
  padding: 1px 6px;
  color: var(--ink-secondary);
  font-size: 11px;
}

.episode-strip {
  display: flex;
  flex-shrink: 0;
  flex-wrap: wrap;
  align-items: center;
  justify-content: space-between;
  gap: 6px;
  border-bottom: 1px solid var(--line);
  padding-bottom: 8px;
}

.ep-ranges {
  display: flex;
  flex-basis: 100%;
  flex-wrap: wrap;
  align-items: center;
  gap: 4px;
  padding-bottom: 2px;
}

.ep-range-btn {
  display: inline-flex;
  align-items: center;
  height: 22px;
  padding: 0 8px;
  border-radius: 5px;
  border: 1px solid var(--line);
  background: var(--card);
  color: var(--muted);
  font-size: 11px;
  font-weight: 600;
  font-variant-numeric: tabular-nums;
  transition: border-color 0.15s, background 0.15s, color 0.15s;
}
.ep-range-btn.has-downloads {
  color: var(--ink-secondary);
}
.ep-range-btn:hover:not(.is-active) {
  border-color: var(--accent-line);
  color: var(--ink);
}
.ep-range-btn.is-active {
  border-color: var(--accent);
  background: var(--accent-soft);
  color: var(--accent-vivid);
}

.ep-list {
  display: flex;
  min-width: 0;
  flex: 1;
  flex-wrap: wrap;
  align-items: center;
  gap: 4px;
}

.ep-item {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  height: 26px;
  padding: 0 9px;
  border-radius: 6px;
  background: var(--panel);
  color: var(--ink-secondary);
  font-size: 12px;
  font-weight: 600;
  font-variant-numeric: tabular-nums;
  white-space: nowrap;
  transition: background 0.15s, color 0.15s;
}
.ep-item:hover:not(:disabled):not(.is-active) {
  background: var(--tint-3);
  color: var(--ink);
}
.ep-item.is-active {
  background: var(--ink);
  color: var(--card);
}
.ep-item:disabled {
  cursor: not-allowed;
  opacity: 0.38;
}
.ep-item-special:not(.is-active) {
  border: 1px solid var(--line);
  background: var(--card);
}
.ep-item-muted:not(.is-active) {
  background: transparent;
  border: 1px dashed var(--line);
  color: var(--muted);
}

.ep-cnt {
  font-size: 10.5px;
  font-weight: 500;
  opacity: 0.68;
}
.ep-item.is-active .ep-cnt {
  opacity: 0.82;
}

.ep-sub-hint {
  border-radius: 3px;
  background: var(--tint-3);
  padding: 0 3.5px;
  font-size: 10px;
  font-weight: 600;
  color: var(--ink-secondary);
}
.ep-item.is-active .ep-sub-hint {
  background: rgba(255, 255, 255, 0.18);
  color: inherit;
}

.badge-new {
  border-radius: 4px;
  background: var(--accent-soft);
  padding: 0 4px;
  color: var(--accent-vivid);
  font-size: 10px;
  font-weight: 700;
}
.ep-item.is-active .badge-new {
  background: var(--accent-vivid);
  color: #ffffff;
}

.ep-more {
  display: inline-flex;
  height: 26px;
  align-items: center;
  padding: 0 6px;
  flex-shrink: 0;
  color: var(--muted);
  font-size: 11.5px;
  font-weight: 500;
  white-space: nowrap;
  transition: color 0.15s;
}
.ep-more:hover {
  color: var(--ink);
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
