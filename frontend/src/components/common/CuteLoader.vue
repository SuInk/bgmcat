<script setup lang="ts">
import { computed } from "vue";
import BrandLogo from "./BrandLogo.vue";

const props = defineProps<{
  label?: string;
  compact?: boolean;
  mode?: "loading" | "connecting";
  size?: "normal" | "large";
}>();

const resolvedLabel = computed(() => props.label ?? (props.mode === "connecting" ? "连接中" : "加载中"));

const logoSize = computed(() => {
  if (props.compact) {
    return 18;
  }
  return props.size === "normal" ? 26 : 36;
});
</script>

<template>
  <div
    v-if="compact"
    class="inline-flex items-center gap-2.5 px-4 py-2.5 text-sm text-[var(--muted)]"
    role="status"
  >
    <svg class="loader-svg size-5" viewBox="0 0 24 24" fill="none" aria-hidden="true">
      <circle class="loader-track" cx="12" cy="12" r="9.5" stroke-width="2.5" />
      <circle class="loader-arc" cx="12" cy="12" r="9.5" stroke-width="2.5" stroke-linecap="round" />
    </svg>
    <span class="font-medium">{{ resolvedLabel }}</span>
  </div>

  <div
    v-else
    class="loader-card"
    role="status"
  >
    <div class="loader-badge">
      <BrandLogo :size="logoSize" />
      <svg class="loader-orbit" viewBox="0 0 64 64" fill="none" aria-hidden="true">
        <circle class="loader-track" cx="32" cy="32" r="29" stroke-width="2.75" />
        <circle class="loader-arc" cx="32" cy="32" r="29" stroke-width="2.75" stroke-linecap="round" />
      </svg>
    </div>
    <span class="text-sm font-semibold text-[var(--ink-secondary)]">{{ resolvedLabel }}</span>
    <div class="loader-progress-track" aria-hidden="true">
      <span class="loader-progress-bar"></span>
    </div>
  </div>
</template>

<style scoped>
.loader-card {
  display: inline-grid;
  justify-items: center;
  gap: 10px;
  padding: 12px 20px;
}

.loader-badge {
  position: relative;
  display: grid;
  place-items: center;
  width: 60px;
  height: 60px;
  border-radius: 999px;
  background: var(--accent-soft);
}

.loader-orbit {
  position: absolute;
  inset: 0;
  width: 100%;
  height: 100%;
  animation: loader-rotate 1.1s linear infinite;
}

.loader-svg {
  flex-shrink: 0;
  animation: loader-rotate 0.9s linear infinite;
}

.loader-track {
  stroke: color-mix(in srgb, var(--line) 85%, transparent);
}

.loader-arc {
  stroke: var(--accent-vivid);
  stroke-dasharray: 48 140;
  stroke-dashoffset: 0;
}

.loader-progress-track {
  position: relative;
  width: 9.5rem;
  height: 5px;
  overflow: hidden;
  border-radius: 999px;
  background: color-mix(in srgb, var(--accent) 14%, var(--panel));
}

.loader-progress-bar {
  position: absolute;
  inset: 0 auto 0 0;
  width: 42%;
  border-radius: 999px;
  background: linear-gradient(90deg, var(--accent), var(--accent-vivid));
  animation: loader-slide 1.25s cubic-bezier(0.4, 0, 0.2, 1) infinite;
}

@keyframes loader-rotate {
  to {
    transform: rotate(360deg);
  }
}

@keyframes loader-slide {
  0% {
    transform: translateX(-110%);
  }
  100% {
    transform: translateX(250%);
  }
}
</style>
