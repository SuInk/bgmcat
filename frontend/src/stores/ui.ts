import { computed, ref, watch } from "vue";

export const SIDEBAR_DEFAULT_WIDTH = 236;
export const SIDEBAR_MIN_WIDTH = 180;
export const SIDEBAR_MAX_WIDTH = 340;

export const DOWNLOAD_SIDE_DEFAULT_WIDTH = 440;
export const DOWNLOAD_SIDE_MIN_WIDTH = 360;
export const DOWNLOAD_SIDE_MAX_WIDTH = 680;

function clamp(value: number, min: number, max: number): number {
  return Math.min(max, Math.max(min, Math.round(value)));
}

function storedNumber(key: string, fallback: number, min: number, max: number): number {
  try {
    const raw = localStorage.getItem(key);
    if (!raw) return fallback;
    const parsed = Number(raw);
    return Number.isFinite(parsed) ? clamp(parsed, min, max) : fallback;
  } catch {
    return fallback;
  }
}

export const viewportWidth = ref(window.innerWidth);
export const viewportHeight = ref(window.innerHeight);
/** 与 Diana 一致：窄屏使用抽屉，桌面允许手动隐藏整个侧栏。 */
export const isMobileViewport = computed(() => viewportWidth.value <= 960);
export const isCompactViewport = computed(() => viewportWidth.value > 960 && viewportWidth.value < 1024);
export const mobileSidebarOpen = ref(false);
function storedCollapsed(): boolean {
  try { return localStorage.getItem("bgmcat.sidebar.collapsed") === "1"; } catch { return false; }
}
export const sidebarCollapsed = ref(storedCollapsed());
watch(sidebarCollapsed, value => { try { localStorage.setItem("bgmcat.sidebar.collapsed", value ? "1" : "0"); } catch { /* optional persistence */ } });

export const sidebarWidth = ref(storedNumber("bgmcat.sidebar.width", SIDEBAR_DEFAULT_WIDTH, SIDEBAR_MIN_WIDTH, SIDEBAR_MAX_WIDTH));
export const sidebarResizing = ref(false);
watch(sidebarWidth, value => {
  const next = clamp(value, SIDEBAR_MIN_WIDTH, SIDEBAR_MAX_WIDTH);
  if (next !== value) {
    sidebarWidth.value = next;
    return;
  }
  try { localStorage.setItem("bgmcat.sidebar.width", String(next)); } catch { /* optional persistence */ }
});

export const downloadSideWidth = ref(
  storedNumber("bgmcat.downloads.sideWidth", DOWNLOAD_SIDE_DEFAULT_WIDTH, DOWNLOAD_SIDE_MIN_WIDTH, DOWNLOAD_SIDE_MAX_WIDTH)
);
watch(downloadSideWidth, value => {
  const next = clamp(value, DOWNLOAD_SIDE_MIN_WIDTH, DOWNLOAD_SIDE_MAX_WIDTH);
  if (next !== value) {
    downloadSideWidth.value = next;
    return;
  }
  try { localStorage.setItem("bgmcat.downloads.sideWidth", String(next)); } catch { /* optional persistence */ }
});

export function handleWindowResize(): void {
  viewportWidth.value = window.innerWidth;
  viewportHeight.value = window.innerHeight;
  if (window.innerWidth > 960) {
    mobileSidebarOpen.value = false;
  }
}
