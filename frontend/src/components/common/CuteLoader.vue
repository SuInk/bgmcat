<script setup lang="ts">
import { computed } from "vue";

const props = defineProps<{
  label?: string;
  compact?: boolean;
  mode?: "loading" | "connecting";
  size?: "normal" | "large";
}>();

const resolvedLabel = computed(() => props.label ?? (props.mode === "connecting" ? "连接中" : "加载中"));

const containerClass = computed(() => {
  if (props.compact) {
    return "px-4 py-3 text-sm";
  }
  return props.size === "normal" ? "px-4 py-3 text-sm" : "px-6 py-4 text-base";
});

const ringClass = computed(() => {
  if (props.compact) {
    return "size-5";
  }
  return props.size === "normal" ? "size-6" : "size-8";
});
</script>

<template>
  <div class="inline-flex items-center gap-2.5 text-[var(--muted)]" :class="containerClass" role="status">
    <span class="loader-ring" :class="ringClass"></span>
    <span class="font-medium">{{ resolvedLabel }}</span>
  </div>
</template>

<style scoped>
.loader-ring {
  animation: loader-ring-spin 800ms linear infinite;
  border: 2px solid var(--line);
  border-top-color: var(--accent);
  border-radius: 999px;
  display: inline-block;
  flex: 0 0 auto;
}

@keyframes loader-ring-spin {
  to {
    transform: rotate(360deg);
  }
}
</style>
