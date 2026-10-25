import { computed, ref } from "vue";

import type { FavoriteAnime, HomepageAnime } from "@shared/index";

import { createFavoriteAnime, createFavoriteSubscription, deleteFavoriteAnime, listFavoriteAnime } from "../api";
import { t } from "../i18n";
import { withBusy } from "./busy";
import { refreshSubscriptionList } from "./subscriptions";
import { toastError, updateFeedback } from "./toasts";

export const favorites = ref<FavoriteAnime[]>([]);

export const favoriteAnimeMap = computed(() => new Map(favorites.value.map((item) => [item.animeId, item])));

export async function refreshFavorites(): Promise<void> {
  favorites.value = await listFavoriteAnime();
}

async function refreshFavoriteData(): Promise<void> {
  await Promise.allSettled([refreshFavorites(), refreshSubscriptionList()]);
}

export async function createFavoriteFromAnime(anime: HomepageAnime, mikanKeyword?: string): Promise<void> {
  await withBusy(`favorite-add:${anime.animeId}`, async () => {
    try {
      await createFavoriteAnime({
        anime,
        mikanKeyword: mikanKeyword ?? anime.searchKeyword ?? anime.title
      });
      await refreshFavoriteData();
      updateFeedback(t("favorites.added"), "success");
    } catch (error) {
      toastError(error, t("favorites.addFailed"));
    }
  });
}

export async function removeFavoriteAnime(animeId: string): Promise<void> {
  await withBusy(`favorite-remove:${animeId}`, async () => {
    try {
      await deleteFavoriteAnime(animeId);
      favorites.value = favorites.value.filter((item) => item.animeId !== animeId);
      updateFeedback(t("favorites.removed"), "success");
    } catch (error) {
      toastError(error, t("favorites.removeFailed"));
    }
  });
}

/**
 * 收藏并同时创建 RSS 订阅（后端幂等）。
 * providerIds 省略时后端用设置里启用的资源来源，一个源一条订阅。
 */
export async function createSubscriptionFromAnime(
  anime: HomepageAnime,
  mikanKeyword?: string,
  providerIds?: string[]
): Promise<void> {
  await withBusy(`favorite-subscribe:${anime.animeId}`, async () => {
    try {
      await createFavoriteSubscription({
        anime,
        mikanKeyword: mikanKeyword ?? anime.searchKeyword ?? anime.title,
        ...(providerIds?.length ? { providerIds } : {})
      });
      await refreshFavoriteData();
      updateFeedback(t("subscriptions.createdFromAnime"), "success");
    } catch (error) {
      toastError(error, t("subscriptions.createFromAnimeFailed"));
    }
  });
}
