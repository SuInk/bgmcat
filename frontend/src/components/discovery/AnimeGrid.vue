<script setup lang="ts">
import { computed, ref } from "vue";

import type { HomepageAnime } from "@shared/index";

import CuteLoader from "../common/CuteLoader.vue";
import { isMobileViewport } from "../../stores/ui";
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
    class="grid h-full auto-rows-max grid-cols-[repeat(auto-fill,minmax(11.5rem,1fr))] content-start items-start gap-4 overflow-auto px-4 pb-4 pt-1"
  >
    <!--
      9.5rem 是常驻详情栏还在时定的:那会儿卡片区只有 400 多像素,只能靠压小卡片换列数。
      详情栏去掉后内容区宽了一倍,卡片可以回到能看清标题的尺寸(1440px 下约 6 列)。
    -->
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
        :compact-meta="isMobileViewport"
        @open="emit('select', anime)"
      />
    </template>
  </div>
</template>
