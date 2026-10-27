<script setup lang="ts">
const props = withDefaults(
  defineProps<{
    currentPage: number;
    totalPages: number;
    paginationItems: Array<number | "ellipsis">;
    rowCount?: number;
    rowCountOptions?: number[];
    rowLabel?: string;
    readonlyCount?: boolean;
    /** 不要顶部分隔线和底色：跟在内容后面，而不是钉在面板底部 */
    plain?: boolean;
  }>(),
  {
    rowCount: undefined,
    rowCountOptions: () => [],
    rowLabel: "每页行数",
    readonlyCount: false,
    plain: false
  }
);

const emit = defineEmits<{
  (event: "update:page", value: number): void;
  (event: "update:rowCount", value: number): void;
}>();
</script>

<template>
  <div
    class="z-10 shrink-0 flex flex-col items-center justify-center gap-3 px-4 py-2 text-[13px] text-[var(--muted)] sm:flex-row sm:flex-wrap sm:gap-4"
    :class="props.plain ? '' : 'border-t border-[var(--line)] bg-[var(--table-panel)]'"
  >
    <div class="flex w-full items-center justify-center gap-2 sm:w-auto">
      <span class="text-xs font-medium text-[var(--muted)]">{{ props.rowLabel }}</span>
      <select
        v-if="!props.readonlyCount && props.rowCountOptions.length"
        :value="props.rowCount"
        class="min-w-16 rounded-lg border border-[var(--line)] bg-[var(--input-bg)] px-2.5 py-1 text-[13px] font-semibold text-[var(--ink)] outline-none transition focus:border-[var(--accent)]"
        @change="emit('update:rowCount', Number(($event.target as HTMLSelectElement).value))"
      >
        <option v-for="size in props.rowCountOptions" :key="size" :value="size">{{ size }}</option>
      </select>
      <div
        v-else
        class="min-w-12 rounded-lg border border-[var(--line)] bg-[var(--card)] px-2.5 py-1 text-center text-[13px] font-semibold text-[var(--ink)]"
      >
        {{ props.rowCount }}
      </div>
    </div>

    <div class="flex w-full flex-wrap items-center justify-center gap-2 sm:w-auto">
      <button
        class="rounded-lg border border-[var(--line)] bg-[var(--card)] px-3 py-1 text-[13px] font-semibold text-[var(--ink)] transition hover:border-[var(--accent)] disabled:cursor-not-allowed disabled:opacity-40"
        :disabled="props.currentPage <= 1"
        @click="emit('update:page', props.currentPage - 1)"
      >
        上一页
      </button>

      <div class="flex flex-wrap items-center justify-center gap-1.5">
        <template v-for="item in props.paginationItems" :key="`${item}-${props.currentPage}`">
          <span v-if="item === 'ellipsis'" class="px-1 text-xs text-[var(--muted)]">...</span>
          <button
            v-else
            class="min-w-8 rounded-lg border px-2.5 py-1 text-[13px] font-semibold transition"
            :class="
              props.currentPage === item
                ? 'border-[var(--accent)] bg-[var(--accent)] text-[var(--accent-contrast)] shadow-[var(--soft-shadow)]'
                : 'border-[var(--line)] bg-[var(--card)] text-[var(--ink)] hover:border-[var(--accent)]'
            "
            @click="emit('update:page', item)"
          >
            {{ item }}
          </button>
        </template>
      </div>

      <button
        class="rounded-lg border border-[var(--line)] bg-[var(--card)] px-3 py-1 text-[13px] font-semibold text-[var(--ink)] transition hover:border-[var(--accent)] disabled:cursor-not-allowed disabled:opacity-40"
        :disabled="props.currentPage >= props.totalPages"
        @click="emit('update:page', props.currentPage + 1)"
      >
        下一页
      </button>
    </div>
  </div>
</template>
