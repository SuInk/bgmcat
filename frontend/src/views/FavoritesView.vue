<script setup lang="ts">
/**
 * 我的收藏：和首页新番同一套卡片网格，顶上按「追更状态」筛。
 * 以前每张卡底下叠「订阅 RSS / 查看订阅」「取消收藏」两个整宽按钮，卡片又被压成 9.5rem，
 * 两部收藏就是两根细长条漂在一大片空白里。现在卡片只留状态标签和一个必要操作，
 * 取消收藏在番剧详情的「已收藏」按钮上。
 */
import { computed, ref } from "vue";

import type { AnimeSubscriptionOptions, FavoriteAnime, HomepageAnime } from "@shared/index";

import AnimeCard from "../components/discovery/AnimeCard.vue";
import AnimeDetailDrawer from "../components/discovery/AnimeDetailDrawer.vue";
import AnimeSubscribeDialog from "../components/discovery/AnimeSubscribeDialog.vue";
import AppNavIcon from "../components/layout/AppNavIcon.vue";
import { isBusy } from "../stores/busy";
import { openAnimeDetail, selectedAnimeId } from "../stores/discovery";
import {
  cancelSubscriptionForAnime,
  createSubscriptionFromAnime,
  favorites
} from "../stores/favorites";
import { selectTab } from "../stores/boot";
import { isMobileViewport } from "../stores/ui";

type FavoriteState = "auto" | "collect" | "none";
type FavoriteFilter = "all" | FavoriteState;

/**
 * 订阅状态三态。二态（已订阅/未订阅）会把 autoDownload=false 的「只收集不下载」
 * 也显示成绿色，用户会以为在自动追番。多源时只要有一个源开了自动下载就算在追更。
 */
function favoriteState(favorite: FavoriteAnime): FavoriteState {
  if (!favorite.subscriptionId) {
    return "none";
  }
  const autoDownload = favorite.subscriptions?.length
    ? favorite.subscriptions.some((subscription) => subscription.autoDownload)
    : favorite.subscriptionAutoDownload;
  return autoDownload ? "auto" : "collect";
}

/** 一部番挂了几个源。多源时标签要说清楚，否则「追更中」看不出跨了几站。 */
function sourceNames(favorite: FavoriteAnime): string[] {
  return (favorite.subscriptions ?? [])
    .map((subscription) => subscription.providerName ?? subscription.name)
    .filter(Boolean) as string[];
}

function stateBadge(favorite: FavoriteAnime): { label: string; className: string; hint: string } {
  const state = favoriteState(favorite);
  const names = sourceNames(favorite);
  const lockedSubgroup =
    favorite.subscriptionRule?.lockedSubgroup?.trim() ||
    favorite.subscriptionRule?.preferredSubgroups?.[0]?.trim() ||
    "";
  const suffix = lockedSubgroup
    ? ` · ${lockedSubgroup}`
    : names.length > 1
      ? ` · ${names.length} 源`
      : "";
  const hintSuffix = names.length ? `。来源：${names.join("、")}` : "";
  if (state === "auto") {
    return { label: `追更中${suffix}`, className: "status-pill-ok", hint: `新集数会自动加入下载${hintSuffix}` };
  }
  if (state === "collect") {
    return {
      label: `仅收集${suffix}`,
      className: "status-pill-warn",
      hint: `已订阅，但没开自动下载：新集数只进订阅列表，不会自动下载${hintSuffix}`
    };
  }
  return { label: "未订阅", className: "status-pill-muted", hint: "还没有为这部番建 RSS 订阅" };
}

const subscribeTarget = ref<FavoriteAnime | null>(null);

async function handleConfirmFavoriteSubscribe(payload: {
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
    subscribeTarget.value = null;
  }
}

async function handleCancelFavoriteSubscribe(): Promise<void> {
  const target = subscribeTarget.value;
  if (!target) {
    return;
  }
  const ok = await cancelSubscriptionForAnime(target.animeId);
  if (ok) {
    subscribeTarget.value = null;
  }
}

const FILTERS: Array<{ key: FavoriteFilter; label: string }> = [
  { key: "all", label: "全部" },
  { key: "auto", label: "追更中" },
  { key: "collect", label: "仅收集" },
  { key: "none", label: "未订阅" }
];

const activeFilter = ref<FavoriteFilter>("all");
const query = ref("");

const filterCounts = computed<Record<FavoriteFilter, number>>(() => {
  const counts: Record<FavoriteFilter, number> = { all: favorites.value.length, auto: 0, collect: 0, none: 0 };
  for (const favorite of favorites.value) {
    counts[favoriteState(favorite)] += 1;
  }
  return counts;
});

const visibleFavorites = computed(() => {
  const keyword = query.value.trim().toLowerCase();
  return favorites.value.filter((favorite) => {
    if (activeFilter.value !== "all" && favoriteState(favorite) !== activeFilter.value) {
      return false;
    }
    return !keyword || favorite.title.toLowerCase().includes(keyword);
  });
});
</script>

<template>
  <AnimeDetailDrawer>
    <template #main>
      <div class="page-shell">
        <div v-if="favorites.length" class="page-toolbar">
          <div class="toolbar-row">
            <div class="toolbar-chips" role="group" aria-label="按订阅状态筛选">
              <button
                v-for="filter in FILTERS"
                :key="filter.key"
                type="button"
                class="chip"
                :class="{ 'chip-active': activeFilter === filter.key }"
                :aria-pressed="activeFilter === filter.key"
                @click="activeFilter = filter.key"
              >
                {{ filter.label }}
                <span class="chip-count">{{ filterCounts[filter.key] }}</span>
              </button>
            </div>
            <span class="toolbar-meta">共 {{ favorites.length }} 部</span>
          </div>
          <div class="toolbar-row">
            <label class="search-field min-w-0">
              <AppNavIcon name="search" aria-hidden="true" />
              <input v-model="query" type="search" placeholder="在收藏里搜索" aria-label="在收藏里搜索" />
            </label>
            <button type="button" class="btn-ghost shrink-0 px-3 py-1.5 text-sm font-semibold" @click="selectTab('subscriptions')">
              管理订阅
            </button>
          </div>
        </div>

        <div class="page-body" :class="{ 'row-span-2': !favorites.length }">
          <div v-if="!favorites.length" class="empty-state h-full">
            <div class="grid max-w-sm gap-3">
              <span class="empty-icon"><AppNavIcon name="favorites" class="size-6" aria-hidden="true" /></span>
              <p class="text-sm font-semibold text-[var(--ink)]">还没有收藏的番剧</p>
              <p class="text-sm text-[var(--muted)]">在「番剧发现」点开一部番剧，点「收藏」就会出现在这里。</p>
              <button type="button" class="btn-primary justify-self-center px-4 py-2 text-sm font-semibold" @click="selectTab('home')">
                去发现番剧
              </button>
            </div>
          </div>

          <p v-else-if="!visibleFavorites.length" class="empty-state text-sm text-[var(--muted)]">
            没有符合条件的收藏
          </p>

          <!-- 与首页 AnimeGrid 同一个最小列宽，两页卡片大小一致 -->
          <div v-else class="grid grid-cols-[repeat(auto-fill,minmax(10rem,1fr))] content-start items-start gap-3">
            <AnimeCard
              v-for="favorite in visibleFavorites"
              :key="favorite.animeId"
              :anime="favorite"
              :selected="selectedAnimeId === favorite.animeId"
              :compact-meta="isMobileViewport"
              @open="openAnimeDetail(favorite)"
            >
              <template #footer>
                <div class="flex min-w-0 items-center justify-between gap-2">
                  <button
                    v-if="favorite.subscriptionId"
                    type="button"
                    class="status-pill min-w-0 truncate transition hover:opacity-85"
                    :class="stateBadge(favorite).className"
                    :title="`${stateBadge(favorite).hint}（点击调整字幕组）`"
                    @click.stop="subscribeTarget = favorite"
                  >
                    {{ stateBadge(favorite).label }}
                  </button>
                  <span v-else class="status-pill min-w-0 truncate" :class="stateBadge(favorite).className" :title="stateBadge(favorite).hint">
                    {{ stateBadge(favorite).label }}
                  </span>
                  <button
                    v-if="!favorite.subscriptionId"
                    type="button"
                    class="btn-primary shrink-0 px-2.5 py-1 text-xs font-semibold"
                    :disabled="isBusy(`favorite-subscribe:${favorite.animeId}`)"
                    :title="`选择字幕组并订阅「${favorite.title}」`"
                    @click.stop="subscribeTarget = favorite"
                  >
                    订阅
                  </button>
                </div>
              </template>
            </AnimeCard>
          </div>
        </div>

        <AnimeSubscribeDialog
          v-if="subscribeTarget"
          :anime="subscribeTarget"
          :mikan-keyword="subscribeTarget.mikanKeyword"
          :submitting="isBusy(`favorite-subscribe:${subscribeTarget.animeId}`) || isBusy(`favorite-unsubscribe:${subscribeTarget.animeId}`)"
          @close="subscribeTarget = null"
          @confirm="handleConfirmFavoriteSubscribe"
          @unsubscribe="handleCancelFavoriteSubscribe"
        />
      </div>
    </template>
  </AnimeDetailDrawer>
</template>
