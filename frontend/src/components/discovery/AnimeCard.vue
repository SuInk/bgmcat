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
  /** 元信息里用首播时刻代替星期（今日更新货架用） */
  showTime?: boolean;
  /** poster：海报卡（默认）；row：紧凑列表的一行，小缩略图 + 标题 + 时间 + 评分 */
  layout?: "poster" | "row";
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
/** 货架卡（今日更新）里星期是废话，换成具体的首播时刻 */
const shelfTimeLabel = computed(() => airTimeLabel.value?.replace(/^每.{2}\s*/, "") ?? null);

/** 紧凑列表那一行：有首播时刻就写「周三 23:30」，没有就退回「周三」 */
const rowMeta = computed(() => [airTimeLabel.value?.replace(/^每/, "") ?? airLabel.value, statusLabel.value].join(" · "));

/** 蜜柑编号只在够宽时出现：窄屏留着也只会被截断 */
const metaParts = computed(() => {
  const parts = props.showTime && shelfTimeLabel.value ? [shelfTimeLabel.value, statusLabel.value] : [airLabel.value, statusLabel.value];
  if (!props.compactMeta) {
    parts.push(props.anime.mikanId ? `蜜柑 #${props.anime.mikanId}` : "蜜柑搜索");
  }
  return parts;
});

/**
 * 命中平台徽章。只在**真有信息平台命中**时才出现：
 * 当季首页那批全是清一色的「本地」/「Bangumi」，挂上去只是噪音。
 */
const DEFAULT_PLATFORMS = new Set(["bangumi-data", "bangumi"]);
const platformBadges = computed(() => {
  const matched = props.anime.matchedPlatforms ?? [];
  if (matched.every((id) => DEFAULT_PLATFORMS.has(id))) {
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
  <!-- 紧凑列表（参考 B 站新番时间表）：一行一部，缩略图 48×64，一屏能扫过三四十部 -->
  <button
    v-if="layout === 'row'"
    type="button"
    class="anime-row"
    :class="{ 'is-selected': selected }"
    :aria-label="openLabel"
    @click="emit('open')"
    @mouseenter="schedulePrefetch"
    @mouseleave="cancelPrefetch"
    @focus="schedulePrefetch"
    @blur="cancelPrefetch"
  >
    <span class="row-thumb">
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
    <span class="grid min-w-0 gap-1">
      <strong class="truncate text-[13.5px] font-semibold leading-tight text-[var(--ink)]" :title="anime.title">{{ anime.title }}</strong>
      <span class="truncate text-xs text-[var(--muted)]">{{ rowMeta }}</span>
    </span>
    <span v-if="ratingLabel" class="row-rating" aria-hidden="true">★ {{ ratingLabel }}</span>
  </button>

  <!--
    海报卡（尺寸参考 B 站番剧页：3:4、8px 圆角、标题 14px）：没有卡片底板和描边，海报本身就是卡片；
    评分做成海报角上的半透明胶囊。选中只给海报描一圈主题色，悬停海报轻微放大并加深阴影。
  -->
  <article v-else class="anime-card grid min-w-0 content-start gap-2" :class="{ 'is-selected': selected }">
    <button
      type="button"
      class="grid min-w-0 gap-2 rounded-lg text-left focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-[var(--accent)]"
      :aria-label="openLabel"
      @click="emit('open')"
      @mouseenter="schedulePrefetch"
      @mouseleave="cancelPrefetch"
      @focus="schedulePrefetch"
      @blur="cancelPrefetch"
    >
      <span class="anime-poster relative grid aspect-[3/4] place-items-center overflow-hidden rounded-lg bg-[var(--panel)] text-2xl font-semibold text-[var(--muted)]">
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
        <span v-if="ratingLabel" class="poster-rating" aria-hidden="true">★ {{ ratingLabel }}</span>
      </span>

      <span class="grid min-w-0 gap-0.5 px-0.5">
        <strong class="truncate text-[13px] font-semibold leading-snug text-[var(--ink)]" :title="anime.title">{{ anime.title }}</strong>
        <span class="truncate text-[11.5px] text-[var(--muted)]">{{ metaParts.join(" · ") }}</span>
        <span
          v-if="platformBadges.length"
          class="mt-0.5 flex flex-wrap gap-1"
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

    <div v-if="$slots.footer" class="grid content-start gap-2 px-0.5">
      <slot name="footer" />
    </div>
  </article>
</template>

<style scoped>
.anime-poster {
  box-shadow: 0 1px 2px rgb(0 0 0 / 0.12), 0 6px 16px -8px rgb(0 0 0 / 0.35);
  transition: transform 0.2s ease, box-shadow 0.2s ease;
}
.anime-card button:hover .anime-poster {
  transform: scale(1.03);
  box-shadow: 0 2px 4px rgb(0 0 0 / 0.14), 0 14px 28px -10px rgb(0 0 0 / 0.5);
}
.anime-card.is-selected .anime-poster {
  outline: 2.5px solid var(--accent-vivid);
  outline-offset: 2px;
}
.poster-rating {
  position: absolute;
  right: 5px;
  bottom: 5px;
  padding: 0 6px;
  border-radius: 999px;
  background: rgb(0 0 0 / 0.55);
  backdrop-filter: blur(8px);
  color: #fff;
  font-size: 11px;
  font-weight: 650;
  line-height: 18px;
}

.anime-row {
  display: grid;
  grid-template-columns: 48px minmax(0, 1fr) auto;
  align-items: center;
  gap: 12px;
  min-width: 0;
  padding: 6px 10px 6px 6px;
  border-radius: 10px;
  text-align: left;
  transition: background 0.15s;
}
.anime-row:hover {
  background: var(--panel);
}
.anime-row.is-selected {
  background: var(--tint-2);
  box-shadow: inset 0 0 0 1.5px var(--accent-vivid);
}
.anime-row:focus-visible {
  outline: 2px solid var(--accent);
  outline-offset: 1px;
}
.row-thumb {
  display: grid;
  place-items: center;
  width: 48px;
  height: 64px;
  overflow: hidden;
  border-radius: 6px;
  background: var(--panel);
  color: var(--muted);
  font-size: 15px;
  font-weight: 600;
  box-shadow: 0 1px 2px rgb(0 0 0 / 0.15);
}
.row-rating {
  color: var(--accent-vivid);
  font-size: 12.5px;
  font-weight: 650;
  white-space: nowrap;
}
@media (prefers-reduced-motion: reduce) {
  .anime-poster { transition: none; }
  .anime-card button:hover .anime-poster { transform: none; }
}
</style>
