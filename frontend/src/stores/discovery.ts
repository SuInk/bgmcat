import { computed, reactive, ref, watch } from "vue";

import type {
  AnimeCatalogFormat,
  AnimeCatalogRegion,
  AnimeCatalogSeason,
  AnimeCatalogSort,
  AnimeCatalogStatus,
  AnimeDetail,
  AnimeMetadataPlatformFailure,
  DownloadJob,
  HomepageAnime,
  MikanDownloadItem,
  ResourceProviderFetchStatus
} from "@shared/index";

import {
  ApiRequestError,
  createDownload,
  createDownloadsBatch,
  getAnimeDetail,
  listCatalogAnime,
  listHomepageAnime,
  searchAnime
} from "../api";
import { t } from "../i18n";
import { isPlaceholderDownloadTitle } from "../lib/downloadTitle";
import { broadcastWeekday, buildPaginationItems, clampPage } from "../lib/format";
import {
  buildMikanCountOptions,
  extractResolutionLabel,
  groupMikanDownloadsByEpisode,
  providerIdOf,
  providerIdsOf,
  resolveSubgroupKey
} from "../lib/mikan";
import { withBusy } from "./busy";
import { detailSearchKeyword, displayTitle, downloads, refreshDownloads, selectedDownloadDetailGid } from "./downloads";
import { feedback, toastError, updateFeedback } from "./toasts";
import { favorites } from "./favorites";
import { hiddenAnimeIds } from "./hiddenAnime";
import { settings } from "./session";
import { refreshFeedItems, subscriptions } from "./subscriptions";
import { viewportHeight } from "./ui";
import { readStorageItem } from "../lib/storageKeys";

const DISCOVERY_SEARCH_HISTORY_KEY = "bgmcat.discovery.search-history";
const LEGACY_DISCOVERY_SEARCH_HISTORY_KEY = "anime-rss-discovery-search-history";
const HOMEPAGE_CACHE_KEY = "bgmcat.discovery.homepage-cache-v1";
const LEGACY_HOMEPAGE_CACHE_KEY = "anime-rss-homepage-cache-v1";
const DISCOVERY_HISTORY_LIMIT = 8;
const DETAIL_PANE_PAGE_PADDING = 270;

function readHomepageCache(): HomepageAnime[] {
  try {
    const parsed = JSON.parse(readStorageItem(HOMEPAGE_CACHE_KEY, LEGACY_HOMEPAGE_CACHE_KEY) ?? "[]");
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

export function getTodayWeekdayKey(now = new Date()): string {
  return String(now.getDay());
}

export const defaultDiscoveryWeekday = getTodayWeekdayKey();
export const discoveryForm = reactive({
  search: "",
  weekday: getTodayWeekdayKey()
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

// —— 双模式：当季新番 (schedule，默认) + 番剧片库 (catalog) ——
export type DiscoveryMode = "schedule" | "catalog";
try {
  window.localStorage.removeItem("bgmcat.discoveryMode");
} catch {
  // ignore storage failure
}

export const discoveryMode = ref<DiscoveryMode>("schedule");

export const CATALOG_FORMAT_OPTIONS: Array<{ key: AnimeCatalogFormat; label: string }> = [
  { key: "all", label: "全部" },
  { key: "TV", label: "TV" },
  { key: "剧场版", label: "剧场版" },
  { key: "OVA", label: "OVA" },
  { key: "WEB", label: "WEB" }
];

export const CATALOG_REGION_OPTIONS: Array<{ key: AnimeCatalogRegion; label: string }> = [
  { key: "all", label: "全部" },
  { key: "jp", label: "日本" },
  { key: "cn", label: "中国" },
  { key: "west", label: "欧美" }
];

export const CATALOG_STATUS_OPTIONS: Array<{ key: AnimeCatalogStatus; label: string }> = [
  { key: "all", label: "全部" },
  { key: "airing", label: "连载" },
  { key: "finished", label: "完结" },
  { key: "upcoming", label: "未播" }
];

const catalogCurrentYear = Math.max(2026, new Date().getFullYear());
export const CATALOG_YEAR_OPTIONS: Array<{ key: string; label: string }> = [
  { key: "all", label: "全部" },
  ...Array.from({ length: catalogCurrentYear - 2018 + 1 }, (_, i) => {
    const y = String(catalogCurrentYear - i);
    return { key: y, label: y };
  }),
  { key: "2015-2017", label: "15-17" },
  { key: "2010-2014", label: "10-14" },
  { key: "2000-2009", label: "00-09" },
  { key: "1990-1999", label: "90年代" },
  { key: "older", label: "更早" }
];

export const CATALOG_SEASON_OPTIONS: Array<{ key: AnimeCatalogSeason; label: string }> = [
  { key: "all", label: "全部" },
  { key: "winter", label: "1月冬" },
  { key: "spring", label: "4月春" },
  { key: "summer", label: "7月夏" },
  { key: "autumn", label: "10月秋" }
];

export const CATALOG_GENRE_OPTIONS: Array<{ key: string; label: string }> = [
  { key: "all", label: "全部" },
  { key: "原创", label: "原创" },
  { key: "漫画改", label: "漫画改" },
  { key: "小说改", label: "小说改" },
  { key: "游戏改", label: "游戏改" },
  { key: "奇幻", label: "奇幻" },
  { key: "战斗", label: "战斗" },
  { key: "恋爱", label: "恋爱" },
  { key: "喜剧", label: "喜剧" },
  { key: "日常", label: "日常" },
  { key: "校园", label: "校园" },
  { key: "科幻", label: "科幻" },
  { key: "悬疑", label: "悬疑" },
  { key: "推理", label: "推理" },
  { key: "治愈", label: "治愈" },
  { key: "冒险", label: "冒险" },
  { key: "热血", label: "热血" },
  { key: "搞笑", label: "搞笑" },
  { key: "百合", label: "百合" },
  { key: "机战", label: "机战" },
  { key: "运动", label: "运动" },
  { key: "音乐", label: "音乐" },
  { key: "偶像", label: "偶像" },
  { key: "催泪", label: "催泪" },
  { key: "智斗", label: "智斗" },
  { key: "历史", label: "历史" },
  { key: "职场", label: "职场" }
];

export const CATALOG_RATING_OPTIONS: Array<{ key: number; label: string }> = [
  { key: 0, label: "不限" },
  { key: 8.5, label: "8.5+" },
  { key: 8, label: "8.0+" },
  { key: 7.5, label: "7.5+" },
  { key: 7, label: "7.0+" },
  { key: 6, label: "6.0+" }
];

export const CATALOG_SORT_OPTIONS: Array<{ key: AnimeCatalogSort; label: string }> = [
  { key: "heat", label: "热度" },
  { key: "score", label: "评分" },
  { key: "rank", label: "排名" },
  { key: "date", label: "最新" }
];

export interface CatalogFilterState {
  keyword: string;
  format: AnimeCatalogFormat;
  region: AnimeCatalogRegion;
  status: AnimeCatalogStatus;
  year: string;
  season: AnimeCatalogSeason;
  genre: string;
  minRating: number;
  sort: AnimeCatalogSort;
}

export const catalogFilters = reactive<CatalogFilterState>({
  keyword: "",
  format: "all",
  region: "all",
  status: "all",
  year: "all",
  season: "all",
  genre: "all",
  minRating: 0,
  sort: "heat"
});

export const failedCoverAnimeIds = ref<Set<string>>(new Set());

export function markAnimeCoverFailed(animeId: string): void {
  if (!animeId || failedCoverAnimeIds.value.has(animeId)) {
    return;
  }
  const next = new Set(failedCoverAnimeIds.value);
  next.add(animeId);
  failedCoverAnimeIds.value = next;
}

export function hasAnimeCover(anime: { animeId: string; imageUrl?: string | null }): boolean {
  const url = anime.imageUrl?.trim() ?? "";
  if (!url || /\/(?:no_icon|info_only)/i.test(url)) {
    return false;
  }
  return !failedCoverAnimeIds.value.has(anime.animeId);
}

export const catalogItems = ref<HomepageAnime[]>([]);
export const visibleCatalogItems = computed(() => {
  const base = catalogFilters.keyword.trim()
    ? catalogItems.value
    : catalogItems.value.filter((item) => !hiddenAnimeIds.value.has(item.animeId));
  return [...base].sort((left, right) => (hasAnimeCover(left) ? 0 : 1) - (hasAnimeCover(right) ? 0 : 1));
});
export const catalogTotal = ref(0);
export const catalogHasMore = ref(false);
export const catalogError = ref("");
export const catalogLoadingInitial = ref(false);
export const catalogLoadingMore = ref(false);
export const catalogInitialized = ref(false);

const CATALOG_PAGE_SIZE = 24;
let catalogOffset = 0;
let catalogAbortController: AbortController | null = null;
let catalogRequestSeq = 0;

export const hasActiveCatalogFilters = computed(() =>
  Boolean(
    catalogFilters.keyword.trim() ||
      catalogFilters.format !== "all" ||
      catalogFilters.region !== "all" ||
      catalogFilters.status !== "all" ||
      catalogFilters.year !== "all" ||
      catalogFilters.season !== "all" ||
      catalogFilters.genre !== "all" ||
      catalogFilters.minRating > 0
  )
);

export const activeCatalogFilterCount = computed(() => {
  let count = 0;
  if (catalogFilters.keyword.trim()) count += 1;
  if (catalogFilters.format !== "all") count += 1;
  if (catalogFilters.region !== "all") count += 1;
  if (catalogFilters.status !== "all") count += 1;
  if (catalogFilters.year !== "all") count += 1;
  if (catalogFilters.season !== "all") count += 1;
  if (catalogFilters.genre !== "all") count += 1;
  if (catalogFilters.minRating > 0) count += 1;
  return count;
});

export const isMultiYearRangeSelected = computed(
  () => catalogFilters.year.includes("-") || catalogFilters.year === "older"
);

export async function refreshCatalogAnime(options: { silent?: boolean } = {}): Promise<void> {
  catalogAbortController?.abort();
  const controller = new AbortController();
  catalogAbortController = controller;
  const seq = ++catalogRequestSeq;

  catalogLoadingInitial.value = true;
  catalogLoadingMore.value = false;
  catalogError.value = "";
  catalogOffset = 0;

  try {
    const result = await listCatalogAnime(
      {
        keyword: catalogFilters.keyword,
        format: catalogFilters.format,
        region: catalogFilters.region,
        status: catalogFilters.status,
        year: catalogFilters.year,
        season: isMultiYearRangeSelected.value ? "all" : catalogFilters.season,
        genre: catalogFilters.genre,
        minRating: catalogFilters.minRating,
        sort: catalogFilters.sort,
        limit: CATALOG_PAGE_SIZE,
        offset: 0
      },
      controller.signal
    );
    if (seq !== catalogRequestSeq) {
      return;
    }
    catalogItems.value = result.items;
    catalogTotal.value = result.total;
    catalogOffset = result.items.length;
    catalogHasMore.value = result.hasMore;
    catalogInitialized.value = true;
    catalogError.value = "";
    if (catalogFilters.keyword.trim()) {
      pushDiscoverySearchHistory(catalogFilters.keyword.trim());
    }
  } catch (error) {
    if (seq !== catalogRequestSeq) {
      return;
    }
    if (error instanceof ApiRequestError && error.code === "REQUEST_ABORTED") {
      return;
    }
    catalogError.value = error instanceof Error ? error.message : t("api.bangumiDataFetchFailed");
    if (!options.silent) {
      toastError(error, t("api.bangumiDataFetchFailed"));
    }
  } finally {
    if (seq === catalogRequestSeq) {
      catalogLoadingInitial.value = false;
    }
  }
}

export async function loadMoreCatalogAnime(): Promise<boolean> {
  if (!catalogHasMore.value || catalogLoadingInitial.value || catalogLoadingMore.value) {
    return false;
  }
  const seq = catalogRequestSeq;
  catalogLoadingMore.value = true;
  try {
    const result = await listCatalogAnime({
      keyword: catalogFilters.keyword,
      format: catalogFilters.format,
      region: catalogFilters.region,
      status: catalogFilters.status,
      year: catalogFilters.year,
      season: isMultiYearRangeSelected.value ? "all" : catalogFilters.season,
      genre: catalogFilters.genre,
      minRating: catalogFilters.minRating,
      sort: catalogFilters.sort,
      limit: CATALOG_PAGE_SIZE,
      offset: catalogOffset
    });
    if (seq !== catalogRequestSeq) {
      return false;
    }
    const seen = new Set(catalogItems.value.map((item) => item.animeId));
    const appended = result.items.filter((item) => !seen.has(item.animeId));
    catalogItems.value = [...catalogItems.value, ...appended];
    catalogOffset += result.items.length;
    catalogTotal.value = result.total;
    catalogHasMore.value = result.hasMore && result.items.length > 0;
    return appended.length > 0;
  } catch (error) {
    if (seq === catalogRequestSeq) {
      toastError(error, t("api.bangumiDataFetchFailed"));
    }
    return false;
  } finally {
    if (seq === catalogRequestSeq) {
      catalogLoadingMore.value = false;
    }
  }
}

export function resetCatalogFilters(options: { keepSort?: boolean } = {}): void {
  catalogFilters.keyword = "";
  catalogFilters.format = "all";
  catalogFilters.region = "all";
  catalogFilters.status = "all";
  catalogFilters.year = "all";
  catalogFilters.season = "all";
  catalogFilters.genre = "all";
  catalogFilters.minRating = 0;
  if (!options.keepSort) {
    catalogFilters.sort = "heat";
  }
  void refreshCatalogAnime({ silent: true });
}

export function openCatalogWithPreset(preset: Partial<CatalogFilterState> = {}): void {
  catalogFilters.keyword = preset.keyword ?? "";
  catalogFilters.format = preset.format ?? "all";
  catalogFilters.region = preset.region ?? "all";
  catalogFilters.status = preset.status ?? "all";
  catalogFilters.year = preset.year ?? "all";
  catalogFilters.season = preset.season ?? "all";
  catalogFilters.genre = preset.genre ?? "all";
  catalogFilters.minRating = preset.minRating ?? 0;
  catalogFilters.sort = preset.sort ?? "heat";
  discoveryMode.value = "catalog";
  void refreshCatalogAnime({ silent: true });
}

export const animePage = ref(1);
// 海报卡一行 5~7 张，移动端首屏缩至 18 张减少蜂窝网络并发封面解码压力
export const animePageSize = ref(
  typeof window !== "undefined" && window.innerWidth > 0 && window.innerWidth < 640 ? 18 : 24
);
export const ANIME_PAGE_SIZE_OPTIONS = [12, 18, 24, 36, 48];

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
  const isKeywordSearching = isHistoricalSearchActive.value || Boolean(keyword);
  return discoveryBaseAnime.value.filter(
    (item) =>
      (isKeywordSearching || !hiddenAnimeIds.value.has(item.animeId)) &&
      (isKeywordSearching ||
        discoveryForm.weekday === "all" ||
        String(broadcastWeekday(item.broadcastAt) ?? item.airDay) === discoveryForm.weekday) &&
      (!keyword || `${item.title} ${item.searchKeyword ?? ""}`.toLowerCase().includes(keyword))
  );
});
export const discoveryBaseTotal = computed(() => discoveryBaseAnime.value.length);

/** 评分排序开关存在服务端设置里（homepageSortMode），由视图层同步进来 */
export const animeSortMode = ref<"default" | "rating">("default");

export const sortedHomepageAnime = computed(() => {
  return [...displayedHomepageAnime.value].sort((left, right) => {
    const coverDiff = (hasAnimeCover(left) ? 0 : 1) - (hasAnimeCover(right) ? 0 : 1);
    if (coverDiff !== 0) {
      return coverDiff;
    }
    if (animeSortMode.value !== "rating") {
      return 0;
    }
    const leftRating = left.rating ?? -1;
    const rightRating = right.rating ?? -1;
    return rightRating - leftRating || left.title.localeCompare(right.title, "zh-Hans-CN");
  });
});

export const animeTotalPages = computed(() =>
  Math.max(1, Math.ceil(sortedHomepageAnime.value.length / animePageSize.value))
);
export const pagedHomepageAnime = computed(() =>
  sortedHomepageAnime.value.slice(0, animePage.value * animePageSize.value)
);
export const hasMoreHomepageAnime = computed(
  () => pagedHomepageAnime.value.length < sortedHomepageAnime.value.length
);
export const animePaginationItems = computed(() => buildPaginationItems(animePage.value, animeTotalPages.value));

export function setAnimePage(nextPage: number): void {
  animePage.value = clampPage(nextPage, animeTotalPages.value);
}

export function loadMoreHomepageAnime(): boolean {
  if (!hasMoreHomepageAnime.value) {
    return false;
  }
  animePage.value = clampPage(animePage.value + 1, animeTotalPages.value);
  return true;
}

// —— 详情侧栏的资源筛选与分页 ——
export const detailSubgroupOptions = computed(() => {
  const items = animeDetail.value?.downloads ?? [];
  return [
    { key: "all", label: "全部", count: items.length },
    ...buildMikanCountOptions(
      items,
      (item) => resolveSubgroupKey(item),
      (key) => (key === "__unknown" ? "未标注字幕组" : key)
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
    if (selectedSubgroup.value !== "all" && resolveSubgroupKey(item) !== selectedSubgroup.value) {
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
  groupMikanDownloadsByEpisode(
    animeDetail.value?.downloads ?? [],
    animeDetail.value?.episodes ?? [],
    animeDetail.value?.anime.title ?? null
  )
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
    const parsed = JSON.parse(readStorageItem(DISCOVERY_SEARCH_HISTORY_KEY, LEGACY_DISCOVERY_SEARCH_HISTORY_KEY) ?? "[]");
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
  window.localStorage.removeItem(LEGACY_DISCOVERY_SEARCH_HISTORY_KEY);
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

export function hydrateHomepageAnime(next: HomepageAnime[]): void {
  if (!Array.isArray(next) || !next.length) {
    return;
  }
  const sameContent =
    next.length === homepageAnime.value.length &&
    next.every(
      (item, index) =>
        item.animeId === homepageAnime.value[index]?.animeId &&
        item.imageUrl === homepageAnime.value[index]?.imageUrl &&
        item.rating === homepageAnime.value[index]?.rating
    );
  if (!sameContent) {
    homepageAnime.value = next;
  }
  persistHomepageCache(next);
  homepageError.value = "";
  animePage.value = clampPage(animePage.value, animeTotalPages.value);
}

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
    discoveryForm.weekday = getTodayWeekdayKey();
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


const PROVIDER_DISPLAY_NAMES: Record<string, string> = {
  mikan: "蜜柑计划 (Mikan)",
  animegarden: "AnimeGarden (聚合)",
  dmhy: "动漫花园 (DMHY)",
  bangumimoe: "萌番组 (Bangumi.moe)",
  acgnx: "末日动漫 (AcgnX)",
  acgrip: "ACG.RIP",
  nyaa: "Nyaa",
  kisssub: "爱恋动漫 (KissSub)",
  comicat: "漫猫动漫 (Comicat)",
  tokyotosho: "Tokyo Toshokan"
};

function buildInitialPendingStatuses(): ResourceProviderFetchStatus[] {
  const configured = settings.value?.enabledResourceProviders;
  const ids = configured && configured.length > 0 ? configured : ["mikan"];
  return ids.map((providerId) => ({
    providerId,
    displayName: PROVIDER_DISPLAY_NAMES[providerId] ?? providerId,
    ok: true,
    pending: true,
    count: 0,
    error: null
  }));
}

/**
 * 前端内存缓存 + 请求合并：
 * - 悬停/按下触发的预取与正式点击共用同一个 Promise，绝不发重复请求；
 * - 已加载过的番剧再次点开 0ms 秒出，后台静默补齐仍在 pending 或过期的源。
 */
const DETAIL_CACHE_MAX = 64;
const DETAIL_CACHE_FRESH_MS = 3 * 60_000;
const DETAIL_CACHE_MAX_AGE_MS = 30 * 60_000;
const detailMemoryCache = new Map<string, { savedAt: number; detail: AnimeDetail }>();
const detailInFlight = new Map<string, Promise<AnimeDetail>>();

function writeDetailCache(animeId: string, detail: AnimeDetail): void {
  detailMemoryCache.delete(animeId);
  detailMemoryCache.set(animeId, { savedAt: Date.now(), detail });
  while (detailMemoryCache.size > DETAIL_CACHE_MAX) {
    const oldest = detailMemoryCache.keys().next().value;
    if (oldest === undefined) break;
    detailMemoryCache.delete(oldest);
  }
}

function readDetailCache(animeId: string): { savedAt: number; detail: AnimeDetail } | null {
  const entry = detailMemoryCache.get(animeId);
  if (!entry) return null;
  if (Date.now() - entry.savedAt > DETAIL_CACHE_MAX_AGE_MS) {
    detailMemoryCache.delete(animeId);
    return null;
  }
  return entry;
}

function fetchProgressiveDetail(animeId: string, keyword?: string): Promise<AnimeDetail> {
  const inflight = detailInFlight.get(animeId);
  if (inflight) {
    return inflight;
  }
  const task = getAnimeDetail(animeId, { progressive: true, keyword })
    .then((detail) => {
      writeDetailCache(animeId, detail);
      if (detail.anime.animeId && detail.anime.animeId !== animeId) {
        writeDetailCache(detail.anime.animeId, detail);
      }
      return detail;
    })
    .finally(() => {
      if (detailInFlight.get(animeId) === task) {
        detailInFlight.delete(animeId);
      }
    });
  detailInFlight.set(animeId, task);
  return task;
}

/**
 * 首屏先带回快源（通常 300~500ms），慢源标着 pending 在服务端后台继续抓。
 * 这里用渐进退避轮询把后续完成的源静默合入，不重置用户的筛选与翻页。
 */
const PENDING_POLL_DELAYS_MS = [650, 1000, 1600, 2200];
const PENDING_POLL_TIMEOUT_MS = 25_000;
let pendingPollTimer: ReturnType<typeof setTimeout> | null = null;
let pendingPollGeneration = 0;

function stopPendingPoll(): void {
  pendingPollGeneration += 1;
  if (pendingPollTimer) {
    clearTimeout(pendingPollTimer);
    pendingPollTimer = null;
  }
}

function schedulePendingRefill(
  animeId: string,
  keyword: string | undefined,
  deadline: number,
  attempt = 0,
  customDelay?: number
): void {
  stopPendingPoll();
  const generation = pendingPollGeneration;
  if (Date.now() >= deadline) {
    return;
  }
  const delay =
    customDelay ?? PENDING_POLL_DELAYS_MS[Math.min(attempt, PENDING_POLL_DELAYS_MS.length - 1)]!;
  pendingPollTimer = setTimeout(() => {
    pendingPollTimer = null;
    if (selectedAnimeId.value !== animeId || generation !== pendingPollGeneration) {
      return;
    }
    void getAnimeDetail(animeId, { progressive: true, keyword })
      .then((detail) => {
        writeDetailCache(animeId, detail);
        if (selectedAnimeId.value !== animeId || generation !== pendingPollGeneration) {
          return;
        }
        const current = animeDetail.value;
        if (current) {
          animeDetail.value = {
            ...current,
            titles: detail.titles?.length ? detail.titles : current.titles,
            summary: detail.summary ?? current.summary,
            intro: detail.intro ?? current.intro,
            tags: detail.tags?.length ? detail.tags : current.tags,
            rank: detail.rank ?? current.rank,
            totalEpisodes: detail.totalEpisodes ?? current.totalEpisodes,
            platforms: detail.platforms?.length ? detail.platforms : current.platforms,
            episodes: detail.episodes,
            detailPending: detail.detailPending,
            downloads: detail.downloads,
            providerStatuses: detail.providerStatuses
          };
        }
        if (detail.detailPending || (detail.providerStatuses ?? []).some((status) => status.pending)) {
          schedulePendingRefill(animeId, keyword, deadline, attempt + 1);
        }
      })
      .catch(() => {
        if (selectedAnimeId.value === animeId && generation === pendingPollGeneration && animeDetail.value) {
          animeDetail.value = {
            ...animeDetail.value,
            detailPending: false,
            providerStatuses: (animeDetail.value.providerStatuses ?? []).map((status) =>
              status.pending ? { ...status, pending: false, ok: false, reason: "连接超时" } : status
            )
          };
        }
      });
  }, delay);
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
  if (detail.detailPending || (detail.providerStatuses ?? []).some((status) => status.pending)) {
    schedulePendingRefill(
      detail.anime.animeId,
      detail.mikanKeyword || detail.anime.title,
      Date.now() + PENDING_POLL_TIMEOUT_MS,
      0
    );
  }
}

function createPendingAnimeDetail(anime: HomepageAnime): AnimeDetail {
  return {
    detailPending: true,
    anime,
    titles: [anime.title],
    summary: null,
    intro: anime.officialSite,
    episodes: [],
    mikanKeyword: anime.title,
    downloads: [],
    providerStatuses: buildInitialPendingStatuses(),
    tags: anime.tags ?? [],
    rank: anime.rank ?? null,
    totalEpisodes: anime.totalEpisodes ?? null
  };
}

let prefetchInFlight = 0;
const PREFETCH_CONCURRENCY = 3;
export function prefetchAnimeDetail(animeId: string, keyword?: string): void {
  if (!animeId || prefetchInFlight >= PREFETCH_CONCURRENCY) return;
  const cached = readDetailCache(animeId);
  if (cached && Date.now() - cached.savedAt < DETAIL_CACHE_FRESH_MS) return;
  if (detailInFlight.has(animeId)) return;
  prefetchInFlight += 1;
  void fetchProgressiveDetail(animeId, keyword)
    .catch(() => {})
    .finally(() => {
      prefetchInFlight -= 1;
    });
}

export async function openAnimeDetail(anime: HomepageAnime): Promise<void> {
  stopPendingPoll();
  selectedAnimeId.value = anime.animeId;
  selectedSubgroup.value = "all";
  selectedResolution.value = "all";
  selectedProvider.value = "all";
  downloadPage.value = 1;
  animeDetailError.value = null;

  const cached = readDetailCache(anime.animeId);
  if (cached) {
    detailLoadingAnimeId.value = null;
    applyAnimeDetail(cached.detail, anime.imageUrl);
    const hasPending =
      Boolean(cached.detail.detailPending) ||
      (cached.detail.providerStatuses ?? []).some((status) => status.pending);
    if (!hasPending && Date.now() - cached.savedAt > DETAIL_CACHE_FRESH_MS) {
      schedulePendingRefill(
        anime.animeId,
        cached.detail.mikanKeyword || anime.title,
        Date.now() + PENDING_POLL_TIMEOUT_MS,
        0,
        80
      );
    }
    return;
  }

  detailLoadingAnimeId.value = anime.animeId;
  animeDetail.value = createPendingAnimeDetail(anime);
  await withBusy(`anime-detail:${anime.animeId}`, async () => {
    try {
      const detail = await fetchProgressiveDetail(anime.animeId, anime.title);
      if (selectedAnimeId.value !== anime.animeId) {
        return;
      }
      applyAnimeDetail(detail, anime.imageUrl);
      const stillPending = (detail.providerStatuses ?? []).some((status) => status.pending);
      if (!animeDetailError.value && !stillPending) {
        updateFeedback(
          t("home.syncMikan", { count: detail.downloads.length }),
          "success"
        );
      } else {
        feedback.value = animeDetailError.value ?? "已展示可用片源，其余来源后台同步中…";
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

/** 从下载任务反查番剧详情（下载页右栏与播放页剧集面板）。 */
export async function openDownloadAnimeDetail(download: DownloadJob): Promise<void> {
  stopPendingPoll();
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
        const cached = readDetailCache(savedAnimeId);
        detail = cached ? cached.detail : await fetchProgressiveDetail(savedAnimeId, keyword || undefined).catch(() => null);
      }
      if (!detail) {
        let matches = await searchAnime(keyword);
        if (!matches.items.length) {
          const cjkPrefix = keyword
            .split(/\s+\/\s+/u)[0]!
            .replace(
              /([\p{Script=Han}\p{Script=Hiragana}\p{Script=Katakana}])\s+(?=.*[a-z])[A-Za-z][A-Za-z0-9'-]*(?:\s+[A-Za-z][A-Za-z0-9'-]*)+$/u,
              "$1"
            )
            .trim();
          if (cjkPrefix && cjkPrefix !== keyword) {
            matches = await searchAnime(cjkPrefix);
          }
        }
        const anime = matches.items[0];
        if (!anime) {
          animeDetailError.value = `没有查到「${keyword}」对应的番剧信息。`;
          feedback.value = animeDetailError.value;
          return;
        }
        const cached = readDetailCache(anime.animeId);
        detail = cached ? cached.detail : await fetchProgressiveDetail(anime.animeId, anime.title);
        fallbackImageUrl = anime.imageUrl ?? fallbackImageUrl;
      }

      if (selectedDownloadDetailGid.value !== download.gid) return;
      selectedAnimeId.value = detail.anime.animeId;
      applyAnimeDetail(detail, fallbackImageUrl);
      if (animeDetailError.value) {
        feedback.value = animeDetailError.value;
      } else if ((detail.providerStatuses ?? []).some((status) => status.pending)) {
        feedback.value = "已展示可用剧集与片源，其余来源后台同步中…";
      } else {
        feedback.value = t("home.queryAnime", { title: detail.anime.title, count: detail.downloads.length });
      }
    } catch (error) {
      if (selectedDownloadDetailGid.value !== download.gid) return;
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
    downloads.value.find((download) =>
      Boolean(
        (download.feedItemSourceUrl && urls.includes(download.feedItemSourceUrl)) ||
          (download.sourceUrl && urls.includes(download.sourceUrl)) ||
          (download.fallbackUrl && urls.includes(download.fallbackUrl))
      )
    ) ?? null
  );
}

export function isMikanDownloadQueued(item: MikanDownloadItem): boolean {
  return Boolean(findQueuedMikanDownload(item));
}

export async function queueMikanDownload(
  item: MikanDownloadItem,
  options: { saveTo?: "local" | "cloud" } = {}
): Promise<DownloadJob | null> {
  const url = item.torrentUrl ?? item.magnetUrl;
  const fallbackUrl = item.torrentUrl && item.magnetUrl ? item.magnetUrl : undefined;
  if (!url) {
    updateFeedback(t("downloads.noMikanUrl"), "error");
    return null;
  }
  const existing = findQueuedMikanDownload(item);
  if (existing && (options.saveTo !== "cloud" || existing.saveTo === "cloud")) {
    updateFeedback(t("downloads.alreadyQueued"), "info");
    return existing;
  }

  return withBusy(`mikan-download:${item.id}`, async () => {
    try {
      const animeTitle = animeDetail.value?.anime.title?.trim() || undefined;
      const created = await createDownload({
        url,
        ...(fallbackUrl ? { fallbackUrl } : {}),
        ...(options.saveTo ? { saveTo: options.saveTo } : {}),
        ...(animeTitle ? { animeTitle } : {}),
        title: item.title,
        imageUrl: animeDetail.value?.anime.imageUrl ?? null,
        animeId: animeDetail.value?.anime.animeId,
        pageUrl: item.pageUrl ?? undefined
      });
      updateFeedback(
        options.saveTo === "cloud" ? "已加入队列，完成后将自动转存至 Google Drive。" : t("downloads.queued"),
        "success"
      );
      await Promise.allSettled([refreshDownloads(), refreshFeedItems()]);
      return created;
    } catch (error) {
      toastError(error, t("downloads.queueFailed"));
      return null;
    }
  });
}

export async function queueMikanDownloadsBatch(
  items: MikanDownloadItem[],
  options: { saveTo?: "local" | "cloud" } = {}
): Promise<void> {
  const candidates = items.filter((item) => {
    if (!item.torrentUrl && !item.magnetUrl) {
      return false;
    }
    const existing = findQueuedMikanDownload(item);
    if (!existing) {
      return true;
    }
    return options.saveTo === "cloud" && existing.saveTo !== "cloud";
  });

  if (!candidates.length) {
    updateFeedback("当前筛选列表里的资源均已在队列中。", "info");
    return;
  }

  await withBusy("mikan-download:batch", async () => {
    try {
      const animeTitle = animeDetail.value?.anime.title?.trim() || undefined;
      const result = await createDownloadsBatch({
        ...(options.saveTo ? { saveTo: options.saveTo } : {}),
        ...(animeTitle ? { animeTitle } : {}),
        items: candidates.map((item) => ({
          url: (item.torrentUrl ?? item.magnetUrl)!,
          ...(item.torrentUrl && item.magnetUrl ? { fallbackUrl: item.magnetUrl } : {}),
          ...(animeTitle ? { animeTitle } : {}),
          title: item.title,
          imageUrl: animeDetail.value?.anime.imageUrl ?? null,
          animeId: animeDetail.value?.anime.animeId,
          pageUrl: item.pageUrl ?? undefined
        }))
      });
      updateFeedback(
        options.saveTo === "cloud"
          ? `已批量加入 ${result.queuedCount} 条资源，下载完成后自动转存至 Google Drive。`
          : `已批量加入 ${result.queuedCount} 条下载任务。`,
        result.queuedCount > 0 ? "success" : "info"
      );
      await Promise.allSettled([refreshDownloads(), refreshFeedItems()]);
    } catch (error) {
      toastError(error, t("downloads.queueFailed"));
    }
  });
}

export function resetDiscoveryView(): void {
  closeAnimeDetail();
  discoveryMode.value = "schedule";
  activeDiscoverySearch.value = "";
  historicalAnimeResults.value = [];
  discoveryForm.search = "";
  discoveryForm.weekday = getTodayWeekdayKey();
  animePage.value = 1;
  downloadPage.value = 1;
  selectedSubgroup.value = "all";
  selectedResolution.value = "all";
  selectedProvider.value = "all";
}
