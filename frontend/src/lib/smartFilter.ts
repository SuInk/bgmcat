/**
 * 资源「一句话筛选」：把「1080p 简体内封 不要 HEVC」这类口语拆成结构化条件。
 * 纯本地规则，不需要模型，输入即生效；本地 AI 开着时再交给模型精筛（见 MikanResourcePane）。
 */

export interface SmartTerm {
  /** 显示在筛选胶囊上的名字 */
  label: string;
  pattern: RegExp;
}

export interface SmartQuery {
  /** 命中的分辨率选项 key（和分辨率筛选行同一套） */
  resolution: string | null;
  /** 命中的字幕组选项 key */
  subgroup: string | null;
  include: SmartTerm[];
  exclude: SmartTerm[];
}

interface FilterOption {
  key: string;
  label: string;
}

/** 常见说法 → 在发布名里怎么写。同一个意思的几种写法都认。 */
const VOCABULARY: Array<{ words: RegExp; term: SmartTerm }> = [
  { words: /简日|简体双语|简中双语/u, term: { label: "简日双语", pattern: /简日|CHS[_&\s]?JP|简体双语/iu } },
  { words: /繁日|繁体双语|繁中双语/u, term: { label: "繁日双语", pattern: /繁日|CHT[_&\s]?JP|繁體雙語|繁体双语/iu } },
  { words: /简繁/u, term: { label: "简繁", pattern: /简繁|CHS[_&\s]?CHT/iu } },
  { words: /简体|简中|简字|(?<![繁])简(?![日繁])/u, term: { label: "简体", pattern: /简|CHS|GB(?![a-z])|SC(?![a-z])/iu } },
  { words: /繁体|繁中|繁字|(?<![简])繁(?![日])/u, term: { label: "繁体", pattern: /繁|CHT|BIG5|TC(?![a-z])/iu } },
  { words: /内封|软字幕|mkv/iu, term: { label: "内封", pattern: /内封|內封|MKV/iu } },
  { words: /内嵌|硬字幕|mp4/iu, term: { label: "内嵌", pattern: /内嵌|內嵌|MP4/iu } },
  { words: /hevc|x\.?265|h\.?265/iu, term: { label: "HEVC", pattern: /HEVC|x\.?265|H\.?265/iu } },
  { words: /avc|x\.?264|h\.?264/iu, term: { label: "AVC", pattern: /AVC|x\.?264|H\.?264/iu } },
  { words: /10\s?bit|10位/iu, term: { label: "10bit", pattern: /10[\s-]?bit|Ma10p/iu } },
  { words: /合集|全集|整季|打包/u, term: { label: "合集", pattern: /合集|全集|\d{1,3}\s?[-~]\s?\d{1,3}(?!\d)|Fin\b/iu } },
  { words: /web[\s-]?dl|webrip/iu, term: { label: "WEB", pattern: /WEB[\s-]?(DL|Rip)/iu } },
  { words: /baha|巴哈/iu, term: { label: "Baha", pattern: /Baha|巴哈/iu } },
  { words: /bilibili|b站|哔哩/iu, term: { label: "B站源", pattern: /bilibili|B-Global|哔哩/iu } }
];

const NEGATION = /(不要|别要|不想要|排除|去掉|过滤掉?|除了|除去|without|no)\s*/iu;

const RESOLUTION_WORDS: Array<{ words: RegExp; needle: string }> = [
  { words: /2160|4k|超高清/iu, needle: "2160" },
  { words: /1080|全高清|蓝光/iu, needle: "1080" },
  { words: /720/u, needle: "720" },
  { words: /480/u, needle: "480" }
];

/** 按逗号、空格等把一句话切成几段；每段自己判断是不是「不要…」 */
function clauses(query: string): Array<{ text: string; negated: boolean }> {
  const out: Array<{ text: string; negated: boolean }> = [];
  for (const part of query.split(/[,，;；、。\n]+/u)) {
    const pieces = part.split(NEGATION);
    // split 带捕获组：[前文, 否定词, 后文, 否定词, 后文…]
    out.push({ text: pieces[0] ?? "", negated: false });
    for (let index = 1; index < pieces.length; index += 2) {
      out.push({ text: pieces[index + 1] ?? "", negated: true });
    }
  }
  return out.filter((clause) => clause.text.trim());
}

export function parseResourceQuery(
  query: string,
  options: { resolutions: FilterOption[]; subgroups: FilterOption[] }
): SmartQuery {
  const result: SmartQuery = { resolution: null, subgroup: null, include: [], exclude: [] };
  const lowerQuery = query.toLowerCase();

  // 字幕组：选项里的名字直接出现在句子里就算（「LoliHouse 的」「要喵萌」）
  const subgroup = options.subgroups
    .filter((option) => option.key !== "all" && option.label.length > 1)
    .sort((left, right) => right.label.length - left.label.length)
    .find((option) => lowerQuery.includes(option.label.toLowerCase()));

  for (const clause of clauses(query)) {
    const text = clause.text;
    const target = clause.negated ? result.exclude : result.include;
    let rest = text;
    for (const entry of VOCABULARY) {
      if (entry.words.test(rest) && !target.some((term) => term.label === entry.term.label)) {
        target.push(entry.term);
        rest = rest.replace(entry.words, " ");
      }
    }
    for (const entry of RESOLUTION_WORDS) {
      if (!entry.words.test(text)) {
        continue;
      }
      if (clause.negated) {
        result.exclude.push({ label: `${entry.needle}P`, pattern: new RegExp(entry.needle === "2160" ? "2160|4K" : entry.needle, "iu") });
      } else if (!result.resolution) {
        result.resolution = options.resolutions.find((option) => option.key !== "all" && option.label.includes(entry.needle))?.key ?? null;
      }
    }
    if (subgroup && text.toLowerCase().includes(subgroup.label.toLowerCase())) {
      if (clause.negated) {
        result.exclude.push({ label: subgroup.label, pattern: new RegExp(escapeRegExp(subgroup.label), "iu") });
      } else {
        result.subgroup = subgroup.key;
      }
    }
  }

  // 「简日双语」已经包含「简体」：同时出现时去掉宽的那条，免得胶囊重复
  dedupe(result.include, "简日双语", "简体");
  dedupe(result.include, "繁日双语", "繁体");
  return result;
}

function dedupe(terms: SmartTerm[], specific: string, broad: string): void {
  if (terms.some((term) => term.label === specific)) {
    const index = terms.findIndex((term) => term.label === broad);
    if (index >= 0) {
      terms.splice(index, 1);
    }
  }
}

function escapeRegExp(value: string): string {
  return value.replace(/[.*+?^${}()|[\]\\]/gu, "\\$&");
}

export function matchesSmartTerms(title: string, include: SmartTerm[], exclude: SmartTerm[]): boolean {
  return include.every((term) => term.pattern.test(title)) && !exclude.some((term) => term.pattern.test(title));
}

export function isEmptySmartQuery(query: SmartQuery): boolean {
  return !query.resolution && !query.subgroup && !query.include.length && !query.exclude.length;
}
