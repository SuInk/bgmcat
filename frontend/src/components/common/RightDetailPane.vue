<script setup lang="ts">
defineProps<{
  loading?: boolean;
  error?: string | null;
  emptyTitle?: string;
  emptyText?: string;
}>();
</script>

<template>
  <aside class="flex h-full min-h-0 flex-col overflow-hidden">
    <slot name="header"></slot>

    <div v-if="loading" class="grid min-h-0 flex-1 place-items-center p-6 text-center">
      <slot name="loading"></slot>
    </div>

    <div v-else-if="error" class="grid min-h-0 flex-1 place-items-center p-6 text-center">
      <div class="grid max-w-sm gap-2 rounded-lg border border-[var(--err-soft)] bg-[var(--err-soft)] px-4 py-4 text-[var(--err)]">
        <strong class="text-sm font-semibold text-[var(--err)]">详情加载失败</strong>
        <p class="text-xs font-medium leading-5 text-[var(--err)]">{{ error }}</p>
      </div>
    </div>

    <div v-else-if="$slots.default" class="min-h-0 flex-1 overflow-hidden">
      <slot></slot>
    </div>

    <div v-else class="grid flex-1 place-items-center p-6 text-center">
      <div class="grid max-w-xs gap-1.5 rounded-lg border border-dashed border-[var(--line)] bg-[var(--card)] px-5 py-6 text-[var(--muted)]">
        <strong class="text-sm font-semibold text-[var(--ink)]">{{ emptyTitle ?? "暂无内容" }}</strong>
        <span class="text-xs">{{ emptyText ?? "选择左侧项目后，这里会显示详情。" }}</span>
      </div>
    </div>

    <slot name="footer"></slot>
  </aside>
</template>
