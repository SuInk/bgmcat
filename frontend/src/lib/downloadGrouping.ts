import type { DownloadJob, Subscription } from "@shared/index";
import { extractEpisodeNumberFromTitle } from "@shared/episode";
import type { DownloadEpisodeGroup } from "../components/downloads/DownloadVersionPane.vue";
import {
  canArchiveToCloud,
  cloudUploadProgress,
  isCloudFailed,
  isCloudStored,
  isCloudTransferring
} from "./cloudArchive";
import { trimTitleSeparators } from "./downloadTitle";
import { formatBytes, formatSpeed, progressPercent } from "./format";

export type DownloadFilterKey =
  | "all"
  | "active"
  | "transferring"
  | "waiting"
  | "paused"
  | "complete"
  | "cloud"
  | "issues";
export type DownloadSortMode = "status" | "updated" | "progress";

export type EpisodeDownloadGroup = DownloadEpisodeGroup & {
  episodeNumber: number | null;
};

export type SeriesDownloadGroup = {
  key: string;
  title: string;
  downloads: DownloadJob[];
  episodes: EpisodeDownloadGroup[];
  primary: DownloadJob;
  playable: DownloadJob | null;
  playableEpisodeLabel: string | null;
};

export const DOWNLOAD_STATUS_PRIORITY: Record<DownloadJob["status"], number> = {
  active: 0,
  waiting: 1,
  queued: 2,
  paused: 3,
  error: 4,
  complete: 5,
  removed: 6
};

type ParsedTitleMeta = {
  episode: string | null;
  series: string;
  resolution: string | null;
  subgroupBracket: string | null;
  coverLabel: string;
};

const MAX_PARSED_TITLE_CACHE = 2000;
const parsedTitleCache = new Map<string, ParsedTitleMeta>();
const bangumiIdCache = new Map<string, string | null>();

function computeCoverLabel(title: string): string {
  const cleaned = title
    .replace(/\[[^\]]+]/g, " ")
    .replace(/\([^)]+\)/g, " ")
    .replace(/\.torrent$/i, "")
    .trim();
  const characters = cleaned.match(/[\p{Script=Han}\p{Script=Hiragana}\p{Script=Katakana}A-Za-z0-9]/gu);
  return characters?.slice(0, 2).join("").toUpperCase() ?? "番";
}

function computeNormalizedSeriesTitle(title: string, episode: string | null): string {
  const withoutExt = title.replace(/\.(?:mkv|mp4|avi|mov|webm|flv|ass|srt|torrent)$/i, " ");
  let bracketCleaned = withoutExt
    .replace(/\[[^\]]+]/g, " ")
    .replace(/【[^】]+】/g, " ")
    .replace(/\([^)]+\)/g, " ");

  // 兼容「【字幕组】[番名][04][1080p]」这种把番名也包在方括号里的命名
  if (!/[\p{Script=Han}\p{Script=Hiragana}\p{Script=Katakana}A-Za-z]/u.test(bracketCleaned)) {
    const secondBracketMatch = withoutExt.match(/^\s*(?:\[[^\]]+\]|【[^】]+】)\s*(?:\[([^\]]+)\]|【([^】]+)】)/u);
    if (secondBracketMatch) {
      bracketCleaned = secondBracketMatch[1] ?? secondBracketMatch[2] ?? bracketCleaned;
    }
  }

  let normalized = bracketCleaned;

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
    .replace(/\b(?:1080p|720p|2160p|4k|hevc|avc|x26[45]|aac|flac|web-?rip|web-?dl|baha|gb|big5|chs|cht|mp4|mkv)\b/gi, " ")
    .replace(/(?:日英双语|简日双语|繁日双语|多国字幕|简繁内封字幕|简繁内封|简日内嵌|繁日内嵌|内嵌|外挂|字幕组|简日|繁日|简体|繁体|合集|先行|正式)/g, " ")
    .replace(/\s+/g, " ")
    .trim();

  const slashSplit = cleaned.split(/\s+\/\s+/u)[0] ?? cleaned;
  // 去掉中文番名后用空格直接拼接的罗马音副标题（如「FX战士久留美 FX Senshi Kurumi-chan」）
  const withoutRomajiTail = slashSplit.replace(
    /([\p{Script=Han}\p{Script=Hiragana}\p{Script=Katakana}])\s+(?=.*[a-z])[A-Za-z][A-Za-z0-9'-]*(?:\s+[A-Za-z][A-Za-z0-9'-]*)+$/u,
    "$1"
  );
  // 统一中英文之间的盘古空格（如「FX 战士久留美」→「FX战士久留美」）
  const unifiedSpacing = withoutRomajiTail
    .replace(/([A-Za-z0-9])\s+([\p{Script=Han}\p{Script=Hiragana}\p{Script=Katakana}])/gu, "$1$2")
    .replace(/([\p{Script=Han}\p{Script=Hiragana}\p{Script=Katakana}])\s+([A-Za-z0-9]+)(?=\s*[\p{Script=Han}\p{Script=Hiragana}\p{Script=Katakana}])/gu, "$1$2");

  return trimTitleSeparators(unifiedSpacing);
}

/**
 * 3 秒轮询下下载任务的标题几乎不变，缓存正则拆解结果避免每轮对全部任务重复构建正则与清洗。
 */
export function parseDownloadTitleMeta(title: string): ParsedTitleMeta {
  const cached = parsedTitleCache.get(title);
  if (cached) {
    return cached;
  }
  if (parsedTitleCache.size >= MAX_PARSED_TITLE_CACHE) {
    parsedTitleCache.clear();
  }
  const episode = extractEpisodeNumberFromTitle(title);
  const series = computeNormalizedSeriesTitle(title, episode);
  const resMatch = title.match(/\b(2160p|1080p|720p|480p|4k)\b/i);
  const bracketMatch = title.match(/^\s*(?:\[([^\]]+)\]|【([^】]+)】)/u);
  const meta: ParsedTitleMeta = {
    episode,
    series,
    resolution: resMatch?.[1]?.toUpperCase() ?? null,
    subgroupBracket: (bracketMatch?.[1] ?? bracketMatch?.[2])?.trim() ?? null,
    coverLabel: computeCoverLabel(title)
  };
  parsedTitleCache.set(title, meta);
  return meta;
}

export function coverLabel(title: string): string {
  return parseDownloadTitleMeta(title).coverLabel;
}

export function coverStyle(seed: string): Record<string, string> {
  let hash = 0;
  for (let i = 0; i < seed.length; i++) {
    hash += seed.charCodeAt(i);
  }
  const hue = hash % 360;
  return {
    background: `hsl(${hue} 14% 36%)`
  };
}

export function extractBangumiId(value: string | null | undefined): string | null {
  const raw = value?.trim();
  if (!raw) {
    return null;
  }
  if (bangumiIdCache.has(raw)) {
    return bangumiIdCache.get(raw) ?? null;
  }
  if (bangumiIdCache.size >= MAX_PARSED_TITLE_CACHE) {
    bangumiIdCache.clear();
  }

  let result: string | null = null;
  if (raw.startsWith("anime:")) {
    result = raw.slice("anime:".length).trim() || null;
  } else {
    try {
      const url = new URL(raw);
      const queryId = url.searchParams.get("bangumiId") ?? url.searchParams.get("BangumiId");
      if (queryId?.trim()) {
        result = queryId.trim();
      } else {
        const pathMatch = url.pathname.match(/\/(?:Home\/)?Bangumi\/([^/?#]+)/i);
        if (pathMatch?.[1]) {
          result = decodeURIComponent(pathMatch[1]);
        }
      }
    } catch {
      const queryMatch = raw.match(/[?&]bangumiId=([^&#]+)/i);
      if (queryMatch?.[1]) {
        result = decodeURIComponent(queryMatch[1]);
      } else {
        const pathMatch = raw.match(/\/(?:Home\/)?Bangumi\/([^/?#]+)/i);
        if (pathMatch?.[1]) {
          result = decodeURIComponent(pathMatch[1]);
        }
      }
    }
  }

  bangumiIdCache.set(raw, result);
  return result;
}

export function resolveDownloadBangumiId(
  download: DownloadJob,
  subscriptionById: Map<number, Subscription>
): string | null {
  if (download.animeId?.trim()) {
    return download.animeId.trim();
  }
  const subscription = download.feedItemSubscriptionId
    ? subscriptionById.get(download.feedItemSubscriptionId) ?? null
    : null;
  if (subscription?.animeId?.trim()) {
    return subscription.animeId.trim();
  }
  const subscriptionUrl = subscription?.url ?? null;
  const candidates = [download.feedItemGuid, download.feedItemLink, download.feedItemSourceUrl, subscriptionUrl];

  for (const candidate of candidates) {
    const id = extractBangumiId(candidate);
    if (id) {
      return id;
    }
  }
  return null;
}

function resolveCanonicalAnimeTitle(
  download: DownloadJob,
  subscriptionById: Map<number, Subscription>
): string | null {
  if (download.animeTitle?.trim()) {
    const raw = download.animeTitle.trim();
    const sepIdx = raw.lastIndexOf(" · ");
    return sepIdx > 0 ? raw.slice(0, sepIdx).trim() : raw;
  }
  const sub = download.feedItemSubscriptionId
    ? subscriptionById.get(download.feedItemSubscriptionId) ?? null
    : null;
  if (sub?.animeId && sub.name?.trim()) {
    const sepIdx = sub.name.lastIndexOf(" · ");
    return sepIdx > 0 ? sub.name.slice(0, sepIdx).trim() : sub.name.trim();
  }
  return null;
}

export function effectiveStatusPriority(download: DownloadJob): number {
  if (download.status === "complete") {
    if (isCloudTransferring(download)) {
      return 0.5;
    }
    if (isCloudFailed(download)) {
      return 4;
    }
  }
  return DOWNLOAD_STATUS_PRIORITY[download.status];
}

export function matchesDownloadFilter(download: DownloadJob, currentFilter: DownloadFilterKey): boolean {
  switch (currentFilter) {
    case "active":
      return download.status === "active";
    case "transferring":
      return isCloudTransferring(download);
    case "waiting":
      return download.status === "waiting" || download.status === "queued";
    case "paused":
      return download.status === "paused";
    case "issues":
      return download.status === "error" || Boolean(download.issueKind) || isCloudFailed(download);
    case "complete":
      return download.status === "complete";
    case "cloud":
      return isCloudStored(download);
    default:
      return true;
  }
}

export function computeFilterCounts(items: DownloadJob[]): Record<DownloadFilterKey, number> {
  const counts: Record<DownloadFilterKey, number> = {
    all: items.length,
    active: 0,
    transferring: 0,
    waiting: 0,
    paused: 0,
    complete: 0,
    cloud: 0,
    issues: 0
  };
  for (const d of items) {
    if (d.status === "active") {
      counts.active++;
    } else if (d.status === "waiting" || d.status === "queued") {
      counts.waiting++;
    } else if (d.status === "paused") {
      counts.paused++;
    } else if (d.status === "complete") {
      counts.complete++;
    }
    if (isCloudTransferring(d)) {
      counts.transferring++;
    }
    if (isCloudStored(d)) {
      counts.cloud++;
    }
    if (d.status === "error" || Boolean(d.issueKind) || isCloudFailed(d)) {
      counts.issues++;
    }
  }
  return counts;
}

export function compareDownloadsByMode(left: DownloadJob, right: DownloadJob, sortMode: DownloadSortMode): number {
  if (sortMode === "updated") {
    return right.updatedAt.localeCompare(left.updatedAt) || left.gid.localeCompare(right.gid);
  }

  if (sortMode === "progress") {
    const leftProgress = progressPercent(left);
    const rightProgress = progressPercent(right);
    return rightProgress - leftProgress || right.updatedAt.localeCompare(left.updatedAt) || left.gid.localeCompare(right.gid);
  }

  const statusDelta = effectiveStatusPriority(left) - effectiveStatusPriority(right);
  if (statusDelta !== 0) {
    return statusDelta;
  }
  const speedDelta = (right.downloadSpeed || 0) - (left.downloadSpeed || 0);
  if (speedDelta !== 0) {
    return speedDelta;
  }
  const progressDelta = progressPercent(right) - progressPercent(left);
  if (progressDelta !== 0) {
    return progressDelta;
  }
  const bytesDelta = (right.completedLength || 0) - (left.completedLength || 0);
  if (bytesDelta !== 0) {
    return bytesDelta;
  }
  return right.updatedAt.localeCompare(left.updatedAt) || left.gid.localeCompare(right.gid);
}

export interface DownloadsTotalProgress {
  percent: number;
  widthPercent: number;
  localCompletedBytes: number;
  localTotalBytes: number;
  estimatedTotalBytes: number;
  hasUnresolvedSize: boolean;
  hasCloudPipeline: boolean;
  allLocalComplete: boolean;
  cloudUploadedBytes: number;
  cloudTotalBytes: number;
  cloudTaskCount: number;
  cloudStoredCount: number;
  finishedTaskCount: number;
  totalTaskCount: number;
  downloadSpeed: number;
  uploadSpeed: number;
  cloudSpeed: number;
  etaSeconds: number | null;
}

function knownDownloadBytes(download: DownloadJob): number {
  const filesTotal = (download.files ?? []).reduce((sum, file) => sum + (file.size ?? 0), 0);
  const archiveFilesTotal = (download.cloudArchive?.files ?? []).reduce((sum, file) => sum + (file.size ?? 0), 0);
  return Math.max(download.totalLength || 0, filesTotal, archiveFilesTotal);
}

function isCloudPipelineDownload(download: DownloadJob): boolean {
  if (download.status === "removed") {
    return false;
  }
  return (
    isCloudStored(download) ||
    isCloudTransferring(download) ||
    isCloudFailed(download) ||
    download.saveTo === "cloud"
  );
}

/**
 * 计算一组任务（整部番或单集多版本）的真实总进度：
 * 1. 未解析出体积（totalLength=0）的排队/暂停磁力任务按已知分集均重计入总分母，避免「1 集下完 + 10 集未开始 = 99%」；
 * 2. 开启云盘归档的任务将「本地下载 + 云端上传」纳入同一总流水线（各占 50%），既反映真实总进度，
 *    又避免最后一集本地刚下完时进度条从 99% 倒跳回单集上传百分比；
 * 3. 总剩余时间综合本地剩余下载与云盘剩余上传耗时。
 */
export function computeDownloadsTotalProgress(groupDownloads: DownloadJob[]): DownloadsTotalProgress {
  if (!groupDownloads.length) {
    return {
      percent: 0,
      widthPercent: 0,
      localCompletedBytes: 0,
      localTotalBytes: 0,
      estimatedTotalBytes: 0,
      hasUnresolvedSize: false,
      hasCloudPipeline: false,
      allLocalComplete: false,
      cloudUploadedBytes: 0,
      cloudTotalBytes: 0,
      cloudTaskCount: 0,
      cloudStoredCount: 0,
      finishedTaskCount: 0,
      totalTaskCount: 0,
      downloadSpeed: 0,
      uploadSpeed: 0,
      cloudSpeed: 0,
      etaSeconds: null
    };
  }

  const knownSizes = groupDownloads.map(knownDownloadBytes).filter((size) => size > 0);
  const avgKnownSize =
    knownSizes.length > 0
      ? Math.round(knownSizes.reduce((sum, size) => sum + size, 0) / knownSizes.length)
      : 0;

  let localCompletedBytes = 0;
  let localTotalBytes = 0;
  let estimatedTotalBytes = 0;
  let hasUnresolvedSize = false;
  let cloudUploadedBytes = 0;
  let cloudTotalBytes = 0;
  let cloudTaskCount = 0;
  let cloudStoredCount = 0;
  let finishedTaskCount = 0;
  let downloadSpeed = 0;
  let uploadSpeed = 0;
  let cloudSpeed = 0;
  let totalWork = 0;
  let doneWork = 0;

  for (const download of groupDownloads) {
    const rawTotal = knownDownloadBytes(download);
    if (rawTotal === 0 && download.status !== "complete") {
      hasUnresolvedSize = true;
    }
    const estTotal = rawTotal > 0 ? rawTotal : avgKnownSize;
    const localDone =
      download.status === "complete"
        ? estTotal
        : Math.min(Math.max(0, download.completedLength || 0), estTotal || Math.max(0, download.completedLength || 0));

    localTotalBytes += rawTotal;
    estimatedTotalBytes += estTotal;
    localCompletedBytes += localDone;
    downloadSpeed += Math.max(0, download.downloadSpeed || 0);
    uploadSpeed += Math.max(0, download.uploadSpeed || 0);

    const needsCloud = isCloudPipelineDownload(download);
    const cloudProg = needsCloud ? cloudUploadProgress(download) : null;
    if (needsCloud && cloudProg) {
      const cTotal = estTotal > 0 ? estTotal : cloudProg.total;
      const cDone = isCloudStored(download) ? cTotal : Math.min(cTotal, cloudProg.uploaded);
      cloudTaskCount += 1;
      if (isCloudStored(download)) {
        cloudStoredCount += 1;
      }
      cloudTotalBytes += cTotal;
      cloudUploadedBytes += cDone;
      cloudSpeed += cloudProg.speed;

      if (estTotal > 0) {
        totalWork += estTotal * 2;
        doneWork += localDone + cDone;
      } else {
        const localFrac = download.status === "complete" ? 1 : progressPercent(download) / 100;
        const cloudFrac = isCloudStored(download) ? 1 : cloudProg.percent / 100;
        totalWork += 1;
        doneWork += 0.5 * localFrac + 0.5 * cloudFrac;
      }
    } else {
      if (estTotal > 0) {
        totalWork += estTotal;
        doneWork += localDone;
      } else {
        totalWork += 1;
        doneWork += download.status === "complete" ? 1 : progressPercent(download) / 100;
      }
    }

    if (download.status === "complete" && (!needsCloud || isCloudStored(download))) {
      finishedTaskCount += 1;
    }
  }

  const allLocalComplete = groupDownloads.every((download) => download.status === "complete");
  const allFullyDone = finishedTaskCount === groupDownloads.length;

  let percent = 0;
  let widthPercent = 0;
  if (allFullyDone) {
    percent = 100;
    widthPercent = 100;
  } else if (totalWork > 0) {
    const ratio = Math.max(0, Math.min(1, doneWork / totalWork));
    percent = Math.min(99, Math.floor(ratio * 100));
    widthPercent = Math.min(99.5, Math.round(ratio * 1000) / 10);
  }

  const dlRemaining = Math.max(0, estimatedTotalBytes - localCompletedBytes);
  const cloudRemaining = Math.max(0, cloudTotalBytes - cloudUploadedBytes);
  const dlEta = downloadSpeed > 0 && dlRemaining > 0 ? Math.ceil(dlRemaining / downloadSpeed) : 0;
  const cloudEta = cloudSpeed > 0 && cloudRemaining > 0 ? Math.ceil(cloudRemaining / cloudSpeed) : 0;
  const etaSeconds = dlEta > 0 || cloudEta > 0 ? Math.max(dlEta, cloudEta) : null;

  return {
    percent,
    widthPercent,
    localCompletedBytes,
    localTotalBytes,
    estimatedTotalBytes,
    hasUnresolvedSize,
    hasCloudPipeline: cloudTaskCount > 0,
    allLocalComplete,
    cloudUploadedBytes,
    cloudTotalBytes,
    cloudTaskCount,
    cloudStoredCount,
    finishedTaskCount,
    totalTaskCount: groupDownloads.length,
    downloadSpeed,
    uploadSpeed,
    cloudSpeed,
    etaSeconds
  };
}

export function aggregateGroupProgress(groupDownloads: DownloadJob[]): number {
  return computeDownloadsTotalProgress(groupDownloads).percent;
}

export function aggregateGroupProgressWidth(groupDownloads: DownloadJob[]): number {
  return computeDownloadsTotalProgress(groupDownloads).widthPercent;
}

export function bestGroupStatusPriority(groupDownloads: DownloadJob[]): number {
  return Math.min(...groupDownloads.map(effectiveStatusPriority));
}

export function canPauseDownload(download: DownloadJob): boolean {
  return ["active", "waiting", "queued"].includes(download.status);
}

export function canResumeDownload(download: DownloadJob): boolean {
  return ["paused", "error"].includes(download.status);
}

export function canPlayDownload(download: DownloadJob): boolean {
  return download.canPlayExistingFile || download.status === "complete";
}

export function playableDownloadForEpisode(episode: EpisodeDownloadGroup): DownloadJob | null {
  return episode.downloads.find(canPlayDownload) ?? null;
}

/**
 * 行级播放键指向「最新一个能播的集」：同番有新集在下载时，代表任务是那个活跃的新集，
 * 但用户想点的是已经下完的最后一集。按集数取最大，集数未知时退到最近更新的那条。
 */
function findPlayableForEpisodes(episodes: EpisodeDownloadGroup[]): {
  download: DownloadJob | null;
  episodeLabel: string | null;
} {
  let best: { download: DownloadJob; episodeNumber: number | null; episodeLabel: string | null } | null = null;
  for (const episode of episodes) {
    for (const download of episode.downloads) {
      if (!canPlayDownload(download)) {
        continue;
      }
      if (!best) {
        best = { download, episodeNumber: episode.episodeNumber, episodeLabel: episode.episodeLabel };
        continue;
      }
      const leftEpisode = episode.episodeNumber;
      const rightEpisode = best.episodeNumber;
      if (leftEpisode !== null && rightEpisode !== null) {
        if (leftEpisode > rightEpisode) {
          best = { download, episodeNumber: leftEpisode, episodeLabel: episode.episodeLabel };
        }
        continue;
      }
      if (leftEpisode !== null && rightEpisode === null) {
        best = { download, episodeNumber: leftEpisode, episodeLabel: episode.episodeLabel };
        continue;
      }
      if (leftEpisode === null && rightEpisode === null && download.updatedAt > best.download.updatedAt) {
        best = { download, episodeNumber: null, episodeLabel: episode.episodeLabel };
      }
    }
  }
  return {
    download: best?.download ?? null,
    episodeLabel: best?.episodeLabel ?? null
  };
}

export function buildGroupedDownloads(
  items: DownloadJob[],
  sortMode: DownloadSortMode,
  getDisplayTitle: (d: DownloadJob) => string,
  getShortTitle: (d: DownloadJob) => string,
  subscriptionById: Map<number, Subscription>
): SeriesDownloadGroup[] {
  const compare = (a: DownloadJob, b: DownloadJob) => compareDownloadsByMode(a, b, sortMode);
  const seriesGroups = new Map<
    string,
    {
      key: string;
      title: string;
      downloads: DownloadJob[];
      episodesByKey: Map<string, EpisodeDownloadGroup>;
    }
  >();
  const groupKeyBySeries = new Map<string, string>();
  const groupKeyByBangumiId = new Map<string, string>();

  for (const download of items) {
    const title = getDisplayTitle(download);
    const { episode, series } = parseDownloadTitleMeta(title);
    const canonicalTitle = resolveCanonicalAnimeTitle(download, subscriptionById);
    const displaySeriesTitle = canonicalTitle || series || getShortTitle(download);
    const normalizedSeriesKey = (canonicalTitle || series) ? (canonicalTitle || series).toLowerCase() : "";
    const bangumiId = resolveDownloadBangumiId(download, subscriptionById);

    const existingKey =
      (bangumiId ? groupKeyByBangumiId.get(bangumiId) : undefined) ??
      (normalizedSeriesKey ? groupKeyBySeries.get(normalizedSeriesKey) : undefined);
    const seriesKey =
      existingKey ??
      (bangumiId ? `bangumi:${bangumiId}` : normalizedSeriesKey ? `series:${canonicalTitle || series}` : `series:${title}`);
    if (normalizedSeriesKey) {
      groupKeyBySeries.set(normalizedSeriesKey, seriesKey);
    }
    if (bangumiId) {
      groupKeyByBangumiId.set(bangumiId, seriesKey);
    }
    const episodeKey = `${seriesKey}::episode:${episode ?? "unknown"}`;

    let seriesGroup = seriesGroups.get(seriesKey);
    if (!seriesGroup) {
      seriesGroup = {
        key: seriesKey,
        title: displaySeriesTitle,
        downloads: [],
        episodesByKey: new Map()
      };
      seriesGroups.set(seriesKey, seriesGroup);
    } else if (canonicalTitle && seriesGroup.title !== canonicalTitle) {
      seriesGroup.title = canonicalTitle;
    }

    seriesGroup.downloads.push(download);
    let episodeGroup = seriesGroup.episodesByKey.get(episodeKey);
    if (!episodeGroup) {
      episodeGroup = {
        key: episodeKey,
        title: series || title,
        episodeLabel: episode ? `第 ${episode.padStart(2, "0")} 集` : "未知集数",
        episodeNumber: episode ? Number(episode) : null,
        downloads: []
      };
      seriesGroup.episodesByKey.set(episodeKey, episodeGroup);
    }
    episodeGroup.downloads.push(download);
  }

  return Array.from(seriesGroups.values())
    .map((seriesGroup): SeriesDownloadGroup => {
      const sortedDownloads = [...seriesGroup.downloads].sort(compare);
      const sortedEpisodes = Array.from(seriesGroup.episodesByKey.values())
        .map((episodeGroup) => ({
          ...episodeGroup,
          downloads: [...episodeGroup.downloads].sort(compare)
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
        });
      const playableInfo = findPlayableForEpisodes(sortedEpisodes);
      return {
        key: seriesGroup.key,
        title: seriesGroup.title,
        downloads: sortedDownloads,
        episodes: sortedEpisodes,
        primary: sortedDownloads[0]!,
        playable: playableInfo.download,
        playableEpisodeLabel: playableInfo.episodeLabel
      };
    })
    .sort((left, right) => {
      if (sortMode === "updated") {
        return (right.downloads[0]?.updatedAt ?? "").localeCompare(left.downloads[0]?.updatedAt ?? "") || left.key.localeCompare(right.key);
      }
      if (sortMode === "progress") {
        return aggregateGroupProgress(right.downloads) - aggregateGroupProgress(left.downloads) || left.title.localeCompare(right.title, "zh-CN");
      }
      const statusDelta = bestGroupStatusPriority(left.downloads) - bestGroupStatusPriority(right.downloads);
      if (statusDelta !== 0) {
        return statusDelta;
      }
      return (right.downloads[0]?.updatedAt ?? "").localeCompare(left.downloads[0]?.updatedAt ?? "") || left.title.localeCompare(right.title, "zh-CN");
    });
}

export function statusLabel(status: DownloadJob["status"]): string {
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

export function groupStatus(group: SeriesDownloadGroup): DownloadJob["status"] {
  return group.primary.status;
}

/** 仍在下载、正在转存云盘、或云盘转存报错待重试的番组都留在上方实时进度区；
 *  若每一集都已有完成且云盘转存就绪的版本，其余被自动暂停的 0% 冗余死种不再把整部番卡在「正在缓存」。
 */
export function isGroupActiveOrTransferring(group: SeriesDownloadGroup): boolean {
  const hasCloudPending = group.downloads.some(
    (download) => isCloudTransferring(download) || isCloudFailed(download)
  );
  if (hasCloudPending) {
    return true;
  }
  const hasActiveOrQueued = group.downloads.some((download) =>
    ["active", "waiting", "queued", "error"].includes(download.status)
  );
  if (hasActiveOrQueued) {
    return true;
  }
  const everyEpisodeHasCompleteVersion =
    group.episodes.length > 0 &&
    group.episodes.every((ep) => ep.downloads.some((d) => d.status === "complete"));
  if (everyEpisodeHasCompleteVersion) {
    return false;
  }
  return group.downloads.some((download) => download.status !== "complete");
}

export function groupCloudTransferSummary(group: SeriesDownloadGroup): {
  transferring: boolean;
  failed: boolean;
  percent: number;
  widthPercent: number;
  uploaded: number;
  total: number;
  speed: number;
  etaSeconds: number | null;
} | null {
  const hasActiveOrFailed = group.downloads.some((d) => isCloudTransferring(d) || isCloudFailed(d));
  if (!hasActiveOrFailed) {
    return null;
  }
  const cloudScope = group.downloads.filter(
    (d) => isCloudStored(d) || isCloudTransferring(d) || isCloudFailed(d)
  );
  let uploaded = 0;
  let total = 0;
  let speed = 0;
  let transferring = false;
  let failed = false;
  for (const item of cloudScope) {
    const prog = cloudUploadProgress(item);
    uploaded += prog.uploaded;
    total += prog.total;
    speed += prog.speed;
    if (isCloudTransferring(item)) {
      transferring = true;
    }
    if (isCloudFailed(item)) {
      failed = true;
    }
  }
  const percent = total > 0 ? Math.max(0, Math.min(99, Math.floor((uploaded / total) * 100))) : 0;
  const widthPercent = total > 0 ? Math.max(0, Math.min(99.5, Math.round((uploaded / total) * 1000) / 10)) : 0;
  const remaining = Math.max(0, total - uploaded);
  const etaSeconds = speed > 0 && remaining > 0 ? Math.ceil(remaining / speed) : null;
  return { transferring, failed, percent, widthPercent, uploaded, total, speed, etaSeconds };
}

export function groupCloudStoredCount(group: SeriesDownloadGroup): number {
  return group.downloads.filter(isCloudStored).length;
}

export function groupArchivableGids(group: SeriesDownloadGroup): string[] {
  return group.downloads.filter(canArchiveToCloud).map((d) => d.gid);
}

export function groupProgress(group: SeriesDownloadGroup): number {
  if (group.episodes.length === 1 && group.downloads.length > 1) {
    return computeDownloadsTotalProgress([group.primary]).percent;
  }
  return computeDownloadsTotalProgress(group.downloads).percent;
}

export function groupProgressWidth(group: SeriesDownloadGroup): number {
  if (group.episodes.length === 1 && group.downloads.length > 1) {
    return computeDownloadsTotalProgress([group.primary]).widthPercent;
  }
  return computeDownloadsTotalProgress(group.downloads).widthPercent;
}

export function groupCompletedLength(group: SeriesDownloadGroup): number {
  return computeDownloadsTotalProgress(group.downloads).localCompletedBytes;
}

export function groupTotalLength(group: SeriesDownloadGroup): number {
  const summary = computeDownloadsTotalProgress(group.downloads);
  return summary.localTotalBytes > 0 ? summary.localTotalBytes : summary.estimatedTotalBytes;
}

export function groupDownloadSpeed(group: SeriesDownloadGroup): number {
  return computeDownloadsTotalProgress(group.downloads).downloadSpeed;
}

export function groupRemainingSeconds(group: SeriesDownloadGroup): number | null {
  return computeDownloadsTotalProgress(group.downloads).etaSeconds;
}

export function formatDuration(seconds: number | null): string {
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

export function currentEpisodeLabel(group: SeriesDownloadGroup): string {
  const activeEpisode = group.episodes.find((episode) =>
    episode.downloads.some((download) => ["active", "waiting", "queued", "paused"].includes(download.status))
  );
  return activeEpisode?.episodeLabel ?? group.episodes[0]?.episodeLabel ?? "--";
}

export function resolutionLabel(download: DownloadJob, getDisplayTitle: (d: DownloadJob) => string): string | null {
  return parseDownloadTitleMeta(getDisplayTitle(download)).resolution;
}

export function subgroupLabel(
  download: DownloadJob,
  getDisplayTitle: (d: DownloadJob) => string,
  subscriptionById: Map<number, Subscription>
): string | null {
  const bracket = parseDownloadTitleMeta(getDisplayTitle(download)).subgroupBracket;
  if (bracket) {
    return bracket;
  }
  return download.feedItemSubscriptionId
    ? subscriptionById.get(download.feedItemSubscriptionId)?.name ?? null
    : null;
}

/** 卡片第二行：单集写「第 04 集」，多集写「12 集」，后面跟字幕组、清晰度、总大小 */
export function groupMetaLine(
  group: SeriesDownloadGroup,
  getDisplayTitle: (d: DownloadJob) => string,
  subscriptionById: Map<number, Subscription>
): string {
  const isSettled = !isGroupActiveOrTransferring(group);
  const completedOnly = group.downloads.filter((d) => d.status === "complete");
  const primary = (isSettled && completedOnly[0]) || group.primary;
  const parts = [group.episodes.length === 1 ? currentEpisodeLabel(group) : `${group.episodes.length} 集`];
  if (group.downloads.length > group.episodes.length) {
    parts.push(`${group.downloads.length} 个版本`);
  }
  for (const part of [subgroupLabel(primary, getDisplayTitle, subscriptionById), resolutionLabel(primary, getDisplayTitle)]) {
    if (part) {
      parts.push(part);
    }
  }
  const total =
    isSettled && completedOnly.length > 0 && completedOnly.length < group.downloads.length
      ? computeDownloadsTotalProgress(completedOnly).localTotalBytes
      : groupTotalLength(group);
  if (total > 0) {
    parts.push(formatBytes(total));
  }
  if (isCloudStored(primary)) {
    parts.push("已存云盘");
  } else if (primary.saveTo === "cloud" && !primary.cloudArchive) {
    parts.push("完成后存到云盘");
  }
  return parts.join(" · ");
}

/** 进度条右边那句：下载中给速度和剩余时间，转存云盘中给云端上传速度与剩余时间，其他状态给状态名 */
export function groupProgressLine(group: SeriesDownloadGroup): string {
  const status = groupStatus(group);
  const summary = computeDownloadsTotalProgress(group.downloads);
  if (status === "complete") {
    const cloud = groupCloudTransferSummary(group);
    if (cloud) {
      if (cloud.transferring) {
        return [
          `总进度 ${summary.percent}%`,
          summary.cloudTotalBytes > 0
            ? `云盘 ${formatBytes(summary.cloudUploadedBytes)} / ${formatBytes(summary.cloudTotalBytes)}`
            : null,
          summary.cloudSpeed > 0 ? `☁↑ ${formatSpeed(summary.cloudSpeed)}` : "准备上传",
          summary.etaSeconds ? `剩余 ${formatDuration(summary.etaSeconds)}` : null
        ]
          .filter(Boolean)
          .join(" · ");
      }
      if (cloud.failed) {
        return `转存异常 · 总进度 ${summary.percent}%`;
      }
    }
  }
  const isMultiVersionSingleEpisode = group.episodes.length === 1 && group.downloads.length > 1;
  const primarySummary = isMultiVersionSingleEpisode ? computeDownloadsTotalProgress([group.primary]) : summary;
  const primaryLocalPct = progressPercent(group.primary);
  const progress = isMultiVersionSingleEpisode
    ? primaryLocalPct > 0 && primaryLocalPct !== summary.percent
      ? `最快版本 ${primaryLocalPct}%（${group.downloads.length} 版本总 ${summary.percent}%）`
      : `总进度 ${primarySummary.percent}%`
    : `总进度 ${summary.percent}%`;
  if (status === "active") {
    const activeConns = group.downloads.reduce(
      (sum, d) => sum + (d.status === "active" ? Math.max(0, d.connectionCount || 0) : 0),
      0
    );
    const speedOrPeerState =
      summary.downloadSpeed > 0
        ? `↓ ${formatSpeed(summary.downloadSpeed)}${activeConns > 0 ? ` (${activeConns} 节点)` : ""}`
        : activeConns > 0
          ? `已连 ${activeConns} 节点，等待数据`
          : "寻源中 (0 活跃做种)";
    return [
      progress,
      speedOrPeerState,
      summary.cloudSpeed > 0 ? `☁↑ ${formatSpeed(summary.cloudSpeed)}` : null,
      primarySummary.etaSeconds ? `剩余 ${formatDuration(primarySummary.etaSeconds)}` : null
    ]
      .filter(Boolean)
      .join(" · ");
  }
  return `${statusLabel(status)} · ${progress}`;
}

/** 封面墙卡片下面那行：单集写「第 04 集」，多集写「已缓存 12 集」，后面跟大小与云盘状态 */
export function cachedMetaLine(group: SeriesDownloadGroup): string {
  const episodes = group.episodes.length === 1 ? currentEpisodeLabel(group) : `已缓存 ${group.episodes.length} 集`;
  const completedDownloads = group.downloads.filter((d) => d.status === "complete");
  const effectiveScope = completedDownloads.length > 0 ? completedDownloads : group.downloads;
  const total = computeDownloadsTotalProgress(effectiveScope).localTotalBytes;
  const storedCount = effectiveScope.filter(isCloudStored).length;
  const parts = [episodes];
  if (total > 0) {
    parts.push(formatBytes(total));
  }
  if (storedCount > 0 && storedCount === effectiveScope.length) {
    parts.push("已存云盘");
  } else if (storedCount > 0) {
    parts.push(`${storedCount}/${effectiveScope.length} 存云盘`);
  }
  return parts.join(" · ");
}
