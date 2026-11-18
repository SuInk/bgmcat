// 判断响应正文是否为反向代理/网关返回的 HTML 错误页（如 Nginx 502 / Cloudflare 52x）
export function gatewayErrorPage(body: string): string {
  const head = body.slice(0, 2_000).toLowerCase();
  if (!head.startsWith("<!doctype html") && !head.startsWith("<html") && !head.includes("<html")) {
    return "";
  }
  const title = /<title[^>]*>([^<]{1,120})<\/title>/i.exec(body.slice(0, 4_000));
  return title ? title[1].trim() : "HTML 错误页";
}

export function serverErrorBase(status: number): string {
  return `后端出错（HTTP ${status}）`;
}

export function describeServerFailure(status: number, responseBody: string): string {
  const base = serverErrorBase(status);
  const page = gatewayErrorPage(responseBody.trim());
  if (page) {
    return `${base}：后端的错误说明被反向代理或网关换成了它自己的错误页（${page}），原始原因请到「运行日志」查看。`;
  }
  if (!responseBody.trim()) {
    return `${base}：没有收到错误说明，可能是代理中断了请求或后端在处理期间退出，原始原因请到「运行日志」查看。`;
  }
  return base;
}
