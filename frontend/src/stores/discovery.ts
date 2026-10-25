import { computed, reactive, ref, watch } from "vue";

import type {
  AnimeDetail,
  AnimeMetadataPlatformFailure,
  DownloadJob,
  HomepageAnime,
  MikanDownloadItem
} from "@shared/index";

import { createDownload, getAnimeDetail, listHomepageAnime, searchAnime } from "../api";
import { t } from "../i18n";
import { isPlaceholderDownloadTitle } from "../lib/downloadTitle";
import { broadcastWeekday, buildPaginationItems, clampPage } from "../lib/format";
import {
  buildMikanCountOptions,
  extractResolutionLabel,
  groupMikanDownloadsByEpisode,
  providerIdOf,
  providerIdsOf
} from "../lib/mikan";
import { withBusy } from "./busy";
import { detailSearchKeyword, displayTitle, downloads, refreshDownloads, selectedDownloadDetailGid } from "./downloads";
import { feedback, toastError, updateFeedback } from "./toasts";
import { favorites } from "./favorites";
import { refreshFeedItems, subscriptions } from "./subscriptions";
import { viewportHeight } from "./ui";

const DISCOVERY_SEARCH_HISTORY_KEY = "anime-rss-discovery-search-history";
const HOMEPAGE_CACHE_KEY = "anime-rss-homepage-cache-v1";
const DISCOVERY_HISTORY_LIMIT = 8;
const DETAIL_PANE_PAGE_PADDING = 270;

function readHomepageCache(): HomepageAnime[] {
  try {
    const parsed = JSON.parse(window.localStorage.getItem(HOMEPAGE_CACHE_KEY) ?? "[]");
    return Array.isArray(parsed) ? (parsed as HomepageAnime[]) : [];
  } catch {
    return [];
  }
}

function persistHomepageCache(list: HomepageAnime[]): void {
  try {
    window.localStorage.setItem(HOMEPAGE_CACHE_KEY, JSON.stringify(list));
  } catch {
    // 存储配额满时放弃缓存即可，不影响功能
  }
}

// stale-while-revalidate：启动先用上次的当季列表立即渲染，后台再静默同步。
// Bangumi 首次同步要 3s+ 外网请求，没有缓存时首页会长时间空白。
export const homepageAnime = ref<HomepageAnime[]>(readHomepageCache());
export const homepageError = ref("");
export const historicalAnimeResults = ref<HomepageAnime[]>([]);
/**
 * 上一轮聚合搜索里没响应的信息平台。
 * 结果照常显示，只在列表顶部浅色提示一行——静悄悄少几条比明说更糟。
 */
export const searchPlatformFailures = ref<AnimeMetadataPlatformFailure[]>([]);
export const activeDiscoverySearch = ref("");
export const discoverySearchHistory = ref<string[]>(readDiscoverySearchHistory());

export const defaultDiscoveryWeekday = String(new Date().getDay());
export const discoveryForm = reactive({
  search: "",
  weekday: defaultDiscoveryWeekday
});

export const WEEKDAY_TABS = [
  { key: "all", label: "全部" },
  { key: "1", label: "周一" },
  { key: "2", label: "周二" },
  { key: "3", label: "周三" },
  { key: "4", label: "周四" },
  { key: "5", label: "周五" },
  { key: "6", label: "周六" },
  { key: "0", label: "周日" }
];

export const animePage = ref(1);
export const animePageSize = ref(18);
export const ANIME_PAGE_SIZE_OPTIONS = [12, 18, 24, 36];

// —— 番剧详情侧栏（首页 / 收藏 / 下载三个页面共用） ——
export const animeDetail = ref<AnimeDetail | null>(null);
export const animeDetailError = ref<string | null>(null);
export const selectedAnimeId = ref<string | null>(null);
export const detailLoadingAnimeId = ref<string | null>(null);
export const selectedSubgroup = ref("all");
export const selectedResolution = ref("all");
/** 资源来源筛选："all" 或某个 providerId。 */
export const selectedProvider = ref("all");
export const downloadPage = ref(1);

export const isHistoricalSearchActive = computed(() => Boolean(activeDiscoverySearch.value));
const discoveryBaseAnime = computed(() =>
  isHistoricalSearchActive.value ? historicalAnimeResults.value : homepageAnime.value
);

export const displayedHomepageAnime = computed(() => {
  const keyword = isHistoricalSearchActive.value ? "" : discoveryForm.search.trim().toLowerCase();
  return discoveryBaseAnime.value.filter(
    (item) =>
      (discoveryForm.weekday === "all" ||
        String(broadcastWeekday(item.broadcastAt) ?? item.airDay) === discoveryForm.weekday) &&
      (!keyword || `${item.title} ${item.searchKeyword ?? ""}`.toLowerCase().includes(keyword))
  );
});
export const discoveryBaseTotal = computed(() => discoveryBaseAnime.value.length);

/** 评分排序开关存在服务端设置里（homepageSortMode），由视图层同步进来 */
export const animeSortMode = ref<"default" | "rating">("default");

export const sortedHomepageAnime = computed(() => {
  if (animeSortMode.value !== "rating") {
    return displayedHomepageAnime.value;
  }
  return [...displayedHomepageAnime.value].sort((left, right) => {
    const leftRating = left.rating ?? -1;
    const rightRating = right.rating ?? -1;
    return rightRating - leftRating || left.title.localeCompare(right.title, "zh-Hans-CN");
  });
});

export const animeTotalPages = computed(() =>
  Math.max(1, Math.ceil(sortedHomepageAnime.value.length / animePageSize.value))
);
export const pagedHomepageAnime = computed(() => {
  const start = (animePage.value - 1) * animePageSize.value;
  return sortedHomepageAnime.value.slice(start, start + animePageSize.value);
});
export const animePaginationItems = computed(() => buildPaginationItems(animePage.value, animeTotalPages.value));

export function setAnimePage(nextPage: number): void {
  animePage.value = clampPage(nextPage, animeTotalPages.value);
}

// —— 详情侧栏的资源筛选与分页 ——
export const detailSubgroupOptions = computed(() => {
  const items = animeDetail.value?.downloads ?? [];
  return [
    { key: "all", label: "全部", count: items.length },
    ...buildMikanCountOptions(
      items,
      (item) => item.subgroup?.trim() || "__unknown",
      (key) => (key === "__unknown" ? "未知字幕组" : key)
    )
  ];
});

export const detailResolutionOptions = computed(() => {
  const items = animeDetail.value?.downloads ?? [];
  return [
    { key: "all", label: "全部分辨率", count: items.length },
    ...buildMikanCountOptions(
      items,
      (item) => extractResolutionLabel(item.title) || "__unknown",
      (key) => (key === "__unknown" ? "分辨率未知" : key)
    )
  ];
});

/** 来源筛选项。只列出这次真的返回了条目的源，空源不占位。 */
export const detailProviderOptions = computed(() => {
  const items = animeDetail.value?.downloads ?? [];
  const nameById = new Map(
    (animeDetail.value?.providerStatuses ?? []).map((status) => [status.providerId, status.displayName])
  );
  return [
    { key: "all", label: "全部来源", count: items.length },
    ...buildMikanCountOptions(
      items,
      // 一条资源可能同时来自多个源；按展示源归档，合并信息在行内的来源徽章里
      (item) => providerIdOf(item) ?? "__unknown",
      (key) => nameById.get(key) ?? (key === "__unknown" ? "未知来源" : key)
    )
  ];
});

/** 每个源本轮的抓取状态。失败的要显示出来，而不是静悄悄少几行。 */
export const failedProviderStatuses = computed(() =>
  (animeDetail.value?.providerStatuses ?? []).filter((status) => !status.ok)
);

export const filteredMikanDownloads = computed(() => {
  const items = animeDetail.value?.downloads ?? [];
  return items.filter((item) => {
    if (selectedSubgroup.value !== "all" && (item.subgroup?.trim() || "__unknown") !== selectedSubgroup.value) {
      return false;
    }
    if (
      selectedResolution.value !== "all" &&
      (extractResolutionLabel(item.title) || "__unknown") !== selectedResolution.value
    ) {
      return false;
    }
    if (selectedProvider.value !== "all" && !providerIdsOf(item).includes(selectedProvider.value)) {
      return false;
    }
    return true;
  });
});

export const detailDownloadPageSize = computed(() => {
  const estimatedCardHeight = 118;
  const availableHeight = Math.max(estimatedCardHeight, viewportHeight.value - DETAIL_PANE_PAGE_PADDING);
  return Math.max(1, Math.floor(availableHeight / estimatedCardHeight));
});

export const downloadTotalPages = computed(() =>
  Math.max(1, Math.ceil(filteredMikanDownloads.value.length / detailDownloadPageSize.value))
);
export const pagedMikanDownloads = computed(() => {
  const start = (downloadPage.value - 1) * detailDownloadPageSize.value;
  return filteredMikanDownloads.value.slice(start, start + detailDownloadPageSize.value);
});
export const pagedMikanDownloadGroups = computed(() =>
  groupMikanDownloadsByEpisode(animeDetail.value?.downloads ?? [], animeDetail.value?.episodes ?? [])
);
export const downloadPaginationItems = computed(() =>
  buildPaginationItems(downloadPage.value, downloadTotalPages.value)
);

export function setDownloadPage(nextPage: number): void {
  downloadPage.value = clampPage(nextPage, downloadTotalPages.value);
}

watch([selectedSubgroup, selectedResolution, selectedProvider], () => {
  downloadPage.value = 1;
});
watch([() => discoveryForm.search, () => discoveryForm.weekday], () => {
  const keyword = discoveryForm.search.trim();
  if (!keyword || (activeDiscoverySearch.value && keyword !== activeDiscoverySearch.value)) {
    activeDiscoverySearch.value = "";
    historicalAnimeResults.value = [];
  }
  animePage.value = 1;
});
watch(animePageSize, () => {
  animePage.value = 1;
});
watch(animeTotalPages, () => {
  animePage.value = clampPage(animePage.value, animeTotalPages.value);
});

function readDiscoverySearchHistory(): string[] {
  try {
    const parsed = JSON.parse(window.localStorage.getItem(DISCOVERY_SEARCH_HISTORY_KEY) ?? "[]");
    if (!Array.isArray(parsed)) {
      return [];
    }
    return parsed
      .filter((item): item is string => typeof item === "string" && item.trim().length > 0)
      .slice(0, DISCOVERY_HISTORY_LIMIT);
  } catch {
    return [];
  }
}

function persistDiscoverySearchHistory(): void {
  window.localStorage.setItem(DISCOVERY_SEARCH_HISTORY_KEY, JSON.stringify(discoverySearchHistory.value));
}

function pushDiscoverySearchHistory(keyword: string): void {
  const normalized = keyword.trim();
  if (!normalized) {
    return;
  }
  discoverySearchHistory.value = [
    normalized,
    ...discoverySearchHistory.value.filter((item) => item.toLocaleLowerCase() !== normalized.toLocaleLowerCase())
  ].slice(0, DISCOVERY_HISTORY_LIMIT);
  persistDiscoverySearchHistory();
}

export function removeDiscoverySearchHistory(keyword: string): void {
  discoverySearchHistory.value = discoverySearchHistory.value.filter((item) => item !== keyword);
  persistDiscoverySearchHistory();
}

export function clearDiscoverySearchHistory(): void {
  discoverySearchHistory.value = [];
  window.localStorage.removeItem(DISCOVERY_SEARCH_HISTORY_KEY);
}

function formatAnimeDetailError(title: string, error: unknown): string {
  const rawMessage = error instanceof Error ? error.message : String(error);
  if (/Mikan|蜜柑|bangumi/i.test(rawMessage)) {
    return t("detail.mikanSyncFailed", { title });
  }
  return t("detail.loadFailed", { title });
}

/** 启动时 bootstrap 与 loadTabData 会各调一次；共享同一个 promise，别打两遍接口 */
let homepageRefreshInFlight: Promise<void> | null = null;

export async function refreshHomepage(options: { silent?: boolean } = {}): Promise<void> {
  if (homepageRefreshInFlight) {
    return homepageRefreshInFlight;
  }
  homepageRefreshInFlight = runHomepageRefresh(options).finally(() => {
    homepageRefreshInFlight = null;
  });
  return homepageRefreshInFlight;
}

async function runHomepageRefresh(options: { silent?: boolean }): Promise<void> {
  await withBusy("discovery-home", async () => {
    try {
      const next = await listHomepageAnime();
      // 内容没变化就不整组替换,避免刷新时列表无谓重渲闪动
      const sameContent =
        next.length === homepageAnime.value.length &&
        next.every((item, index) => item.animeId === homepageAnime.value[index]?.animeId &&
          item.imageUrl === homepageAnime.value[index]?.imageUrl &&
          item.rating === homepageAnime.value[index]?.rating);
      if (!sameContent) {
        homepageAnime.value = next;
      }
      persistHomepageCache(next);
      historicalAnimeResults.value = [];
      activeDiscoverySearch.value = "";
      homepageError.value = "";
      animePage.value = clampPage(animePage.value, animeTotalPages.value);
      if (!options.silent) {
        updateFeedback(t("home.syncSeason", { count: homepageAnime.value.length }), "success");
      }
    } catch (error) {
      homepageError.value = error instanceof Error ? error.message : t("api.bangumiDataFetchFailed");
      if (!options.silent) {
        toastError(error, t("api.bangumiDataFetchFailed"));
      }
    }
  });
}

/** 搜索历史番剧（Bangumi 全库）。空关键词时回到当季列表。 */
export async function runDiscoverySearch(): Promise<void> {
  const keyword = discoveryForm.search.trim();
  if (!keyword) {
    historicalAnimeResults.value = [];
    searchPlatformFailures.value = [];
    activeDiscoverySearch.value = "";
    discoveryForm.weekday = defaultDiscoveryWeekday;
    closeAnimeDetail();
    updateFeedback(t("home.backToSeason"), "info");
    return;
  }

  await withBusy("discovery-search", async () => {
    try {
      discoveryForm.weekday = "all";
      const { items, platformFailures } = await searchAnime(keyword);
      activeDiscoverySearch.value = keyword;
      historicalAnimeResults.value = items;
      searchPlatformFailures.value = platformFailures;
      closeAnimeDetail();
      animePage.value = 1;
      homepageError.value = "";
      pushDiscoverySearchHistory(keyword);
      updateFeedback(t("home.historyHits", { count: items.length }), "success");
    } catch (error) {
      toastError(error, t("api.bangumiDataFetchFailed"));
    }
  });
}

export function useDiscoverySearchHistory(keyword: string): void {
  discoveryForm.search = keyword;
  void runDiscoverySearch();
}

export function closeAnimeDetail(): void {
  stopPendingPoll();
  animeDetail.value = null;
  animeDetailError.value = null;
  selectedAnimeId.value = null;
  selectedDownloadDetailGid.value = null;
}


/**
 * 首屏可能只带回了快的源的结果，慢的源标着 pending。
 * 这里盯着它把剩下的补齐——服务端那一轮还在后台跑，跑完就写进缓存，
 * 我们只要再取一次即可（命中缓存，等于免费）。
 *
 * 用户已经切走就停手；最多盯 30 秒，避免一个永远回不来的源让它转到天荒地老。
 */
const PENDING_POLL_INTERVAL_MS = 2000;
const PENDING_POLL_TIMEOUT_MS = 30_000;
let pendingPollTimer: ReturnType<typeof setTimeout> | null = null;

function stopPendingPoll(): void {
  if (pendingPollTimer) {
    clearTimeout(pendingPollTimer);
    pendingPollTimer = null;
  }
}

function schedulePendingRefill(animeId: string, deadline: number): void {
  stopPendingPoll();
  if (Date.now() >= deadline) {
    return;
  }
  pendingPollTimer = setTimeout(() => {
    pendingPollTimer = null;
    if (selectedAnimeId.value !== animeId) {
      return;
    }
    void getAnimeDetail(animeId)
      .then((detail) => {
        if (selectedAnimeId.value !== animeId) {
          return;
        }
        // 只补资源，不动用户当前的筛选与翻页——补齐不该把人踢回第一页
        const current = animeDetail.value;
        if (current) {
          animeDetail.value = {
            ...current,
            downloads: detail.downloads,
            providerStatuses: detail.providerStatuses
          };
        }
        if ((detail.providerStatuses ?? []).some((status) => status.pending)) {
          schedulePendingRefill(animeId, deadline);
        }
      })
      .catch(() => {
        // 补齐失败不打扰用户：首屏那批结果还在，下次再说
      });
  }, PENDING_POLL_INTERVAL_MS);
}

function applyAnimeDetail(detail: AnimeDetail, fallbackImageUrl: string | null = null): void {
  selectedSubgroup.value = "all";
  selectedResolution.value = "all";
  selectedProvider.value = "all";
  downloadPage.value = 1;
  animeDetail.value = {
    ...detail,
    anime: { ...detail.anime, imageUrl: detail.anime.imageUrl ?? fallbackImageUrl }
  };
  animeDetailError.value = detail.downloadError
    ? formatAnimeDetailError(detail.anime.title, new Error(`Mikan RSS: ${detail.downloadError}`))
    : null;

  stopPendingPoll();
  if ((detail.providerStatuses ?? []).some((status) => status.pending)) {
    schedulePendingRefill(detail.anime.animeId, Date.now() + PENDING_POLL_TIMEOUT_MS);
  }
}

function createPendingAnimeDetail(anime: HomepageAnime): AnimeDetail {
  return {
    anime,
    titles: [anime.title],
    summary: null,
    intro: anime.officialSite,
    episodes: [],
    mikanKeyword: anime.searchKeyword ?? anime.title,
    downloads: []
  };
}

/**
 * 悬停预取。真正慢的是服务端那一轮跨源抓取(蜜柑一轮 6~10s),
 * 打一次详情接口就能把服务端缓存焐热,等用户真点进去往往已经是命中。
 *
 * 三道闸:同一部番只预取一次、同时最多 2 路在飞、结果整个丢掉不进 UI。
 * 预取失败一律咽掉——它只是省时间,不该冒出任何提示。
 */
const prefetchedAnimeIds = new Set<string>();
let prefetchInFlight = 0;
const PREFETCH_CONCURRENCY = 2;

export function prefetchAnimeDetail(animeId: string): void {
  if (!animeId || prefetchedAnimeIds.has(animeId) || prefetchInFlight >= PREFETCH_CONCURRENCY) {
    return;
  }
  prefetchedAnimeIds.add(animeId);
  prefetchInFlight += 1;
  void getAnimeDetail(animeId)
    .catch(() => {
      // 预取失败就当没发生:让用户真点进去时走正常路径,该报错那时再报
      prefetchedAnimeIds.delete(animeId);
    })
    .finally(() => {
      prefetchInFlight -= 1;
    });
}

export async function openAnimeDetail(anime: HomepageAnime): Promise<void> {
  selectedAnimeId.value = anime.animeId;
  detailLoadingAnimeId.value = anime.animeId;
  selectedSubgroup.value = "all";
  selectedResolution.value = "all";
  selectedProvider.value = "all";
  downloadPage.value = 1;
  animeDetail.value = createPendingAnimeDetail(anime);
  animeDetailError.value = null;
  await withBusy(`anime-detail:${anime.animeId}`, async () => {
    try {
      const detail = await getAnimeDetail(anime.animeId);
      // 用户可能在加载途中点了别的番剧，只应用最后一次点击的结果
      if (selectedAnimeId.value !== anime.animeId) {
        return;
      }
      applyAnimeDetail(detail, anime.imageUrl);
      if (!animeDetailError.value) {
        updateFeedback(
          t("home.syncMikan", { count: detail.downloads.length }),
          "success"
        );
      } else {
        feedback.value = animeDetailError.value;
      }
    } catch (error) {
      if (selectedAnimeId.value === anime.animeId) {
        animeDetailError.value = formatAnimeDetailError(anime.title, error);
        feedback.value = animeDetailError.value;
      }
    } finally {
      if (detailLoadingAnimeId.value === anime.animeId) {
        detailLoadingAnimeId.value = null;
      }
    }
  });
}

function downloadDetailAnimeId(download: DownloadJob): string | null {
  const guid = download.feedItemGuid?.trim() ?? "";
  if (guid.startsWith("anime:")) {
    return guid.slice("anime:".length).trim() || null;
  }
  if (download.feedItemSubscriptionId) {
    const subscription = subscriptions.value.find((item) => item.id === download.feedItemSubscriptionId) ?? null;
    const favorite = favorites.value.find(
      (item) =>
        item.subscriptionId === download.feedItemSubscriptionId ||
        Boolean(subscription?.url && item.rssUrl === subscription.url)
    );
    return favorite?.animeId ?? null;
  }
  return null;
}

/** 从下载任务反查番剧详情（下载页右栏）。 */
export async function openDownloadAnimeDetail(download: DownloadJob): Promise<void> {
  selectedAnimeId.value = null;
  const keyword = detailSearchKeyword(download);
  const savedAnimeId = downloadDetailAnimeId(download);
  if (!savedAnimeId && (!keyword || isPlaceholderDownloadTitle(keyword))) {
    animeDetail.value = null;
    animeDetailError.value = "这个下载任务缺少可用于联网查询的番剧标题。";
    feedback.value = animeDetailError.value;
    return;
  }

  selectedDownloadDetailGid.value = download.gid;
  await withBusy(`download-detail:${download.gid}`, async () => {
    animeDetailError.value = null;
    try {
      let detail: AnimeDetail | null = null;
      let fallbackImageUrl: string | null = download.feedItemImageUrl ?? null;
      if (savedAnimeId) {
        detail = await getAnimeDetail(savedAnimeId).catch(() => null);
      }
      if (!detail) {
        const matches = await searchAnime(keyword);
        const anime = matches.items[0];
        if (!anime) {
          animeDetailError.value = `没有查到「${keyword}」对应的番剧信息。`;
          feedback.value = animeDetailError.value;
          return;
        }
        detail = await getAnimeDetail(anime.animeId);
        fallbackImageUrl = anime.imageUrl ?? fallbackImageUrl;
      }

      applyAnimeDetail(detail, fallbackImageUrl);
      if (animeDetailError.value) {
        feedback.value = animeDetailError.value;
      } else {
        updateFeedback(
          t("home.queryAnime", { title: detail.anime.title, count: detail.downloads.length }),
          "success"
        );
      }
    } catch (error) {
      animeDetailError.value = formatAnimeDetailError(keyword || displayTitle(download), error);
      feedback.value = animeDetailError.value;
    }
  });
}

export function findQueuedMikanDownload(item: MikanDownloadItem): DownloadJob | null {
  const urls = [item.magnetUrl, item.torrentUrl].filter((value): value is string => Boolean(value));
  if (!urls.length) {
    return null;
  }
  return (
    downloads.value.find((download) => Boolean(download.feedItemSourceUrl && urls.includes(download.feedItemSourceUrl))) ??
    null
  );
}

export function isMikanDownloadQueued(item: MikanDownloadItem): boolean {
  return Boolean(findQueuedMikanDownload(item));
}

export async function queueMikanDownload(item: MikanDownloadItem): Promise<void> {
  const url = item.torrentUrl ?? item.magnetUrl;
  if (!url) {
    updateFeedback(t("downloads.noMikanUrl"), "error");
    return;
  }
  if (isMikanDownloadQueued(item)) {
    updateFeedback(t("downloads.alreadyQueued"), "info");
    return;
  }

  await withBusy(`mikan-download:${item.id}`, async () => {
    try {
      await createDownload({
        url,
        title: item.title,
        imageUrl: animeDetail.value?.anime.imageUrl ?? null,
        animeId: animeDetail.value?.anime.animeId,
        pageUrl: item.pageUrl ?? undefined
      });
      updateFeedback(t("downloads.queued"), "success");
      await Promise.allSettled([refreshDownloads(), refreshFeedItems()]);
    } catch (error) {
      toastError(error, t("downloads.queueFailed"));
    }
  });
}

export function resetDiscoveryView(): void {
  closeAnimeDetail();
  activeDiscoverySearch.value = "";
  historicalAnimeResults.value = [];
  discoveryForm.search = "";
  discoveryForm.weekday = defaultDiscoveryWeekday;
  animePage.value = 1;
  downloadPage.value = 1;
  selectedSubgroup.value = "all";
  selectedResolution.value = "all";
  selectedProvider.value = "all";
}
