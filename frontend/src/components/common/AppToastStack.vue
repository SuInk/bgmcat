<script setup lang="ts">
export type ToastTone = "info" | "success" | "error";

defineProps<{
  toasts: Array<{
    id: number;
    message: string;
    tone: ToastTone;
  }>;
}>();

const emit = defineEmits<{
  (event: "dismiss", id: number): void;
}>();

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
    class="pointer-events-none fixed bottom-[max(1.5rem,calc(env(safe-area-inset-bottom)+0.75rem))] right-6 z-50 grid w-[min(24rem,calc(100vw-2rem))] gap-3"
  >
    <div
      v-for="toast in toasts"
      :key="toast.id"
      class="pointer-events-auto flex items-start gap-3 rounded-md border border-[var(--line)] border-l-[3px] bg-[var(--card)] px-3.5 py-2.5 text-[var(--ink)] shadow-[var(--strong-shadow)]"
      :class="toneClass(toast.tone)"
    >
      <div class="min-w-0 flex-1">
        <p class="break-words text-sm font-medium leading-6">{{ toast.message }}</p>
      </div>
      <button
        type="button"
        class="rounded px-1.5 py-0.5 text-xs opacity-60 transition hover:opacity-100"
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
