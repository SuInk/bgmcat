<script setup lang="ts">
import { computed } from "vue";

import { formatBytes } from "../../lib/format";

export interface StorageDonutSegment {
  key: string;
  label: string;
  bytes: number;
  color: string;
}

const props = defineProps<{
  segments: StorageDonutSegment[];
  total: number;
  centerValue: string;
  centerLabel: string;
}>();

const circumference = 2 * Math.PI * 62;
const arcGap = 3;

const arcs = computed(() => {
  const total = props.total > 0 ? props.total : props.segments.reduce((sum, segment) => sum + segment.bytes, 0);
  if (total <= 0) {
    return [];
  }
  const drawn = props.segments.filter((segment) => segment.bytes > 0);
  let offset = 0;
  return drawn.map((segment) => {
    const ratio = Math.min(1, segment.bytes / total);
    const length = ratio * circumference;
    const arc = {
      ...segment,
      length: drawn.length > 1 && length > arcGap * 2 ? length - arcGap : length,
      offset,
      percentLabel: `${(ratio * 100).toFixed(ratio < 0.1 ? 1 : 0)}%`
    };
    offset += length;
    return arc;
  });
});

const ariaLabel = computed(() => {
  const parts = arcs.value.map((arc) => `${arc.label} ${formatBytes(arc.bytes)}`);
  return `存储占用饼图：${parts.join("，")}`;
});
</script>

<template>
  <div class="storage-donut">
    <svg class="storage-donut-ring" viewBox="0 0 160 160" role="img" :aria-label="ariaLabel">
      <circle class="storage-donut-track" cx="80" cy="80" r="62" fill="none" stroke-width="16" />
      <circle
        v-for="arc in arcs"
        :key="arc.key"
        class="storage-donut-arc"
        cx="80"
        cy="80"
        r="62"
        fill="none"
        stroke-width="16"
        stroke-linecap="butt"
        :stroke="arc.color"
        :stroke-dasharray="`${arc.length} ${circumference - arc.length}`"
        :stroke-dashoffset="-arc.offset"
      >
        <title>{{ arc.label }} {{ formatBytes(arc.bytes) }}（{{ arc.percentLabel }}）</title>
      </circle>
    </svg>
    <div class="storage-donut-center">
      <strong>{{ centerValue }}</strong>
      <span class="text-[var(--muted)]">{{ centerLabel }}</span>
    </div>
  </div>
</template>

<style scoped>
.storage-donut {
  position: relative;
  width: 160px;
  height: 160px;
  flex: none;
}

.storage-donut-ring {
  width: 100%;
  height: 100%;
  transform: rotate(-90deg);
}

.storage-donut-track {
  stroke: var(--panel);
}

.storage-donut-arc {
  transition: stroke-dasharray 0.3s ease;
}

.storage-donut-center {
  position: absolute;
  inset: 0;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 2px;
  padding: 0 18px;
  text-align: center;
  pointer-events: none;
}

.storage-donut-center strong {
  color: var(--ink);
  font-size: 18px;
  font-weight: 700;
  line-height: 1.15;
  font-variant-numeric: tabular-nums;
}

.storage-donut-center span {
  font-size: 11.5px;
  line-height: 1.2;
}
</style>
