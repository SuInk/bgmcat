<script setup lang="ts">
import { computed, ref } from "vue";

import type { HomepageAnime } from "@shared/index";

import CuteLoader from "../common/CuteLoader.vue";
import AnimeCard from "./AnimeCard.vue";

const props = defineProps<{
  items: HomepageAnime[];
  selectedAnimeId?: string | null;
  loading?: boolean;
  errorMessage?: string;
  /** poster：海报网格；row：紧凑列表 */
  layout?: "poster" | "row";
}>();

const emit = defineEmits<{
  (event: "select", anime: HomepageAnime): void;
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
    <template v-if="showLoader">
      <div
        class="col-span-full grid min-h-[18rem] place-items-center p-6"
      >
        <CuteLoader mode="loading" />
      </div>
    </template>

    <template v-else-if="showError">
      <div
        class="col-span-full grid min-h-[18rem] place-items-center p-6 text-center"
      >
        <div class="grid max-w-md gap-3">
          <div class="text-3xl font-semibold text-[var(--muted)]" aria-hidden="true">!</div>
          <p class="text-sm font-medium text-[var(--muted)]">{{ errorMessage }}</p>
        </div>
      </div>
    </template>

    <template v-else>
      <AnimeCard
        v-for="anime in items"
        :key="anime.animeId"
        :anime="anime"
        :selected="selectedAnimeId === anime.animeId"
        :compact-meta="true"
        :layout="layout"
        @open="emit('select', anime)"
      />
    </template>
  </div>
</template>
