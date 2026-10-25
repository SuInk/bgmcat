<script setup lang="ts">
/**
 * 番剧详情的自包含封装。
 * 首页 / 收藏 / 下载三个页面共用，数据与操作全部来自 stores，
 * 视图只需要往 #main 槽里放自己的主内容。
 *
 * 详情一律「接管」整个内容区，不再并排一条常驻详情栏：
 * 那条栏在没选中番剧时是空的，却常年占掉三分之一横向空间，
 * 逼得列表挤成窄卡片、资源表的发布名压成两行小字。
 * 列表就是列表，点开才有详情，关闭即返回。
 */
import { computed } from "vue";

import type { HomepageAnime, MikanDownloadItem } from "@shared/index";

import { copyText, openExternalLink } from "../../lib/actions";
import { isBusy, isBusyMatching } from "../../stores/busy";
import {
  animeDetail,
  animeDetailError,
  closeAnimeDetail,
  detailDownloadPageSize,
  detailProviderOptions,
  detailResolutionOptions,
  detailSubgroupOptions,
  downloadPage,
  downloadPaginationItems,
  downloadTotalPages,
  filteredMikanDownloads,
  findQueuedMikanDownload,
  isMikanDownloadQueued,
  pagedMikanDownloadGroups,
  pagedMikanDownloads,
  queueMikanDownload,
  selectedProvider,
  selectedResolution,
  selectedSubgroup,
  setDownloadPage
} from "../../stores/discovery";
import { openPlayback } from "../../stores/downloads";
import {
  createFavoriteFromAnime,
  createSubscriptionFromAnime,
  favoriteAnimeMap,
  removeFavoriteAnime
} from "../../stores/favorites";
import { subscriptions } from "../../stores/subscriptions";
import AppNavIcon from "../layout/AppNavIcon.vue";
import AnimeDetailPane from "./AnimeDetailPane.vue";

const detailLoading = computed(() =>
  isBusyMatching((key) => key.startsWith("anime-detail:") || key.startsWith("download-detail:"))
);

/** 加载中也要立刻接管，否则点下去到数据回来之间像是没反应。 */
const detailOpen = computed(() =>
  Boolean(animeDetail.value || animeDetailError.value || detailLoading.value)
);

const currentAnimeFavorite = computed(() =>
  animeDetail.value ? favoriteAnimeMap.value.get(animeDetail.value.anime.animeId) ?? null : null
);
const currentAnimeSubscriptionId = computed(() => {
  const favorite = currentAnimeFavorite.value;
  if (favorite?.subscriptionId) {
    return favorite.subscriptionId;
  }
  const rssUrl = favorite?.rssUrl;
  if (!rssUrl) {
    return null;
  }
  return subscriptions.value.find((item) => item.url === rssUrl)?.id ?? null;
});

function isMikanItemBusy(item: MikanDownloadItem): boolean {
  return isBusy(`mikan-download:${item.id}`);
}

function handleFavorite(payload: { anime: HomepageAnime; mikanKeyword?: string }): void {
  void createFavoriteFromAnime(payload.anime, payload.mikanKeyword);
}

function handleSubscribe(payload: {
  anime: HomepageAnime;
  mikanKeyword?: string;
  providerIds?: string[];
}): void {
  void createSubscriptionFromAnime(payload.anime, payload.mikanKeyword, payload.providerIds);
}
</script>

<template>
  <section
    class="grid h-full min-h-0 overflow-hidden rounded-lg border border-[var(--line)] bg-[var(--table-bg)] shadow-[var(--soft-shadow)]"
  >
    <div v-show="!detailOpen" class="h-full min-h-0 overflow-hidden">
      <slot name="main"></slot>
    </div>

    <div
      v-if="detailOpen"
      class="grid h-full min-h-0 overflow-hidden"
      :class="animeDetail ? 'grid-rows-[minmax(0,1fr)]' : 'grid-rows-[auto_minmax(0,1fr)]'"
    >
      <!--
        数据回来之前（加载 / 错误）内层没有头部，这条栏负责一键回列表。
        数据回来后返回按钮就在番剧头部里，这条栏再留着就是两层标题叠在一起白占高度。
      -->
      <div v-if="!animeDetail" class="flex items-center gap-2 border-b border-[var(--line)] bg-[var(--card)] px-3 py-2">
        <button type="button" class="btn-ghost flex items-center gap-1.5 px-3 py-1.5 text-sm font-semibold" @click="closeAnimeDetail">
          <AppNavIcon name="chevron-left" class="size-4" />
          返回列表
        </button>
        <span class="min-w-0 truncate text-sm font-semibold text-[var(--ink)]">
          番剧详情
        </span>
      </div>
      <AnimeDetailPane
        :anime-detail="animeDetail"
        :detail-error="animeDetailError"
        :is-favorite="Boolean(currentAnimeFavorite)"
        :subscription-id="currentAnimeSubscriptionId"
        :filtered-downloads="filteredMikanDownloads"
        :paged-downloads="pagedMikanDownloads"
        :paged-download-groups="pagedMikanDownloadGroups"
        :selected-subgroup="selectedSubgroup"
        :selected-resolution="selectedResolution"
        :selected-provider="selectedProvider"
        :detail-subgroup-options="detailSubgroupOptions"
        :detail-resolution-options="detailResolutionOptions"
        :detail-provider-options="detailProviderOptions"
        :busy-key="null"
        :detail-loading="detailLoading"
        :is-download-busy="isMikanItemBusy"
        :is-download-queued="isMikanDownloadQueued"
        :get-queued-download="findQueuedMikanDownload"
        :detail-download-page-size="detailDownloadPageSize"
        :download-page="downloadPage"
        :download-total-pages="downloadTotalPages"
        :download-pagination-items="downloadPaginationItems"
        @close="closeAnimeDetail"
        @favorite="handleFavorite"
        @remove-favorite="removeFavoriteAnime($event)"
        @subscribe="handleSubscribe"
        @select-subgroup="selectedSubgroup = $event"
        @select-resolution="selectedResolution = $event"
        @select-provider="selectedProvider = $event"
        @update:download-page="setDownloadPage"
        @queue-download="queueMikanDownload"
        @play-download="openPlayback"
        @copy-text="copyText"
        @open-link="openExternalLink"
      />
    </div>
  </section>
</template>
