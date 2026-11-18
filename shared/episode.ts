/**
 * 从资源标题中提取集数。
 * 难点在于标题里混着大量“像集数但不是”的数字：分辨率(1080p/1920x1080)、
 * 编码(x264/x265)、位深(10bit)、帧率(60fps)、日期(2026.07.26)、版本号(24.04.3)。
 * 策略：先剥离这些技术性数字段，再按“标记明确 → 宽松兜底”的优先级产出候选。
 */

const IGNORED_EPISODE_NUMBERS = new Set([264, 265, 480, 576, 720, 1080, 1440, 2160]);

const CN_SEASON_DIGITS: Record<string, number> = {
  一: 1,
  二: 2,
  三: 3,
  四: 4,
  五: 5,
  六: 6,
  七: 7,
  八: 8,
  九: 9,
  十: 10
};

function parseChineseSmallInt(raw: string): number | null {
  const trimmed = raw.trim();
  if (/^\d{1,2}$/.test(trimmed)) {
    const n = Number.parseInt(trimmed, 10);
    return n > 0 ? n : null;
  }
  if (trimmed.length === 1 && CN_SEASON_DIGITS[trimmed]) {
    return CN_SEASON_DIGITS[trimmed]!;
  }
  if (trimmed.length === 2 && trimmed.startsWith("十") && CN_SEASON_DIGITS[trimmed[1]!]) {
    return 10 + CN_SEASON_DIGITS[trimmed[1]!]!;
  }
  return null;
}

/**
 * 提取番剧或资源标题中的明确季度号（如「第二季」「S2」「S02E06」「2nd Season」）。
 * 无明确季度标记时返回 null。
 */
export function extractSeasonNumber(rawTitle: string): number | null {
  const cnMatch = rawTitle.match(/第\s*([一二三四五六七八九十\d]{1,2})\s*[季期]/u);
  if (cnMatch?.[1]) {
    const parsed = parseChineseSmallInt(cnMatch[1]);
    if (parsed !== null) {
      return parsed;
    }
  }

  const seMatch = rawTitle.match(/\bS0?(\d{1,2})\s*(?:E\d{1,4}|[-–—]\s*\d{1,4}|\b)/i);
  if (seMatch?.[1]) {
    const n = Number.parseInt(seMatch[1], 10);
    if (n >= 1 && n <= 20) {
      return n;
    }
  }

  const enSeason = rawTitle.match(/\bSeason\s*0?(\d{1,2})\b/i);
  if (enSeason?.[1]) {
    const n = Number.parseInt(enSeason[1], 10);
    if (n >= 1 && n <= 20) {
      return n;
    }
  }

  const ordinalSeason = rawTitle.match(/\b(\d{1,2})(?:st|nd|rd|th)\s+Season\b/i);
  if (ordinalSeason?.[1]) {
    const n = Number.parseInt(ordinalSeason[1], 10);
    if (n >= 1 && n <= 20) {
      return n;
    }
  }

  return null;
}

function stripTechnicalNumbers(title: string): string {
  return title
    .replace(/\b\d{3,4}\s*[xX×]\s*\d{3,4}\b/g, " ")
    .replace(/\b[xh]\.?26[45]\b/gi, " ")
    .replace(/\b\d{1,2}\s*bits?\b/gi, " ")
    .replace(/\b\d{2,3}\s*fps\b/gi, " ")
    .replace(/\b(?:2160|1440|1080|720|576|480)[pi]\b/gi, " ")
    .replace(/\b(?:19|20)\d{2}[.\-/年]\s*\d{1,2}(?:[.\-/月]\s*\d{1,2}日?)?\b/g, " ")
    .replace(/\b\d{1,4}(?:\.\d{1,4}){1,}\b/g, " ")
    // 剥离独立季度/分段号（保留 S02E06 与 S02 - 06 供上方高优集数正则提取）
    .replace(/第\s*[一二三四五六七八九十\d]{1,2}\s*[季期部]/gu, " ")
    .replace(/\b(?:Season|Part|Cour)\s*0?\d{1,2}\b/gi, " ")
    .replace(/\b\d{1,2}(?:st|nd|rd|th)\s+Season\b/gi, " ")
    .replace(/\bS0?\d{1,2}(?!\s*(?:E\d|[-–—]\s*\d))\b/gi, " ");
}

// 按可信度排序：带明确集数标记的模式在前，括号及无前缀宽松模式垫底。
// 全部用全局匹配，同一模式可产出多个候选（如“第01-02话”这类合集）。
const EPISODE_PATTERNS: Array<{ pattern: RegExp; checkIgnored: boolean }> = [
  { pattern: /\bS\d{1,2}\s*E0*(\d{1,4})\b/gi, checkIgnored: false },
  { pattern: /\bS\d{1,2}\s*[-–—]\s*0*(\d{1,4})(?:\s*[vV]\d+)?\b/gi, checkIgnored: false },
  { pattern: /第\s*0*(\d{1,4})\s*(?:話|话|集)/g, checkIgnored: false },
  { pattern: /\b(?:EP?|Episode)\s*0*(\d{1,4})\b(?!\s*(?:季|期))/gi, checkIgnored: false },
  { pattern: /[\[【]\s*0*(\d{1,4})(?:\s*[vV]\d+)?(?:\s*END)?\s*[\]】]/g, checkIgnored: true },
  { pattern: /[-–—]\s*0*(\d{1,4})(?:\s*[vV]\d+)?(?:\s*END)?\s*(?=$|[\s\[\(（【])/g, checkIgnored: false },
  { pattern: /(?:^|[\s._])0*(\d{1,3})(?:[vV]\d+)?(?=[\s._\]\)）】]|$)/g, checkIgnored: true }
];

/** 按可信度顺序返回去重后的集数候选。 */
export function extractEpisodeCandidates(rawTitle: string): number[] {
  const title = stripTechnicalNumbers(rawTitle);
  const candidates: number[] = [];
  const seen = new Set<number>();

  for (const { pattern, checkIgnored } of EPISODE_PATTERNS) {
    for (const match of title.matchAll(pattern)) {
      const value = Number.parseInt(match[1] ?? "", 10);
      if (!Number.isFinite(value) || value <= 0 || value > 4999 || seen.has(value)) {
        continue;
      }
      if (checkIgnored && (IGNORED_EPISODE_NUMBERS.has(value) || (value >= 1980 && value <= 2039))) {
        continue;
      }
      seen.add(value);
      candidates.push(value);
    }
  }
  return candidates;
}

/** 取最可信的一个集数（无候选时返回 null）。 */
export function extractEpisodeNumberFromTitle(rawTitle: string): string | null {
  const [first] = extractEpisodeCandidates(rawTitle);
  return first === undefined ? null : String(first);
}

/** 取最可信的一个集数（数值形式，供入库用）。 */
export function extractEpisodeNumber(rawTitle: string): number | null {
  const [first] = extractEpisodeCandidates(rawTitle);
  return first ?? null;
}

// 合集/批量资源的特征。这类资源不应该按「单集」参与同集择优，
// 默认也不自动下载：一个 BDBOX 可能是几十 GB。
const BATCH_MARKERS: RegExp[] = [
  /合集/,
  /全\s*\d{1,4}\s*[话話集]/,
  /全集/,
  /\bBD-?BOX\b/i,
  /\bBatch\b/i,
  /\bComplete(?:\s+Series)?\b/i,
  /\b(?:Season|S)\d{1,2}\s*(?:Complete|Full)\b/i,
  /\bFull\s+Season\b/i
];

// “01-12”“01~24”“[01-12]”这类区间写法。要先剥掉技术性数字，
// 否则 1920x1080 / 版本号之类会被当成区间。
const EPISODE_RANGE_PATTERNS: RegExp[] = [
  /[\[【(]\s*0*\d{1,4}\s*[-–—~～]\s*0*\d{1,4}\s*(?:END|完)?\s*[\]】)]/i,
  /第\s*0*\d{1,4}\s*[-–—~～]\s*0*\d{1,4}\s*[话話集]/,
  /\b(?:EP?|Episodes?)\s*0*\d{1,4}\s*[-–—~～]\s*(?:(?:EP?|Episodes?)\s*)?0*\d{1,4}\b/i
];

/** 判断是否为合集/批量资源。 */
export function isBatchRelease(rawTitle: string): boolean {
  if (BATCH_MARKERS.some((pattern) => pattern.test(rawTitle))) {
    return true;
  }
  const stripped = stripTechnicalNumbers(rawTitle);
  return EPISODE_RANGE_PATTERNS.some((pattern) => pattern.test(stripped));
}

// 特典/番外/预告/OP&ED：避免将 [NCOP 01]、[PV 02]、[Menu] 等误归入正片第 1/2 话
const SPECIAL_EXTRA_PATTERNS: RegExp[] = [
  /(?:^|[\s\[【(])(?:NCOP|NCED|PV|CM|MENU|OVA|OAD|OAV)\s*\d{0,2}(?=$|[\s\]】)._])/i,
  /[\[【(]\s*SP\s*\d{0,2}\s*[\]】)]/i,
  /(?:映像特典|特典映像|无字OP|无字ED|先行PV|正式PV|预告篇|总集篇)/u
];

/** 判断是否为特典、OP/ED、PV、OVA 等非正片单集资源。 */
export function isSpecialOrExtraRelease(rawTitle: string): boolean {
  return SPECIAL_EXTRA_PATTERNS.some((pattern) => pattern.test(rawTitle));
}

/** 提取修正版标记（如 06v2 / [v2] / v3 -> "v2"）。 */
export function extractReleaseVersion(rawTitle: string): string | null {
  const match = rawTitle.match(/(?:\b\d{1,4}\s*|[[\s【(])([vV][2-9])\b/);
  return match?.[1] ? match[1].toLowerCase() : null;
}

// 开头可能挂着新番横幅或年月标签（如 ★10月新番★、[10月新番]、[2026.10]、[新番]），跳过后再读字幕组。
const LEADING_BANNER_PREFIX =
  /^\s*(?:[★☆][^★☆]{1,24}[★☆]\s*|(?:[\[【]\s*(?:(?:\d{2,4}\s*[年./-]\s*)?\d{1,2}\s*月?(?:新番)?|新番|连载|周[一二三四五六日天]|(?:19|20)\d{2}(?:[./-]\d{1,2})?)\s*[\]】]\s*))+/iu;

// 开头方括号里未必是字幕组，也可能是技术标签（[1080p]、[简繁内嵌]、[01]…）。
const NON_SUBGROUP_TOKEN =
  /^(?:\d{1,4}|\d{3,4}[pi]|4k|8k|\d{3,4}\s*[xX×]\s*\d{3,4}|x?26[45]|hevc|avc|av1|aac|flac|opus|mp4|mkv|web-?dl|web-?rip|bd-?rip|bluray|hdtv|tvrip|\d{1,2}\s*bits?|\d{2,3}\s*fps|(?:chs|cht|gb|big5|jpn|简体|繁体|简中|繁中|简繁|简日|繁日|双语|多语|内嵌|内封|外挂|软字|硬字|中文|字幕|无修|修正|合集|全集|完结|[\s_/+&-])+|[vV]\d+)$/i;

/**
 * 提取字幕组。约定俗成：字幕组写在标题最前面的方括号/方头括号里（可跳过 ★10月新番★ 等前缀）。
 * 与后端 subgroupOf 保持一致，保留联合发布组名（如「三明治摆烂组&LoliHouse」）。
 */
export function extractSubgroup(rawTitle: string): string | null {
  const cleaned = rawTitle.replace(LEADING_BANNER_PREFIX, "").trim();
  const match = cleaned.match(/^(?:\[([^\]]{1,48})]|【([^】]{1,48})】)/);
  const raw = (match?.[1] ?? match?.[2] ?? "").trim();
  if (!raw || /^\d+$/.test(raw)) {
    return null;
  }
  // 若用竖线附带了分辨率等规格（如 [Subgroup | 1080P]），剥掉右侧规格段；保留 & 联合字幕组名
  const pipeParts = raw.split(/[|｜]/).map((s) => s.trim()).filter(Boolean);
  const candidate =
    pipeParts.length >= 2 && NON_SUBGROUP_TOKEN.test(pipeParts[pipeParts.length - 1]!)
      ? pipeParts[0]!
      : raw;
  if (!candidate || NON_SUBGROUP_TOKEN.test(candidate)) {
    return null;
  }
  return candidate;
}

const RESOLUTION_BY_HEIGHT: Record<string, string> = {
  "2160": "2160p",
  "1440": "1440p",
  "1080": "1080p",
  "720": "720p",
  "576": "576p",
  "480": "480p"
};

/** 提取并归一化分辨率（1920x1080 / 4K / 1080P 都归一到 "1080p"）。 */
export function extractResolution(rawTitle: string): string | null {
  const dimensions = rawTitle.match(/\b\d{3,4}\s*[xX×]\s*(\d{3,4})\b/);
  const byDimension = dimensions?.[1] ? RESOLUTION_BY_HEIGHT[dimensions[1]] : undefined;
  if (byDimension) {
    return byDimension;
  }

  const tagged = rawTitle.match(/\b(2160|1440|1080|720|576|480)\s*[pi]\b/i);
  const byTag = tagged?.[1] ? RESOLUTION_BY_HEIGHT[tagged[1]] : undefined;
  if (byTag) {
    return byTag;
  }

  if (/\b4k\b/i.test(rawTitle)) {
    return "2160p";
  }
  return null;
}

/**
 * 一次性算出入库需要的集数相关字段。
 * RSS 解析链路在写库前调用，避免每次展示都重新正则一遍。
 */
export function parseEpisodeMetadata(rawTitle: string): {
  episodeNumber: number | null;
  isBatch: boolean;
  subgroup: string | null;
  resolution: string | null;
} {
  const isBatch = isBatchRelease(rawTitle);
  return {
    // 合集没有单一集数，硬塞一个会污染同集去重
    episodeNumber: isBatch ? null : extractEpisodeNumber(rawTitle),
    isBatch,
    subgroup: extractSubgroup(rawTitle),
    resolution: extractResolution(rawTitle)
  };
}
