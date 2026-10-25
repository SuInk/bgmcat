<script setup lang="ts">
import { computed, onBeforeUnmount, ref } from "vue";

import type { HomepageAnime } from "@shared/index";

import { airDayLabel, broadcastLabel, broadcastWeekday } from "../../lib/format";
import { metadataPlatformLabel } from "../../lib/metadataPlatform";
import { prefetchAnimeDetail } from "../../stores/discovery";

const props = defineProps<{
  anime: HomepageAnime;
  selected?: boolean;
  /** 窄屏下省略蜜柑编号等次要元信息，避免截断成「蜜柑 #3…」这种无意义碎片 */
  compactMeta?: boolean;
}>();

const emit = defineEmits<{
  (event: "open"): void;
}>();

const imageFailed = ref(false);

/**
 * 悬停预取:停够 220ms 才发,鼠标横扫整屏卡片不会触发一串请求。
 * 键盘 focus 同样触发——用方向键走到这张卡的人，下一步多半就是回车。
 */
const PREFETCH_DWELL_MS = 220;
let prefetchTimer: ReturnType<typeof setTimeout> | null = null;

function schedulePrefetch(): void {
  if (prefetchTimer) {
    return;
  }
  prefetchTimer = setTimeout(() => {
    prefetchTimer = null;
    prefetchAnimeDetail(props.anime.animeId);
  }, PREFETCH_DWELL_MS);
}

function cancelPrefetch(): void {
  if (prefetchTimer) {
    clearTimeout(prefetchTimer);
    prefetchTimer = null;
  }
}

onBeforeUnmount(cancelPrefetch);

const ratingLabel = computed(() => {
  const value = props.anime.rating;
  return typeof value === "number" && Number.isFinite(value) && value > 0 ? value.toFixed(1) : null;
});

const airLabel = computed(() => airDayLabel(broadcastWeekday(props.anime.broadcastAt) ?? props.anime.airDay));
const airTimeLabel = computed(() => broadcastLabel(props.anime.broadcastAt));
const statusLabel = computed(() => (props.anime.isOnAir ? "连载" : "待播"));

/** 蜜柑编号只在够宽时出现：窄屏留着也只会被截断 */
const metaParts = computed(() => {
  const parts = [airLabel.value, statusLabel.value];
  if (!props.compactMeta) {
    parts.push(props.anime.mikanId ? `蜜柑 #${props.anime.mikanId}` : "蜜柑搜索");
  }
  return parts;
});

/**
 * 命中平台徽章。只在**真有信息平台命中**时才出现：
 * 当季首页那批全是清一色的「本地」，挂上去只是噪音。
 */
const platformBadges = computed(() => {
  const matched = props.anime.matchedPlatforms ?? [];
  if (!matched.some((id) => id !== "bangumi-data")) {
    return [];
  }
  return matched.map((id) => ({ id, label: metadataPlatformLabel(id) }));
});

const openLabel = computed(() => {
  const rating = ratingLabel.value ? `，评分 ${ratingLabel.value}` : "";
  const air = airTimeLabel.value ?? `${airLabel.value}${statusLabel.value}`;
  return `${props.anime.title}，${air}${rating}`;
});
</script>

<template>
  <article
    class="grid grid-rows-[auto_minmax(0,1fr)] overflow-hidden rounded-lg border transition hover:-translate-y-0.5"
    :class="
      selected
        ? 'border-[var(--accent)] bg-[var(--tint-2)] shadow-[var(--soft-shadow)]'
        : 'border-[var(--line)] bg-[var(--card)] shadow-[var(--soft-shadow)] hover:border-[var(--accent)]'
    "
  >
    <button
      type="button"
      class="grid grid-rows-[8.2rem_minmax(0,1fr)] text-left focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-[-2px] focus-visible:outline-[var(--accent)]"
      :aria-label="openLabel"
      @click="emit('open')"
      @mouseenter="schedulePrefetch"
      @mouseleave="cancelPrefetch"
      @focus="schedulePrefetch"
      @blur="cancelPrefetch"
    >
      <span class="grid min-h-0 place-items-center overflow-hidden bg-[var(--panel)] text-3xl font-semibold text-[var(--muted)]">
        <img
          v-if="anime.imageUrl && !imageFailed"
          :src="anime.imageUrl"
          alt=""
          class="size-full object-cover"
          loading="lazy"
          decoding="async"
          @error="imageFailed = true"
        />
        <span v-else aria-hidden="true">{{ anime.title.slice(0, 1) || "番" }}</span>
      </span>

      <span class="grid min-h-0 content-start gap-1 px-3 py-2.5">
        <strong class="truncate text-sm font-bold text-[var(--ink)]" :title="anime.title">{{ anime.title }}</strong>
        <span class="truncate text-xs font-semibold text-[var(--muted)]">{{ metaParts.join(" · ") }}</span>
        <span v-if="ratingLabel" class="text-xs font-semibold text-[var(--accent-vivid)]">★ {{ ratingLabel }}</span>

        <span
          v-if="platformBadges.length"
          class="flex flex-wrap gap-1"
          :title="`命中平台：${platformBadges.map((badge) => badge.label).join('、')}`"
        >
          <span
            v-for="badge in platformBadges"
            :key="badge.id"
            class="rounded border border-[var(--line)] px-1 py-px text-[0.625rem] font-semibold leading-tight text-[var(--muted)]"
          >{{ badge.label }}</span>
        </span>
      </span>
    </button>

    <div v-if="$slots.footer" class="grid content-start gap-2 border-t border-[var(--line)] px-3 py-2.5">
      <slot name="footer" />
    </div>
  </article>
</template>
