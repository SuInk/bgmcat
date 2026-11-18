<script setup lang="ts">
/**
 * 订阅配置：按番剧分组的列表。
 * 同一部番往往同时挂着蜜柑 / Nyaa / ACG.RIP 几条订阅，以前每条一张肥卡片、各带三个大按钮，
 * 右边再常驻一栏只在选中后才有内容的条目列表。现在一部番一张卡：卡头汇总状态，
 * 每个源一行，点行就地展开它抓到的条目。
 */
import { computed, reactive, ref, watch } from "vue";

import type { AnimeSubscriptionOptions, FeedItem, HomepageAnime, Subscription } from "@shared/index";

import { copyText } from "../lib/actions";
import AppDialog from "../components/common/AppDialog.vue";
import SegmentedControl from "../components/common/SegmentedControl.vue";
import AnimeSubscribeDialog from "../components/discovery/AnimeSubscribeDialog.vue";
import AppNavIcon from "../components/layout/AppNavIcon.vue";
import { formatDateTime } from "../lib/format";
import { isBusy } from "../stores/busy";
import {
  cancelSubscriptionForAnime,
  createSubscriptionFromAnime,
  favoriteAnimeMap
} from "../stores/favorites";
import {
  editingSubscriptionId,
  failedSubscriptionCount,
  feedItems,
  fillSubscriptionForm,
  queueFeedItem,
  refreshAllSubscriptions,
  refreshOneSubscription,
  removeSubscription,
  resetSubscriptionForm,
  submitSubscription,
  subscriptionForm,
  subscriptions
} from "../stores/subscriptions";

type StatusFilter = "all" | "enabled" | "disabled" | "error" | "auto";
type CsvField = "includeKeywords" | "excludeKeywords" | "preferredResolutions" | "preferredSubgroups";

const searchQuery = ref("");
const statusFilter = ref<StatusFilter>("all");
const expandedIds = reactive(new Set<number>());
const subscriptionDialogOpen = ref(false);
const deleteCandidate = ref<Subscription | null>(null);

const ENABLED_OPTIONS = [
  { value: true, label: "自动刷新" },
  { value: false, label: "暂停" }
] as const;
const AUTO_DOWNLOAD_OPTIONS = [
  { value: true, label: "自动下载" },
  { value: false, label: "仅收集" }
] as const;
// 合集 / BDBOX 动辄几十 GB，默认跳过
const SKIP_BATCH_OPTIONS = [
  { value: true, label: "跳过合集" },
  { value: false, label: "一并下载" }
] as const;

const filterTabs = computed(() => [
  { key: "all" as const, label: "全部", count: subscriptions.value.length },
  { key: "enabled" as const, label: "启用", count: subscriptions.value.filter((item) => item.enabled).length },
  { key: "disabled" as const, label: "停用", count: subscriptions.value.filter((item) => !item.enabled).length },
  { key: "error" as const, label: "错误", count: failedSubscriptionCount.value },
  { key: "auto" as const, label: "自动下载", count: subscriptions.value.filter((item) => item.rule.autoDownload).length }
]);

const filteredSubscriptions = computed(() => {
  const query = searchQuery.value.trim().toLowerCase();
  return subscriptions.value
    .filter((subscription) => {
      if (statusFilter.value === "enabled" && !subscription.enabled) {
        return false;
      }
      if (statusFilter.value === "disabled" && subscription.enabled) {
        return false;
      }
      if (statusFilter.value === "error" && !subscription.lastError) {
        return false;
      }
      if (statusFilter.value === "auto" && !subscription.rule.autoDownload) {
        return false;
      }
      if (!query) {
        return true;
      }
      return [
        subscription.name,
        subscription.url,
        subscription.rule.includeKeywords.join(" "),
        subscription.rule.excludeKeywords.join(" ")
      ]
        .join(" ")
        .toLowerCase()
        .includes(query);
    })
    .sort(
      (left, right) =>
        Number(Boolean(right.lastError)) - Number(Boolean(left.lastError)) ||
        Number(right.enabled) - Number(left.enabled)
    );
});

const totalMatchedItems = computed(() => feedItems.value.filter((item) => item.matched).length);
// pending 是「抓到了、还没进下载队列」（仅收集模式的条目一直停在这里），不算入队
const totalQueuedItems = computed(
  () => feedItems.value.filter((item) => ["queued", "complete"].includes(item.downloadStatus)).length
);

const feedItemsBySubscription = computed(() => {
  const map = new Map<number, FeedItem[]>();
  for (const item of feedItems.value) {
    if (item.subscriptionId === null) {
      continue;
    }
    const list = map.get(item.subscriptionId) ?? [];
    list.push(item);
    map.set(item.subscriptionId, list);
  }
  for (const list of map.values()) {
    list.sort((left, right) => (right.publishedAt || right.createdAt).localeCompare(left.publishedAt || left.createdAt));
  }
  return map;
});

/** 「尼古喵喵（Nyaa）」→ 番名「尼古喵喵」+ 来源「Nyaa」。手动加的订阅没有括号，就整条当名字。 */
function splitName(subscription: Subscription): { title: string; source: string } {
  const match = subscription.name.match(/^(.*?)\s*[（(]([^（）()]+)[）)]\s*$/u);
  if (match?.[1] && match[2]) {
    return { title: match[1], source: match[2] };
  }
  return { title: subscription.name, source: subscription.providerId ?? "RSS" };
}

interface SubscriptionGroup {
  key: string;
  animeId: string | null;
  title: string;
  imageUrl: string | null;
  items: Subscription[];
}

/** 按番剧分组：有 animeId 的同番合并，手动加的杂源订阅各自成组。出错的组排最前。 */
const groupedSubscriptions = computed<SubscriptionGroup[]>(() => {
  const groups = new Map<string, SubscriptionGroup>();
  for (const subscription of filteredSubscriptions.value) {
    const key = subscription.animeId ? `anime:${subscription.animeId}` : `sub:${subscription.id}`;
    let group = groups.get(key);
    if (!group) {
      const favorite = subscription.animeId ? favoriteAnimeMap.value.get(subscription.animeId) : undefined;
      group = {
        key,
        animeId: subscription.animeId ?? null,
        title: favorite?.title ?? splitName(subscription).title,
        imageUrl: favorite?.imageUrl ?? null,
        items: []
      };
      groups.set(key, group);
    }
    group.items.push(subscription);
  }
  return [...groups.values()].sort(
    (left, right) =>
      Number(right.items.some((item) => item.lastError)) - Number(left.items.some((item) => item.lastError)) ||
      Number(right.items.some((item) => item.enabled)) - Number(left.items.some((item) => item.enabled))
  );
});

function groupRuleSummary(group: SubscriptionGroup): string {
  const rule = group.items[0]?.rule;
  if (!rule) {
    return "";
  }
  const parts: string[] = [];
  const subgroup = rule.lockedSubgroup?.trim() || rule.preferredSubgroups?.[0]?.trim();
  parts.push(subgroup || "自动优选");
  if (rule.onlyNew) {
    parts.push("仅追新集");
  }
  return parts.join(" · ");
}

const editingAnimeGroup = ref<SubscriptionGroup | null>(null);
const editingAnimeModel = computed<HomepageAnime | null>(() => {
  const group = editingAnimeGroup.value;
  if (!group?.animeId) {
    return null;
  }
  const fav = favoriteAnimeMap.value.get(group.animeId);
  if (fav) {
    return fav;
  }
  return {
    animeId: group.animeId,
    bangumiId: group.animeId,
    title: group.title,
    imageUrl: group.imageUrl,
    searchKeyword: group.title,
    airDay: null,
    isOnAir: true,
    isRestricted: false,
    rating: null,
    source: "bangumi",
    mikanId: null,
    officialSite: null
  };
});

async function handleSaveAnimeGroupRule(payload: {
  anime: HomepageAnime;
  mikanKeyword?: string;
  providerIds: string[];
  options: AnimeSubscriptionOptions;
}): Promise<void> {
  const ok = await createSubscriptionFromAnime(
    payload.anime,
    payload.mikanKeyword,
    payload.providerIds,
    payload.options
  );
  if (ok) {
    editingAnimeGroup.value = null;
  }
}

async function handleUnsubscribeAnimeGroup(): Promise<void> {
  const animeId = editingAnimeGroup.value?.animeId;
  if (!animeId) {
    return;
  }
  const ok = await cancelSubscriptionForAnime(animeId);
  if (ok) {
    editingAnimeGroup.value = null;
  }
}

/** 组的汇总状态：有一个源出错就提示出错；有一个源在自动下载就算追更中。 */
function groupState(group: SubscriptionGroup): { label: string; className: string } {
  if (group.items.some((item) => item.lastError)) {
    return { label: "有源出错", className: "status-pill-err" };
  }
  const active = group.items.filter((item) => item.enabled);
  if (!active.length) {
    return { label: "已停用", className: "status-pill-muted" };
  }
  if (active.some((item) => item.rule.autoDownload)) {
    return { label: "追更中", className: "status-pill-ok" };
  }
  return { label: "仅收集", className: "status-pill-warn" };
}

function groupDownloaded(group: SubscriptionGroup): { count: number; latest: number | null } {
  const episodes = new Set<number>();
  let latest: number | null = null;
  for (const subscription of group.items) {
    subscription.downloadedEpisodes.forEach((episode) => episodes.add(episode));
    const known = latestEpisodeNumber(subscription);
    if (known !== null) {
      latest = latest === null ? known : Math.max(latest, known);
    }
  }
  return { count: episodes.size, latest };
}

/** RSS 地址里的番名是百分号编码，原样显示是一串 %E5%B0…，解出来才读得懂 */
function readableUrl(url: string): string {
  try {
    return decodeURI(url);
  } catch {
    return url;
  }
}

function toggleExpanded(id: number): void {
  if (expandedIds.has(id)) {
    expandedIds.delete(id);
  } else {
    expandedIds.add(id);
  }
}

function parseCsv(value: string): string[] {
  return value.split(",").map((item) => item.trim()).filter(Boolean);
}

function writeCsv(field: CsvField, values: string[]): void {
  subscriptionForm[field] = Array.from(new Set(values)).join(", ");
}

function toggleCsvValue(field: CsvField, value: string): void {
  const values = parseCsv(subscriptionForm[field]);
  writeCsv(field, values.includes(value) ? values.filter((item) => item !== value) : [...values, value]);
}

function fieldContains(field: CsvField, value: string): boolean {
  return parseCsv(subscriptionForm[field]).includes(value);
}

/** 字幕组偏好当前的顺序。它是有序列表，不是集合 —— 顺序就是优先级。 */
const preferredSubgroupList = computed(() => parseCsv(subscriptionForm.preferredSubgroups));

function moveSubgroupPreference(index: number, delta: number): void {
  const values = parseCsv(subscriptionForm.preferredSubgroups);
  const target = index + delta;
  if (target < 0 || target >= values.length) {
    return;
  }
  const [moved] = values.splice(index, 1);
  values.splice(target, 0, moved as string);
  writeCsv("preferredSubgroups", values);
}

function removeSubgroupPreference(index: number): void {
  const values = parseCsv(subscriptionForm.preferredSubgroups);
  values.splice(index, 1);
  writeCsv("preferredSubgroups", values);
}

/**
 * 备选字幕组来自已经抓到的条目：凭空列一串站点常见字幕组没意义，
 * 用户真正会填的就是自己订阅里出现过的那几个。
 */
const knownSubgroups = computed(() => {
  const counts = new Map<string, number>();
  for (const item of feedItems.value) {
    const name = item.subgroup?.trim();
    if (name) {
      counts.set(name, (counts.get(name) ?? 0) + 1);
    }
  }
  return Array.from(counts.entries())
    .sort((left, right) => right[1] - left[1] || left[0].localeCompare(right[0]))
    .slice(0, 12)
    .map(([name]) => name);
});

function openCreateDialog(): void {
  resetSubscriptionForm();
  subscriptionDialogOpen.value = true;
}

function openEditDialog(subscription: Subscription): void {
  fillSubscriptionForm(subscription);
  subscriptionDialogOpen.value = true;
}

async function submitDialog(): Promise<void> {
  const succeeded = await submitSubscription();
  if (succeeded) {
    subscriptionDialogOpen.value = false;
  }
}

async function confirmDelete(): Promise<void> {
  const target = deleteCandidate.value;
  if (!target) {
    return;
  }
  deleteCandidate.value = null;
  await removeSubscription(target.id);
}

function subscriptionStats(subscriptionId: number): { total: number; matched: number; queued: number } {
  const items = feedItemsBySubscription.value.get(subscriptionId) ?? [];
  return {
    total: items.length,
    matched: items.filter((item) => item.matched).length,
    queued: items.filter((item) => ["pending", "queued", "complete"].includes(item.downloadStatus)).length
  };
}

/** 已知的最大集数，用作「已下 8/12 集」里的分母（抓到多少算多少）。 */
function latestEpisodeNumber(subscription: Subscription): number | null {
  const numbers = feedItems.value
    .filter((item) => item.subscriptionId === subscription.id && item.episodeNumber !== null)
    .map((item) => item.episodeNumber as number);
  const known = [...numbers, ...subscription.downloadedEpisodes];
  return known.length ? Math.max(...known) : null;
}

function feedStatusLabel(item: FeedItem): string {
  // ignored 是用户删过这条下载的显式决定，即使命中规则也不会再自动入队，
  // 所以要先于「未命中」判断，否则用户看不出「为什么它不下了」。
  if (item.downloadStatus === "ignored") {
    return "已忽略";
  }
  if (!item.matched) {
    return "未命中";
  }
  switch (item.downloadStatus) {
    case "pending":
      return "待下载";
    case "queued":
      return "下载中";
    case "complete":
      return "已完成";
    case "error":
      return "出错";
    default:
      return "未下载";
  }
}

function feedStatusClass(item: FeedItem): string {
  if (item.downloadStatus === "ignored") {
    return "status-pill-muted line-through";
  }
  if (!item.matched) {
    return "status-pill-muted";
  }
  if (item.downloadStatus === "queued" || item.downloadStatus === "pending") {
    return "status-pill-accent";
  }
  if (item.downloadStatus === "complete") {
    return "status-pill-ok";
  }
  if (item.downloadStatus === "error") {
    return "status-pill-err";
  }
  return "status-pill-warn";
}

function queueLabel(item: FeedItem): string {
  if (isBusy(`feed:${item.id}`)) {
    return "加入中…";
  }
  if (item.downloadStatus === "complete") {
    return "已完成";
  }
  if (item.downloadStatus === "queued") {
    return "下载中";
  }
  return item.downloadStatus === "ignored" ? "重新下载" : "下载";
}

watch(
  () => subscriptions.value.map((subscription) => subscription.id).join(","),
  () => {
    for (const id of [...expandedIds]) {
      if (!subscriptions.value.some((subscription) => subscription.id === id)) {
        expandedIds.delete(id);
      }
    }
  }
);

watch(editingSubscriptionId, (id) => {
  // 从收藏页「编辑规则」跳转过来时自动打开弹窗
  if (id !== null && !subscriptionDialogOpen.value) {
    subscriptionDialogOpen.value = true;
  }
});
</script>

<template>
  <section class="h-full min-h-0 overflow-hidden">
    <!-- 新建 / 编辑弹窗 -->
    <AppDialog
      v-if="subscriptionDialogOpen"
      size="lg"
      :title="editingSubscriptionId ? '编辑订阅' : '新建订阅'"
      description="按包含、排除、分辨率和保存目录控制 RSS 自动入队。"
      @close="subscriptionDialogOpen = false"
    >
      <form id="subscription-form" class="grid gap-3" @submit.prevent="submitDialog">
        <label class="grid gap-1.5">
          <span class="text-sm font-semibold text-[var(--muted)]">名称</span>
          <input v-model="subscriptionForm.name" class="input-base px-4 py-3" placeholder="例如：葬送的芙莉莲 1080p" />
        </label>
        <label class="grid gap-1.5">
          <span class="text-sm font-semibold text-[var(--muted)]">RSS URL</span>
          <input v-model="subscriptionForm.url" class="input-base px-4 py-3" placeholder="https://example.com/feed.xml" />
        </label>

        <div class="grid gap-3 md:grid-cols-2">
          <div class="grid gap-1.5">
            <span class="text-sm font-semibold text-[var(--muted)]">启用状态</span>
            <SegmentedControl v-model="subscriptionForm.enabled" block :options="ENABLED_OPTIONS" aria-label="启用状态" />
          </div>
          <div class="grid gap-1.5">
            <span class="text-sm font-semibold text-[var(--muted)]">命中后动作</span>
            <SegmentedControl v-model="subscriptionForm.autoDownload" block :options="AUTO_DOWNLOAD_OPTIONS" aria-label="命中后动作" />
          </div>
          <div class="grid gap-1.5">
            <span class="text-sm font-semibold text-[var(--muted)]">合集资源</span>
            <SegmentedControl v-model="subscriptionForm.skipBatch" block :options="SKIP_BATCH_OPTIONS" aria-label="合集资源" />
          </div>
        </div>

        <label class="grid gap-1.5">
          <span class="text-sm font-semibold text-[var(--muted)]">下载目录覆盖</span>
          <input v-model="subscriptionForm.downloadDirOverride" class="input-base px-4 py-3" placeholder="可选；留空使用默认下载目录" />
        </label>

        <div class="grid gap-3 rounded-xl border border-[var(--line)] bg-[var(--tint-2)] p-3.5">
          <label class="grid gap-1.5">
            <span class="text-sm font-semibold text-[var(--muted)]">必须包含</span>
            <input v-model="subscriptionForm.includeKeywords" class="input-base px-3 py-2.5" placeholder="番剧名, 字幕组" />
          </label>
          <div class="flex flex-wrap gap-2">
            <button
              v-for="keyword in ['简体', '繁体', 'CHS', 'CHT']"
              :key="keyword"
              type="button"
              class="chip px-3 py-1 text-xs"
              :class="{ 'chip-active': fieldContains('includeKeywords', keyword) }"
              @click="toggleCsvValue('includeKeywords', keyword)"
            >
              {{ keyword }}
            </button>
          </div>

          <label class="grid gap-1.5">
            <span class="text-sm font-semibold text-[var(--muted)]">排除关键词</span>
            <input v-model="subscriptionForm.excludeKeywords" class="input-base px-3 py-2.5" placeholder="合集, NCOP, NCED, SP, PV" />
          </label>
          <div class="flex flex-wrap gap-2">
            <button
              v-for="keyword in ['合集', 'NCOP', 'NCED', 'SP', 'PV']"
              :key="keyword"
              type="button"
              class="chip px-3 py-1 text-xs"
              :class="{ 'chip-active': fieldContains('excludeKeywords', keyword) }"
              @click="toggleCsvValue('excludeKeywords', keyword)"
            >
              {{ keyword }}
            </button>
          </div>

          <label class="grid gap-1.5">
            <span class="text-sm font-semibold text-[var(--muted)]">分辨率偏好</span>
            <input v-model="subscriptionForm.preferredResolutions" class="input-base px-3 py-2.5" placeholder="1080p, 2160p" />
          </label>
          <div class="flex flex-wrap gap-2">
            <button
              v-for="keyword in ['720p', '1080p', '2160p']"
              :key="keyword"
              type="button"
              class="chip px-3 py-1 text-xs"
              :class="{ 'chip-active': fieldContains('preferredResolutions', keyword) }"
              @click="toggleCsvValue('preferredResolutions', keyword)"
            >
              {{ keyword }}
            </button>
          </div>

          <!--
            字幕组偏好是跨源同集择优的第一优先级（字幕组 > 分辨率 > 先到先得），
            所以顺序本身有意义：越靠前越优先。用左右箭头调，别让用户回去改逗号分隔的字符串。
          -->
          <label class="grid gap-1.5">
            <span class="text-sm font-semibold text-[var(--muted)]">字幕组偏好（有序，越靠前越优先）</span>
            <input
              v-model="subscriptionForm.preferredSubgroups"
              class="input-base rounded-lg px-3 py-2.5"
              placeholder="喵萌奶茶屋, 桜都字幕组"
            />
          </label>
          <div v-if="preferredSubgroupList.length" class="flex flex-wrap items-center gap-2">
            <span
              v-for="(name, index) in preferredSubgroupList"
              :key="name"
              class="chip chip-active inline-flex items-center gap-1 rounded-lg px-2.5 py-1.5 text-xs"
            >
              <span class="font-semibold">{{ index + 1 }}. {{ name }}</span>
              <button
                type="button"
                class="px-0.5 disabled:opacity-40"
                :disabled="index === 0"
                aria-label="上移"
                @click="moveSubgroupPreference(index, -1)"
              >
                ←
              </button>
              <button
                type="button"
                class="px-0.5 disabled:opacity-40"
                :disabled="index === preferredSubgroupList.length - 1"
                aria-label="下移"
                @click="moveSubgroupPreference(index, 1)"
              >
                →
              </button>
              <button type="button" class="px-0.5" aria-label="移除" @click="removeSubgroupPreference(index)">
                ×
              </button>
            </span>
          </div>
          <div v-if="knownSubgroups.length" class="flex flex-wrap gap-2">
            <button
              v-for="name in knownSubgroups"
              :key="name"
              type="button"
              class="chip px-3 py-1 text-xs"
              :class="{ 'chip-active': fieldContains('preferredSubgroups', name) }"
              @click="toggleCsvValue('preferredSubgroups', name)"
            >
              {{ name }}
            </button>
          </div>
        </div>
      </form>
      <template #footer>
        <button type="button" class="btn-ghost" @click="subscriptionDialogOpen = false">取消</button>
        <button type="submit" form="subscription-form" class="btn-primary" :disabled="isBusy('subscription-form')">
          {{ isBusy("subscription-form") ? "提交中…" : editingSubscriptionId ? "保存订阅" : "创建订阅（创建后自动抓取一次）" }}
        </button>
      </template>
    </AppDialog>

    <AnimeSubscribeDialog
      v-if="editingAnimeGroup && editingAnimeModel"
      :anime="editingAnimeModel"
      :mikan-keyword="favoriteAnimeMap.get(editingAnimeModel.animeId)?.mikanKeyword ?? editingAnimeModel.title"
      :submitting="isBusy(`favorite-subscribe:${editingAnimeModel.animeId}`) || isBusy(`favorite-unsubscribe:${editingAnimeModel.animeId}`)"
      @close="editingAnimeGroup = null"
      @confirm="handleSaveAnimeGroupRule"
      @unsubscribe="handleUnsubscribeAnimeGroup"
    />

    <!-- 删除确认弹窗 -->
    <AppDialog
      v-if="deleteCandidate"
      size="sm"
      :title="`删除订阅「${deleteCandidate.name}」？`"
      description="该订阅抓取的 RSS 条目记录会一并删除；已创建的下载任务和本地文件不受影响。此操作无法撤销。"
      @close="deleteCandidate = null"
    >
      <template #footer>
        <button type="button" class="btn-ghost" @click="deleteCandidate = null">取消</button>
        <button type="button" class="btn-danger" @click="confirmDelete">删除订阅</button>
      </template>
    </AppDialog>

    <div class="page-shell">
      <div class="page-toolbar">
        <div class="toolbar-row">
          <div class="toolbar-chips" role="group" aria-label="按状态筛选">
            <button
              v-for="tab in filterTabs"
              :key="tab.key"
              type="button"
              class="chip"
              :class="{ 'chip-active': statusFilter === tab.key }"
              :aria-pressed="statusFilter === tab.key"
              @click="statusFilter = tab.key"
            >
              {{ tab.label }}
              <span class="chip-count">{{ tab.count }}</span>
            </button>
          </div>
          <span class="toolbar-meta">
            命中 {{ totalMatchedItems }} · 入队 {{ totalQueuedItems }}
            <template v-if="failedSubscriptionCount"> · <span class="text-[var(--err)]">{{ failedSubscriptionCount }} 个出错</span></template>
          </span>
        </div>
        <div class="toolbar-row flex-wrap">
          <label class="search-field min-w-[12rem]">
            <AppNavIcon name="search" aria-hidden="true" />
            <input v-model="searchQuery" type="search" placeholder="搜索番名、RSS 地址或关键词" aria-label="搜索订阅" />
          </label>
          <button
            type="button"
            class="btn-ghost shrink-0 px-3 py-1.5 text-sm font-semibold"
            :disabled="isBusy('subscription-refresh-all') || !subscriptions.length"
            @click="refreshAllSubscriptions"
          >
            {{ isBusy("subscription-refresh-all") ? "刷新中…" : "刷新全部" }}
          </button>
          <button type="button" class="btn-primary shrink-0 px-3 py-1.5 text-sm font-semibold" @click="openCreateDialog">
            新建订阅
          </button>
        </div>
      </div>

      <div class="page-body">
        <div v-if="!subscriptions.length" class="empty-state h-full">
          <div class="grid max-w-sm gap-3">
            <span class="empty-icon"><AppNavIcon name="subscriptions" class="size-6" aria-hidden="true" /></span>
            <p class="text-sm font-semibold text-[var(--ink)]">还没有订阅</p>
            <p class="text-sm text-[var(--muted)]">在番剧详情里点「订阅并自动追更」，或者点右上角「新建订阅」手动添加 RSS。</p>
          </div>
        </div>
        <p v-else-if="!groupedSubscriptions.length" class="empty-state text-sm text-[var(--muted)]">没有符合条件的订阅</p>

        <div v-else class="grid content-start gap-3">
          <section v-for="group in groupedSubscriptions" :key="group.key" class="list-panel">
            <!-- 卡头：这部番整体在干什么 -->
            <header class="flex min-w-0 items-center gap-3 bg-[var(--tint-1)] px-3.5 py-3">
              <div class="grid h-12 w-9 shrink-0 place-items-center overflow-hidden rounded-[9px] bg-[var(--panel)] text-sm font-semibold text-[var(--muted)]">
                <img v-if="group.imageUrl" :src="group.imageUrl" alt="" class="size-full object-cover" loading="lazy" />
                <span v-else aria-hidden="true">{{ group.title.slice(0, 1) }}</span>
              </div>
              <div class="grid min-w-0 flex-1 gap-0.5">
                <h3 class="truncate text-sm font-semibold text-[var(--ink)]" :title="group.title">{{ group.title }}</h3>
                <p class="text-xs text-[var(--muted)]">
                  {{ group.items.length }} 个源
                  <template v-if="group.animeId && groupRuleSummary(group)">
                    · {{ groupRuleSummary(group) }}
                  </template>
                  <template v-if="groupDownloaded(group).count">
                    · 已下 {{ groupDownloaded(group).count }}{{ groupDownloaded(group).latest ? ` / ${groupDownloaded(group).latest}` : "" }} 集
                  </template>
                </p>
              </div>
              <button
                v-if="group.animeId"
                type="button"
                class="btn-ghost shrink-0 px-2.5 py-1 text-xs font-semibold"
                @click="editingAnimeGroup = group"
              >
                追更设置
              </button>
              <span class="status-pill shrink-0" :class="groupState(group).className">{{ groupState(group).label }}</span>
            </header>

            <!-- 每个源一行 -->
            <div v-for="subscription in group.items" :key="subscription.id">
              <div
                class="grid min-w-0 cursor-pointer grid-cols-[auto_minmax(0,1fr)_auto] items-center gap-3 px-3 py-2 transition hover:bg-[var(--tint-2)]"
                @click="toggleExpanded(subscription.id)"
              >
                <button
                  type="button"
                  class="grid size-6 place-items-center rounded text-[var(--muted)] hover:text-[var(--ink)]"
                  :aria-expanded="expandedIds.has(subscription.id)"
                  :aria-label="`${expandedIds.has(subscription.id) ? '收起' : '展开'}「${subscription.name}」的条目`"
                  @click.stop="toggleExpanded(subscription.id)"
                >
                  <AppNavIcon name="chevron-right" class="size-3.5 transition-transform" :class="{ 'rotate-90': expandedIds.has(subscription.id) }" />
                </button>

                <div class="grid min-w-0 gap-0.5">
                  <div class="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1">
                    <strong class="text-[13px] font-semibold text-[var(--ink)]">{{ splitName(subscription).source }}</strong>
                    <span class="status-pill" :class="subscription.enabled ? (subscription.rule.autoDownload ? 'status-pill-ok' : 'status-pill-warn') : 'status-pill-muted'">
                      {{ !subscription.enabled ? "已停用" : subscription.rule.autoDownload ? "自动下载" : "仅收集" }}
                    </span>
                    <span class="text-xs tabular-nums text-[var(--muted)]">
                      命中 {{ subscriptionStats(subscription.id).matched }} / {{ subscriptionStats(subscription.id).total }}
                      · 刷新于 {{ formatDateTime(subscription.lastFetchedAt) }}
                    </span>
                  </div>
                  <p class="truncate text-[11px] text-[var(--muted)]" :title="subscription.url">{{ readableUrl(subscription.url) }}</p>
                  <p v-if="subscription.lastError" class="text-xs font-semibold text-[var(--err)]">{{ subscription.lastError }}</p>
                </div>

                <div class="flex shrink-0 items-center gap-1.5" @click.stop>
                  <button
                    type="button"
                    class="icon-btn"
                    :disabled="isBusy(`subscription-refresh:${subscription.id}`)"
                    :title="isBusy(`subscription-refresh:${subscription.id}`) ? '刷新中…' : '刷新这个源'"
                    :aria-label="`刷新 ${subscription.name}`"
                    @click="refreshOneSubscription(subscription.id)"
                  >
                    <AppNavIcon name="refresh" :class="{ 'animate-spin': isBusy(`subscription-refresh:${subscription.id}`) }" />
                  </button>
                  <button type="button" class="btn-ghost h-8 px-2.5 text-xs font-semibold" @click="openEditDialog(subscription)">编辑</button>
                  <button
                    type="button"
                    class="icon-btn hover:!border-[var(--err)] hover:!text-[var(--err)]"
                    :disabled="isBusy(`subscription-delete:${subscription.id}`)"
                    title="删除订阅"
                    :aria-label="`删除 ${subscription.name}`"
                    @click="deleteCandidate = subscription"
                  >
                    <AppNavIcon name="trash" />
                  </button>
                </div>
              </div>

              <!-- 就地展开：这个源抓到的条目 -->
              <div v-if="expandedIds.has(subscription.id)" class="border-t border-[var(--line)] bg-[var(--tint-1)] py-1 pl-12 pr-3">
                <p v-if="!(feedItemsBySubscription.get(subscription.id) ?? []).length" class="py-3 text-xs text-[var(--muted)]">
                  还没有抓到条目，点右侧刷新试试。
                </p>
                <div
                  v-for="item in feedItemsBySubscription.get(subscription.id) ?? []"
                  :key="item.id"
                  class="grid min-w-0 grid-cols-[minmax(0,1fr)_auto] items-center gap-3 border-b border-[var(--line)] py-1.5 last:border-b-0"
                >
                  <div class="grid min-w-0">
                    <span class="truncate text-xs text-[var(--ink)]" :title="item.title">{{ item.title }}</span>
                    <span class="truncate text-[11px] text-[var(--muted)]" :title="item.matchReason ?? undefined">
                      {{ formatDateTime(item.publishedAt || item.createdAt) }}<template v-if="item.matchReason"> · {{ item.matchReason }}</template>
                    </span>
                  </div>
                  <div class="flex shrink-0 items-center gap-1.5">
                    <span class="status-pill" :class="feedStatusClass(item)">{{ feedStatusLabel(item) }}</span>
                    <button
                      type="button"
                      class="btn-ghost h-7 px-2 text-xs font-semibold"
                      :disabled="!item.sourceUrl || isBusy(`feed:${item.id}`) || item.downloadStatus === 'queued' || item.downloadStatus === 'complete'"
                      :title="!item.sourceUrl ? '该条目没有可下载的资源地址' : item.downloadStatus === 'ignored' ? '这条下载被删除过，点击重新下载' : undefined"
                      @click="queueFeedItem(item.id)"
                    >
                      {{ queueLabel(item) }}
                    </button>
                    <button
                      type="button"
                      class="icon-btn size-7"
                      :disabled="!item.sourceUrl"
                      title="复制资源地址"
                      aria-label="复制资源地址"
                      @click="copyText(item.sourceUrl ?? '')"
                    >
                      <AppNavIcon name="copy" />
                    </button>
                  </div>
                </div>
              </div>
            </div>
          </section>
        </div>
      </div>
    </div>
  </section>
</template>
