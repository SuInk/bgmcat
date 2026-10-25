import { computed, ref } from "vue";

/**
 * 并发安全的忙碌状态跟踪：每个操作一个 key，互不影响。
 * 替代旧版全局单一 busyKey（并发操作会互相覆盖对方的按钮状态）。
 */
const busyKeys = ref<Set<string>>(new Set());

export const busyCount = computed(() => busyKeys.value.size);

export function isBusy(key: string): boolean {
  return busyKeys.value.has(key);
}

export function isBusyMatching(predicate: (key: string) => boolean): boolean {
  for (const key of busyKeys.value) {
    if (predicate(key)) {
      return true;
    }
  }
  return false;
}

function setBusy(key: string, value: boolean): void {
  const next = new Set(busyKeys.value);
  if (value) {
    next.add(key);
  } else {
    next.delete(key);
  }
  busyKeys.value = next;
}

export async function withBusy<T>(key: string, task: () => Promise<T>): Promise<T> {
  setBusy(key, true);
  try {
    return await task();
  } finally {
    setBusy(key, false);
  }
}
