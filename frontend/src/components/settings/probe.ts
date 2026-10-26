/** 资源站 / 信息平台「测试」按钮的结果，两边的展示规则一样。 */
export interface ProbeResult {
  ok: boolean;
  count: number;
  error: string | null;
}

export function probeStatusLabel(result: ProbeResult | undefined, idleLabel = "未测试"): string {
  if (!result) {
    return idleLabel;
  }
  return result.ok ? `可达 · ${result.count} 条` : `不可达：${result.error || "未知错误"}`;
}

export function probeStatusClass(result: ProbeResult | undefined): string {
  if (!result) {
    return "provider-status-idle";
  }
  return result.ok ? "provider-status-ok" : "provider-status-error";
}
