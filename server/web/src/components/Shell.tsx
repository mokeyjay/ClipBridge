import type { ReactNode } from "react";
import { RiLogoutBoxRLine, RiUserAddLine, RiUserSharedLine } from "@remixicon/react";
import { useI18n } from "../i18n";
import { IconButton, TopBar, useAuth } from "../App";

type View = "admin" | "user";

// ConsoleShell 是后台统一外壳：顶栏（品牌 | 居中 tabs | 右侧身份切换/语言/主题/退出），
// 风格贴合桌面客户端。center 放入 Tabs.List，children 放入对应 Tabs.Panel。
export function ConsoleShell({
  subtitle,
  center,
  children,
}: {
  subtitle?: string;
  center: ReactNode;
  children: ReactNode;
}) {
  const { t } = useI18n();
  const { sessions, view, setView, logout, startAddLogin } = useAuth();
  const other: View = view === "admin" ? "user" : "admin";
  const hasOther = !!sessions[other];

  return (
    // 根容器需 w-full：HeroUI 的 <Tabs> 根是 flex 列容器，带 auto 外边距的子项
    // 若无 w-full 会收缩到内容固有宽度，导致各 tab 宽度不一致且偏窄。
    <div className="flex min-h-full w-full flex-col">
      {/* 顶栏：全屏宽度 + 液态玻璃质感（强模糊/提饱和/半透明），内层 1400px 居中。 */}
      <header className="glass-bar sticky top-0 z-20">
        <div className="mx-auto grid w-full max-w-[1400px] grid-cols-[1fr_auto_1fr] items-center gap-4 px-4 py-2">
          <div className="flex min-w-0 items-baseline gap-2">
            <span className="font-display whitespace-nowrap text-base font-semibold text-foreground">{t("appName")}</span>
            {subtitle && <span className="truncate text-xs text-foreground-secondary">{subtitle}</span>}
          </div>
          <div className="flex justify-center">{center}</div>
          <div className="flex items-center justify-end gap-1">
            <TopBar
              iconOnly
              right={
                <>
                  {hasOther ? (
                    <IconButton
                      label={other === "admin" ? t("switchToAdmin") : t("switchToUser")}
                      onPress={() => setView(other)}
                      icon={RiUserSharedLine}
                    />
                  ) : (
                    <IconButton label={t("loginOther")} onPress={startAddLogin} icon={RiUserAddLine} />
                  )}
                  <IconButton label={t("logout")} onPress={() => void logout(view)} icon={RiLogoutBoxRLine} />
                </>
              }
            />
          </div>
        </div>
      </header>
      {/* 主内容：统一 1400px 居中，不再随各 tab 内容收缩；自身无底色，浮于氛围背景上。 */}
      <main className="mx-auto w-full max-w-[1400px] p-4 sm:p-6">{children}</main>
    </div>
  );
}

// Row 是一个标签 + 值的展示行，用于总览类信息。
export function Row({ label, value }: { label: string; value: ReactNode }) {
  return (
    <div className="flex items-center justify-between gap-4 border-b border-default-200 py-2 last:border-0">
      <span className="text-sm text-foreground-secondary">{label}</span>
      <span className="text-right text-sm font-medium text-foreground">{value}</span>
    </div>
  );
}

// StatusChip 用语义颜色展示设备/用户的生命周期状态。
export function StatusChip({ status }: { status: string }) {
  const { t } = useI18n();
  const tone =
    status === "active" ? "text-success" : status === "revoked" ? "text-danger" : "text-warning";
  const label = status === "active" ? t("active") : status === "revoked" ? t("revoked") : t("disabled");
  return <span className={`text-sm font-medium ${tone}`}>{label}</span>;
}

// OnlineDot 用绿点/灰点 + 文案表示设备是否在线。title 可选：hover 显示绝对时间。
export function OnlineDot({ online, label, title }: { online: boolean; label: string; title?: string }) {
  return (
    <span title={title} className="inline-flex items-center gap-1.5 text-sm">
      <span className={`size-2 rounded-full ${online ? "bg-success" : "bg-default-300"}`} />
      <span className={online ? "font-medium text-success" : "text-foreground-secondary"}>{label}</span>
    </span>
  );
}
