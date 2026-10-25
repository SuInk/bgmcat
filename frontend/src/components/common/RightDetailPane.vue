<script setup lang="ts">
defineProps<{
  loading?: boolean;
  error?: string | null;
  emptyTitle?: string;
  emptyText?: string;
}>();
</script>

<template>
  <aside class="grid h-full min-h-0 overflow-hidden grid-rows-[auto_minmax(0,1fr)_auto] border-t border-[var(--line)] bg-[var(--card)] lg:border-t-0">
    <slot name="header"></slot>

    <div v-if="loading" class="grid min-h-0 place-items-center p-8 text-center">
      <slot name="loading"></slot>
    </div>

    <div v-else-if="error" class="grid min-h-0 place-items-center p-8 text-center">
      <div class="grid max-w-sm gap-3 rounded-lg border border-[var(--err-soft)] bg-[var(--err-soft)] px-6 py-6 text-[var(--err)]">
        <strong class="text-base font-semibold text-[var(--err)]">详情加载失败</strong>
        <p class="text-sm font-semibold leading-6 text-[var(--err)]">{{ error }}</p>
      </div>
    </div>

    <div v-else-if="$slots.default" class="min-h-0 overflow-auto">
      <slot></slot>
    </div>

    <div v-else class="grid place-items-center p-8 text-center">
      <div class="grid max-w-xs gap-2 rounded-lg border border-dashed border-[var(--line)] bg-[var(--card)] px-6 py-8 text-[var(--muted)]">
        <strong class="text-base font-semibold text-[var(--ink)]">{{ emptyTitle ?? "暂无内容" }}</strong>
        <span class="text-sm">{{ emptyText ?? "选择左侧项目后，这里会显示详情。" }}</span>
      </div>
    </div>

    <slot name="footer"></slot>
  </aside>
</template>
