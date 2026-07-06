import { toast } from "@heroui/react";

// 统一 toast 封装：成功用默认样式，错误用 danger（与 server/web 保持一致）。
export function toastOK(title: string, description?: string) {
  toast(title, { description });
}
export function toastErr(title: string, description?: string) {
  toast(title, { description, variant: "danger" });
}

// humanError 把后端原始错误转为更友好的中文/原文提示。Wails 调用 reject 的是
// 结构化对象（{message, cause, kind}），需先取出 message，避免把整段 JSON 弹出来。
export function humanError(e: unknown): string {
  let msg: string;
  if (e instanceof Error) msg = e.message;
  else if (e && typeof e === "object" && "message" in e) msg = String((e as { message: unknown }).message);
  else msg = String(e);
  // Wails 的 RuntimeError 把 Go 错误序列化成 JSON 串塞进 message
  // （{"message":"…","cause":{},"kind":"RuntimeError"}）——把内层 message 解出来。
  const trimmed = msg.trim();
  if (trimmed.startsWith("{") && trimmed.includes('"message"')) {
    try {
      const inner = JSON.parse(trimmed) as { message?: unknown };
      if (typeof inner.message === "string") msg = inner.message;
    } catch {
      /* 非 JSON，原样使用 */
    }
  }
  if (/fingerprint/i.test(msg)) return "证书指纹不匹配，连接已被阻断。";
  if (/拒绝|rejected/i.test(msg)) return "配对请求被拒绝。";
  if (/过期|expired/i.test(msg)) return "配对码或请求已过期，请重新生成。";
  if (/connection refused|dial|connect/i.test(msg)) return "无法连接到服务器，请检查地址与网络。";
  return msg.replace(/^[a-z/]+:\s*/, "") || "操作失败";
}
