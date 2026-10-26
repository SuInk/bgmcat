<script setup lang="ts" generic="T extends string | number | boolean">
/**
 * 二选一 / 三选一的分段按钮。block=true 时撑满父容器、各段等宽（表单里用），
 * 默认按内容收宽（设置行右侧用）。
 */
defineProps<{
  options: ReadonlyArray<{ value: T; label: string }>;
  ariaLabel: string;
  block?: boolean;
}>();

const model = defineModel<T>({ required: true });
</script>

<template>
  <div class="segmented" :class="{ 'segmented-block': block }" role="radiogroup" :aria-label="ariaLabel">
    <button
      v-for="option in options"
      :key="String(option.value)"
      type="button"
      role="radio"
      class="segmented-item"
      :class="{ 'segmented-item-active': model === option.value }"
      :aria-checked="model === option.value"
      @click="model = option.value"
    >
      {{ option.label }}
    </button>
  </div>
</template>
