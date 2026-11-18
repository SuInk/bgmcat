import { computed, ref } from "vue";

import { DEFAULT_THEME_ACCENT } from "@shared/index";
import { readStorageItem } from "../lib/storageKeys";

export type ThemeMode = "system" | "light" | "dark";

const THEME_MODE_KEY = "bgmcat.theme.mode";
const LEGACY_THEME_MODE_KEY = "anime-rss-theme-mode";
/** 主题色的真身在设置里（跟着账号走）；这里只缓存一份，用来在设置到达前先上色，免得闪一下默认蓝。 */
const THEME_ACCENT_KEY = "bgmcat.theme.accent";
const LEGACY_THEME_ACCENT_KEY = "anime-rss-theme-accent";

const HEX_RE = /^#[0-9a-fA-F]{6}$/u;

function normalizeAccent(value: unknown): string | null {
  const text = typeof value === "string" ? value.trim().toLowerCase() : "";
  return HEX_RE.test(text) ? text : null;
}

export const themeAccent = ref<string>(
  normalizeAccent(readStorageItem(THEME_ACCENT_KEY, LEGACY_THEME_ACCENT_KEY)) ?? DEFAULT_THEME_ACCENT
);

export const themeMode = ref<ThemeMode>(
  (readStorageItem(THEME_MODE_KEY, LEGACY_THEME_MODE_KEY) as ThemeMode | null) ?? "system"
);

export const activeThemeLabel = computed(() => {
  if (themeMode.value === "system") {
    return "自动";
  }
  return themeMode.value === "dark" ? "深色" : "浅色";
});

let systemThemeQuery: MediaQueryList | null = null;

function resolveTheme(mode: ThemeMode): "light" | "dark" {
  if (mode !== "system") {
    return mode;
  }
  // 旧版这里恒返回 light，导致「跟随系统」永远是浅色
  return systemThemeQuery?.matches ? "dark" : "light";
}

export function applyTheme(): void {
  document.documentElement.dataset.theme = resolveTheme(themeMode.value);
  document.documentElement.dataset.themeMode = themeMode.value;
  document.documentElement.style.setProperty("--accent-seed", themeAccent.value);
}

/** 实时预览：只写根元素变量，不落库。设置页保存后由 syncThemeAccent 收口。 */
export function previewThemeAccent(value: string): void {
  const next = normalizeAccent(value);
  if (!next || next === themeAccent.value) {
    return;
  }
  themeAccent.value = next;
  applyTheme();
}

/** 设置加载/保存后调用：认账并写进本地缓存，供下次冷启动直接上色。 */
export function syncThemeAccent(value: unknown): void {
  const next = normalizeAccent(value) ?? DEFAULT_THEME_ACCENT;
  themeAccent.value = next;
  window.localStorage.setItem(THEME_ACCENT_KEY, next);
  applyTheme();
}

export function setThemeMode(mode: ThemeMode): void {
  themeMode.value = mode;
  window.localStorage.setItem(THEME_MODE_KEY, mode);
  applyTheme();
}

export function initTheme(): void {
  systemThemeQuery = window.matchMedia("(prefers-color-scheme: dark)");
  systemThemeQuery.addEventListener("change", applyTheme);
  applyTheme();
}

export function teardownTheme(): void {
  systemThemeQuery?.removeEventListener("change", applyTheme);
  systemThemeQuery = null;
}
