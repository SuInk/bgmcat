<script setup lang="ts">
import { computed, onMounted, ref, watch } from "vue";

import { extractEpisodeNumber } from "@shared/episode";
import type {
  AnimeSubscriptionOptions,
  HomepageAnime,
  MikanDownloadItem,
  ResourceProviderFetchStatus,
  SubscriptionLanguagePref
} from "@shared/index";

import { getAnimeDetail } from "../../api";
import { buildMikanCountOptions, resolveSubgroupKey } from "../../lib/mikan";
import { settings } from "../../stores/session";
import { subscriptions } from "../../stores/subscriptions";
import AppDialog from "../common/AppDialog.vue";

const PROVIDER_DISPLAY_NAMES: Record<string, string> = {
  mikan: "蜜柑计划",
  dmhy: "动漫花园",
  acgrip: "ACG.RIP",
  bangumimoe: "萌番组",
  nyaa: "Nyaa",
  animegarden: "AnimeGarden"
};

const CHS_RE = /(?:简日|简体|简中|简繁|多国字幕|多语|\bchs\b|\bgb\b)/i;
const CHT_RE = /(?:繁日|繁体|繁中|简繁|\bcht\b|\bbig5\b)/i;

const props = defineProps<{
  anime: HomepageAnime;
  mikanKeyword?: string;
  downloads?: MikanDownloadItem[];
  providerStatuses?: ResourceProviderFetchStatus[];
  knownLatestEpisode?: number | null;
  initialSubgroup?: string;
  submitting?: boolean;
}>();

const emit = defineEmits<{
  (event: "close"): void;
  (
    event: "confirm",
    payload: {
      anime: HomepageAnime;
      mikanKeyword?: string;
      providerIds: string[];
      options: AnimeSubscriptionOptions;
    }
  ): void;
  (event: "unsubscribe"): void;
}>();

const fetchedDownloads = ref<MikanDownloadItem[]>([]);
const fetchedProviders = ref<ResourceProviderFetchStatus[]>([]);
const loadingDetail = ref(false);

const activeDownloads = computed(() =>
  props.downloads?.length ? props.downloads : fetchedDownloads.value
);

const existingSubs = computed(() =>
  subscriptions.value.filter((sub) => sub.animeId === props.anime.animeId)
);
const isSubscribed = computed(() => existingSubs.value.length > 0);

const subgroupOptions = computed(() =>
  buildMikanCountOptions(
    activeDownloads.value,
    resolveSubgroupKey,
    (key) => (key === "__unknown" ? "" : key)
  ).filter((opt) => opt.key !== "__unknown" && opt.label)
);

const providerOptions = computed(() => {
  const statuses = props.providerStatuses?.length ? props.providerStatuses : fetchedProviders.value;
  if (statuses.length) {
    return statuses.map((st) => ({
      id: st.providerId,
      label: st.displayName || PROVIDER_DISPLAY_NAMES[st.providerId] || st.providerId
    }));
  }
  const enabled = settings.value?.enabledResourceProviders ?? ["mikan"];
  return enabled.map((id) => ({
    id,
    label: PROVIDER_DISPLAY_NAMES[id] || id
  }));
});

const baselineEpisode = computed(() => {
  let maxEp = props.knownLatestEpisode ?? 0;
  for (const item of activeDownloads.value) {
    const ep = extractEpisodeNumber(item.title);
    if (ep !== null && ep > maxEp) {
      maxEp = ep;
    }
  }
  const savedBase = existingSubs.value[0]?.rule.baselineEpisode ?? 0;
  if (savedBase > maxEp) {
    maxEp = savedBase;
  }
  return maxEp;
});

const selectedSubgroup = ref<string>("all");
const selectedResolution = ref<"1080p" | "2160p" | "any">("1080p");
const selectedLanguage = ref<SubscriptionLanguagePref>("chs");
const onlyNew = ref<boolean>(true);
const selectedProviders = ref<string[]>([]);

function initFormFromState(): void {
  const primarySub = existingSubs.value[0];
  if (primarySub) {
    const rule = primarySub.rule;
    selectedSubgroup.value =
      rule.lockedSubgroup?.trim() ||
      rule.preferredSubgroups?.[0]?.trim() ||
      "all";
    const firstRes = rule.preferredResolutions?.[0]?.toLowerCase() ?? "";
    if (firstRes === "2160p" || firstRes === "4k") {
      selectedResolution.value = "2160p";
    } else if ((rule.preferredResolutions?.length ?? 0) === 0) {
      selectedResolution.value = "any";
    } else {
      selectedResolution.value = "1080p";
    }
    selectedLanguage.value = rule.preferredLanguage ?? "chs";
    onlyNew.value = rule.onlyNew ?? true;
    const subProviders = existingSubs.value
      .map((s) => s.providerId)
      .filter((id): id is string => Boolean(id));
    selectedProviders.value = subProviders.length
      ? subProviders
      : providerOptions.value.map((p) => p.id);
    return;
  }

  const presetSubgroup = props.initialSubgroup?.trim();
  selectedSubgroup.value =
    presetSubgroup && presetSubgroup !== "all" && presetSubgroup !== "__unknown"
      ? presetSubgroup
      : "all";
  selectedResolution.value = "1080p";
  selectedLanguage.value = "chs";
  onlyNew.value = true;
  selectedProviders.value = providerOptions.value.map((p) => p.id);
}

watch(
  providerOptions,
  (opts) => {
    if (selectedProviders.value.length === 0 && opts.length > 0) {
      selectedProviders.value = opts.map((p) => p.id);
    }
  },
  { immediate: true }
);

function pickSubgroup(subgroupKey: string): void {
  selectedSubgroup.value = subgroupKey;
  if (subgroupKey === "all") {
    return;
  }
  // 若该字幕组现有资源全部为纯繁中，自动把「简中」放宽为「不限」，避免误筛空
  const groupItems = activeDownloads.value.filter(
    (item) => resolveSubgroupKey(item) === subgroupKey
  );
  if (groupItems.length > 0 && selectedLanguage.value === "chs") {
    const allPureCht = groupItems.every(
      (item) => CHT_RE.test(item.title) && !CHS_RE.test(item.title)
    );
    if (allPureCht) {
      selectedLanguage.value = "any";
    }
  }
}

function toggleProvider(id: string): void {
  selectedProviders.value = selectedProviders.value.includes(id)
    ? selectedProviders.value.filter((item) => item !== id)
    : [...selectedProviders.value, id];
}

function selectAllProviders(): void {
  selectedProviders.value = providerOptions.value.map((p) => p.id);
}

function submit(): void {
  if (!selectedProviders.value.length) {
    return;
  }
  emit("confirm", {
    anime: props.anime,
    mikanKeyword: props.mikanKeyword ?? props.anime.searchKeyword ?? props.anime.title,
    providerIds: selectedProviders.value,
    options: {
      subgroup: selectedSubgroup.value === "all" ? null : selectedSubgroup.value,
      resolution: selectedResolution.value,
      language: selectedLanguage.value,
      onlyNew: onlyNew.value,
      baselineEpisode: onlyNew.value ? baselineEpisode.value : 0
    }
  });
}

onMounted(async () => {
  initFormFromState();
  if (!props.downloads?.length) {
    loadingDetail.value = true;
    try {
      const detail = await getAnimeDetail(props.anime.animeId, {
        keyword: props.mikanKeyword ?? props.anime.searchKeyword ?? props.anime.title
      });
      fetchedDownloads.value = detail.downloads ?? [];
      fetchedProviders.value = detail.providerStatuses ?? [];
      if (!isSubscribed.value && selectedProviders.value.length === 0 && detail.providerStatuses?.length) {
        selectedProviders.value = detail.providerStatuses.map((s) => s.providerId);
      }
    } catch {
      // 保持自动优选即可
    } finally {
      loadingDetail.value = false;
    }
  }
});
</script>

<template>
  <AppDialog
    size="sm"
    :title="isSubscribed ? '追更设置' : '订阅追更'"
    :description="anime.title"
    :dismissible="!submitting"
    @close="emit('close')"
  >
    <div class="grid gap-3.5">
      <!-- 1. 字幕组 -->
      <div class="grid gap-1.5">
        <div class="flex items-center justify-between">
          <span class="text-xs font-semibold text-[var(--muted)]">字幕组</span>
          <span v-if="loadingDetail && !subgroupOptions.length" class="text-[11px] text-[var(--muted)]">
            加载中…
          </span>
        </div>
        <div class="flex max-h-36 flex-wrap gap-1.5 overflow-y-auto pr-0.5">
          <button
            type="button"
            class="chip px-2.5 py-1 text-xs"
            :class="{ 'chip-active': selectedSubgroup === 'all' }"
            :aria-pressed="selectedSubgroup === 'all'"
            @click="pickSubgroup('all')"
          >
            自动优选
          </button>
          <button
            v-for="opt in subgroupOptions"
            :key="opt.key"
            type="button"
            class="chip px-2.5 py-1 text-xs"
            :class="{ 'chip-active': selectedSubgroup === opt.key }"
            :aria-pressed="selectedSubgroup === opt.key"
            @click="pickSubgroup(opt.key)"
          >
            <span class="max-w-[11rem] truncate">{{ opt.label }}</span>
            <span class="chip-count">{{ opt.count }}</span>
          </button>
        </div>
      </div>

      <!-- 2. 画质 & 字幕 -->
      <div class="grid grid-cols-2 gap-3">
        <div class="grid gap-1.5">
          <span class="text-xs font-semibold text-[var(--muted)]">画质</span>
          <div class="flex flex-wrap gap-1">
            <button
              v-for="res in [
                { key: '1080p', label: '1080P' },
                { key: '2160p', label: '4K' },
                { key: 'any', label: '不限' }
              ] as const"
              :key="res.key"
              type="button"
              class="chip px-2.5 py-1 text-xs"
              :class="{ 'chip-active': selectedResolution === res.key }"
              :aria-pressed="selectedResolution === res.key"
              @click="selectedResolution = res.key"
            >
              {{ res.label }}
            </button>
          </div>
        </div>

        <div class="grid gap-1.5">
          <span class="text-xs font-semibold text-[var(--muted)]">字幕</span>
          <div class="flex flex-wrap gap-1">
            <button
              v-for="lang in [
                { key: 'chs', label: '简中' },
                { key: 'cht', label: '繁中' },
                { key: 'any', label: '不限' }
              ] as const"
              :key="lang.key"
              type="button"
              class="chip px-2.5 py-1 text-xs"
              :class="{ 'chip-active': selectedLanguage === lang.key }"
              :aria-pressed="selectedLanguage === lang.key"
              @click="selectedLanguage = lang.key"
            >
              {{ lang.label }}
            </button>
          </div>
        </div>
      </div>

      <!-- 3. 追更范围 -->
      <div class="grid gap-1.5">
        <span class="text-xs font-semibold text-[var(--muted)]">范围</span>
        <div class="grid grid-cols-2 gap-1.5">
          <button
            type="button"
            class="chip justify-center px-2.5 py-1.5 text-xs"
            :class="{ 'chip-active': onlyNew }"
            :aria-pressed="onlyNew"
            @click="onlyNew = true"
          >
            <span>仅追新集</span>
            <span v-if="baselineEpisode > 0" class="text-[11px] opacity-75">
              第 {{ baselineEpisode + 1 }} 话起
            </span>
          </button>
          <button
            type="button"
            class="chip justify-center px-2.5 py-1.5 text-xs"
            :class="{ 'chip-active': !onlyNew }"
            :aria-pressed="!onlyNew"
            @click="onlyNew = false"
          >
            <span>补齐已出</span>
            <span class="text-[11px] opacity-75">每集 1 份</span>
          </button>
        </div>
      </div>

      <!-- 4. 来源（多源时展示） -->
      <div v-if="providerOptions.length > 1" class="grid gap-1.5">
        <div class="flex items-center justify-between">
          <span class="text-xs font-semibold text-[var(--muted)]">来源</span>
          <button
            v-if="selectedProviders.length < providerOptions.length"
            type="button"
            class="text-[11px] font-semibold text-[var(--accent-vivid)] hover:underline"
            @click="selectAllProviders"
          >
            全选
          </button>
        </div>
        <div class="flex flex-wrap gap-1.5">
          <button
            v-for="provider in providerOptions"
            :key="provider.id"
            type="button"
            class="chip px-2.5 py-1 text-xs"
            :class="{ 'chip-active': selectedProviders.includes(provider.id) }"
            :aria-pressed="selectedProviders.includes(provider.id)"
            @click="toggleProvider(provider.id)"
          >
            {{ provider.label }}
          </button>
        </div>
      </div>
    </div>

    <template #footer>
      <div class="flex w-full items-center justify-between gap-2">
        <button
          v-if="isSubscribed"
          type="button"
          class="btn-ghost px-2.5 py-1.5 text-xs font-semibold text-[var(--err)]"
          :disabled="submitting"
          @click="emit('unsubscribe')"
        >
          取消追更
        </button>
        <span v-else />

        <div class="flex items-center gap-2">
          <button
            type="button"
            class="btn-ghost px-3.5 py-1.5 text-sm font-semibold"
            :disabled="submitting"
            @click="emit('close')"
          >
            取消
          </button>
          <button
            type="button"
            class="btn-primary px-4 py-1.5 text-sm font-semibold"
            :disabled="submitting || selectedProviders.length === 0"
            @click="submit"
          >
            {{ submitting ? "保存中…" : isSubscribed ? "保存" : "确认订阅" }}
          </button>
        </div>
      </div>
    </template>
  </AppDialog>
</template>
