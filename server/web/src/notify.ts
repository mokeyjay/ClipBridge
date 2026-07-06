import { toast } from "@heroui/react";
import { ApiError } from "./api";

// 统一的 toast 封装：成功用默认样式，错误用 danger。
export function toastOK(title: string, description?: string) {
  toast(title, { description });
}
export function toastErr(title: string, description?: string) {
  toast(title, { description, variant: "danger" });
}

// errText 把后端错误码映射为当前语言的清晰文案。t 为 i18n 翻译函数。
export function errText(e: unknown, t: (k: string) => string): string {
  if (e instanceof ApiError) {
    switch (e.code) {
      case "USERNAME_TAKEN":
        return t("usernameTaken");
      case "AUTH_REQUIRED":
        return t("loginFailed");
      case "USER_DISABLED":
        return t("disabled");
      case "FORBIDDEN":
        return t("opFailed");
      case "PAIRING_NOT_PENDING":
        return t("opFailed");
      default:
        return e.message || t("opFailed");
    }
  }
  return t("networkError");
}
