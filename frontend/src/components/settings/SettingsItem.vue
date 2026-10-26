<script setup lang="ts">
/**
 * 设置卡片里的一行：左边标题 + 说明，右边（stacked 时在下面）放控件。
 * 默认渲染成 label，点文字就能切开关、聚焦输入框；行里放按钮时用 as="div"，
 * 否则点说明文字会误触第一个按钮。
 */
withDefaults(defineProps<{ title?: string; desc?: string; stacked?: boolean; as?: "label" | "div" }>(), {
  as: "label"
});
</script>

<template>
  <component :is="as" class="settings-item" :class="{ 'settings-item-stacked': stacked }">
    <span v-if="title || desc || $slots.desc">
      <span v-if="title" class="settings-title">{{ title }}</span>
      <span v-if="desc || $slots.desc" class="settings-desc"><slot name="desc">{{ desc }}</slot></span>
    </span>
    <slot />
  </component>
</template>
