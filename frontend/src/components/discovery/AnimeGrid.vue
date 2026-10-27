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
        : 'grid-cols-[repeat(auto-fill,minmax(10rem,1fr))] gap-x-4 gap-y-5'
    "
  >
    <!--
      海报尺寸对齐 B 站番剧页（实测 177×235、3:4、间距 20px）：10rem 起，1280 宽的窗口一行 5 张、
      1440 宽 6 张。紧凑列表 17rem 一栏，同样宽度排三栏。跟着首页整页滚动，不再自带滚动区。
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
