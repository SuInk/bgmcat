<script setup lang="ts">
/**
 * 字幕组 × 剧集 双维精细资源工作区：
 * 1. 顶部字幕组总览卡片：直接展示全部字幕组的「更至集数 · 字幕语种/封装 · 条目数」，告别超出 5 个后藏进原生下拉的盲盒体验
 * 2. 双视图自由切换：「按字幕组」分区（对比各组规格、集数覆盖与合集） / 「按剧集」分区（最新话在前，同集跨字幕组横向比对）
 * 3. 行级细粒度标签：清晰区分「简日内嵌 / 繁日内嵌 / 简繁内封 / 生肉」「片源渠道 Baha/ABEMA/BDRip」「修正版 v2」与「MP4/MKV · HEVC 10bit/AVC · FLAC/AAC」
 */
import { computed, onMounted, reactive, ref, watch } from "vue";

import { extractEpisodeNumber } from "@shared/episode";
import type { AnimeStreamingLink, MikanDownloadItem, ResourceProviderFetchStatus } from "@shared/index";

import { getLocalAiStatus, sendLocalAiChat } from "../../api";
import { formatDateTime } from "../../lib/format";
import {
  buildSubgroupProfiles,
  inspectReleaseItem,
  matchesSubtitleFilter,
  type MikanDownloadGroup,
  providerIdsOf,
  type ReleaseInspection,
  type SubgroupProfile,
  type SubtitleFilterKey
} from "../../lib/mikan";
import { isEmptySmartQuery, matchesSmartTerms, parseResourceQuery, type SmartQuery, type SmartTerm } from "../../lib/smartFilter";
import { toastError } from "../../stores/toasts";
import AppSelect from "../common/AppSelect.vue";
import AppNavIcon from "../layout/AppNavIcon.vue";

const props = defineProps<{
  selectedDownloadGroup: MikanDownloadGroup | null;
  downloadGroups?: MikanDownloadGroup[];
  scopedDownloads: MikanDownloadItem[];
  scopedFilteredDownloads: MikanDownloadItem[];
  scopedSubgroupOptions: Array<{ key: string; label: string; count: number }>;
  scopedResolutionOptions: Array<{ key: string; label: string; count: number }>;
  scopedProviderOptions: Array<{ key: string; label: string; count: number }>;
  selectedSubgroup: string;
  selectedResolution: string;
  selectedProvider: string;
  failedProviders?: ResourceProviderFetchStatus[];
  pendingProviders?: ResourceProviderFetchStatus[];
  providerNames?: Record<string, string>;
  busyKey: string | null;
  isDownloadBusy?: (item: MikanDownloadItem) => boolean;
  isDownloadQueued: (item: MikanDownloadItem) => boolean;
  isStreamBusy?: (item: MikanDownloadItem) => boolean;
  streamLabel?: (item: MikanDownloadItem) => string;
  animeTitles?: string[];
  animeTitle?: string;
  streamingLinks?: AnimeStreamingLink[];
}>();

const emit = defineEmits<{
  (event: "select-subgroup", value: string): void;
  (event: "select-resolution", value: string): void;
  (event: "select-provider", value: string): void;
  (event: "select-download-group", value: string | null): void;
  (event: "queue-download", item: MikanDownloadItem): void;
  (event: "stream-download", item: MikanDownloadItem): void;
  (event: "open-link", url: string): void;
}>();

const emptyFallbackWatchLinks = computed<AnimeStreamingLink[]>(() => {
  if (props.streamingLinks?.length) {
    return props.streamingLinks;
  }
  const keyword = (props.animeTitle ?? props.animeTitles?.[0] ?? "").trim();
  if (!keyword) {
    return [];
  }
  return [
    {
      id: "bilibili-search",
      label: "B 站搜索",
      url: `https://search.bilibili.com/all?keyword=${encodeURIComponent(keyword)}`
    }
  ];
});

/* ---------- 剧集归属索引与字幕组画像 ---------- */

const groupByItemId = computed(() => {
  const map = new Map<string, MikanDownloadGroup>();
  for (const group of props.downloadGroups ?? []) {
    for (const item of group.downloads) {
      map.set(item.id, group);
    }
  }
  return map;
});

const latestEpisodeOrder = computed(() => {
  let maxOrder = -1;
  for (const group of props.downloadGroups ?? []) {
    if ((group.kind === "episode" || group.kind === "synthetic") && group.order > maxOrder) {
      maxOrder = group.order;
    }
  }
  return maxOrder > 0 ? maxOrder : null;
});

const subgroupProfiles = computed<SubgroupProfile[]>(() =>
  buildSubgroupProfiles(props.scopedDownloads, groupByItemId.value)
);

const subgroupProfileByKey = computed(() => {
  const map = new Map<string, SubgroupProfile>();
  for (const p of subgroupProfiles.value) {
    map.set(p.key, p);
  }
  return map;
});

const MAX_COLLAPSED_SUBGROUP_CARDS = 8;
const showAllSubgroupCards = ref(false);

const visibleSubgroupProfiles = computed(() => {
  const all = subgroupProfiles.value;
  if (showAllSubgroupCards.value || all.length <= MAX_COLLAPSED_SUBGROUP_CARDS) {
    return all;
  }
  const sliced = all.slice(0, MAX_COLLAPSED_SUBGROUP_CARDS);
  if (props.selectedSubgroup !== "all" && !sliced.some((p) => p.key === props.selectedSubgroup)) {
    const picked = subgroupProfileByKey.value.get(props.selectedSubgroup);
    if (picked) {
      return [...sliced.slice(0, MAX_COLLAPSED_SUBGROUP_CARDS - 1), picked];
    }
  }
  return sliced;
});

const hiddenSubgroupCardCount = computed(() =>
  Math.max(0, subgroupProfiles.value.length - visibleSubgroupProfiles.value.length)
);

const activeSubgroupProfile = computed(() =>
  props.selectedSubgroup !== "all" ? subgroupProfileByKey.value.get(props.selectedSubgroup) ?? null : null
);

function toggleSubgroupCard(key: string): void {
  if (key === "all") {
    emit("select-subgroup", "all");
    return;
  }
  emit("select-subgroup", props.selectedSubgroup === key ? "all" : key);
}

/* ---------- 视图维度与字幕属性筛选 ---------- */

const viewMode = ref<"subgroup" | "episode">("subgroup");
const selectedSubtitleFilter = ref<SubtitleFilterKey>("all");

const canSwitchViewMode = computed(
  () => !props.selectedDownloadGroup && props.selectedSubgroup === "all" && subgroupProfiles.value.length > 0
);

const subtitleFilterOptions = computed(() => {
  let chs = 0;
  let cht = 0;
  let soft = 0;
  let hard = 0;
  for (const item of props.scopedDownloads) {
    const info = inspectReleaseItem(item);
    if (info.hasChs) chs += 1;
    if (info.hasCht) cht += 1;
    if (info.isSoftSub) soft += 1;
    if (info.isHardSub) hard += 1;
  }
  const options: Array<{ key: SubtitleFilterKey; label: string; count: number }> = [
    { key: "all", label: "全部", count: props.scopedDownloads.length }
  ];
  if (chs > 0) options.push({ key: "chs", label: "简体中文", count: chs });
  if (cht > 0) options.push({ key: "cht", label: "繁体中文", count: cht });
  if (soft > 0) options.push({ key: "soft", label: "内封软字", count: soft });
  if (hard > 0) options.push({ key: "hard", label: "内嵌硬字", count: hard });
  return options;
});

/* ---------- 行级可读解析 ---------- */

export interface EnrichedRow {
  item: MikanDownloadItem;
  info: ReleaseInspection;
  group: MikanDownloadGroup | null;
  episodeBadge: string;
  seasonSubBadge: string | null;
  episodeTitle: string | null;
  episodeOrder: number;
  isBatch: boolean;
  rawSubtitle: string;
  provider: { text: string; fullTitle: string };
  publishedTimestamp: number;
}

function hasDownloadSource(item: MikanDownloadItem): boolean {
  return Boolean(item.magnetUrl || item.torrentUrl);
}

function shortProviderLabel(fullName: string): string {
  return fullName
    .replace(/\s*\(Mikan\)\s*/i, "")
    .replace(/\s*\(聚合\)\s*/i, "")
    .replace(/\s*\(DMHY\)\s*/i, "")
    .replace(/\s*\(Bangumi\.moe\)\s*/i, "")
    .replace(/末日动漫\s*\(AcgnX\)/i, "AcgnX")
    .trim();
}

function providerSummaryOf(item: MikanDownloadItem): { text: string; fullTitle: string } {
  const fullList = providerIdsOf(item).map((id) => props.providerNames?.[id] ?? id);
  if (!fullList.length) {
    return { text: "—", fullTitle: "" };
  }
  const firstShort = shortProviderLabel(fullList[0]!);
  const extra = fullList.length > 1 ? ` +${fullList.length - 1}` : "";
  return {
    text: `${firstShort}${extra}`,
    fullTitle: fullList.join("、")
  };
}

function isBusyItem(item: MikanDownloadItem): boolean {
  return props.isDownloadBusy ? props.isDownloadBusy(item) : props.busyKey === `mikan:${item.id}`;
}

function buildCompactRawSubtitle(rawTitle: string, knownTitles: string[]): string {
  const trimmed = rawTitle.trim();
  const bracketMatch = trimmed.match(/^((?:\[[^\]]+\]|【[^】]+】)\s*)/u);
  const prefix = bracketMatch?.[1] ?? "";
  let rest = prefix ? trimmed.slice(prefix.length) : trimmed;

  if (rest.includes(" / ")) {
    const parts = rest.split(" / ").map((s) => s.trim()).filter(Boolean);
    if (parts.length >= 2 && (parts[0]?.length ?? 0) >= 12) {
      const firstLower = parts[0]!.toLowerCase();
      const matchesKnown = knownTitles.some((t) => {
        const clean = t.trim().toLowerCase();
        return clean.length >= 6 && (firstLower.includes(clean) || clean.includes(firstLower));
      });
      if (matchesKnown || /[\u4e00-\u9fa5]{10,}/u.test(parts[0]!)) {
        rest = parts.slice(1).join(" / ");
      }
    }
  }
  const decoded = `${prefix}${rest}`.trim() || rawTitle;
  return decoded
    .replace(/&amp;/g, "&")
    .replace(/&lt;/g, "<")
    .replace(/&gt;/g, ">")
    .replace(/&quot;/g, "\"")
    .replace(/&#39;|&apos;/g, "'");
}

function buildEnrichedRow(item: MikanDownloadItem): EnrichedRow {
  const info = inspectReleaseItem(item);
  const group = groupByItemId.value.get(item.id) ?? null;

  let episodeBadge = "番外";
  let seasonSubBadge: string | null = null;
  let episodeTitle: string | null = null;
  let episodeOrder = -1;

  if (info.isBatch || group?.kind === "batch") {
    episodeBadge = "合集";
    episodeOrder = 0;
  } else if (group && (group.kind === "episode" || group.kind === "synthetic")) {
    episodeBadge = group.shortLabel;
    seasonSubBadge = group.seasonSubLabel;
    episodeTitle = group.episodeTitle;
    episodeOrder = group.order;
  } else {
    const fallbackEp = extractEpisodeNumber(item.title);
    if (fallbackEp !== null) {
      episodeBadge = `第 ${String(fallbackEp).padStart(2, "0")} 话`;
      episodeOrder = fallbackEp;
    } else if (group?.kind === "other-season") {
      episodeBadge = "他季";
      episodeOrder = -2;
    }
  }

  const pubTs = item.publishedAt ? Date.parse(item.publishedAt) : Number.NaN;

  return {
    item,
    info,
    group,
    episodeBadge,
    seasonSubBadge,
    episodeTitle,
    episodeOrder,
    isBatch: info.isBatch || group?.kind === "batch",
    rawSubtitle: buildCompactRawSubtitle(item.title, props.animeTitles ?? []),
    provider: providerSummaryOf(item),
    publishedTimestamp: Number.isFinite(pubTs) ? pubTs : 0
  };
}

function friendlyTime(value: string | null | undefined): string {
  if (!value) return "—";
  const date = new Date(value);
  if (!Number.isNaN(date.getTime())) {
    const now = new Date();
    const sameDay =
      date.getFullYear() === now.getFullYear() &&
      date.getMonth() === now.getMonth() &&
      date.getDate() === now.getDate();
    const hh = String(date.getHours()).padStart(2, "0");
    const mm = String(date.getMinutes()).padStart(2, "0");
    if (sameDay) {
      return `今天 ${hh}:${mm}`;
    }
    const mo = String(date.getMonth() + 1).padStart(2, "0");
    const da = String(date.getDate()).padStart(2, "0");
    return `${mo}-${da} ${hh}:${mm}`;
  }
  return formatDateTime(value, "—").replace(/^\d{4}-/, "");
}

/* ---------- 一句话筛选 + 本地 AI 精筛 ---------- */

const smartText = ref("");
const smart = reactive<SmartQuery>({ resolution: null, subgroup: null, include: [], exclude: [] });
const aiPickedIds = ref<Set<string> | null>(null);
const aiThinking = ref(false);
const aiAvailable = ref(false);

onMounted(async () => {
  try {
    const status = await getLocalAiStatus();
    aiAvailable.value = Boolean(status.enabled && status.reachable);
  } catch {
    aiAvailable.value = false;
  }
});

watch(smartText, (text) => {
  const parsed = parseResourceQuery(text, {
    resolutions: props.scopedResolutionOptions,
    subgroups: props.scopedSubgroupOptions
  });
  Object.assign(smart, parsed);
  aiPickedIds.value = null;
});

function optionLabel(options: Array<{ key: string; label: string }>, key: string | null): string | null {
  return key ? options.find((option) => option.key === key)?.label ?? null : null;
}

const smartResolutionLabel = computed(() => optionLabel(props.scopedResolutionOptions, smart.resolution));
const smartSubgroupLabel = computed(() => optionLabel(props.scopedSubgroupOptions, smart.subgroup));

function matchesSmart(row: EnrichedRow): boolean {
  if (smartResolutionLabel.value) {
    const needle = smartResolutionLabel.value.replace(/p$/iu, "");
    if (!(row.info.resolution ?? row.item.title).toLowerCase().includes(needle.toLowerCase())) {
      return false;
    }
  }
  if (
    smartSubgroupLabel.value &&
    row.info.subgroup !== smartSubgroupLabel.value &&
    !row.item.title.includes(smartSubgroupLabel.value)
  ) {
    return false;
  }
  if (aiPickedIds.value && !aiPickedIds.value.has(row.item.id)) {
    return false;
  }
  return matchesSmartTerms(row.item.title, smart.include, smart.exclude);
}

const smartActive = computed(
  () => !isEmptySmartQuery(smart) || aiPickedIds.value !== null || selectedSubtitleFilter.value !== "all"
);

const allFilteredEnrichedRows = computed<EnrichedRow[]>(() => {
  const out: EnrichedRow[] = [];
  for (const item of props.scopedFilteredDownloads) {
    const row = buildEnrichedRow(item);
    if (!matchesSubtitleFilter(row.info, selectedSubtitleFilter.value)) {
      continue;
    }
    if ((!isEmptySmartQuery(smart) || aiPickedIds.value !== null) && !matchesSmart(row)) {
      continue;
    }
    out.push(row);
  }
  return out;
});

/* ---------- 排序与分区构建 ---------- */

function compareRowsWithinSubgroup(a: EnrichedRow, b: EnrichedRow): number {
  // 正片单集按集数倒序（最新话在最前），其次合集，最后番外/其他；同集内修正版 v2 优先
  const rankKind = (r: EnrichedRow): number => {
    if (r.episodeOrder > 0) return 0;
    if (r.isBatch) return 1;
    return 2;
  };
  const ka = rankKind(a);
  const kb = rankKind(b);
  if (ka !== kb) return ka - kb;
  if (a.episodeOrder !== b.episodeOrder) {
    return b.episodeOrder - a.episodeOrder;
  }
  const va = a.info.versionTag ? 1 : 0;
  const vb = b.info.versionTag ? 1 : 0;
  if (va !== vb) return vb - va;
  return b.publishedTimestamp - a.publishedTimestamp;
}

function compareRowsWithinEpisode(a: EnrichedRow, b: EnrichedRow): number {
  // 已标注字幕组优先；修正版优先；其余按字幕组画像权重与发布时间
  const unknownA = a.info.subgroupKey === "__unknown" ? 1 : 0;
  const unknownB = b.info.subgroupKey === "__unknown" ? 1 : 0;
  if (unknownA !== unknownB) return unknownA - unknownB;
  const va = a.info.versionTag ? 1 : 0;
  const vb = b.info.versionTag ? 1 : 0;
  if (va !== vb) return vb - va;
  const countA = subgroupProfileByKey.value.get(a.info.subgroupKey)?.count ?? 0;
  const countB = subgroupProfileByKey.value.get(b.info.subgroupKey)?.count ?? 0;
  if (countA !== countB) return countB - countA;
  return b.publishedTimestamp - a.publishedTimestamp;
}

interface ResourceSection {
  key: string;
  mode: "subgroup" | "episode" | "focused";
  title: string;
  seasonSubLabel: string | null;
  subtitle: string | null;
  isLatestEpisode: boolean;
  badges: Array<{ text: string; tone: "softsub" | "neutral" | "source" }>;
  rightMeta: string;
  rightTime: string | null;
  filterActionKey: string | null;
  filterActionLabel: string | null;
  rows: EnrichedRow[];
  defaultLimit: number;
}

const collapsedSectionKeys = reactive(new Set<string>());
const expandedSectionKeys = reactive(new Set<string>());

watch(
  () => [
    props.selectedDownloadGroup?.key,
    props.selectedSubgroup,
    props.selectedResolution,
    props.selectedProvider,
    selectedSubtitleFilter.value,
    viewMode.value,
    smartText.value
  ],
  () => {
    collapsedSectionKeys.clear();
    expandedSectionKeys.clear();
  }
);

function toggleSectionCollapse(key: string): void {
  if (collapsedSectionKeys.has(key)) {
    collapsedSectionKeys.delete(key);
  } else {
    collapsedSectionKeys.add(key);
  }
}

function toggleSectionExpandRows(key: string): void {
  if (expandedSectionKeys.has(key)) {
    expandedSectionKeys.delete(key);
  } else {
    expandedSectionKeys.add(key);
  }
}

const structuredSections = computed<ResourceSection[]>(() => {
  const rows = allFilteredEnrichedRows.value;
  if (!rows.length) {
    return [];
  }

  // 1. 已聚焦单话或单分类（如点选了上方「第 07 话」「合集」）
  if (props.selectedDownloadGroup) {
    const sorted = [...rows].sort(compareRowsWithinEpisode);
    const sgCount = new Set(sorted.map((r) => r.info.subgroupKey)).size;
    return [
      {
        key: `focused:${props.selectedDownloadGroup.key}`,
        mode: "focused",
        title: props.selectedDownloadGroup.shortLabel || props.selectedDownloadGroup.label,
        seasonSubLabel: props.selectedDownloadGroup.seasonSubLabel ?? null,
        subtitle: props.selectedDownloadGroup.episodeTitle ?? null,
        isLatestEpisode:
          latestEpisodeOrder.value !== null && props.selectedDownloadGroup.order === latestEpisodeOrder.value,
        badges: [],
        rightMeta: `${sgCount} 个字幕组 · ${sorted.length} 条资源`,
        rightTime: props.selectedDownloadGroup.detail ? `${props.selectedDownloadGroup.detail} 首播` : null,
        filterActionKey: null,
        filterActionLabel: null,
        rows: sorted,
        defaultLimit: 20
      }
    ];
  }

  // 2. 已聚焦单个字幕组（点选了某个字幕组卡片）
  if (props.selectedSubgroup !== "all") {
    const sorted = [...rows].sort(compareRowsWithinSubgroup);
    const profile = activeSubgroupProfile.value;
    const badges: ResourceSection["badges"] = [];
    if (profile?.dominantSubBadge) {
      badges.push({
        text: profile.dominantSubBadge,
        tone: profile.dominantSubBadge.includes("内封") ? "softsub" : "neutral"
      });
    }
    if (profile?.dominantContainer) badges.push({ text: profile.dominantContainer, tone: "neutral" });
    if (profile?.dominantResolution) badges.push({ text: profile.dominantResolution, tone: "neutral" });
    if (profile?.dominantCodec) badges.push({ text: profile.dominantCodec, tone: "neutral" });
    if (profile?.dominantSource) badges.push({ text: profile.dominantSource, tone: "source" });

    return [
      {
        key: `focused-sg:${props.selectedSubgroup}`,
        mode: "subgroup",
        title: profile?.label ?? props.selectedSubgroup,
        seasonSubLabel: null,
        subtitle: null,
        isLatestEpisode: false,
        badges,
        rightMeta: profile?.progressText ?? `${sorted.length} 条资源`,
        rightTime: profile?.latestPublishedAt ? `${friendlyTime(profile.latestPublishedAt)} 更新` : null,
        filterActionKey: null,
        filterActionLabel: null,
        rows: sorted,
        defaultLimit: 24
      }
    ];
  }

  // 3. 全局「按字幕组」分组模式
  if (viewMode.value === "subgroup") {
    const bySubgroup = new Map<string, EnrichedRow[]>();
    for (const row of rows) {
      const list = bySubgroup.get(row.info.subgroupKey);
      if (list) {
        list.push(row);
      } else {
        bySubgroup.set(row.info.subgroupKey, [row]);
      }
    }

    const orderedKeys = subgroupProfiles.value
      .map((p) => p.key)
      .filter((k) => bySubgroup.has(k));
    for (const k of bySubgroup.keys()) {
      if (!orderedKeys.includes(k)) orderedKeys.push(k);
    }

    const perGroupPreview = orderedKeys.length <= 2 ? 8 : 4;
    return orderedKeys.map((sgKey) => {
      const sgRows = [...(bySubgroup.get(sgKey) ?? [])].sort(compareRowsWithinSubgroup);
      const profile = subgroupProfileByKey.value.get(sgKey);
      const badges: ResourceSection["badges"] = [];
      if (profile?.dominantSubBadge) {
        badges.push({
          text: profile.dominantSubBadge,
          tone: profile.dominantSubBadge.includes("内封") ? "softsub" : "neutral"
        });
      }
      if (profile?.dominantContainer) badges.push({ text: profile.dominantContainer, tone: "neutral" });
      if (profile?.dominantResolution) badges.push({ text: profile.dominantResolution, tone: "neutral" });
      if (profile?.dominantCodec) badges.push({ text: profile.dominantCodec, tone: "neutral" });
      if (profile?.dominantSource) badges.push({ text: profile.dominantSource, tone: "source" });

      return {
        key: `sg:${sgKey}`,
        mode: "subgroup",
        title: profile?.label ?? (sgKey === "__unknown" ? "未标注字幕组" : sgKey),
        seasonSubLabel: null,
        subtitle: null,
        isLatestEpisode: false,
        badges,
        rightMeta: profile?.progressText ?? `${sgRows.length} 条`,
        rightTime: profile?.latestPublishedAt ? friendlyTime(profile.latestPublishedAt) : null,
        filterActionKey: sgKey,
        filterActionLabel: "只看该组",
        rows: sgRows,
        defaultLimit: perGroupPreview
      };
    });
  }

  // 4. 全局「按剧集」分组模式（最新话在最前，合集与番外殿后）
  const byEpGroup = new Map<string, { group: MikanDownloadGroup | null; rows: EnrichedRow[] }>();
  for (const row of rows) {
    const gKey = row.group?.key ?? (row.isBatch ? "batch" : "unmatched");
    const existing = byEpGroup.get(gKey);
    if (existing) {
      existing.rows.push(row);
    } else {
      byEpGroup.set(gKey, { group: row.group, rows: [row] });
    }
  }

  const entries = Array.from(byEpGroup.entries()).sort(([, left], [, right]) => {
    const rankGroup = (g: MikanDownloadGroup | null): number => {
      if (!g) return 2;
      if (g.kind === "episode" || g.kind === "synthetic") return 0;
      if (g.kind === "batch") return 1;
      if (g.kind === "unmatched") return 2;
      return 3;
    };
    const rl = rankGroup(left.group);
    const rr = rankGroup(right.group);
    if (rl !== rr) return rl - rr;
    // 正片剧集按最新话在前排列，方便第一时间对比最新更新
    return (right.group?.order ?? 0) - (left.group?.order ?? 0);
  });

  const perEpPreview = entries.length <= 3 ? 10 : 6;
  return entries.map(([gKey, { group, rows: epRows }]) => {
    const sorted = [...epRows].sort(compareRowsWithinEpisode);
    const sgCount = new Set(sorted.map((r) => r.info.subgroupKey)).size;
    return {
      key: `ep:${gKey}`,
      mode: "episode",
      title: group?.shortLabel ?? (gKey === "batch" ? "合集 / 全集" : "番外 / 其他"),
      seasonSubLabel: group?.seasonSubLabel ?? null,
      subtitle: group?.episodeTitle ?? null,
      isLatestEpisode:
        latestEpisodeOrder.value !== null &&
        (group?.kind === "episode" || group?.kind === "synthetic") &&
        group.order === latestEpisodeOrder.value,
      badges: [],
      rightMeta: `${sgCount} 个字幕组 · ${sorted.length} 条`,
      rightTime: group?.detail ? `${group.detail} 首播` : null,
      filterActionKey: group?.key ?? null,
      filterActionLabel: group ? "单看本项" : null,
      rows: sorted,
      defaultLimit: perEpPreview
    };
  });
});

function visibleRowsOfSection(section: ResourceSection): EnrichedRow[] {
  if (expandedSectionKeys.has(section.key) || section.rows.length <= section.defaultLimit) {
    return section.rows;
  }
  return section.rows.slice(0, section.defaultLimit);
}

function hiddenRowCountOfSection(section: ResourceSection): number {
  return Math.max(0, section.rows.length - visibleRowsOfSection(section).length);
}

function handleSectionAction(section: ResourceSection): void {
  if (!section.filterActionKey) return;
  if (section.mode === "subgroup") {
    emit("select-subgroup", section.filterActionKey);
  } else if (section.mode === "episode") {
    emit("select-download-group", section.filterActionKey);
  }
}

function removeTerm(list: SmartTerm[], term: SmartTerm): void {
  list.splice(list.indexOf(term), 1);
}

function clearSmart(): void {
  smartText.value = "";
  selectedSubtitleFilter.value = "all";
  Object.assign(smart, { resolution: null, subgroup: null, include: [], exclude: [] });
  aiPickedIds.value = null;
}

async function runAiFilter(): Promise<void> {
  const query = smartText.value.trim();
  const candidates = props.scopedFilteredDownloads.slice(0, 80);
  if (!query || !candidates.length || aiThinking.value) {
    return;
  }
  aiThinking.value = true;
  try {
    const list = candidates.map((item, index) => `${index + 1}. ${item.title}`).join("\n");
    const response = await sendLocalAiChat([
      {
        role: "system",
        content: "你是番剧资源筛选器。根据用户的要求，从编号列表里挑出符合的资源。只输出一个 JSON 数组，内容是编号，例如 [1,3]；一个都不符合就输出 []。不要解释。"
      },
      { role: "user", content: `要求：${query}\n\n资源列表：\n${list}` }
    ]);
    const raw = response.message?.content ?? "";
    const numbers = (raw.match(/\[[^\]]*\]/u)?.[0] ?? "").match(/\d+/gu) ?? [];
    aiPickedIds.value = new Set(
      numbers.map((value) => candidates[Number(value) - 1]?.id).filter((id): id is string => Boolean(id))
    );
  } catch (error) {
    toastError(error, "AI 精筛失败");
  } finally {
    aiThinking.value = false;
  }
}

function downloadLabel(item: MikanDownloadItem): string {
  if (props.isDownloadQueued(item)) {
    return "已加入";
  }
  return isBusyItem(item) ? "加入中…" : "下载";
}
</script>

<template>
  <section class="@container flex flex-col gap-2">
    <!-- 顶部控制面板：字幕组总览卡片 + 视图切换与多维精筛 -->
    <div v-if="scopedDownloads.length > 0" class="grid gap-2">
      <!-- 1. 字幕组总览卡片（超过 1 个字幕组时展示每个组的进度、主规格与条数） -->
      <div
        v-if="subgroupProfiles.length > 1"
        class="sg-grid"
        role="group"
        aria-label="按字幕组筛选与规格对比"
      >
        <button
          type="button"
          class="sg-card"
          :class="{ 'is-active': selectedSubgroup === 'all' }"
          :aria-pressed="selectedSubgroup === 'all'"
          @click="toggleSubgroupCard('all')"
        >
          <span class="sg-card-top">
            <strong class="sg-card-name">全部</strong>
            <span class="sg-card-cnt">{{ scopedDownloads.length }}</span>
          </span>
          <span class="sg-card-sub">{{ subgroupProfiles.length }} 个字幕组</span>
        </button>

        <button
          v-for="profile in visibleSubgroupProfiles"
          :key="profile.key"
          type="button"
          class="sg-card"
          :class="{ 'is-active': selectedSubgroup === profile.key }"
          :aria-pressed="selectedSubgroup === profile.key"
          :title="`${profile.label} · ${profile.progressText}${profile.specSummary ? ` · ${profile.specSummary}` : ''}`"
          @click="toggleSubgroupCard(profile.key)"
        >
          <span class="sg-card-top">
            <strong class="sg-card-name">{{ profile.shortLabel }}</strong>
            <span class="sg-card-cnt">{{ profile.count }}</span>
          </span>
          <span class="sg-card-sub">{{ profile.cardHint }}</span>
        </button>

        <button
          v-if="subgroupProfiles.length > MAX_COLLAPSED_SUBGROUP_CARDS"
          type="button"
          class="sg-card sg-card-more"
          :aria-expanded="showAllSubgroupCards"
          @click="showAllSubgroupCards = !showAllSubgroupCards"
        >
          <span class="sg-card-top">
            <strong class="sg-card-name">{{ showAllSubgroupCards ? "收起 ▴" : `+${hiddenSubgroupCardCount} 更多 ▾` }}</strong>
          </span>
        </button>
      </div>

      <!-- 2. 视图维度切换 & 精细筛选项 -->
      <div class="flex flex-wrap items-center justify-between gap-2">
        <!-- 左侧：分组方式切换 或 当前聚焦范围提示 -->
        <div class="flex min-w-0 flex-wrap items-center gap-1.5">
          <div v-if="canSwitchViewMode" class="mode-switch" role="group" aria-label="列表分组维度">
            <button
              type="button"
              class="mode-btn"
              :class="{ 'is-active': viewMode === 'subgroup' }"
              :aria-pressed="viewMode === 'subgroup'"
              @click="viewMode = 'subgroup'"
            >
              按字幕组
            </button>
            <button
              type="button"
              class="mode-btn"
              :class="{ 'is-active': viewMode === 'episode' }"
              :aria-pressed="viewMode === 'episode'"
              @click="viewMode = 'episode'"
            >
              按剧集
            </button>
          </div>

          <div v-else-if="selectedDownloadGroup" class="scope-crumb">
            <strong class="text-xs font-semibold text-[var(--ink)]">{{ selectedDownloadGroup.label }}</strong>
            <span v-if="selectedDownloadGroup.detail" class="text-[11px] text-[var(--muted)]">
              · {{ selectedDownloadGroup.detail }}
            </span>
            <button
              type="button"
              class="scope-reset-btn"
              aria-label="显示全部剧集"
              @click="emit('select-download-group', null)"
            >
              ×
            </button>
          </div>

          <div v-else-if="activeSubgroupProfile" class="scope-crumb">
            <strong class="text-xs font-semibold text-[var(--ink)]">{{ activeSubgroupProfile.label }}</strong>
            <span class="text-[11px] text-[var(--muted)]">· {{ activeSubgroupProfile.progressText }}</span>
            <button
              type="button"
              class="scope-reset-btn"
              aria-label="显示全部字幕组"
              @click="emit('select-subgroup', 'all')"
            >
              ×
            </button>
          </div>
        </div>

        <!-- 右侧：字幕类型 + 画质 + 来源 + 搜索框 -->
        <div class="flex flex-wrap items-center gap-1.5">
          <AppSelect
            v-if="subtitleFilterOptions.length > 2"
            size="sm"
            prefix="字幕"
            :model-value="selectedSubtitleFilter"
            aria-label="按字幕语言或内封内嵌筛选"
            :options="subtitleFilterOptions.map((opt) => ({
              value: opt.key,
              label: opt.key === 'all' ? '全部' : `${opt.label} (${opt.count})`
            }))"
            @update:model-value="selectedSubtitleFilter = $event as SubtitleFilterKey"
          />

          <AppSelect
            v-if="scopedResolutionOptions.length > 2"
            size="sm"
            prefix="画质"
            :model-value="selectedResolution"
            aria-label="按画质筛选"
            :options="scopedResolutionOptions.map((opt) => ({
              value: opt.key,
              label: opt.key === 'all' ? '全部' : `${opt.label} (${opt.count})`
            }))"
            @update:model-value="emit('select-resolution', String($event))"
          />

          <AppSelect
            v-if="scopedProviderOptions.length > 2"
            size="sm"
            prefix="来源"
            :model-value="selectedProvider"
            aria-label="按来源筛选"
            :options="scopedProviderOptions.map((opt) => ({
              value: opt.key,
              label: opt.key === 'all' ? '全部' : `${shortProviderLabel(opt.label)} (${opt.count})`
            }))"
            @update:model-value="emit('select-provider', String($event))"
          />

          <label class="search-input">
            <AppNavIcon name="search" class="size-3 shrink-0 text-[var(--muted)]" aria-hidden="true" />
            <input
              v-model="smartText"
              type="text"
              class="min-w-0 flex-1 bg-transparent text-xs text-[var(--ink)] outline-none placeholder:text-[var(--muted)]"
              :placeholder="aiAvailable ? '筛选 / 回车 AI…' : '筛选…'"
              aria-label="搜索或筛选资源"
              spellcheck="false"
              @keydown.enter.prevent="aiAvailable && runAiFilter()"
            />
            <button
              v-if="smartText"
              type="button"
              class="text-xs text-[var(--muted)] hover:text-[var(--ink)]"
              aria-label="清除筛选"
              @click="clearSmart"
            >×</button>
          </label>

          <button
            v-if="aiAvailable && smartText.trim()"
            type="button"
            class="btn-ghost h-[28px] rounded-[7px] px-2 text-xs font-semibold"
            :disabled="aiThinking"
            @click="runAiFilter"
          >
            {{ aiThinking ? "筛选中…" : "AI 精筛" }}
          </button>
        </div>
      </div>

      <!-- 已激活条件提示条 -->
      <div v-if="smartActive" class="flex flex-wrap items-center gap-1" aria-label="已生效的筛选条件">
        <span class="text-[11px] font-medium text-[var(--muted)]">条件：</span>
        <button
          v-if="selectedSubtitleFilter !== 'all'"
          type="button"
          class="smart-chip"
          @click="selectedSubtitleFilter = 'all'"
        >
          {{ subtitleFilterOptions.find((o) => o.key === selectedSubtitleFilter)?.label }} ×
        </button>
        <button v-if="smartResolutionLabel" type="button" class="smart-chip" @click="smart.resolution = null">{{ smartResolutionLabel }} ×</button>
        <button v-if="smartSubgroupLabel" type="button" class="smart-chip" @click="smart.subgroup = null">{{ smartSubgroupLabel }} ×</button>
        <button v-for="term in smart.include" :key="`in-${term.label}`" type="button" class="smart-chip" @click="removeTerm(smart.include, term)">{{ term.label }} ×</button>
        <button v-for="term in smart.exclude" :key="`ex-${term.label}`" type="button" class="smart-chip smart-chip-exclude" @click="removeTerm(smart.exclude, term)">排除 {{ term.label }} ×</button>
        <button v-if="aiPickedIds" type="button" class="smart-chip" @click="aiPickedIds = null">AI 精选 {{ aiPickedIds.size }} 条 ×</button>
      </div>

      <p v-if="failedProviders?.length" class="text-[11px] text-[var(--warn)]">
        {{ failedProviders.map((s) => s.displayName).join("、") }} 暂不可用，当前结果不含以上来源。
      </p>
      <p v-if="pendingProviders?.length" class="text-[11px] text-[var(--muted)]" role="status">
        正在同步 {{ pendingProviders.map((s) => s.displayName).join("、") }}…
      </p>
      <div v-if="pendingProviders?.length || aiThinking" class="scope-progress" role="progressbar" aria-label="正在加载资源"><span /></div>
    </div>

    <!-- 分区式结构化资源列表（按字幕组 / 按剧集 / 单维聚焦） -->
    <div v-if="structuredSections.length" class="flex flex-col gap-2">
      <div
        v-for="section in structuredSections"
        :key="section.key"
        class="sec-card"
      >
        <!-- 分区头部：清晰展示字幕组规格画像 或 剧集标题/首播/字幕组数 -->
        <div class="sec-head">
          <button
            type="button"
            class="sec-head-main"
            :aria-expanded="!collapsedSectionKeys.has(section.key)"
            @click="toggleSectionCollapse(section.key)"
          >
            <span class="sec-chevron" aria-hidden="true">
              {{ collapsedSectionKeys.has(section.key) ? "▸" : "▾" }}
            </span>
            <strong class="sec-title">{{ section.title }}</strong>
            <span v-if="section.seasonSubLabel" class="sec-season-tag">{{ section.seasonSubLabel }}</span>
            <span v-if="section.isLatestEpisode" class="sec-latest-tag">最新</span>
            <span v-if="section.subtitle" class="sec-subtitle truncate" :title="section.subtitle">
              {{ section.subtitle }}
            </span>
            <span
              v-for="badge in section.badges"
              :key="badge.text"
              class="sec-badge"
              :class="{
                'is-softsub': badge.tone === 'softsub',
                'is-source': badge.tone === 'source'
              }"
            >{{ badge.text }}</span>
          </button>

          <div class="sec-head-right">
            <span v-if="section.rightTime" class="hidden text-[11px] text-[var(--muted)] sm:inline">
              {{ section.rightTime }}
            </span>
            <span class="text-[11.5px] font-medium tabular-nums text-[var(--ink-secondary)]">
              {{ section.rightMeta }}
            </span>
            <button
              v-if="section.filterActionKey && structuredSections.length > 1"
              type="button"
              class="sec-focus-btn"
              @click.stop="handleSectionAction(section)"
            >
              {{ section.filterActionLabel }}
            </button>
          </div>
        </div>

        <!-- 分区条目体 -->
        <div v-if="!collapsedSectionKeys.has(section.key)">
          <div
            v-for="(row, rowIdx) in visibleRowsOfSection(section)"
            :key="row.item.id"
            class="r-row"
            :class="{ 'is-top-pick': structuredSections.length === 1 && rowIdx === 0 && !isDownloadQueued(row.item) }"
          >
            <!-- 1. 剧集号（带多季副标或合集标识） -->
            <div class="flex flex-col items-start gap-0.5 leading-tight">
              <span
                class="ep-badge"
                :class="{ 'is-batch': row.isBatch }"
              >
                {{ row.episodeBadge }}
              </span>
              <span v-if="row.seasonSubBadge" class="text-[10px] font-medium tabular-nums text-[var(--muted)]">
                {{ row.seasonSubBadge }}
              </span>
            </div>

            <!-- 2. 主信息列：字幕组 / 单集标题 + 字幕封装与片源渠道胶囊 + 原始发布名 -->
            <div class="grid min-w-0 gap-0.5">
              <div class="flex min-w-0 flex-wrap items-center gap-1.5">
                <!-- 在「按剧集」或「单话聚焦」视图中突出显示字幕组名；在「按字幕组」视图中突出显示单集名或组名 -->
                <span
                  v-if="section.mode !== 'subgroup'"
                  class="truncate text-[13px] font-semibold leading-snug text-[var(--ink)]"
                  :title="row.info.subgroup"
                >
                  {{ row.info.subgroup }}
                </span>
                <span
                  v-else-if="row.episodeTitle"
                  class="truncate text-[12.5px] font-semibold leading-snug text-[var(--ink)]"
                  :title="row.episodeTitle"
                >
                  {{ row.episodeTitle }}
                </span>
                <span
                  v-else
                  class="truncate text-[12.5px] font-semibold leading-snug text-[var(--ink)]"
                >
                  {{ row.isBatch ? "季度合集打包" : row.info.subgroup }}
                </span>

                <!-- 细粒度区分标签：字幕语种/封装、片源渠道、修正版本 -->
                <span
                  v-if="row.info.subtitleBadge"
                  class="sub-tag"
                  :class="{ 'is-soft': row.info.isSoftSub }"
                >
                  {{ row.info.subtitleBadge }}
                </span>
                <span v-if="row.info.streamSource" class="src-tag">
                  {{ row.info.streamSource }}
                </span>
                <span v-if="row.info.versionTag" class="ver-tag">
                  {{ row.info.versionTag }} 修正
                </span>

                <!-- 窄屏内联展示规格、体积与发布时间 -->
                <span class="inline-flex flex-wrap items-center gap-1 text-[11px] text-[var(--muted)] tabular-nums @2xl:hidden">
                  <template v-if="row.info.specs.length">{{ row.info.specs.join(" · ") }}</template>
                  <template v-if="row.item.sizeText">{{ row.info.specs.length ? " · " : "" }}{{ row.item.sizeText }}</template>
                  <template v-if="row.item.publishedAt">{{ (row.info.specs.length || row.item.sizeText) ? " · " : "" }}{{ friendlyTime(row.item.publishedAt) }}</template>
                </span>
              </div>

              <p class="break-words [overflow-wrap:anywhere] text-[11.5px] leading-[1.35] text-[var(--muted)]" :title="row.item.title">
                {{ row.rawSubtitle }}
              </p>
            </div>

            <!-- 3. 音视频与封装规格 -->
            <div class="hidden flex-wrap items-center gap-1 @2xl:flex">
              <span v-for="spec in row.info.specs" :key="spec" class="spec-pill">{{ spec }}</span>
              <span v-if="!row.info.specs.length" class="text-xs text-[var(--muted)]">—</span>
            </div>

            <!-- 4. 聚合来源 -->
            <div class="hidden truncate text-xs text-[var(--ink-secondary)] @2xl:block" :title="row.provider.fullTitle">
              {{ row.provider.text }}
            </div>

            <!-- 5. 体积与时间 -->
            <div class="hidden flex-col items-end leading-tight tabular-nums @2xl:flex">
              <span class="text-xs font-semibold text-[var(--ink)]">{{ row.item.sizeText ?? "—" }}</span>
              <span class="text-[11px] text-[var(--muted)]">{{ friendlyTime(row.item.publishedAt) }}</span>
            </div>

            <!-- 6. 下载与边下边播 -->
            <div class="flex flex-col items-stretch justify-center gap-1">
              <button
                v-if="hasDownloadSource(row.item)"
                type="button"
                class="dl-btn"
                :class="{
                  'is-primary': rowIdx === 0 && !isDownloadQueued(row.item) && structuredSections.length === 1,
                  'is-queued': isDownloadQueued(row.item)
                }"
                :disabled="isBusyItem(row.item) || isDownloadQueued(row.item)"
                :aria-label="`下载 ${row.info.subgroup} ${row.episodeBadge}`"
                @click="emit('queue-download', row.item)"
              >
                {{ downloadLabel(row.item) }}
              </button>
              <button
                v-if="hasDownloadSource(row.item)"
                type="button"
                class="dl-btn stream-btn"
                :disabled="isBusyItem(row.item) || Boolean(isStreamBusy?.(row.item))"
                :title="isDownloadQueued(row.item) ? '直接播放已加入的任务，未下完的部分边下边播' : '加入下载并立即开始播放'"
                :aria-label="`边下边播 ${row.info.subgroup} ${row.episodeBadge}`"
                @click="emit('stream-download', row.item)"
              >
                <span aria-hidden="true" class="stream-icon">▶</span>{{ streamLabel ? streamLabel(row.item) : "边下边播" }}
              </button>
            </div>
          </div>

          <button
            v-if="section.rows.length > section.defaultLimit"
            type="button"
            class="more-rows-btn"
            @click="toggleSectionExpandRows(section.key)"
          >
            {{ expandedSectionKeys.has(section.key) ? "收起 ▴" : `+${hiddenRowCountOfSection(section)} 条 ▾` }}
          </button>
        </div>
      </div>
    </div>

    <!-- 紧凑空状态提示条 -->
    <div v-else class="empty-bar">
      <template v-if="!scopedDownloads.length && pendingProviders?.length">
        <span class="text-xs text-[var(--muted)]">检索中…</span>
      </template>
      <template v-else-if="!scopedDownloads.length">
        <span class="text-xs text-[var(--muted)]">
          {{
            selectedDownloadGroup
              ? "当前选集暂无 BT 资源"
              : streamingLinks?.length
                ? "各大 BT 站暂无片源，可前往在线平台观看"
                : "暂无 BT 资源"
          }}
        </span>
        <div v-if="emptyFallbackWatchLinks.length" class="ml-auto flex flex-wrap items-center gap-2">
          <button
            v-for="(link, idx) in emptyFallbackWatchLinks"
            :key="link.id"
            type="button"
            class="dl-btn gap-1.5"
            :class="{ 'is-primary': idx === 0 && Boolean(streamingLinks?.length) }"
            :title="link.url"
            @click="emit('open-link', link.url)"
          >
            <span>{{ streamingLinks?.length ? `去 ${link.label} 观看` : `去 ${link.label}` }}</span>
            <AppNavIcon name="external" class="size-3.5 opacity-85" aria-hidden="true" />
          </button>
        </div>
      </template>
      <template v-else-if="smartActive && scopedFilteredDownloads.length">
        <span class="text-xs text-[var(--muted)]">
          无匹配资源 ·
          <button type="button" class="font-semibold text-[var(--accent-vivid)] hover:underline" @click="clearSmart">重置</button>
        </span>
      </template>
      <template v-else>
        <span class="text-xs text-[var(--muted)]">无匹配资源</span>
      </template>
    </div>
  </section>
</template>

<style scoped>
/* 字幕组总览卡片矩阵 */
.sg-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(8.5rem, 1fr));
  gap: 5px;
}

.sg-card {
  display: flex;
  min-width: 0;
  flex-direction: column;
  align-items: stretch;
  justify-content: center;
  gap: 1.5px;
  padding: 4.5px 8px;
  border-radius: 7px;
  border: 1px solid var(--line);
  background: var(--card);
  text-align: left;
  transition: border-color 0.14s, background 0.14s, box-shadow 0.14s;
}
.sg-card:hover:not(.is-active) {
  border-color: var(--accent-line);
  background: var(--tint-1);
}
.sg-card.is-active {
  border-color: var(--accent);
  background: color-mix(in srgb, var(--accent-soft) 65%, var(--card));
  box-shadow: 0 0 0 1px var(--accent-line);
}
.sg-card-more {
  border-style: dashed;
  background: var(--panel);
}

.sg-card-top {
  display: flex;
  min-width: 0;
  align-items: baseline;
  justify-content: space-between;
  gap: 6px;
}
.sg-card-name {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: 12px;
  font-weight: 650;
  color: var(--ink);
}
.sg-card.is-active .sg-card-name {
  color: var(--accent-vivid);
}
.sg-card-cnt {
  flex-shrink: 0;
  border-radius: 4px;
  background: var(--panel);
  padding: 0 4.5px;
  font-size: 10.5px;
  font-weight: 600;
  font-variant-numeric: tabular-nums;
  color: var(--ink-secondary);
}
.sg-card.is-active .sg-card-cnt {
  background: var(--accent);
  color: var(--accent-contrast);
}
.sg-card-sub {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: 10.5px;
  line-height: 1.35;
  color: var(--muted);
}

/* 视图模式分段控件 */
.mode-switch {
  display: inline-flex;
  align-items: center;
  gap: 2px;
  padding: 2px;
  border-radius: 7px;
  background: var(--panel);
}
.mode-btn {
  height: 30px;
  padding: 0 12px;
  border-radius: 6px;
  font-size: 12px;
  font-weight: 550;
  color: var(--ink-secondary);
  white-space: nowrap;
  transition: background 0.14s, color 0.14s, box-shadow 0.14s;
}
.mode-btn:hover:not(.is-active) {
  color: var(--ink);
}
.mode-btn.is-active {
  background: var(--card);
  color: var(--ink);
  font-weight: 650;
  box-shadow: var(--soft-shadow);
}

.scope-crumb {
  display: inline-flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 5px;
  min-height: 28px;
  padding: 2px 9px;
  border-radius: 7px;
  border: 1px solid var(--line);
  background: var(--panel);
}
.scope-reset-btn {
  margin-left: 2px;
  font-size: 11.5px;
  font-weight: 600;
  color: var(--accent-vivid);
}
.scope-reset-btn:hover {
  text-decoration: underline;
}

/* 右侧紧凑下拉与搜索框 */
.select-pill {
  display: inline-flex;
  flex-shrink: 0;
  align-items: center;
  gap: 4px;
  height: 34px;
  padding: 0 10px;
  border-radius: 8px;
  border: 1px solid var(--line);
  background: var(--card);
  color: var(--ink);
  font-size: 12px;
  font-weight: 500;
  white-space: nowrap;
  cursor: pointer;
  transition: border-color 0.15s;
}
.select-pill:hover {
  border-color: var(--accent-line);
}
.pill-native-select {
  -webkit-appearance: none;
  appearance: none;
  border: 0;
  background: transparent;
  font-weight: 600;
  color: var(--ink);
  outline: none;
  cursor: pointer;
  padding: 0 2px;
}

.search-input {
  display: inline-flex;
  width: 12rem;
  align-items: center;
  gap: 5px;
  height: 34px;
  padding: 0 10px;
  border-radius: 8px;
  border: 1px solid var(--line);
  background: var(--card);
  cursor: text;
  transition: border-color 0.15s, box-shadow 0.15s;
}
.search-input:focus-within {
  border-color: var(--accent);
  box-shadow: 0 0 0 2px var(--accent-soft);
}
.search-input > input:focus-visible {
  outline: none;
}

.smart-chip {
  display: inline-flex;
  align-items: center;
  height: 20px;
  padding: 0 7px;
  border-radius: 5px;
  background: var(--accent-soft);
  color: var(--accent-vivid);
  font-size: 11px;
  font-weight: 600;
}
.smart-chip-exclude {
  background: var(--err-soft);
  color: var(--err);
}

/* 结构化分区卡片 */
.sec-card {
  display: flex;
  flex-direction: column;
  overflow: hidden;
  border-radius: 8px;
  border: 1px solid var(--line);
  background: var(--card);
}

.sec-head {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: space-between;
  gap: 6px 10px;
  padding: 6.5px 11px;
  border-bottom: 1px solid var(--line);
  background: color-mix(in srgb, var(--panel) 55%, var(--card));
}

.sec-head-main {
  display: inline-flex;
  min-width: 0;
  flex: 1;
  flex-wrap: wrap;
  align-items: center;
  gap: 5px;
  text-align: left;
}

.sec-chevron {
  width: 11px;
  flex-shrink: 0;
  font-size: 11px;
  color: var(--muted);
}

.sec-title {
  font-size: 13px;
  font-weight: 700;
  color: var(--ink);
  white-space: nowrap;
}

.sec-season-tag {
  border-radius: 4px;
  background: var(--tint-3);
  padding: 0.5px 5px;
  font-size: 10.5px;
  font-weight: 600;
  font-variant-numeric: tabular-nums;
  color: var(--ink-secondary);
}

.sec-latest-tag {
  border-radius: 4px;
  background: var(--accent-soft);
  padding: 0.5px 5px;
  font-size: 10.5px;
  font-weight: 700;
  color: var(--accent-vivid);
}

.sec-subtitle {
  max-width: 22rem;
  font-size: 12px;
  font-weight: 500;
  color: var(--ink-secondary);
}

.sec-badge {
  border-radius: 4px;
  border: 1px solid var(--line);
  background: var(--card);
  padding: 0.5px 5.5px;
  font-size: 10.5px;
  font-weight: 600;
  color: var(--ink-secondary);
  white-space: nowrap;
}
.sec-badge.is-softsub {
  border-color: var(--accent-line);
  background: var(--accent-soft);
  color: var(--accent-vivid);
}
.sec-badge.is-source {
  background: var(--tint-2);
  color: var(--ink);
}

.sec-head-right {
  display: inline-flex;
  flex-shrink: 0;
  align-items: center;
  gap: 8px;
}

.sec-focus-btn {
  border-radius: 5px;
  padding: 1px 6px;
  font-size: 11px;
  font-weight: 600;
  color: var(--accent-vivid);
  transition: background 0.12s;
}
.sec-focus-btn:hover {
  background: var(--accent-soft);
}

/* 分区内单行布局 */
.r-row {
  display: grid;
  grid-template-columns: 64px minmax(0, 1fr) auto;
  align-items: center;
  gap: 8px;
  padding: 7px 10px;
  border-bottom: 1px solid color-mix(in srgb, var(--line) 70%, transparent);
  transition: background 0.12s;
}
.r-row:last-child {
  border-bottom: none;
}
@container (min-width: 42rem) {
  .r-row {
    grid-template-columns: 68px minmax(0, 1fr) 156px 84px 106px 88px;
    gap: 10px;
    padding: 7.5px 12px;
  }
}
.r-row:hover {
  background: var(--tint-1);
}
.r-row.is-top-pick {
  background: color-mix(in srgb, var(--accent-soft) 26%, var(--card));
}

.ep-badge {
  display: inline-flex;
  align-items: center;
  border-radius: 4px;
  background: var(--panel);
  padding: 1.5px 6px;
  font-size: 11.5px;
  font-weight: 700;
  font-variant-numeric: tabular-nums;
  color: var(--ink);
  white-space: nowrap;
}
.ep-badge.is-batch {
  background: var(--accent-soft);
  color: var(--accent-vivid);
}

.sub-tag {
  border-radius: 4px;
  border: 1px solid var(--line);
  background: var(--tint-1);
  padding: 0.5px 5.5px;
  font-size: 10.5px;
  font-weight: 600;
  color: var(--ink-secondary);
  white-space: nowrap;
}
.sub-tag.is-soft {
  border-color: var(--accent-line);
  background: var(--accent-soft);
  color: var(--accent-vivid);
}

.src-tag {
  border-radius: 4px;
  background: var(--panel);
  padding: 0.5px 5px;
  font-size: 10.5px;
  font-weight: 600;
  color: var(--ink-secondary);
  white-space: nowrap;
}

.ver-tag {
  border-radius: 4px;
  background: var(--ok-soft);
  padding: 0.5px 5px;
  font-size: 10.5px;
  font-weight: 700;
  color: var(--ok);
  white-space: nowrap;
}

.spec-pill {
  border-radius: 4px;
  background: var(--panel);
  padding: 1px 5.5px;
  color: var(--ink-secondary);
  font-size: 11px;
  font-weight: 600;
  white-space: nowrap;
}

.more-rows-btn {
  display: flex;
  height: 29px;
  width: 100%;
  align-items: center;
  justify-content: center;
  border-top: 1px solid var(--line);
  background: color-mix(in srgb, var(--panel) 45%, var(--card));
  color: var(--ink-secondary);
  font-size: 11.5px;
  font-weight: 550;
  transition: background 0.12s, color 0.12s;
}
.more-rows-btn:hover {
  background: var(--tint-1);
  color: var(--ink);
}

.empty-bar {
  display: flex;
  flex: none;
  flex-wrap: wrap;
  align-items: center;
  gap: 4px 10px;
  padding: 10px 14px;
  border-radius: 8px;
  border: 1px solid var(--line);
  background: var(--panel);
}

.dl-btn {
  display: inline-flex;
  min-height: 34px;
  align-items: center;
  justify-content: center;
  padding: 0 13px;
  border-radius: 8px;
  border: 1px solid var(--line);
  background: var(--card);
  color: var(--ink);
  font-size: 12px;
  font-weight: 600;
  white-space: nowrap;
  transition: border-color 0.15s, background 0.15s, color 0.15s, opacity 0.15s;
}
.dl-btn:hover:not(:disabled) {
  border-color: var(--accent);
  background: var(--accent);
  color: var(--accent-contrast);
}
.dl-btn.is-primary {
  border-color: var(--accent);
  background: var(--accent);
  color: var(--accent-contrast);
}
.dl-btn.is-primary:hover:not(:disabled) {
  background: var(--accent-strong);
}
.dl-btn.is-queued {
  border-color: var(--line);
  background: var(--panel);
  color: var(--muted);
}
.dl-btn.stream-btn {
  min-height: 28px;
  gap: 4px;
  padding: 0 10px;
  border-color: color-mix(in srgb, var(--accent) 45%, var(--line));
  color: var(--accent-vivid, var(--accent));
}
.stream-icon {
  font-size: 9px;
  line-height: 1;
}
.dl-btn:disabled {
  cursor: not-allowed;
  opacity: 0.65;
}
</style>
