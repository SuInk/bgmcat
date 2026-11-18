import { computed, ref } from "vue";

import type { HiddenAnime, HomepageAnime } from "@shared/index";

import {
  clearHiddenAnime,
  hideHomepageAnime,
  listHiddenAnime,
  unhideHomepageAnime
} from "../api";
import { toastError, updateFeedback } from "./toasts";

const HIDDEN_ANIME_CACHE_KEY = "bgmcat.discovery.hidden-cache-v1";

function readHiddenAnimeCache(): HiddenAnime[] {
  try {
    const raw = window.localStorage.getItem(HIDDEN_ANIME_CACHE_KEY);
    const parsed = raw ? JSON.parse(raw) : [];
    return Array.isArray(parsed) ? (parsed as HiddenAnime[]) : [];
  } catch {
    return [];
  }
}

function persistHiddenAnimeCache(items: HiddenAnime[]): void {
  try {
    window.localStorage.setItem(HIDDEN_ANIME_CACHE_KEY, JSON.stringify(items));
  } catch {
    // 忽略本地配额限制
  }
}

export const hiddenAnimeList = ref<HiddenAnime[]>(readHiddenAnimeCache());

export const hiddenAnimeIds = computed(
  () => new Set(hiddenAnimeList.value.map((item) => item.animeId))
);

export function isAnimeHidden(animeId: string | null | undefined): boolean {
  return Boolean(animeId && hiddenAnimeIds.value.has(animeId));
}

export function hydrateHiddenAnime(items: HiddenAnime[]): void {
  hiddenAnimeList.value = items;
  persistHiddenAnimeCache(items);
}

export async function refreshHiddenAnime(): Promise<void> {
  const items = await listHiddenAnime();
  hiddenAnimeList.value = items;
  persistHiddenAnimeCache(items);
}

export async function hideAnime(anime: HomepageAnime): Promise<void> {
  if (!anime.animeId) {
    return;
  }
  const previous = [...hiddenAnimeList.value];
  const optimistic: HiddenAnime = {
    ...anime,
    hiddenAt: new Date().toISOString()
  };
  hiddenAnimeList.value = [
    optimistic,
    ...hiddenAnimeList.value.filter((item) => item.animeId !== anime.animeId)
  ];
  persistHiddenAnimeCache(hiddenAnimeList.value);

  try {
    const saved = await hideHomepageAnime(anime);
    hiddenAnimeList.value = [
      saved,
      ...hiddenAnimeList.value.filter((item) => item.animeId !== anime.animeId)
    ];
    persistHiddenAnimeCache(hiddenAnimeList.value);
    updateFeedback(`已隐藏《${anime.title}》`, "info", {
      label: "撤销",
      onClick: () => {
        void unhideAnime(anime.animeId);
      }
    });
  } catch (error) {
    hiddenAnimeList.value = previous;
    persistHiddenAnimeCache(previous);
    toastError(error, "隐藏番剧失败");
  }
}

export async function unhideAnime(animeId: string): Promise<void> {
  if (!animeId) {
    return;
  }
  const previous = [...hiddenAnimeList.value];
  const target = previous.find((item) => item.animeId === animeId);
  hiddenAnimeList.value = previous.filter((item) => item.animeId !== animeId);
  persistHiddenAnimeCache(hiddenAnimeList.value);

  try {
    await unhideHomepageAnime(animeId);
    updateFeedback(target ? `已恢复《${target.title}》` : "已恢复显示", "success");
  } catch (error) {
    hiddenAnimeList.value = previous;
    persistHiddenAnimeCache(previous);
    toastError(error, "恢复显示失败");
  }
}

export async function unhideAllAnime(): Promise<void> {
  if (!hiddenAnimeList.value.length) {
    return;
  }
  const previous = [...hiddenAnimeList.value];
  hiddenAnimeList.value = [];
  persistHiddenAnimeCache([]);

  try {
    await clearHiddenAnime();
    updateFeedback("已恢复全部隐藏番剧", "success");
  } catch (error) {
    hiddenAnimeList.value = previous;
    persistHiddenAnimeCache(previous);
    toastError(error, "恢复隐藏番剧失败");
  }
}
