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
/* 对齐 Diana 骨架屏：平滑扫光 shimmer，替代生硬的整体闪烁 */
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
  border-radius: 6px;
  background: linear-gradient(
    90deg,
    var(--panel) 25%,
    color-mix(in srgb, var(--line) 90%, transparent) 50%,
    var(--panel) 75%
  );
  background-size: 200% 100%;
  animation: skeleton-shimmer 1.4s infinite;
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

@keyframes skeleton-shimmer {
  0% {
    background-position: 200% 0;
  }
  100% {
    background-position: -200% 0;
  }
}

@media (prefers-reduced-motion: reduce) {
  .async-bar {
    animation: none;
  }
}
</style>
