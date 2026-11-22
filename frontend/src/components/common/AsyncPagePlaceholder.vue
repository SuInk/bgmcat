<template>
  <div class="async-page-placeholder" role="status" aria-label="正在加载页面">
    <div class="async-bar async-bar-title" />
    <div class="async-card">
      <div class="async-bar async-bar-lg" />
      <div class="async-bar async-bar-md" />
      <div class="async-bar async-bar-sm" />
    </div>
    <div class="async-card">
      <div class="async-bar async-bar-lg" />
      <div class="async-bar async-bar-md" />
    </div>
  </div>
</template>

<style scoped>
.async-page-placeholder {
  display: grid;
  align-content: start;
  gap: 14px;
  padding: 18px 20px;
}

.async-card {
  display: grid;
  gap: 10px;
  padding: 16px;
  border-radius: 14px;
  border: 1px solid var(--line);
  background: var(--card);
}

.async-bar {
  height: 12px;
  border-radius: 999px;
  background: color-mix(in srgb, var(--line) 70%, transparent);
  animation: async-pulse 1.4s ease-in-out infinite;
}

.async-bar-title {
  width: 140px;
  height: 18px;
}

.async-bar-lg {
  width: 68%;
}

.async-bar-md {
  width: 48%;
}

.async-bar-sm {
  width: 32%;
}

@keyframes async-pulse {
  0%, 100% {
    opacity: 0.45;
  }
  50% {
    opacity: 0.85;
  }
}
</style>
