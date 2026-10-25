import { computed, ref } from "vue";

export const viewportWidth = ref(window.innerWidth);
export const viewportHeight = ref(window.innerHeight);
/** 真手机宽度才用覆盖式抽屉侧栏；桌面窄窗口一律用不遮内容的图标窄栏 */
export const isMobileViewport = computed(() => viewportWidth.value < 480);
/** 桌面窄窗口（480–1024）：侧栏自动收成图标窄栏，仍可手动展开 */
export const isCompactViewport = computed(
  () => viewportWidth.value >= 480 && viewportWidth.value < 1024
);
export const mobileSidebarOpen = ref(false);
/** 参照项目的侧栏不做手动折叠：桌面常展开,窄窗口(480–1024)自动收成图标栏 */
export const sidebarCollapsed = isCompactViewport;

export function handleWindowResize(): void {
  viewportWidth.value = window.innerWidth;
  viewportHeight.value = window.innerHeight;
  if (window.innerWidth >= 480) {
    mobileSidebarOpen.value = false;
  }
}
