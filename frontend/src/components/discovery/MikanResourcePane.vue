<script setup lang="ts">
/**
 * 资源列表，放在番剧详情的右栏。
 * 栏够宽走真表格，发布名单独一列一行显示完；栏窄退回卡片，免得逼用户横向拖表。
 *
 * 跨源重复项由服务端合并成一条，来源徽章在「来源」列里并排显示。
 */
import { computed, onMounted, reactive, ref, watch } from "vue";

import type { MikanDownloadItem, ResourceProviderFetchStatus } from "@shared/index";

import { getLocalAiStatus, sendLocalAiChat } from "../../api";
import { formatDateTime } from "../../lib/format";
import { isEmptySmartQuery, matchesSmartTerms, parseResourceQuery, type SmartQuery, type SmartTerm } from "../../lib/smartFilter";
import { toastError } from "../../stores/toasts";
import AppNavIcon from "../layout/AppNavIcon.vue";
import { extractResolutionLabel, providerIdsOf } from "../../lib/mikan";
import type { MikanDownloadGroup } from "./AnimeDetailPane.vue";

const props = defineProps<{
  selectedDownloadGroup: MikanDownloadGroup | null;
  scopedDownloads: MikanDownloadItem[];
  scopedFilteredDownloads: MikanDownloadItem[];
  scopedSubgroupOptions: Array<{ key: string; label: string; count: number }>;
  scopedResolutionOptions: Array<{ key: string; label: string; count: number }>;
  scopedProviderOptions: Array<{ key: string; label: string; count: number }>;
  selectedSubgroup: string;
  selectedResolution: string;
  selectedProvider: string;
  /** 本轮抓取失败的源。要显示出来，否则用户只会觉得「资源怎么变少了」。 */
  failedProviders?: ResourceProviderFetchStatus[];
  /** 这一轮还没回来的源。慢的源先不拦着,结果照出,顶上提示一行。 */
  pendingProviders?: ResourceProviderFetchStatus[];
  /** providerId -> 显示名。 */
  providerNames?: Record<string, string>;
  busyKey: string | null;
  /** 新版 store 传入的按条目忙碌判断；提供时优先于 busyKey */
  isDownloadBusy?: (item: MikanDownloadItem) => boolean;
  isDownloadQueued: (item: MikanDownloadItem) => boolean;
}>();

const emit = defineEmits<{
  (event: "select-subgroup", value: string): void;
  (event: "select-resolution", value: string): void;
  (event: "select-provider", value: string): void;
  (event: "queue-download", item: MikanDownloadItem): void;
}>();

type FilterKind = "subgroup" | "resolution" | "provider";

/**
 * 筛选行。只剩「全部」加一个选项时筛了也没区别，整行不出，省一行高度。
 */
const filterRows = computed(() =>
  [
    { kind: "subgroup" as FilterKind, label: "字幕组", options: props.scopedSubgroupOptions, selected: props.selectedSubgroup },
    { kind: "resolution" as FilterKind, label: "分辨率", options: props.scopedResolutionOptions, selected: props.selectedResolution },
    { kind: "provider" as FilterKind, label: "来源", options: props.scopedProviderOptions, selected: props.selectedProvider }
  ].filter((row) => row.options.length > 2)
);

function selectFilter(kind: FilterKind, key: string): void {
  if (kind === "subgroup") {
    emit("select-subgroup", key);
  } else if (kind === "resolution") {
    emit("select-resolution", key);
  } else {
    emit("select-provider", key);
  }
}

function hasDownloadSource(item: MikanDownloadItem): boolean {
  return Boolean(item.magnetUrl || item.torrentUrl);
}

function resolutionOf(item: MikanDownloadItem): string | null {
  return extractResolutionLabel(item.title);
}

/** 行内来源徽章：多个源都有这份资源时全部列出。 */
function providerLabelsOf(item: MikanDownloadItem): string[] {
  return providerIdsOf(item).map((id) => props.providerNames?.[id] ?? id);
}

function isBusyItem(item: MikanDownloadItem): boolean {
  return props.isDownloadBusy ? props.isDownloadBusy(item) : props.busyKey === `mikan:${item.id}`;
}

/* ---------- 一句话筛选（本地规则）+ AI 精筛（本地模型开着时） ---------- */

const smartText = ref("");
const smart = reactive<SmartQuery>({ resolution: null, subgroup: null, include: [], exclude: [] });
/** AI 挑出来的资源 id；null 表示没在用 AI 精筛 */
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

/** 打字即生效：规则解析是纯本地的，不用等回车 */
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

function matchesSmart(item: MikanDownloadItem): boolean {
  if (smartResolutionLabel.value) {
    const needle = smartResolutionLabel.value.replace(/p$/iu, "");
    if (!(resolutionOf(item) ?? item.title).toLowerCase().includes(needle.toLowerCase())) {
      return false;
    }
  }
  if (smartSubgroupLabel.value && item.subgroup !== smartSubgroupLabel.value && !item.title.includes(smartSubgroupLabel.value)) {
    return false;
  }
  if (aiPickedIds.value && !aiPickedIds.value.has(item.id)) {
    return false;
  }
  return matchesSmartTerms(item.title, smart.include, smart.exclude);
}

const smartActive = computed(() => !isEmptySmartQuery(smart) || aiPickedIds.value !== null);
const visibleDownloads = computed(() =>
  smartActive.value ? props.scopedFilteredDownloads.filter(matchesSmart) : props.scopedFilteredDownloads
);

function removeTerm(list: SmartTerm[], term: SmartTerm): void {
  list.splice(list.indexOf(term), 1);
}

function clearSmart(): void {
  smartText.value = "";
  Object.assign(smart, { resolution: null, subgroup: null, include: [], exclude: [] });
  aiPickedIds.value = null;
}

/**
 * AI 精筛：把这句话和当前候选（最多 80 条）的标题编号发给本地模型，让它只回编号。
 * 规则解析不了的说法（「画质最好的那个」「字幕组口碑好的」）交给它。
 */
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
  <!--
    窄屏不做内部滚动:整页一起滚才看得到下面的资源。
    只有宽屏(表格那一档)才把列表关进自己的滚动区,好让筛选头固定在上面。
  -->
  <section class="@container grid rounded-lg border border-[var(--line)] bg-[var(--card)] shadow-[var(--soft-shadow)] lg:min-h-0 lg:grid-rows-[auto_minmax(0,1fr)] lg:overflow-hidden">
    <header class="grid gap-2 border-b border-[var(--line)] px-3 py-2.5">
      <!-- 右栏自己说清楚现在列的是哪一集，不用回头去左栏找高亮 -->
      <div class="flex min-w-0 items-baseline justify-between gap-3">
        <h3 class="min-w-0 truncate text-sm font-semibold text-[var(--ink)]" :title="selectedDownloadGroup?.label ?? '全部资源'">
          {{ selectedDownloadGroup?.label ?? "全部资源" }}
        </h3>
        <span class="shrink-0 text-xs font-semibold tabular-nums text-[var(--muted)]">
          <template v-if="visibleDownloads.length !== scopedDownloads.length">{{ visibleDownloads.length }} / </template>{{ scopedDownloads.length }} 条
        </span>
      </div>

      <!--
        一句话筛选：像 B 站筛选那样把理解出来的条件摆成胶囊，点 × 去掉单个条件。
        规则解析即时生效；本地 AI 开着时多一个「AI 精筛」处理规则听不懂的说法。
      -->
      <div v-if="scopedDownloads.length > 1" class="grid gap-1.5">
        <div class="flex min-w-0 items-center gap-2">
          <label class="smart-input">
            <AppNavIcon name="sparkles" class="size-4 shrink-0 text-[var(--accent-vivid)]" aria-hidden="true" />
            <input
              v-model="smartText"
              type="text"
              class="min-w-0 flex-1 bg-transparent text-[13px] text-[var(--ink)] outline-none placeholder:text-[var(--muted)]"
              placeholder="一句话筛选：1080p 简体内封 不要 HEVC"
              aria-label="一句话筛选资源"
              spellcheck="false"
              @keydown.enter.prevent="aiAvailable && runAiFilter()"
            />
            <button v-if="smartText" type="button" class="text-xs text-[var(--muted)] hover:text-[var(--ink)]" aria-label="清除一句话筛选" @click="clearSmart">×</button>
          </label>
          <button
            v-if="aiAvailable"
            type="button"
            class="btn-ghost shrink-0 px-2.5 py-1 text-xs font-semibold"
            :disabled="!smartText.trim() || aiThinking"
            title="交给本地 AI 模型挑选（回车也可以）"
            @click="runAiFilter"
          >
            {{ aiThinking ? "AI 思考中…" : "AI 精筛" }}
          </button>
        </div>
        <div v-if="smartActive" class="flex flex-wrap items-center gap-1.5" aria-label="已理解的筛选条件">
          <span class="text-[11px] font-semibold text-[var(--muted)]">已理解</span>
          <button v-if="smartResolutionLabel" type="button" class="smart-chip" @click="smart.resolution = null">{{ smartResolutionLabel }} <span aria-hidden="true">×</span></button>
          <button v-if="smartSubgroupLabel" type="button" class="smart-chip" @click="smart.subgroup = null">{{ smartSubgroupLabel }} <span aria-hidden="true">×</span></button>
          <button v-for="term in smart.include" :key="`in-${term.label}`" type="button" class="smart-chip" @click="removeTerm(smart.include, term)">{{ term.label }} <span aria-hidden="true">×</span></button>
          <button v-for="term in smart.exclude" :key="`ex-${term.label}`" type="button" class="smart-chip smart-chip-exclude" @click="removeTerm(smart.exclude, term)">不要 {{ term.label }} <span aria-hidden="true">×</span></button>
          <button v-if="aiPickedIds" type="button" class="smart-chip smart-chip-ai" @click="aiPickedIds = null">AI 选出 {{ aiPickedIds.size }} 条 <span aria-hidden="true">×</span></button>
        </div>
        <p v-else-if="smartText.trim()" class="text-[11px] text-[var(--muted)]">
          没认出筛选条件。{{ aiAvailable ? "按回车交给 AI 精筛。" : "试试分辨率、简繁、内封 / 内嵌、HEVC、字幕组名。" }}
        </p>
      </div>

      <!--
        每组筛选一行：栏窄时横向滑动（右侧渐隐提示还有），栏宽时才换行铺开。
        以前一律换行，窄栏里字幕组能铺四行，资源被压到折叠线以下。
      -->
      <div v-if="filterRows.length" class="grid gap-1.5">
        <div v-for="row in filterRows" :key="row.kind" class="flex min-w-0 items-center gap-2">
          <span class="w-9 shrink-0 text-[11px] font-semibold text-[var(--muted)]">{{ row.label }}</span>
          <div class="filter-chips flex min-w-0 flex-1 gap-1.5 overflow-x-auto @2xl:flex-wrap" role="group" :aria-label="`按${row.label}筛选`">
            <button
              v-for="option in row.options"
              :key="option.key"
              type="button"
              class="max-w-[14rem] shrink-0 truncate rounded-md border px-2 py-0.5 text-xs font-semibold leading-5 transition"
              :class="row.selected === option.key ? 'border-[var(--accent)] bg-[var(--accent)] text-[var(--accent-contrast)]' : 'border-[var(--line)] bg-[var(--card)] text-[var(--ink)] hover:border-[var(--accent)]'"
              :aria-pressed="row.selected === option.key"
              :title="`${option.label} · ${option.count}`"
              @click="selectFilter(row.kind, option.key)"
            >
              {{ option.label }}<span class="ml-1 opacity-70">{{ option.count }}</span>
            </button>
          </div>
        </div>
      </div>

      <!-- 某个源挂了不该表现成「资源变少了」，把它点出来 -->
      <p
        v-if="failedProviders?.length"
        class="text-[10px] font-semibold leading-4 text-[var(--warn)]"
      >
        {{ failedProviders.map((status) => status.displayName).join("、") }} 暂时不可用，以上结果不含这些来源。
      </p>
      <p
        v-if="pendingProviders?.length"
        class="text-[10px] font-semibold leading-4 text-[var(--muted)]"
        role="status"
      >
        还在找 {{ pendingProviders.map((status) => status.displayName).join("、") }} 的资源，找到会自动补进来。
      </p>
    </header>

    <div v-if="visibleDownloads.length" class="lg:min-h-0 lg:overflow-auto">
      <!--
        按资源栏自己的宽度（容器查询）切换，而不是按窗口宽度：它左边还有剧集栏，
        窗口宽不代表这一栏宽。≥42rem 走表格，发布名独占一列吃掉剩余宽度；
        分辨率与体积合成「规格」一列，发布日期到 ≥56rem 才单独成列。更窄退回卡片。
      -->
      <table class="hidden w-full table-fixed border-collapse @2xl:table">
        <thead>
          <tr class="sticky top-0 z-10 bg-[var(--card)]">
            <th class="w-[120px] px-3 pb-2 pt-3 text-left text-[11px] font-semibold text-[var(--muted)]">字幕组</th>
            <th class="px-3 pb-2 pt-3 text-left text-[11px] font-semibold text-[var(--muted)]">发布名</th>
            <th class="w-[152px] px-3 pb-2 pt-3 text-left text-[11px] font-semibold text-[var(--muted)]">规格</th>
            <th class="w-[96px] px-3 pb-2 pt-3 text-left text-[11px] font-semibold text-[var(--muted)]">来源</th>
            <th class="hidden w-[92px] px-3 pb-2 pt-3 text-left text-[11px] font-semibold text-[var(--muted)] @4xl:table-cell">发布</th>
            <th class="w-[84px] px-3 pb-2 pt-3 text-right text-[11px] font-semibold text-[var(--muted)]">操作</th>
          </tr>
        </thead>
        <tbody>
          <tr
            v-for="item in visibleDownloads"
            :key="item.id"
            class="border-t border-[var(--line)] transition hover:bg-[var(--tint-1)]"
          >
            <td class="truncate px-3 py-2.5 text-[13px] font-semibold text-[var(--ink)]" :title="item.subgroup ?? '未标注字幕组'">
              {{ item.subgroup ?? "未标注字幕组" }}
            </td>
            <td class="truncate px-3 py-2.5 text-xs text-[var(--muted)]" :title="item.title">{{ item.title }}</td>
            <td class="truncate px-3 py-2.5 text-[13px] tabular-nums text-[var(--ink)]">
              {{ resolutionOf(item) ?? "—" }}<span class="text-[var(--muted)]"> · {{ item.sizeText ?? "—" }}</span>
            </td>
            <td class="px-3 py-2.5">
              <span class="flex flex-wrap gap-1">
                <span
                  v-for="label in providerLabelsOf(item)"
                  :key="label"
                  class="rounded bg-[var(--tint-2)] px-1.5 py-0.5 text-[10px] font-semibold text-[var(--muted)]"
                >
                  {{ label }}
                </span>
              </span>
            </td>
            <td class="hidden truncate px-3 py-2.5 text-xs tabular-nums text-[var(--muted)] @4xl:table-cell">{{ formatDateTime(item.publishedAt || null, "—") }}</td>
            <td class="px-3 py-2.5 text-right">
              <button
                v-if="hasDownloadSource(item)"
                type="button"
                class="rounded-md px-3 py-1.5 text-xs font-semibold"
                :class="isDownloadQueued(item) ? 'btn-ghost' : 'btn-primary'"
                :disabled="isBusyItem(item) || isDownloadQueued(item)"
                :aria-label="`下载 ${item.subgroup ?? ''} ${resolutionOf(item) ?? ''} 版本`"
                @click="emit('queue-download', item)"
              >
                {{ downloadLabel(item) }}
              </button>
            </td>
          </tr>
        </tbody>
      </table>

      <!-- 窄屏：表格塞不下，退回卡片 -->
      <div class="grid content-start gap-2 p-3 @2xl:hidden">
        <article
          v-for="item in visibleDownloads"
          :key="item.id"
          class="grid grid-cols-[minmax(0,1fr)_auto] items-center gap-3 rounded-lg border border-[var(--line)] bg-[var(--card)] px-3 py-2.5"
        >
          <div class="grid min-w-0 content-start gap-1">
            <div class="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1">
              <strong class="truncate text-xs font-semibold text-[var(--ink)]">{{ item.subgroup ?? "未标注字幕组" }}</strong>
              <span
                v-if="resolutionOf(item)"
                class="rounded bg-[var(--tint-2)] px-1.5 py-0.5 text-[10px] font-semibold text-[var(--muted)]"
              >
                {{ resolutionOf(item) }}
              </span>
              <span
                v-for="label in providerLabelsOf(item)"
                :key="label"
                class="rounded bg-[var(--accent-soft)] px-1.5 py-0.5 text-[10px] font-semibold text-[var(--accent-vivid)]"
              >
                {{ label }}
              </span>
              <span v-if="item.sizeText" class="text-[11px] font-semibold text-[var(--muted)]">{{ item.sizeText }}</span>
              <span v-if="item.publishedAt" class="text-[11px] text-[var(--muted)]">{{ formatDateTime(item.publishedAt) }}</span>
            </div>
            <p
              class="overflow-hidden text-[11px] leading-4 text-[var(--muted)] [display:-webkit-box] [-webkit-box-orient:vertical] [-webkit-line-clamp:2]"
              :title="item.title"
            >
              {{ item.title }}
            </p>
          </div>
          <button
            v-if="hasDownloadSource(item)"
            type="button"
            class="shrink-0 rounded-md px-3 py-1.5 text-xs font-semibold"
            :class="isDownloadQueued(item) ? 'btn-ghost' : 'btn-primary'"
            :disabled="isBusyItem(item) || isDownloadQueued(item)"
            :aria-label="`下载 ${item.subgroup ?? ''} ${resolutionOf(item) ?? ''} 版本`"
            @click="emit('queue-download', item)"
          >
            {{ downloadLabel(item) }}
          </button>
        </article>
      </div>
    </div>

    <!--
      三种「空」要说清楚是哪一种：还在找、资源站上压根没有、只是被筛选条件挡住了。
      以前一律写「当前筛选条件下没有下载候选」，没资源的新番看着像是筛选出了问题。
    -->
    <div v-else class="grid place-items-center px-4 py-10 text-center">
      <div class="grid max-w-sm gap-1.5">
        <template v-if="!scopedDownloads.length && pendingProviders?.length">
          <strong class="text-sm font-semibold text-[var(--ink)]">正在资源站里找…</strong>
          <span class="text-xs leading-5 text-[var(--muted)]">找到后会自动出现在这里。</span>
        </template>
        <template v-else-if="!scopedDownloads.length">
          <strong class="text-sm font-semibold text-[var(--ink)]">{{ selectedDownloadGroup ? "这一集暂时没有资源" : "资源站上暂时还没有这部番" }}</strong>
          <span class="text-xs leading-5 text-[var(--muted)]">
            {{ selectedDownloadGroup ? "换一集看看，或者等字幕组发布。" : "通常是刚开播、字幕组还没发布。点「订阅并自动追更」，有新资源时会自动下载。" }}
          </span>
        </template>
        <template v-else-if="smartActive && scopedFilteredDownloads.length">
          <strong class="text-sm font-semibold text-[var(--ink)]">没有符合这句话的资源</strong>
          <span class="text-xs leading-5 text-[var(--muted)]">
            共 {{ scopedFilteredDownloads.length }} 条。
            <button type="button" class="font-semibold text-[var(--accent-vivid)] hover:underline" @click="clearSmart">清除一句话筛选</button>
          </span>
        </template>
        <template v-else>
          <strong class="text-sm font-semibold text-[var(--ink)]">没有符合筛选条件的资源</strong>
          <span class="text-xs leading-5 text-[var(--muted)]">共 {{ scopedDownloads.length }} 条，换个字幕组或分辨率试试。</span>
        </template>
      </div>
    </div>
  </section>
</template>

<style scoped>
.smart-input {
  display: flex;
  min-width: 0;
  flex: 1;
  align-items: center;
  gap: 7px;
  height: 32px;
  padding: 0 10px;
  border-radius: 9px;
  background: var(--panel);
  cursor: text;
  transition: box-shadow 0.15s, background 0.15s;
}
.smart-input:focus-within {
  background: var(--card);
  box-shadow: 0 0 0 3px color-mix(in srgb, var(--accent-vivid) 30%, transparent);
}
.smart-chip {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  height: 24px;
  padding: 0 8px;
  border-radius: 999px;
  background: var(--accent-soft);
  color: var(--accent-vivid);
  font-size: 12px;
  font-weight: 600;
}
.smart-chip:hover {
  filter: brightness(1.08);
}
.smart-chip-exclude {
  background: var(--err-soft);
  color: var(--err);
}
.smart-chip-ai {
  background: color-mix(in srgb, var(--accent-vivid) 22%, transparent);
}

.filter-chips {
  scrollbar-width: none;
  mask-image: linear-gradient(to right, #000 calc(100% - 1.25rem), transparent);
}

.filter-chips::-webkit-scrollbar {
  display: none;
}

/* 栏够宽时换行铺开，不再需要渐隐 */
@container (min-width: 42rem) {
  .filter-chips {
    mask-image: none;
  }
}
</style>
