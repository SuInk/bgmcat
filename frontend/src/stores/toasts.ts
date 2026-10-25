import { ref } from "vue";

import type { ToastTone } from "../components/common/AppToastStack.vue";
import { t } from "../i18n";

const TOAST_LIMIT = 4;
const TOAST_DURATION_MS = 3400;

export const toasts = ref<Array<{ id: number; message: string; tone: ToastTone }>>([]);
export const feedback = ref(t("dashboard.ready"));

let nextToastId = 1;

export function dismissToast(id: number): void {
  toasts.value = toasts.value.filter((toast) => toast.id !== id);
}

export function showToast(message: string, tone: ToastTone = "info"): void {
  if (!message.trim() || message === t("dashboard.ready")) {
    return;
  }
  // 同文案 + 同色调去重：轮询/批量操作触发的重复错误只显示一条
  if (toasts.value.some((toast) => toast.message === message && toast.tone === tone)) {
    return;
  }

  const id = nextToastId;
  nextToastId += 1;
  toasts.value = [...toasts.value, { id, message, tone }].slice(-TOAST_LIMIT);
  window.setTimeout(() => dismissToast(id), TOAST_DURATION_MS);
}

export function updateFeedback(message: string, tone: ToastTone = "info"): void {
  feedback.value = message;
  showToast(message, tone);
}

export function toastError(error: unknown, fallbackMessage: string): void {
  updateFeedback(error instanceof Error && error.message ? error.message : fallbackMessage, "error");
}
