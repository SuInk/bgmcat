<script setup lang="ts">
import { computed, onBeforeUnmount, ref } from "vue";

import type { HomepageAnime } from "@shared/index";

import { airDayLabel, broadcastLabel, broadcastWeekday } from "../../lib/format";
import { metadataPlatformLabel } from "../../lib/metadataPlatform";
import { markAnimeCoverFailed, prefetchAnimeDetail } from "../../stores/discovery";
import AppNavIcon from "../layout/AppNavIcon.vue";

const props = defineProps<{
  anime: HomepageAnime;
  selected?: boolean;
  /** 窄屏下省略蜜柑编号等次要元信息，避免截断成「蜜柑 #3…」这种无意义碎片 */
  compactMeta?: boolean;
  /** 元信息里用首播时刻代替星期（今日更新货架用） */
  showTime?: boolean;
  /** 番剧索引 / 全库检索模式：展示开播年月、类型与话数而非星期几 */
  catalogMeta?: boolean;
  /** poster：海报卡（默认）；row：紧凑列表的一行，小缩略图 + 标题 + 时间 + 评分 */
  layout?: "poster" | "row";
  /** 是否在首页卡片上提供一键隐藏按钮 */
  allowHide?: boolean;
  /** 当前是否处于已隐藏状态（关键词搜索命中已隐藏条目时用于恢复） */
  isHidden?: boolean;
}>();

const emit = defineEmits<{
  (event: "open"): void;
  (event: "hide", anime: HomepageAnime): void;
}>();

const imageFailed = ref(false);

function handleCoverError(): void {
  imageFailed.value = true;
  markAnimeCoverFailed(props.anime.animeId);
}

/**
 * 悬停预取：停够 120ms 或按下鼠标/手指时立即预热详情与快源，点击时直接命中内存缓存。
 */
const PREFETCH_DWELL_MS = 120;
let prefetchTimer: ReturnType<typeof setTimeout> | null = null;

function warmImmediate(): void {
  cancelPrefetch();
  prefetchAnimeDetail(props.anime.animeId, props.anime.title);
}

function handlePointerDown(event: PointerEvent): void {
  // 触摸滑动时手指按下不能触发 7 源并发预取，避免移动端滑屏时占满带宽与连接池
  if (event.pointerType === "mouse") {
    warmImmediate();
  }
}

function schedulePrefetch(): void {
  if (prefetchTimer) {
    return;
  }
  prefetchTimer = setTimeout(() => {
    prefetchTimer = null;
    prefetchAnimeDetail(props.anime.animeId, props.anime.title);
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

const airYearMonth = computed(() => {
  const raw = props.anime.airDate?.trim();
  if (!raw) {
    return null;
  }
  const match = /^(\d{4})-(\d{2})/.exec(raw);
  return match ? `${match[1]}.${match[2]}` : raw;
});

const airLabel = computed(() => airDayLabel(broadcastWeekday(props.anime.broadcastAt) ?? props.anime.airDay));
const airTimeLabel = computed(() => broadcastLabel(props.anime.broadcastAt));
const statusLabel = computed(() => {
  if (props.anime.airStatus === "upcoming") {
    return "未开播";
  }
  if (props.anime.airStatus === "finished") {
    return "已完结";
  }
  return props.anime.isOnAir ? "连载" : "待播";
});

const useCatalogStyleMeta = computed(
  () =>
    Boolean(props.catalogMeta) ||
    props.anime.airStatus === "finished" ||
    props.anime.airStatus === "upcoming"
);

const catalogMetaParts = computed(() => {
  const parts: string[] = [];
  if (airYearMonth.value) {
    parts.push(airYearMonth.value);
  }
  if (props.anime.format) {
    parts.push(props.anime.format);
  }
  const eps = props.anime.totalEpisodes;
  if (props.anime.airStatus === "upcoming") {
    parts.push("未开播");
  } else if (props.anime.format === "剧场版") {
    parts.push(eps && eps > 1 ? `全${eps}话` : "已上映");
  } else if (eps && eps > 0) {
    parts.push(props.anime.isOnAir ? `${eps}话·连载` : `全${eps}话`);
  } else {
    parts.push(statusLabel.value);
  }
  return parts;
});

/** 货架卡（今日更新）里星期是废话，换成具体的首播时刻 */
const shelfTimeLabel = computed(() => airTimeLabel.value?.replace(/^每.{2}\s*/, "") ?? null);

/** 紧凑列表那一行：索引模式展示年月/类型/话数/题材，时间表模式展示星期与首播时刻 */
const rowMeta = computed(() => {
  if (useCatalogStyleMeta.value) {
    const base = [...catalogMetaParts.value];
    const genreSnippet = (props.anime.tags ?? []).slice(0, 2).join(" / ");
    if (genreSnippet) {
      base.push(genreSnippet);
    }
    return base.join(" · ");
  }
  return [airTimeLabel.value?.replace(/^每/, "") ?? airLabel.value, statusLabel.value].join(" · ");
});

/** 蜜柑编号只在够宽时出现：窄屏留着也只会被截断 */
const metaParts = computed(() => {
  if (useCatalogStyleMeta.value) {
    return catalogMetaParts.value;
  }
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
  <div
    v-if="layout === 'row'"
    class="anime-row"
    :class="{ 'is-selected': selected, 'is-hidden-card': isHidden }"
  >
    <button
      type="button"
      class="anime-row-main"
      :aria-label="openLabel"
      @click="emit('open')"
      @pointerdown="handlePointerDown"
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
          @error="handleCoverError"
        />
        <span v-else aria-hidden="true">{{ anime.title.slice(0, 1) || "番" }}</span>
      </span>
      <span class="grid min-w-0 gap-1">
        <strong class="truncate text-[13.5px] font-semibold leading-tight text-[var(--ink)]" :title="anime.title">{{ anime.title }}</strong>
        <span class="truncate text-xs text-[var(--muted)]">{{ isHidden ? `已隐藏 · ${rowMeta}` : rowMeta }}</span>
      </span>
      <span v-if="ratingLabel" class="row-rating" aria-hidden="true">★ {{ ratingLabel }}</span>
    </button>

    <button
      v-if="allowHide"
      type="button"
      class="row-hide-btn"
      :class="{ 'is-visible': isHidden }"
      :title="isHidden ? '恢复在首页显示' : '在首页隐藏'"
      :aria-label="isHidden ? `恢复显示 ${anime.title}` : `在首页隐藏 ${anime.title}`"
      @click.stop="emit('hide', anime)"
    >
      <AppNavIcon :name="isHidden ? 'eye' : 'eye-off'" class="size-3" aria-hidden="true" />
    </button>
  </div>

  <!--
    海报卡（尺寸参考 B 站番剧页：3:4、8px 圆角、标题 14px）：没有卡片底板和描边，海报本身就是卡片；
    评分做成海报角上的半透明胶囊。选中只给海报描一圈主题色，悬停海报轻微放大并加深阴影。
  -->
  <article
    v-else
    class="anime-card relative grid min-w-0 content-start gap-2"
    :class="{ 'is-selected': selected, 'is-hidden-card': isHidden }"
  >
    <button
      type="button"
      class="anime-open-btn grid min-w-0 gap-2 rounded-[16px] text-left focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-[var(--accent)]"
      :aria-label="openLabel"
      @click="emit('open')"
      @pointerdown="handlePointerDown"
      @mouseenter="schedulePrefetch"
      @mouseleave="cancelPrefetch"
      @focus="schedulePrefetch"
      @blur="cancelPrefetch"
    >
      <span class="anime-poster relative grid aspect-[3/4] place-items-center overflow-hidden rounded-[16px] bg-[var(--panel)] text-2xl font-semibold text-[var(--muted)]">
        <img
          v-if="anime.imageUrl && !imageFailed"
          :src="anime.imageUrl"
          alt=""
          class="size-full object-cover"
          loading="lazy"
          decoding="async"
          @error="handleCoverError"
        />
        <span v-else aria-hidden="true">{{ anime.title.slice(0, 1) || "番" }}</span>
        <span v-if="isHidden" class="poster-hidden-tag" aria-hidden="true">已隐藏</span>
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
            class="rounded-full border border-[var(--line)] px-2 py-0.5 text-[0.625rem] font-semibold leading-tight text-[var(--muted)]"
          >{{ badge.label }}</span>
        </span>
      </span>
    </button>

    <button
      v-if="allowHide"
      type="button"
      class="poster-hide-btn"
      :class="{ 'is-visible': isHidden }"
      :title="isHidden ? '恢复在首页显示' : '在首页隐藏'"
      :aria-label="isHidden ? `恢复显示 ${anime.title}` : `在首页隐藏 ${anime.title}`"
      @click.stop="emit('hide', anime)"
    >
      <AppNavIcon :name="isHidden ? 'eye' : 'eye-off'" class="size-3" aria-hidden="true" />
    </button>

    <div v-if="$slots.footer" class="grid content-start gap-2 px-0.5">
      <slot name="footer" />
    </div>
  </article>
</template>

<style scoped>
.anime-poster {
  border-radius: 16px;
  box-shadow: inset 0 0 0 1px rgb(255 255 255 / 0.08), 0 2px 5px rgb(0 0 0 / 0.14), 0 8px 18px -8px rgb(0 0 0 / 0.35);
  transition: transform 0.22s cubic-bezier(0.22, 1, 0.36, 1), box-shadow 0.22s ease;
}
.anime-card:hover .anime-poster {
  transform: translateY(-2px) scale(1.025);
  box-shadow: inset 0 0 0 1px rgb(255 255 255 / 0.14), 0 4px 10px rgb(0 0 0 / 0.16), 0 16px 30px -10px rgb(0 0 0 / 0.5);
}
.anime-card.is-selected .anime-poster {
  outline: 2.5px solid var(--accent-vivid);
  outline-offset: 2px;
}
.anime-card.is-hidden-card .anime-poster img,
.anime-row.is-hidden-card .row-thumb img {
  opacity: 0.55;
}
.poster-rating {
  position: absolute;
  right: 8px;
  bottom: 8px;
  padding: 1px 8px;
  border-radius: 999px;
  background: rgb(15 19 32 / 0.62);
  backdrop-filter: blur(8px);
  color: #fff;
  font-size: 11px;
  font-weight: 650;
  line-height: 18px;
}
.poster-hidden-tag {
  position: absolute;
  left: 8px;
  top: 8px;
  padding: 1px 7px;
  border-radius: 999px;
  background: rgb(15 19 32 / 0.72);
  backdrop-filter: blur(8px);
  color: rgb(255 255 255 / 0.9);
  font-size: 10.5px;
  font-weight: 600;
  line-height: 18px;
}
.poster-hide-btn {
  position: absolute;
  right: 7px;
  top: 7px;
  z-index: 2;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 22px;
  height: 22px;
  border: none;
  border-radius: 999px;
  background: rgb(15 19 32 / 0.36);
  backdrop-filter: blur(6px);
  color: rgb(255 255 255 / 0.72);
  opacity: 0;
  pointer-events: none;
  transition:
    opacity 0.16s ease,
    transform 0.22s cubic-bezier(0.22, 1, 0.36, 1),
    background 0.15s ease,
    color 0.15s ease;
}
.anime-card:hover .poster-hide-btn {
  opacity: 0.38;
  pointer-events: auto;
  transform: translateY(-2px);
}
.poster-hide-btn.is-visible {
  opacity: 0.72;
  pointer-events: auto;
}
.anime-card:hover .poster-hide-btn:hover,
.poster-hide-btn:hover,
.poster-hide-btn:focus-visible {
  opacity: 0.94;
  pointer-events: auto;
  background: rgb(15 19 32 / 0.72);
  color: #fff;
}
.poster-hide-btn:focus-visible {
  outline: 2px solid var(--accent-vivid);
  outline-offset: 1px;
}
@media (hover: none) {
  .anime-card:hover .poster-hide-btn:not(.is-visible),
  .poster-hide-btn:not(.is-visible) {
    opacity: 0;
    pointer-events: none;
  }
}

@media (hover: none), (max-width: 767px) {
  .poster-rating,
  .poster-hidden-tag,
  .poster-hide-btn {
    backdrop-filter: none;
    -webkit-backdrop-filter: none;
  }
  .poster-rating {
    background: rgb(15 19 32 / 0.8);
  }
}

.anime-row {
  display: flex;
  align-items: center;
  gap: 6px;
  min-width: 0;
  padding: 6px 8px 6px 6px;
  border-radius: 14px;
  transition: background 0.15s;
}
.anime-row:hover {
  background: var(--panel);
}
.anime-row.is-selected {
  background: var(--tint-2);
  box-shadow: inset 0 0 0 1.5px var(--accent-vivid);
}
.anime-row-main {
  display: grid;
  flex: 1;
  grid-template-columns: 48px minmax(0, 1fr) auto;
  align-items: center;
  gap: 12px;
  min-width: 0;
  border-radius: 10px;
  text-align: left;
}
.anime-row-main:focus-visible {
  outline: 2px solid var(--accent);
  outline-offset: 2px;
}
.row-hide-btn {
  display: inline-flex;
  flex-shrink: 0;
  align-items: center;
  justify-content: center;
  width: 24px;
  height: 24px;
  border-radius: 8px;
  color: var(--muted);
  opacity: 0;
  pointer-events: none;
  transition: opacity 0.15s ease, background 0.15s ease, color 0.15s ease;
}
.anime-row:hover .row-hide-btn {
  opacity: 0.42;
  pointer-events: auto;
}
.row-hide-btn.is-visible {
  opacity: 0.75;
  pointer-events: auto;
}
.anime-row:hover .row-hide-btn:hover,
.row-hide-btn:hover,
.row-hide-btn:focus-visible {
  opacity: 1;
  pointer-events: auto;
  background: var(--tint-2);
  color: var(--ink);
}
.row-thumb {
  display: grid;
  place-items: center;
  width: 48px;
  height: 64px;
  overflow: hidden;
  border-radius: 10px;
  background: var(--panel);
  color: var(--muted);
  font-size: 15px;
  font-weight: 600;
  box-shadow: 0 1px 3px rgb(0 0 0 / 0.15);
}
.row-rating {
  color: var(--accent-vivid);
  font-size: 12.5px;
  font-weight: 650;
  white-space: nowrap;
}
@media (prefers-reduced-motion: reduce) {
  .anime-poster,
  .poster-hide-btn { transition: none; }
  .anime-card:hover .anime-poster,
  .anime-card:hover .poster-hide-btn { transform: none; }
}
</style>
