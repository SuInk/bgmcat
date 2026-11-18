<script setup lang="ts">
import { computed, ref } from "vue";

import type { HomepageAnime } from "@shared/index";

import AnimeCard from "./AnimeCard.vue";

const props = defineProps<{
  items: HomepageAnime[];
  selectedAnimeId?: string | null;
  loading?: boolean;
  errorMessage?: string;
  emptyMessage?: string;
  catalogMeta?: boolean;
  /** poster：海报网格；row：紧凑列表 */
  layout?: "poster" | "row";
  allowHide?: boolean;
  hiddenIds?: ReadonlySet<string>;
}>();

const emit = defineEmits<{
  (event: "select", anime: HomepageAnime): void;
  (event: "hide", anime: HomepageAnime): void;
}>();

const rootEl = ref<HTMLElement | null>(null);

const showLoader = computed(() => Boolean(props.loading && props.items.length === 0));
const showError = computed(() => Boolean(!props.loading && props.errorMessage));

defineExpose({ rootEl });
</script>

<template>
  <div
    ref="rootEl"
    class="grid auto-rows-max content-start items-start"
    :class="
      layout === 'row'
        ? 'grid-cols-[repeat(auto-fill,minmax(17rem,1fr))] gap-x-3 gap-y-1'
        : 'grid-cols-[repeat(auto-fill,minmax(6.25rem,1fr))] gap-x-2.5 gap-y-3.5 sm:grid-cols-[repeat(auto-fill,minmax(8.5rem,1fr))] sm:gap-x-3.5 sm:gap-y-[18px]'
    "
  >
    <!--
      海报比 B 站番剧页（实测 177×235）小一档：8.5rem 起，1280 宽的窗口一行 6 张（约 150px），
      收起侧栏 7 张。窄于 640px（手机、窄窗口）降到 6.25rem，再窄也至少两列，不会一张海报占满一屏。紧凑列表 17rem 一栏，同样宽度排三栏。跟着首页整页滚动，不再自带滚动区。
    -->
    <!--
      骨架屏（参考 Apple TV / YouTube）：先摆出和真卡片同形的灰块，数据回来直接替换，
      不会从一个居中的转圈突然跳成满屏海报。
    -->
    <template v-if="showLoader">
      <div v-for="index in 12" :key="index" class="grid gap-2" aria-hidden="true">
        <template v-if="layout === 'row'">
          <div class="flex items-center gap-3 p-1.5">
            <span class="skeleton h-16 w-12 shrink-0 rounded-[10px]"></span>
            <span class="grid flex-1 gap-1.5">
              <span class="skeleton h-3 w-4/5 rounded-full"></span>
              <span class="skeleton h-2.5 w-2/5 rounded-full"></span>
            </span>
          </div>
        </template>
        <template v-else>
          <span class="skeleton aspect-[3/4] rounded-[16px]"></span>
          <span class="skeleton h-3 w-4/5 rounded-full"></span>
          <span class="skeleton h-2.5 w-1/2 rounded-full"></span>
        </template>
      </div>
      <span class="sr-only" role="status">正在加载当季番剧…</span>
    </template>

    <template v-else-if="showError">
      <div
        class="col-span-full grid min-h-[18rem] place-items-center p-6 text-center"
      >
        <div class="grid max-w-md gap-3">
          <div class="empty-icon text-lg font-bold" aria-hidden="true">!</div>
          <p class="text-sm font-medium text-[var(--muted)]">{{ errorMessage }}</p>
        </div>
      </div>
    </template>

    <template v-else-if="items.length === 0">
      <div class="col-span-full grid min-h-[14rem] place-items-center p-6 text-center">
        <slot name="empty">
          <p class="text-sm font-medium text-[var(--muted)]">{{ emptyMessage || "没有符合筛选条件的番剧" }}</p>
        </slot>
      </div>
    </template>

    <template v-else>
      <AnimeCard
        v-for="anime in items"
        :key="anime.animeId"
        :anime="anime"
        :selected="selectedAnimeId === anime.animeId"
        :compact-meta="true"
        :catalog-meta="catalogMeta"
        :layout="layout"
        :allow-hide="allowHide"
        :is-hidden="Boolean(hiddenIds?.has(anime.animeId))"
        @open="emit('select', anime)"
        @hide="emit('hide', $event)"
      />
    </template>
  </div>
</template>
