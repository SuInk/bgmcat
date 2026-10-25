import { t } from "../i18n";
import { updateFeedback } from "../stores/toasts";

export async function copyText(value: string | undefined): Promise<void> {
  if (!value) {
    return;
  }
  try {
    await navigator.clipboard.writeText(value);
    updateFeedback(t("common.copied"), "success");
  } catch {
    updateFeedback("复制失败：浏览器未授予剪贴板权限。", "error");
  }
}

export function openExternalLink(value: string): void {
  try {
    const url = new URL(value);
    if (["https:", "http:"].includes(url.protocol)) {
      window.open(url.toString(), "_blank", "noopener,noreferrer");
    }
  } catch {
    // 非法链接直接忽略
  }
}
