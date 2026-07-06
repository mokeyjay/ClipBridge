// 通用格式化与主题辅助。
import type { Lang } from "./i18n";

// formatBytes 以二进制单位人性化展示字节数。
export function formatBytes(n: number): string {
  if (!n || n <= 0) return "0 B";
  const units = ["B", "KB", "MB", "GB", "TB"];
  let i = 0;
  let v = n;
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i++;
  }
  return `${v >= 100 || i === 0 ? Math.round(v) : v.toFixed(1)} ${units[i]}`;
}

// mibToBytes / bytesToMiB 在 MiB 数值与字节间互转。
export function mibToBytes(mib: number): number {
  return Math.round(mib * 1024 * 1024);
}
export function bytesToMiB(bytes: number): number {
  if (!bytes || bytes <= 0) return 0;
  return Math.round((bytes / (1024 * 1024)) * 100) / 100;
}

// relTime 把 RFC3339 时间渲染为相对时间（按语言）。
export function relTime(at: string, lang: Lang): string {
  const d = new Date(at).getTime();
  if (isNaN(d)) return "";
  const s = Math.max(0, Math.floor((Date.now() - d) / 1000));
  if (lang === "en") {
    if (s < 10) return "just now";
    if (s < 60) return `${s}s ago`;
    if (s < 3600) return `${Math.floor(s / 60)} min ago`;
    if (s < 86400) return `${Math.floor(s / 3600)} h ago`;
    return `${Math.floor(s / 86400)} d ago`;
  }
  if (s < 10) return "刚刚";
  if (s < 60) return `${s} 秒前`;
  if (s < 3600) return `${Math.floor(s / 60)} 分钟前`;
  if (s < 86400) return `${Math.floor(s / 3600)} 小时前`;
  return `${Math.floor(s / 86400)} 天前`;
}

// serverHost 从 URL 中提取 host:port。
export function serverHost(url: string): string {
  return url.replace(/^https?:\/\//, "").replace(/\/$/, "") || "—";
}

// absTime 把 RFC3339 时间渲染为本地绝对时间字符串（用于 hover 提示），无效时返回空串。
export function absTime(at: string): string {
  const d = new Date(at);
  if (isNaN(d.getTime())) return "";
  return d.toLocaleString();
}

// fpHeadTail 把长指纹缩略为「前 6 … 后 6」。
export function fpHeadTail(fp: string): string {
  if (!fp) return "—";
  const parts = fp.split(":");
  if (parts.length <= 12) return fp;
  return parts.slice(0, 6).join(":") + "…" + parts.slice(-6).join(":");
}

// contentTypeKey 把后端内容类型映射为 i18n key（用于「文本/图片/文件/富文本」）。
export function contentTypeKey(t: string): "type_text" | "type_image" | "type_file" | "type_rich" {
  switch (t) {
    case "image":
      return "type_image";
    case "file":
      return "type_file";
    case "rich_text":
      return "type_rich";
    default:
      return "type_text";
  }
}

export type Theme = "light" | "dark" | "system";

// applyTheme 把主题写入 <html>（HeroUI 用 .dark class，同时写 data-theme）。
export function applyTheme(theme: Theme): void {
  const root = document.documentElement;
  const dark =
    theme === "dark" ||
    (theme === "system" && window.matchMedia("(prefers-color-scheme: dark)").matches);
  root.classList.toggle("dark", dark);
  root.dataset.theme = dark ? "dark" : "light";
}
