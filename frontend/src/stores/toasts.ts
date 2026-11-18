import { ref } from "vue";

import type { ToastTone } from "../components/common/AppToastStack.vue";
import { t } from "../i18n";

const TOAST_LIMIT = 4;
const TOAST_DURATION_MS = 3400;
const TOAST_ACTION_DURATION_MS = 5200;

export interface ToastItem {
  id: number;
  message: string;
  tone: ToastTone;
  actionLabel?: string;
  onAction?: () => void;
}

export const toasts = ref<ToastItem[]>([]);
export const feedback = ref(t("dashboard.ready"));

let nextToastId = 1;

export function dismissToast(id: number): void {
  toasts.value = toasts.value.filter((toast) => toast.id !== id);
}

export function showToast(
  message: string,
  tone: ToastTone = "info",
  action?: { label: string; onClick: () => void }
): void {
  if (!message.trim() || message === t("dashboard.ready")) {
    return;
  }
  // 同文案 + 同色调去重：轮询/批量操作触发的重复错误只显示一条
  if (!action && toasts.value.some((toast) => toast.message === message && toast.tone === tone)) {
    return;
  }

  const id = nextToastId;
  nextToastId += 1;
  toasts.value = [
    ...toasts.value,
    {
      id,
      message,
      tone,
      ...(action ? { actionLabel: action.label, onAction: action.onClick } : {})
    }
  ].slice(-TOAST_LIMIT);
  window.setTimeout(() => dismissToast(id), action ? TOAST_ACTION_DURATION_MS : TOAST_DURATION_MS);
}

export function updateFeedback(
  message: string,
  tone: ToastTone = "info",
  action?: { label: string; onClick: () => void }
): void {
  feedback.value = message;
  showToast(message, tone, action);
}

export function toastError(error: unknown, fallbackMessage: string): void {
  updateFeedback(error instanceof Error && error.message ? error.message : fallbackMessage, "error");
}
