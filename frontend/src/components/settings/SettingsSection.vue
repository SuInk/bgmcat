<script setup lang="ts">
/**
 * 设置页的一个小节：小标题 + 内容（通常是一张 .settings-group 卡片）。
 * data-section 给 SettingsView 的搜索过滤用；只有一个小节的分类页会把小标题藏掉。
 */
defineProps<{ id: string; title: string; desc?: string }>();
</script>

<template>
  <section :id="`settings-section-${id}`" class="settings-section" :data-section="id">
    <header class="settings-section-head">
      <h3 class="settings-section-title">{{ title }}</h3>
      <p v-if="desc" class="settings-section-desc">{{ desc }}</p>
    </header>
    <slot />
  </section>
</template>
