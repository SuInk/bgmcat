import { computed, reactive, ref } from "vue";

import type { FeedItem, Subscription } from "@shared/index";

import {
  createDownload,
  createSubscription,
  deleteSubscription,
  listFeedItems,
  listSubscriptions,
  refreshSubscription,
  updateSubscription
} from "../api";
import { t } from "../i18n";
import { withBusy } from "./busy";
import { refreshDownloads } from "./downloads";
import { refreshFavorites } from "./favorites";
import { toastError, updateFeedback } from "./toasts";

export const subscriptions = ref<Subscription[]>([]);
export const feedItems = ref<FeedItem[]>([]);
export const editingSubscriptionId = ref<number | null>(null);

export const feedItemById = computed(() => new Map(feedItems.value.map((item) => [item.id, item])));
export const failedSubscriptionCount = computed(
  () => subscriptions.value.filter((item) => item.lastError).length
);

export const subscriptionForm = reactive({
  name: "",
  url: "",
  enabled: true,
  downloadDirOverride: "",
  includeKeywords: "",
  excludeKeywords: "",
  preferredResolutions: "",
  preferredSubgroups: "",
  autoDownload: true,
  skipBatch: true
});

function toCsv(value: string[]): string {
  return value.join(", ");
}

function parseCsv(value: string): string[] {
  return value
    .split(",")
    .map((item) => item.trim())
    .filter(Boolean);
}

export function resetSubscriptionForm(): void {
  editingSubscriptionId.value = null;
  Object.assign(subscriptionForm, {
    name: "",
    url: "",
    enabled: true,
    downloadDirOverride: "",
    includeKeywords: "",
    excludeKeywords: "",
    preferredResolutions: "",
    preferredSubgroups: "",
    autoDownload: true,
    skipBatch: true
  });
}

export function fillSubscriptionForm(subscription: Subscription): void {
  editingSubscriptionId.value = subscription.id;
  Object.assign(subscriptionForm, {
    name: subscription.name,
    url: subscription.url,
    enabled: subscription.enabled,
    downloadDirOverride: subscription.downloadDirOverride ?? "",
    includeKeywords: toCsv(subscription.rule.includeKeywords),
    excludeKeywords: toCsv(subscription.rule.excludeKeywords),
    preferredResolutions: toCsv(subscription.rule.preferredResolutions),
    preferredSubgroups: toCsv(subscription.rule.preferredSubgroups ?? []),
    autoDownload: subscription.rule.autoDownload,
    skipBatch: subscription.rule.skipBatch
  });
}

export async function refreshSubscriptionList(): Promise<void> {
  subscriptions.value = await listSubscriptions();
}

export async function refreshFeedItems(): Promise<void> {
  feedItems.value = await listFeedItems();
}

/** 创建或更新订阅；新建后立即触发一次抓取（后端创建接口本身不抓取）。 */
export async function submitSubscription(): Promise<boolean> {
  return withBusy("subscription-form", async () => {
    const prevRule = editingSubscriptionId.value
      ? subscriptions.value.find((s) => s.id === editingSubscriptionId.value)?.rule
      : undefined;
    const parsedSubgroups = parseCsv(subscriptionForm.preferredSubgroups);
    const payload = {
      name: subscriptionForm.name.trim(),
      url: subscriptionForm.url.trim(),
      enabled: subscriptionForm.enabled,
      downloadDirOverride: subscriptionForm.downloadDirOverride.trim() || null,
      rule: {
        ...(prevRule
          ? {
              lockedSubgroup:
                prevRule.lockedSubgroup && parsedSubgroups.includes(prevRule.lockedSubgroup)
                  ? prevRule.lockedSubgroup
                  : null,
              preferredLanguage: prevRule.preferredLanguage,
              onlyNew: prevRule.onlyNew,
              subscribedAt: prevRule.subscribedAt,
              baselineEpisode: prevRule.baselineEpisode
            }
          : {}),
        includeKeywords: parseCsv(subscriptionForm.includeKeywords),
        excludeKeywords: parseCsv(subscriptionForm.excludeKeywords),
        preferredResolutions: parseCsv(subscriptionForm.preferredResolutions),
        preferredSubgroups: parsedSubgroups,
        autoDownload: subscriptionForm.autoDownload,
        skipBatch: subscriptionForm.skipBatch
      }
    };

    if (!payload.name || !payload.url) {
      updateFeedback("请填写订阅名称和 RSS URL。", "error");
      return false;
    }

    try {
      if (editingSubscriptionId.value) {
        await updateSubscription(editingSubscriptionId.value, payload);
        updateFeedback(t("subscriptions.updated"), "success");
      } else {
        const created = await createSubscription(payload);
        updateFeedback(t("subscriptions.created"), "success");
        void refreshOneSubscription(created.id, { silent: true });
      }
      resetSubscriptionForm();
      await refreshSubscriptionList();
      return true;
    } catch (error) {
      toastError(error, t("subscriptions.requestFailed"));
      return false;
    }
  });
}

export async function refreshOneSubscription(id: number, options: { silent?: boolean } = {}): Promise<void> {
  await withBusy(`subscription-refresh:${id}`, async () => {
    try {
      const result = await refreshSubscription(id);
      if (!options.silent) {
        updateFeedback(
          t("subscriptions.refreshOneDone", {
            createdCount: result.createdCount,
            queuedCount: result.queuedCount
          }),
          "success"
        );
      }
      await Promise.allSettled([refreshSubscriptionList(), refreshFeedItems(), refreshDownloads()]);
    } catch (error) {
      toastError(error, t("subscriptions.refreshFailed"));
      await refreshSubscriptionList().catch(() => undefined);
    }
  });
}

export async function refreshAllSubscriptions(): Promise<void> {
  await withBusy("subscription-refresh-all", async () => {
    const enabled = subscriptions.value.filter((subscription) => subscription.enabled);
    if (!enabled.length) {
      updateFeedback("当前没有启用中的订阅。", "info");
      return;
    }

    let createdCount = 0;
    let queuedCount = 0;
    let failedCount = 0;
    for (const subscription of enabled) {
      try {
        const result = await refreshSubscription(subscription.id);
        createdCount += result.createdCount;
        queuedCount += result.queuedCount;
      } catch {
        failedCount += 1;
      }
    }

    await Promise.allSettled([refreshSubscriptionList(), refreshFeedItems(), refreshDownloads()]);
    if (failedCount) {
      updateFeedback(`刷新完成，抓取 ${createdCount} 条，入队 ${queuedCount} 条，${failedCount} 个订阅失败。`, "error");
    } else {
      updateFeedback(t("subscriptions.refreshAllDone", { createdCount, queuedCount }), "success");
    }
  });
}

export async function removeSubscription(id: number): Promise<void> {
  await withBusy(`subscription-delete:${id}`, async () => {
    try {
      await deleteSubscription(id);
      updateFeedback(t("subscriptions.deleted"), "success");
      if (editingSubscriptionId.value === id) {
        resetSubscriptionForm();
      }
      // 收藏卡片上有「已订阅」状态徽章，删除订阅后要一并刷掉
      await Promise.allSettled([refreshSubscriptionList(), refreshFeedItems(), refreshFavorites()]);
    } catch (error) {
      toastError(error, t("subscriptions.deleteFailed"));
    }
  });
}

export async function queueFeedItem(feedItemId: number): Promise<void> {
  await withBusy(`feed:${feedItemId}`, async () => {
    try {
      await createDownload({ feedItemId });
      updateFeedback(t("downloads.feedQueued"), "success");
      await Promise.allSettled([refreshFeedItems(), refreshDownloads()]);
    } catch (error) {
      toastError(error, t("downloads.feedQueueFailed"));
    }
  });
}
