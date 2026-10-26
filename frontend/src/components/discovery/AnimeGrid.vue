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
    class="grid h-full auto-rows-max grid-cols-[repeat(auto-fill,minmax(8rem,1fr))] content-start items-start gap-3 overflow-auto px-4 pb-4 pt-1"
  >
    <!-- 竖版海报卡 8rem 起：1070px 窗口一行 5 张，1440px 下 8 列；卡片窄，蜜柑编号一律省略 -->
    <template v-if="showLoader">
      <div
        class="col-span-full grid min-h-[18rem] place-items-center rounded-lg border border-dashed border-[var(--line)] bg-[var(--card)] p-6"
      >
        <CuteLoader mode="loading" />
      </div>
    </template>

    <template v-else-if="showError">
      <div
        class="col-span-full grid min-h-[18rem] place-items-center rounded-lg border border-dashed border-[var(--line)] bg-[var(--card)] p-6 text-center"
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
        @open="emit('select', anime)"
      />
    </template>
  </div>
</template>
