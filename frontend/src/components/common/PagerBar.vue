<script setup lang="ts">
/**
 * 分页器。只有一页时整条不出——「上一页 1 下一页」三个按钮里两个是灰的，纯占地方。
 * 样式跟分段控件一套：无边框的浅底胶囊，当前页用主题色。
 */
import AppNavIcon from "../layout/AppNavIcon.vue";
import AppSelect from "./AppSelect.vue";

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
  <nav
    v-if="props.totalPages > 1"
    class="z-10 flex shrink-0 flex-wrap items-center justify-center gap-x-4 gap-y-2 px-4 py-2 text-[13px] text-[var(--muted)]"
    :class="props.plain ? '' : 'border-t border-[var(--line)] bg-[var(--table-panel)]'"
    aria-label="分页"
  >
    <div class="flex items-center gap-2">
      <span class="text-xs font-medium">{{ props.rowLabel }}</span>
      <AppSelect
        v-if="!props.readonlyCount && props.rowCountOptions.length"
        size="sm"
        direction="up"
        :model-value="props.rowCount"
        :aria-label="props.rowLabel"
        :options="props.rowCountOptions.map((size) => ({ value: size, label: String(size) }))"
        @update:model-value="emit('update:rowCount', Number($event))"
      />
      <span v-else class="pager-item pager-static">{{ props.rowCount }}</span>
    </div>

    <div class="flex flex-wrap items-center justify-center gap-1">
      <button
        type="button"
        class="pager-item"
        aria-label="上一页"
        title="上一页"
        :disabled="props.currentPage <= 1"
        @click="emit('update:page', props.currentPage - 1)"
      >
        <AppNavIcon name="chevron-left" class="size-4" aria-hidden="true" />
      </button>
      <template v-for="item in props.paginationItems" :key="`${item}-${props.currentPage}`">
        <span v-if="item === 'ellipsis'" class="px-1 text-xs">…</span>
        <button
          v-else
          type="button"
          class="pager-item tabular-nums"
          :class="{ 'is-active': props.currentPage === item }"
          :aria-current="props.currentPage === item ? 'page' : undefined"
          @click="emit('update:page', item)"
        >
          {{ item }}
        </button>
      </template>
      <button
        type="button"
        class="pager-item"
        aria-label="下一页"
        title="下一页"
        :disabled="props.currentPage >= props.totalPages"
        @click="emit('update:page', props.currentPage + 1)"
      >
        <AppNavIcon name="chevron-right" class="size-4" aria-hidden="true" />
      </button>
    </div>
  </nav>
</template>

<style scoped>
.pager-item {
  display: inline-grid;
  place-items: center;
  min-width: 32px;
  height: 32px;
  padding: 0 10px;
  border-radius: 999px;
  background: var(--panel);
  color: var(--ink-secondary);
  font-size: 13px;
  font-weight: 600;
  transition: background 0.15s, color 0.15s;
}
.pager-item:hover:not(:disabled):not(.is-active) {
  color: var(--ink);
}
.pager-item.is-active {
  background: var(--accent);
  color: var(--accent-contrast);
}
.pager-item:disabled {
  cursor: not-allowed;
  opacity: 0.4;
}
.pager-static {
  cursor: default;
}
.pager-select {
  height: 32px;
  padding: 0 26px 0 12px;
  border: 0;
  border-radius: 999px;
  background: var(--panel);
  color: var(--ink);
  font-size: 13px;
  font-weight: 600;
  outline: none;
}
.pager-select:focus-visible {
  box-shadow: 0 0 0 3px color-mix(in srgb, var(--accent-vivid) 30%, transparent);
}
</style>
