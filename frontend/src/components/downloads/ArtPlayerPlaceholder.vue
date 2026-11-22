<template>
  <div class="player-placeholder" role="status" aria-label="正在加载播放器">
    <span class="player-spinner" aria-hidden="true" />
    <span class="player-label">正在加载播放器…</span>
  </div>
</template>

<style scoped>
.player-placeholder {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 10px;
  width: 100%;
  aspect-ratio: 16 / 9;
  border-radius: 12px;
  background: #0b0d12;
  border: 1px solid rgba(255, 255, 255, 0.06);
  color: rgba(255, 255, 255, 0.65);
}

.player-spinner {
  width: 26px;
  height: 26px;
  border-radius: 999px;
  border: 2.5px solid rgba(255, 255, 255, 0.14);
  border-top-color: var(--accent-vivid, #60a5fa);
  animation: player-spin 0.8s linear infinite;
}

.player-label {
  font-size: 12.5px;
  font-weight: 500;
  letter-spacing: 0.01em;
}

@keyframes player-spin {
  to {
    transform: rotate(360deg);
  }
}
</style>
