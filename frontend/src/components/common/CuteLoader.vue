<script setup lang="ts">
import { computed } from "vue";

const props = defineProps<{
  label?: string;
  compact?: boolean;
  mode?: "loading" | "connecting";
  size?: "normal" | "large";
}>();

const resolvedLabel = computed(() => props.label ?? (props.mode === "connecting" ? "连接中…" : "加载中…"));
</script>

<template>
  <div
    v-if="compact"
    class="loader-inline"
    role="status"
    aria-live="polite"
  >
    <span class="loader-spinner is-sm" aria-hidden="true" />
    <span class="loader-text">{{ resolvedLabel }}</span>
  </div>

  <div
    v-else
    class="loader-stack"
    role="status"
    aria-live="polite"
  >
    <span class="loader-spinner" :class="size === 'large' ? 'is-lg' : ''" aria-hidden="true" />
    <span class="loader-text">{{ resolvedLabel }}</span>
  </div>
</template>

<style scoped>
/* 对齐 Diana 控制台的克制加载动效：细环旋转 + 柔和文字，不叠加多余图标与滑动条 */
.loader-inline {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  padding: 6px 10px;
  color: var(--muted);
  font-size: 13px;
}

.loader-stack {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 12px;
  padding: 12px 16px;
  color: var(--muted);
  font-size: 13px;
}

.loader-spinner {
  width: 24px;
  height: 24px;
  flex-shrink: 0;
  border-radius: 50%;
  border: 2px solid var(--line);
  border-top-color: var(--accent);
  animation: loader-spin 0.8s linear infinite;
}

.loader-spinner.is-sm {
  width: 16px;
  height: 16px;
  border-width: 2px;
}

.loader-spinner.is-lg {
  width: 28px;
  height: 28px;
  border-width: 2.5px;
}

.loader-text {
  font-weight: 500;
  color: var(--ink-secondary);
}

@keyframes loader-spin {
  to {
    transform: rotate(360deg);
  }
}

@media (prefers-reduced-motion: reduce) {
  .loader-spinner {
    animation: none;
  }
}
</style>
