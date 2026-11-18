<script setup lang="ts">
export type ToastTone = "info" | "success" | "error";

defineProps<{
  toasts: Array<{
    id: number;
    message: string;
    tone: ToastTone;
    actionLabel?: string;
    onAction?: () => void;
  }>;
}>();

const emit = defineEmits<{
  (event: "dismiss", id: number): void;
}>();

function handleAction(toast: { id: number; onAction?: () => void }): void {
  toast.onAction?.();
  emit("dismiss", toast.id);
}

function toneClass(tone: ToastTone): string {
  switch (tone) {
    case "success":
      return "border-l-[var(--ok)]";
    case "error":
      return "border-l-[var(--err)]";
    default:
      return "border-l-[var(--accent)]";
  }
}
</script>

<template>
  <TransitionGroup
    name="toast"
    tag="div"
    class="pointer-events-none fixed bottom-[max(1.5rem,calc(env(safe-area-inset-bottom)+0.75rem))] right-6 z-[210] grid w-[min(24rem,calc(100vw-2rem))] gap-3 max-sm:bottom-[max(5.5rem,calc(env(safe-area-inset-bottom)+4.5rem))] max-sm:right-4"
  >
    <div
      v-for="toast in toasts"
      :key="toast.id"
      class="pointer-events-auto flex items-start gap-3 rounded-[14px] border border-[var(--line)] border-l-[3.5px] bg-[var(--card)] px-4 py-3 text-[var(--ink)] shadow-[var(--strong-shadow)]"
      :class="toneClass(toast.tone)"
    >
      <div class="min-w-0 flex-1">
        <p class="break-words text-sm font-medium leading-6">{{ toast.message }}</p>
      </div>
      <button
        v-if="toast.actionLabel && toast.onAction"
        type="button"
        class="shrink-0 rounded-lg bg-[var(--accent-soft)] px-2.5 py-1 text-xs font-semibold text-[var(--accent-vivid)] transition hover:opacity-90"
        @click="handleAction(toast)"
      >
        {{ toast.actionLabel }}
      </button>
      <button
        type="button"
        class="rounded-full px-1.5 py-0.5 text-xs opacity-60 transition hover:bg-[var(--panel)] hover:opacity-100"
        aria-label="关闭提示"
        @click="emit('dismiss', toast.id)"
      >
        ✕
      </button>
    </div>
  </TransitionGroup>
</template>

<style scoped>









.toast-enter-active,
.toast-leave-active {
  transition:
    transform 180ms ease,
    opacity 180ms ease;
}

.toast-enter-from,
.toast-leave-to {
  opacity: 0;
  transform: translateY(-14px) scale(0.96);
}
</style>
