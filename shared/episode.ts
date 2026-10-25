/**
 * 从资源标题中提取集数。
 * 难点在于标题里混着大量“像集数但不是”的数字：分辨率(1080p/1920x1080)、
 * 编码(x264/x265)、位深(10bit)、帧率(60fps)、日期(2026.07.26)、版本号(24.04.3)。
 * 策略：先剥离这些技术性数字段，再按“标记明确 → 宽松兜底”的优先级产出候选。
 */

const IGNORED_EPISODE_NUMBERS = new Set([264, 265, 480, 576, 720, 1080, 1440, 2160]);

function stripTechnicalNumbers(title: string): string {
  return title
    .replace(/\b\d{3,4}\s*[xX×]\s*\d{3,4}\b/g, " ")
    .replace(/\b[xh]\.?26[45]\b/gi, " ")
    .replace(/\b\d{1,2}\s*bits?\b/gi, " ")
    .replace(/\b\d{2,3}\s*fps\b/gi, " ")
    .replace(/\b(?:2160|1440|1080|720|576|480)[pi]\b/gi, " ")
    .replace(/\b(?:19|20)\d{2}[.\-/年]\s*\d{1,2}(?:[.\-/月]\s*\d{1,2}日?)?\b/g, " ")
    .replace(/\b\d{1,4}(?:\.\d{1,4}){1,}\b/g, " ");
}

// 按可信度排序：带明确集数标记的模式在前，宽松的分隔符模式垫底。
// 全部用全局匹配，同一模式可产出多个候选（如“第01-02话”这类合集）。
const EPISODE_PATTERNS: RegExp[] = [
  /\bS\d{1,2}\s*E0*(\d{1,3})\b/gi,
  /第\s*0*(\d{1,3})\s*(?:話|话|集)/g,
  /\b(?:EP?|Episode)\s*0*(\d{1,3})\b(?!\s*(?:季|期))/gi,
  /[\[【]\s*0*(\d{1,3})(?:\s*[vV]\d+)?(?:\s*END)?\s*[\]】]/g,
  /[-–—]\s*0*(\d{1,3})(?:\s*[vV]\d+)?(?:\s*END)?\s*(?=$|[\s\[\(（【])/g,
  /(?:^|[\s._])0*(\d{1,3})(?:[vV]\d+)?(?=[\s._\]\)）】]|$)/g
];

/** 按可信度顺序返回去重后的集数候选。 */
export function extractEpisodeCandidates(rawTitle: string): number[] {
  const title = stripTechnicalNumbers(rawTitle);
  const candidates: number[] = [];
  const seen = new Set<number>();

  for (const pattern of EPISODE_PATTERNS) {
    for (const match of title.matchAll(pattern)) {
      const value = Number.parseInt(match[1] ?? "", 10);
      if (!Number.isFinite(value) || value <= 0 || IGNORED_EPISODE_NUMBERS.has(value) || seen.has(value)) {
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
  /全\s*\d{1,3}\s*[话話集]/,
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
  /[\[【(]\s*0*\d{1,3}\s*[-–—~～]\s*0*\d{1,3}\s*(?:END|完)?\s*[\]】)]/i,
  /第\s*0*\d{1,3}\s*[-–—~～]\s*0*\d{1,3}\s*[话話集]/,
  /\bEP?\s*0*\d{1,3}\s*[-–—~～]\s*(?:EP?\s*)?0*\d{1,3}\b/i
];

/** 判断是否为合集/批量资源。 */
export function isBatchRelease(rawTitle: string): boolean {
  if (BATCH_MARKERS.some((pattern) => pattern.test(rawTitle))) {
    return true;
  }
  const stripped = stripTechnicalNumbers(rawTitle);
  return EPISODE_RANGE_PATTERNS.some((pattern) => pattern.test(stripped));
}

// 开头方括号里未必是字幕组，也可能是技术标签（[1080p]、[简繁内嵌]…）。
const NON_SUBGROUP_TOKEN =
  /^(?:\d{3,4}[pi]|4k|8k|\d{3,4}\s*[xX×]\s*\d{3,4}|x?26[45]|hevc|avc|aac|flac|opus|web-?dl|web-?rip|bd-?rip|bluray|hdtv|tvrip|\d{1,2}\s*bits?|\d{2,3}\s*fps|chs|cht|gb|big5|简体|繁体|简繁|简日|繁日|内嵌|内封|外挂|合集|完结|字幕)/i;

/**
 * 提取字幕组。约定俗成：字幕组写在标题最前面的方括号/方头括号里。
 * 与 MikanDownloadItem.subgroup 语义一致（蜜柑详情页给的是同一个名字）。
 */
export function extractSubgroup(rawTitle: string): string | null {
  const match = rawTitle.trim().match(/^\s*(?:\[([^\]]+)]|【([^】]+)】)/);
  const raw = (match?.[1] ?? match?.[2] ?? "").trim();
  if (!raw) {
    return null;
  }
  // “[Foo&Bar]”这类联合发布取第一个名字
  const name = raw.split(/[&/|｜]/)[0]?.trim() ?? raw;
  if (!name || NON_SUBGROUP_TOKEN.test(name)) {
    return null;
  }
  return name;
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
