<script setup lang="ts">
withDefaults(
  defineProps<{
    size?: number;
  }>(),
  {
    size: 32
  }
);
</script>

<template>
  <img
    src="/icons/logo-96.png"
    alt=""
    class="brand-logo"
    :width="size"
    :height="size"
    aria-hidden="true"
  />
</template>

<style scoped>
.brand-logo {
  display: inline-block;
  flex-shrink: 0;
  border-radius: 32%;
  object-fit: cover;
  box-shadow: 0 2px 8px rgba(0, 65, 253, 0.18);
}
</style>
