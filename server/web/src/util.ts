// 通用格式化与主题辅助函数。

// formatBytes 以二进制单位人性化展示字节数。
export function formatBytes(n: number): string {
  if (n <= 0) return "0 B";
  const units = ["B", "KiB", "MiB", "GiB", "TiB"];
  const i = Math.min(units.length - 1, Math.floor(Math.log(n) / Math.log(1024)));
  const v = n / Math.pow(1024, i);
  return `${i === 0 ? v : v.toFixed(2)} ${units[i]}`;
}

// parseBytesMiB 把 MiB 数值转为字节。
export function mibToBytes(mib: number): number {
  return Math.round(mib * 1024 * 1024);
}

// bytesToMiB 把字节转为 MiB（保留两位）。
export function bytesToMiB(bytes: number): number {
  return Math.round((bytes / (1024 * 1024)) * 100) / 100;
}

// formatTime 把 RFC3339 时间渲染为本地可读字符串，空值显示破折号。
export function formatTime(rfc3339?: string): string {
  if (!rfc3339) return "—";
  const d = new Date(rfc3339);
  if (isNaN(d.getTime())) return "—";
  return d.toLocaleString();
}

// relativeTime 把 RFC3339 时间渲染为相对时间（按语言），空值显示破折号。
export function relativeTime(rfc3339: string | undefined, lang: "zh" | "en"): string {
  if (!rfc3339) return "—";
  const d = new Date(rfc3339).getTime();
  if (isNaN(d)) return "—";
  const s = Math.max(0, Math.floor((Date.now() - d) / 1000));
  if (lang === "en") {
    if (s < 60) return "just now";
    if (s < 3600) return `${Math.floor(s / 60)} min ago`;
    if (s < 86400) return `${Math.floor(s / 3600)} h ago`;
    return `${Math.floor(s / 86400)} d ago`;
  }
  if (s < 60) return "刚刚";
  if (s < 3600) return `${Math.floor(s / 60)} 分钟前`;
  if (s < 86400) return `${Math.floor(s / 3600)} 小时前`;
  return `${Math.floor(s / 86400)} 天前`;
}

// secondsUntil 计算到目标 RFC3339 时间的剩余秒数（不为负）。
export function secondsUntil(rfc3339?: string): number {
  if (!rfc3339) return 0;
  const diff = Math.floor((new Date(rfc3339).getTime() - Date.now()) / 1000);
  return diff > 0 ? diff : 0;
}

// mmss 把秒数格式化为 m:ss。
export function mmss(total: number): string {
  const m = Math.floor(total / 60);
  const s = total % 60;
  return `${m}:${s.toString().padStart(2, "0")}`;
}

export type Theme = "light" | "dark" | "system";

// applyTheme 把主题写入 <html>（class + data-theme），system 跟随系统。
export function applyTheme(theme: Theme): void {
  const root = document.documentElement;
  const dark =
    theme === "dark" ||
    (theme === "system" && window.matchMedia("(prefers-color-scheme: dark)").matches);
  root.classList.toggle("dark", dark);
  root.dataset.theme = dark ? "dark" : "light";
}
