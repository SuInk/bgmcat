import { computed, ref, watch } from "vue";

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

export function handleWindowResize(): void {
  viewportWidth.value = window.innerWidth;
  viewportHeight.value = window.innerHeight;
  if (window.innerWidth > 960) {
    mobileSidebarOpen.value = false;
  }
}
