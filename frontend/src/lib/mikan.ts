import {
  extractEpisodeCandidates,
  extractReleaseVersion,
  extractSeasonNumber,
  extractSubgroup,
  isBatchRelease,
  isSpecialOrExtraRelease
} from "@shared/episode";
import type { AnimeDetail, MikanDownloadItem } from "@shared/index";

export type MikanDownloadGroupKind =
  | "episode"
  | "synthetic"
  | "batch"
  | "unmatched"
  | "other-season";

export interface MikanDownloadGroup {
  key: string;
  label: string;
  shortLabel: string;
  seasonSubLabel: string | null;
  episodeTitle: string | null;
  kind: MikanDownloadGroupKind;
  detail: string | null;
  order: number;
  episodeNumber: number | null;
  seasonEpisodeNumber: number | null;
  subgroupCount: number;
  downloads: MikanDownloadItem[];
}

export type SubtitleFilterKey = "all" | "chs" | "cht" | "soft" | "hard";

export interface ReleaseInspection {
  subgroup: string;
  subgroupKey: string;
  resolution: string | null;
  container: "MP4" | "MKV" | null;
  videoCodec: string | null;
  audioCodec: string | null;
  subtitleBadge: string | null;
  isSoftSub: boolean;
  isHardSub: boolean;
  hasChs: boolean;
  hasCht: boolean;
  streamSource: string | null;
  versionTag: string | null;
  isBatch: boolean;
  specs: string[];
}

export interface SubgroupProfile {
  key: string;
  label: string;
  shortLabel: string;
  count: number;
  episodeCount: number;
  latestEpisodeNumber: number | null;
  latestSeasonEpisodeNumber: number | null;
  hasBatch: boolean;
  dominantResolution: string | null;
  dominantContainer: "MP4" | "MKV" | null;
  dominantCodec: string | null;
  dominantSubBadge: string | null;
  dominantSource: string | null;
  latestPublishedAt: string | null;
  latestTimestamp: number;
  progressText: string;
  specSummary: string;
  cardHint: string;
}

/**
 * 一条资源的展示来源。老的 MikanDownloadItem 没有 providerId 字段
 * （比如从缓存里读出来的旧数据），这种一律当蜜柑。
 */
export function providerIdOf(item: MikanDownloadItem): string | null {
  return (item as { providerId?: string }).providerId ?? null;
}

/** 提供了这条资源的全部来源（跨源合并后可能有多个）。 */
export function providerIdsOf(item: MikanDownloadItem): string[] {
  const merged = (item as { providerIds?: string[] }).providerIds;
  if (merged?.length) {
    return merged;
  }
  const single = providerIdOf(item);
  return single ? [single] : [];
}

/** 解析条目的字幕组名（后端字段缺失时从标题前缀兜底提取）。 */
export function resolveSubgroupName(item: MikanDownloadItem): string | null {
  const fromField = item.subgroup?.trim();
  if (fromField) {
    return fromField;
  }
  return extractSubgroup(item.title);
}

export function resolveSubgroupKey(item: MikanDownloadItem): string {
  return resolveSubgroupName(item) || "__unknown";
}

export function shortSubgroupName(name: string): string {
  if (!name || name === "__unknown") {
    return "未标注组";
  }
  return (
    name
      .replace(/^三明治摆烂组\s*[&＆]\s*LoliHouse$/i, "三明治&LoliHouse")
      .replace(/字幕社$/u, "")
      .replace(/字幕组$/u, "")
      .trim() || name
  );
}

export function extractResolutionLabel(title: string): string | null {
  const resolutionMatch = title.match(/\b(2160p|1440p|1080p|720p|480p)\b/i);
  if (resolutionMatch?.[1]) {
    return resolutionMatch[1].toUpperCase();
  }
  const dimensionMatch = title.match(/\b(?:3840|2560|1920|1280|854|720|640)\s*[x×]\s*(2160|1440|1080|720|480)\b/i);
  if (dimensionMatch?.[1]) {
    return `${dimensionMatch[1]}P`;
  }
  if (/\b4k\b/i.test(title)) {
    return "2160P";
  }
  return null;
}

export function inspectReleaseItem(item: MikanDownloadItem): ReleaseInspection {
  const title = item.title;
  const subgroupName = resolveSubgroupName(item);
  const subgroup = subgroupName ?? "未标注字幕组";
  const subgroupKey = subgroupName ?? "__unknown";

  const resolution = extractResolutionLabel(title);
  const container: "MP4" | "MKV" | null = /\bmp4\b/iu.test(title)
    ? "MP4"
    : /\bmkv\b/iu.test(title)
      ? "MKV"
      : null;

  const is10Bit = /\b10\s*bits?\b|hi10p|main10/iu.test(title);
  let videoCodec: string | null = null;
  if (/hevc|[xh]\.?265/iu.test(title)) {
    videoCodec = is10Bit ? "HEVC 10bit" : "HEVC";
  } else if (/avc|[xh]\.?264/iu.test(title)) {
    videoCodec = is10Bit ? "AVC 10bit" : "AVC";
  } else if (/\bav1\b/iu.test(title)) {
    videoCodec = is10Bit ? "AV1 10bit" : "AV1";
  }

  let audioCodec: string | null = null;
  if (/\bflac\b/iu.test(title)) {
    audioCodec = "FLAC";
  } else if (/\be-?ac-?3\b|\bddp\b/iu.test(title)) {
    audioCodec = "EAC3";
  } else if (/\bopus\b/iu.test(title)) {
    audioCodec = "Opus";
  } else if (/\baac\b/iu.test(title)) {
    audioCodec = "AAC";
  }

  let isSoftSub = /内封|內封|软字幕|软字|\bass\b|\bsrt\b|multiple\s+subtitle|multi[\s-]?subs?/iu.test(title);
  let isHardSub = /内嵌|內嵌|硬字幕|硬字/u.test(title);
  const isExternalSub = /外挂|外掛/u.test(title);

  // 未显式写明内封/内嵌时，结合简繁双轨与封装格式推断常见字幕组惯例
  const hasBilingualTrack = /简繁|chs[_&\s/+]+cht|sc[_&\s/+]+tc/iu.test(title);
  if (!isSoftSub && !isHardSub && !isExternalSub) {
    if (hasBilingualTrack && container === "MKV") {
      isSoftSub = true;
    } else if (container === "MP4" && /简|繁|chs|cht|gb|big5/iu.test(title)) {
      isHardSub = true;
    }
  }

  let hasChs = false;
  let hasCht = false;
  let subtitleBadge: string | null = null;

  if (/简繁日|chs[_&\s/+]*cht[_&\s/+]*jpn/iu.test(title)) {
    hasChs = true;
    hasCht = true;
    subtitleBadge = isHardSub ? "简繁日内嵌" : "简繁日内封";
    if (!isHardSub) isSoftSub = true;
  } else if (hasBilingualTrack) {
    hasChs = true;
    hasCht = true;
    subtitleBadge = isSoftSub ? "简繁内封" : isHardSub ? "简繁内嵌" : "简繁双语";
  } else if (/简日|简体双语|chs[_&\s/+]*jpn?/iu.test(title)) {
    hasChs = true;
    subtitleBadge = isSoftSub ? "简日内封" : isHardSub ? "简日内嵌" : "简日双语";
  } else if (/繁日|繁体双语|cht[_&\s/+]*jpn?/iu.test(title)) {
    hasCht = true;
    subtitleBadge = isSoftSub ? "繁日内封" : isHardSub ? "繁日内嵌" : "繁日双语";
  } else if (/多语|multi[\s-]?subs?|multiple\s+subtitle/iu.test(title)) {
    isSoftSub = true;
    subtitleBadge = "多语内封";
  } else if (/简体|简中|(?<![繁])简(?![日繁])|\bchs\b|\bgb\b|\bsc\b/iu.test(title)) {
    hasChs = true;
    subtitleBadge = isSoftSub ? "简体内封" : isHardSub ? "简体内嵌" : "简体中文";
  } else if (/繁体|繁中|(?<![简])繁(?![日])|\bcht\b|\bbig5\b|\btc\b/iu.test(title)) {
    hasCht = true;
    subtitleBadge = isSoftSub ? "繁体内封" : isHardSub ? "繁体内嵌" : "繁体中文";
  } else if (isExternalSub) {
    subtitleBadge = "外挂字幕";
  } else if (isSoftSub) {
    subtitleBadge = "内封软字";
  } else if (isHardSub) {
    subtitleBadge = "内嵌硬字";
  } else if (/\braw\b|生肉|无字幕|無字幕/iu.test(title)) {
    subtitleBadge = "生肉无字";
  }

  let streamSource: string | null = null;
  if (/bd[\s-]?rip|blu[\s-]?ray/iu.test(title)) streamSource = "BDRip";
  else if (/baha|巴哈/iu.test(title)) streamSource = "Baha";
  else if (/b-global/iu.test(title)) streamSource = "B-Global";
  else if (/ani-?one/iu.test(title)) streamSource = "Ani-One";
  else if (/\babema\b/iu.test(title)) streamSource = "ABEMA";
  else if (/crunchyroll|\bcr\b/iu.test(title)) streamSource = "CR";
  else if (/bilibili|哔哩/iu.test(title)) streamSource = "B站源";
  else if (/web-?dl/iu.test(title)) streamSource = "WEB-DL";
  else if (/web-?rip/iu.test(title)) streamSource = "WebRip";

  const versionTag = extractReleaseVersion(title);
  const isBatch = isBatchRelease(title);

  const specs: string[] = [];
  if (resolution) specs.push(resolution);
  if (container) specs.push(container);
  if (videoCodec) specs.push(videoCodec);
  if (audioCodec && audioCodec !== "AAC") {
    specs.push(audioCodec);
  } else if (audioCodec && specs.length < 3) {
    specs.push(audioCodec);
  }

  return {
    subgroup,
    subgroupKey,
    resolution,
    container,
    videoCodec,
    audioCodec,
    subtitleBadge,
    isSoftSub,
    isHardSub,
    hasChs,
    hasCht,
    streamSource,
    versionTag,
    isBatch,
    specs
  };
}

export function matchesSubtitleFilter(info: ReleaseInspection, filter: SubtitleFilterKey): boolean {
  switch (filter) {
    case "all":
      return true;
    case "chs":
      return info.hasChs;
    case "cht":
      return info.hasCht;
    case "soft":
      return info.isSoftSub;
    case "hard":
      return info.isHardSub;
  }
}

export function buildMikanCountOptions(
  items: MikanDownloadItem[],
  getKey: (item: MikanDownloadItem) => string,
  getLabel: (key: string) => string
): Array<{ key: string; label: string; count: number }> {
  const counts = new Map<string, number>();
  for (const item of items) {
    const key = getKey(item);
    counts.set(key, (counts.get(key) ?? 0) + 1);
  }

  return Array.from(counts.entries())
    .map(([key, count]) => ({ key, label: getLabel(key), count }))
    .sort((left, right) => right.count - left.count || left.label.localeCompare(right.label, "zh-Hans-CN"));
}

function normalizeEpisodeNumber(value: string | null | undefined): number | null {
  if (!value) {
    return null;
  }
  const numeric = Number.parseInt(value, 10);
  return Number.isFinite(numeric) && numeric > 0 ? numeric : null;
}

function cleanEpisodeTitle(raw: string | null | undefined): string | null {
  const trimmed = raw?.trim();
  if (!trimmed || /^第\s*\d+(?:\.\d+)?\s*[集话話]$/u.test(trimmed)) {
    return null;
  }
  return trimmed;
}

function normalizeEpisodeTitleForMatch(value: string): string {
  return value
    .toLocaleLowerCase()
    .replace(/[第\s\-_:：・·.。/\\[\]【】（）()'"“”‘’]+/g, "")
    .replace(/[集话話]/g, "");
}

interface EpisodeLookupContext {
  animeSeason: number | null;
  minSort: number | null;
  maxSort: number | null;
  firstAirTimeMs: number | null;
  episodes: AnimeDetail["episodes"];
  bySort: Map<number, AnimeDetail["episodes"][number]>;
  bySeason: Map<number, AnimeDetail["episodes"][number]>;
  seasonNumOfEpisodeId: Map<string, number>;
}

function buildEpisodeLookupContext(
  episodes: AnimeDetail["episodes"],
  animeTitle?: string | null
): EpisodeLookupContext {
  const animeSeason = animeTitle ? extractSeasonNumber(animeTitle) : null;
  const bySort = new Map<number, AnimeDetail["episodes"][number]>();
  const bySeason = new Map<number, AnimeDetail["episodes"][number]>();
  const seasonNumOfEpisodeId = new Map<string, number>();

  let minSort: number | null = null;
  let maxSort: number | null = null;
  let firstAirTimeMs: number | null = null;

  for (const ep of episodes) {
    const sortNum = normalizeEpisodeNumber(ep.episodeNumber);
    if (sortNum !== null) {
      bySort.set(sortNum, ep);
      if (minSort === null || sortNum < minSort) minSort = sortNum;
      if (maxSort === null || sortNum > maxSort) maxSort = sortNum;
    }
    if (ep.airDate) {
      const parsed = Date.parse(ep.airDate);
      if (Number.isFinite(parsed) && (firstAirTimeMs === null || parsed < firstAirTimeMs)) {
        firstAirTimeMs = parsed;
      }
    }
  }

  for (const ep of episodes) {
    const sortNum = normalizeEpisodeNumber(ep.episodeNumber);
    const explicitSeasonEp = normalizeEpisodeNumber(ep.seasonNumber);
    const impliedSeasonEp =
      explicitSeasonEp ??
      (sortNum !== null && minSort !== null && minSort > 1 ? sortNum - minSort + 1 : null);

    if (impliedSeasonEp !== null && impliedSeasonEp !== sortNum) {
      if (!bySeason.has(impliedSeasonEp)) {
        bySeason.set(impliedSeasonEp, ep);
      }
      seasonNumOfEpisodeId.set(ep.episodeId, impliedSeasonEp);
    }
  }

  return {
    animeSeason,
    minSort,
    maxSort,
    firstAirTimeMs,
    episodes,
    bySort,
    bySeason,
    seasonNumOfEpisodeId
  };
}

function isPublishedBeforeCurrentSeason(item: MikanDownloadItem, firstAirTimeMs: number | null): boolean {
  if (firstAirTimeMs === null || !item.publishedAt) {
    return false;
  }
  const pubMs = Date.parse(item.publishedAt);
  if (!Number.isFinite(pubMs)) {
    return false;
  }
  // 早于本季首播 45 天以上的无季号老资源（例如搜第二季时混入两年前第一季同集号）归入「其他季度」
  return pubMs < firstAirTimeMs - 45 * 24 * 3600 * 1000;
}

function hasConflictingSeason(itemSeason: number | null, ctx: EpisodeLookupContext): boolean {
  if (itemSeason === null) {
    return false;
  }
  if (ctx.animeSeason !== null) {
    return itemSeason !== ctx.animeSeason;
  }
  // 当前番剧标题无季号且正片从第 1 话起算（第一季），排除明确标了 S2 / 第二季 的资源
  if (ctx.minSort === 1 && itemSeason >= 2) {
    return true;
  }
  return false;
}

export interface ClassifiedDownloadTarget {
  key: string;
  kind: MikanDownloadGroupKind;
  episode: AnimeDetail["episodes"][number] | null;
  episodeNumber: number | null;
  seasonEpisodeNumber: number | null;
}

export function classifyDownloadForEpisodes(
  item: MikanDownloadItem,
  ctx: EpisodeLookupContext
): ClassifiedDownloadTarget {
  const itemSeason = extractSeasonNumber(item.title);
  if (hasConflictingSeason(itemSeason, ctx)) {
    return {
      key: "other-season",
      kind: "other-season",
      episode: null,
      episodeNumber: null,
      seasonEpisodeNumber: null
    };
  }

  const isOldSeasonItem =
    ((ctx.animeSeason !== null && ctx.animeSeason >= 2) || (ctx.minSort !== null && ctx.minSort > 1)) &&
    itemSeason === null &&
    isPublishedBeforeCurrentSeason(item, ctx.firstAirTimeMs);

  if (isBatchRelease(item.title)) {
    if (isOldSeasonItem) {
      return {
        key: "other-season",
        kind: "other-season",
        episode: null,
        episodeNumber: null,
        seasonEpisodeNumber: null
      };
    }
    return {
      key: "batch",
      kind: "batch",
      episode: null,
      episodeNumber: null,
      seasonEpisodeNumber: null
    };
  }

  if (isSpecialOrExtraRelease(item.title)) {
    return {
      key: isOldSeasonItem ? "other-season" : "unmatched",
      kind: isOldSeasonItem ? "other-season" : "unmatched",
      episode: null,
      episodeNumber: null,
      seasonEpisodeNumber: null
    };
  }

  const candidates = extractEpisodeCandidates(item.title);

  // 1. 优先匹配 Bangumi 总集数 sort（如第 26..37 话，或单季番 1..12 话）
  for (const candidate of candidates) {
    const matched = ctx.bySort.get(candidate);
    if (matched) {
      if (isOldSeasonItem && !ctx.bySeason.size) {
        return {
          key: "other-season",
          kind: "other-season",
          episode: null,
          episodeNumber: null,
          seasonEpisodeNumber: null
        };
      }
      return {
        key: `episode:${matched.episodeId}`,
        kind: "episode",
        episode: matched,
        episodeNumber: normalizeEpisodeNumber(matched.episodeNumber),
        seasonEpisodeNumber: ctx.seasonNumOfEpisodeId.get(matched.episodeId) ?? null
      };
    }
  }

  // 2. 多季番季内集数匹配（如字幕组标 S2 - 06，对应 Bangumi 第 31 话 / ep=6）
  if (ctx.bySeason.size > 0) {
    for (const candidate of candidates) {
      const matched = ctx.bySeason.get(candidate);
      if (matched) {
        if (isOldSeasonItem) {
          return {
            key: "other-season",
            kind: "other-season",
            episode: null,
            episodeNumber: null,
            seasonEpisodeNumber: null
          };
        }
        return {
          key: `episode:${matched.episodeId}`,
          kind: "episode",
          episode: matched,
          episodeNumber: normalizeEpisodeNumber(matched.episodeNumber),
          seasonEpisodeNumber: ctx.seasonNumOfEpisodeId.get(matched.episodeId) ?? candidate
        };
      }
    }
  }

  // 3. 单集标题匹配兜底
  const normalizedTitle = normalizeEpisodeTitleForMatch(item.title);
  const byTitleMatch = ctx.episodes.find((ep) => {
    const t = normalizeEpisodeTitleForMatch(ep.title);
    return Boolean(t && t.length >= 4 && !/^\d+$/.test(t) && normalizedTitle.includes(t));
  });
  if (byTitleMatch) {
    return {
      key: `episode:${byTitleMatch.episodeId}`,
      kind: "episode",
      episode: byTitleMatch,
      episodeNumber: normalizeEpisodeNumber(byTitleMatch.episodeNumber),
      seasonEpisodeNumber: ctx.seasonNumOfEpisodeId.get(byTitleMatch.episodeId) ?? null
    };
  }

  if (isOldSeasonItem) {
    return {
      key: "other-season",
      kind: "other-season",
      episode: null,
      episodeNumber: null,
      seasonEpisodeNumber: null
    };
  }

  // 4. 若 Bangumi 分集列表为空或尚未录入最新集，按标题提取出的单集号合成剧集组，避免全部堆进「未匹配」
  const primaryCandidate = candidates[0] ?? null;
  if (
    primaryCandidate !== null &&
    primaryCandidate >= 1 &&
    primaryCandidate <= 4999 &&
    (ctx.episodes.length === 0 || (ctx.maxSort !== null && primaryCandidate > ctx.maxSort && primaryCandidate <= ctx.maxSort + 120))
  ) {
    return {
      key: `synthetic:${primaryCandidate}`,
      kind: "synthetic",
      episode: null,
      episodeNumber: primaryCandidate,
      seasonEpisodeNumber: null
    };
  }

  return {
    key: "unmatched",
    kind: "unmatched",
    episode: null,
    episodeNumber: null,
    seasonEpisodeNumber: null
  };
}

function formatSeasonSubLabel(seasonNum: number | null, animeSeason: number | null): string | null {
  if (seasonNum === null) {
    return null;
  }
  const padded = String(seasonNum).padStart(2, "0");
  return animeSeason && animeSeason >= 2 ? `S${animeSeason}E${padded}` : `本季 ${padded}`;
}

export function groupMikanDownloadsByEpisode(
  items: MikanDownloadItem[],
  episodes: AnimeDetail["episodes"],
  animeTitle?: string | null
): MikanDownloadGroup[] {
  if (!items.length) {
    return [];
  }

  const ctx = buildEpisodeLookupContext(episodes, animeTitle);
  const groups = new Map<string, MikanDownloadGroup>();
  const subgroupSets = new Map<string, Set<string>>();

  for (const item of items) {
    const target = classifyDownloadForEpisodes(item, ctx);
    let group = groups.get(target.key);
    if (!group) {
      if (target.kind === "episode" && target.episode) {
        const epNum = target.episodeNumber;
        const padNum = epNum !== null ? String(epNum).padStart(2, "0") : null;
        const shortLabel = padNum ? `第 ${padNum} 话` : "剧集";
        const epTitle = cleanEpisodeTitle(target.episode.title);
        const seasonSubLabel = formatSeasonSubLabel(target.seasonEpisodeNumber, ctx.animeSeason);
        const labelParts = [shortLabel];
        if (seasonSubLabel) labelParts.push(`(${seasonSubLabel})`);
        if (epTitle) labelParts.push(`· ${epTitle}`);
        group = {
          key: target.key,
          label: labelParts.join(" "),
          shortLabel,
          seasonSubLabel,
          episodeTitle: epTitle,
          kind: "episode",
          detail: target.episode.airDate ?? null,
          order: epNum ?? 800_000,
          episodeNumber: epNum,
          seasonEpisodeNumber: target.seasonEpisodeNumber,
          subgroupCount: 0,
          downloads: []
        };
      } else if (target.kind === "synthetic" && target.episodeNumber !== null) {
        const padNum = String(target.episodeNumber).padStart(2, "0");
        const shortLabel = `第 ${padNum} 话`;
        group = {
          key: target.key,
          label: shortLabel,
          shortLabel,
          seasonSubLabel: null,
          episodeTitle: null,
          kind: "synthetic",
          detail: null,
          order: target.episodeNumber,
          episodeNumber: target.episodeNumber,
          seasonEpisodeNumber: null,
          subgroupCount: 0,
          downloads: []
        };
      } else if (target.kind === "batch") {
        group = {
          key: "batch",
          label: "合集 / 全集",
          shortLabel: "合集",
          seasonSubLabel: null,
          episodeTitle: "季度合集与批量打包",
          kind: "batch",
          detail: null,
          order: 900_000,
          episodeNumber: null,
          seasonEpisodeNumber: null,
          subgroupCount: 0,
          downloads: []
        };
      } else if (target.kind === "other-season") {
        group = {
          key: "other-season",
          label: "其他季度",
          shortLabel: "其他季度",
          seasonSubLabel: null,
          episodeTitle: "非当前季度的关联资源",
          kind: "other-season",
          detail: null,
          order: 900_002,
          episodeNumber: null,
          seasonEpisodeNumber: null,
          subgroupCount: 0,
          downloads: []
        };
      } else {
        group = {
          key: "unmatched",
          label: "番外 / 特典 / 其他",
          shortLabel: "番外/其他",
          seasonSubLabel: null,
          episodeTitle: null,
          kind: "unmatched",
          detail: null,
          order: 900_001,
          episodeNumber: null,
          seasonEpisodeNumber: null,
          subgroupCount: 0,
          downloads: []
        };
      }
      groups.set(target.key, group);
    }

    group.downloads.push(item);
    let sgSet = subgroupSets.get(target.key);
    if (!sgSet) {
      sgSet = new Set<string>();
      subgroupSets.set(target.key, sgSet);
    }
    sgSet.add(resolveSubgroupKey(item));
    group.subgroupCount = sgSet.size;
  }

  return Array.from(groups.values()).sort((left, right) => left.order - right.order);
}

function pickMostFrequent<T extends string>(counts: Map<T, number>): T | null {
  let best: T | null = null;
  let bestCount = 0;
  for (const [key, count] of counts.entries()) {
    if (count > bestCount) {
      best = key;
      bestCount = count;
    }
  }
  return best;
}

/**
 * 统计每个字幕组的更新进度、集数覆盖、主流封装/画质/字幕规格，供字幕组卡片与分组头展示。
 */
export function buildSubgroupProfiles(
  items: MikanDownloadItem[],
  groupByItemId: Map<string, MikanDownloadGroup>
): SubgroupProfile[] {
  const buckets = new Map<string, MikanDownloadItem[]>();
  for (const item of items) {
    const key = resolveSubgroupKey(item);
    const list = buckets.get(key);
    if (list) {
      list.push(item);
    } else {
      buckets.set(key, [item]);
    }
  }

  const profiles: SubgroupProfile[] = [];
  for (const [key, groupItems] of buckets.entries()) {
    const label = key === "__unknown" ? "未标注字幕组" : key;
    const epNums = new Set<number>();
    let latestEp: number | null = null;
    let latestSeasonEp: number | null = null;
    let hasBatch = false;
    let latestTimestamp = 0;
    let latestPublishedAt: string | null = null;

    const resCounts = new Map<string, number>();
    const containerCounts = new Map<"MP4" | "MKV", number>();
    const codecCounts = new Map<string, number>();
    const subCounts = new Map<string, number>();
    const sourceCounts = new Map<string, number>();

    for (const item of groupItems) {
      const info = inspectReleaseItem(item);
      const matchedGroup = groupByItemId.get(item.id);
      if (info.isBatch || matchedGroup?.kind === "batch") {
        hasBatch = true;
      }
      const epNum = matchedGroup?.episodeNumber ?? null;
      if (epNum !== null) {
        epNums.add(epNum);
        if (latestEp === null || epNum >= latestEp) {
          latestEp = epNum;
          latestSeasonEp = matchedGroup?.seasonEpisodeNumber ?? null;
        }
      }

      if (info.resolution) resCounts.set(info.resolution, (resCounts.get(info.resolution) ?? 0) + 1);
      if (info.container) containerCounts.set(info.container, (containerCounts.get(info.container) ?? 0) + 1);
      if (info.videoCodec) codecCounts.set(info.videoCodec, (codecCounts.get(info.videoCodec) ?? 0) + 1);
      if (info.subtitleBadge) subCounts.set(info.subtitleBadge, (subCounts.get(info.subtitleBadge) ?? 0) + 1);
      if (info.streamSource) sourceCounts.set(info.streamSource, (sourceCounts.get(info.streamSource) ?? 0) + 1);

      if (item.publishedAt) {
        const ts = Date.parse(item.publishedAt);
        if (Number.isFinite(ts) && ts > latestTimestamp) {
          latestTimestamp = ts;
          latestPublishedAt = item.publishedAt;
        }
      }
    }

    const dominantResolution = pickMostFrequent(resCounts);
    const dominantContainer = pickMostFrequent(containerCounts);
    const dominantCodec = pickMostFrequent(codecCounts);
    const dominantSubBadge = pickMostFrequent(subCounts);
    const dominantSource = pickMostFrequent(sourceCounts);

    let progressText = `${groupItems.length} 条资源`;
    if (latestEp !== null) {
      const padEp = String(latestEp).padStart(2, "0");
      progressText = epNums.size > 1 ? `更至 ${padEp} 话 · 共 ${epNums.size} 话` : `第 ${padEp} 话`;
      if (hasBatch) {
        progressText += " · 含合集";
      }
    } else if (hasBatch) {
      progressText = "季度合集";
    }

    const specParts = [
      dominantSubBadge,
      dominantContainer,
      dominantResolution,
      dominantCodec
    ].filter(Boolean);
    const specSummary = specParts.join(" · ");

    const cardHintParts: string[] = [];
    if (latestEp !== null) {
      cardHintParts.push(`更至${String(latestEp).padStart(2, "0")}话`);
    } else if (hasBatch) {
      cardHintParts.push("合集");
    }
    if (dominantSubBadge) {
      cardHintParts.push(dominantSubBadge.replace(/双语/u, ""));
    } else if (dominantResolution) {
      cardHintParts.push(dominantResolution);
    }
    if (dominantContainer) {
      cardHintParts.push(dominantContainer);
    }

    profiles.push({
      key,
      label,
      shortLabel: shortSubgroupName(key),
      count: groupItems.length,
      episodeCount: epNums.size,
      latestEpisodeNumber: latestEp,
      latestSeasonEpisodeNumber: latestSeasonEp,
      hasBatch,
      dominantResolution,
      dominantContainer,
      dominantCodec,
      dominantSubBadge,
      dominantSource,
      latestPublishedAt,
      latestTimestamp,
      progressText,
      specSummary,
      cardHint: cardHintParts.slice(0, 3).join(" · ") || `${groupItems.length} 条`
    });
  }

  return profiles.sort((left, right) => {
    // 未标注组垫底；其余按覆盖最新话数 -> 覆盖总话数 -> 条目数 -> 发布时间排序
    if (left.key === "__unknown" && right.key !== "__unknown") return 1;
    if (right.key === "__unknown" && left.key !== "__unknown") return -1;
    const epDiff = (right.latestEpisodeNumber ?? -1) - (left.latestEpisodeNumber ?? -1);
    if (epDiff !== 0) return epDiff;
    const covDiff = right.episodeCount - left.episodeCount;
    if (covDiff !== 0) return covDiff;
    const cntDiff = right.count - left.count;
    if (cntDiff !== 0) return cntDiff;
    return right.latestTimestamp - left.latestTimestamp || left.label.localeCompare(right.label, "zh-Hans-CN");
  });
}
