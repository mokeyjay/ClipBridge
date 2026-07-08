import { useCallback, useEffect, useState } from "react";
import { Tabs } from "@heroui/react";
import { Window } from "@wailsio/runtime";
import { App as Svc, onStatus, type StatusDTO } from "./api";
import { useI18n } from "./i18n";
import { applyTheme, type Theme } from "./util";
import { StatusChip, Spinner, UpdateBadge } from "./components/common";
import { OverviewPage } from "./pages/Overview";
import { SettingsPage } from "./pages/Settings";
import { AboutPage } from "./pages/About";

const THEME_KEY = "cb-theme";

// App 是客户端外壳：可拖动顶栏（macOS 交通灯 | 居中 Tabs | 右侧状态）+ 内容区。
// 组件全部用 HeroUI v3；状态绑定 guiservice 真实能力，主题持久化到 localStorage。
export function App() {
  const { t, lang } = useI18n();
  const [tab, setTab] = useState("overview");
  const [status, setStatus] = useState<StatusDTO | null>(null);
  const [theme, setTheme] = useState<Theme>(() => (localStorage.getItem(THEME_KEY) as Theme) || "system");

  const refresh = useCallback(() => {
    Svc.Status().then(setStatus).catch(() => {});
  }, []);

  // 把解析后的界面语言推给 Go 端，使托盘右键菜单语言跟随设置里的切换。
  useEffect(() => {
    Svc.SetLanguage(lang).catch(() => {});
  }, [lang]);

  useEffect(() => {
    refresh();
    return onStatus(setStatus);
  }, [refresh]);

  // 主题写入 <html> 并持久化；跟随系统时监听变化。
  useEffect(() => {
    applyTheme(theme);
    localStorage.setItem(THEME_KEY, theme);
    if (theme === "system" && window.matchMedia) {
      const mq = window.matchMedia("(prefers-color-scheme: dark)");
      const onChange = () => applyTheme("system");
      mq.addEventListener("change", onChange);
      return () => mq.removeEventListener("change", onChange);
    }
  }, [theme]);

  const paired = status?.paired ?? false;
  const headerState = !paired
    ? "unpaired"
    : status?.paused
      ? "paused"
      : status?.connected
        ? "connected"
        : "connecting";
  const isWindows = status?.platform === "windows";
  // 发现新版本时的顶栏入口：mac 放在连接状态左侧，Windows 放在连接状态右侧。
  const updateBadge = status?.update_available ? (
    <UpdateBadge version={status.latest_version} url={status.update_url} />
  ) : null;

  // 平台标记写入 <html>，驱动平台相关样式（如 Windows 默认微软雅黑字体）。
  useEffect(() => {
    if (status?.platform) document.documentElement.dataset.os = status.platform;
  }, [status?.platform]);

  const tabs: { key: string; label: string }[] = [
    { key: "overview", label: t("tab_overview") },
    { key: "settings", label: t("tab_settings") },
    { key: "about", label: t("tab_about") },
  ];

  return (
    <div className="flex h-full flex-col text-foreground">
      {/* 顶栏：grid 三栏。整条可拖动，交互元素 no-drag。
          macOS：[交通灯留白 | Tabs | 连接状态]；
          Windows：无系统标题栏，[连接状态 | Tabs | 窗口控制按钮]（控制按钮在右侧）。 */}
      <header className="drag cb-divider-b grid h-[52px] shrink-0 grid-cols-[1fr_auto_1fr] items-center px-3">
        {/* 左：mac 为交通灯留白（OS 在统一工具栏中垂直居中绘制）；Windows 放连接状态 + 更新入口。 */}
        <div className="flex items-center gap-2">
          {isWindows ? (
            <>
              <StatusChip state={headerState} />
              {updateBadge}
            </>
          ) : (
            <div aria-hidden className="w-[68px]" />
          )}
        </div>
        <Tabs
          selectedKey={tab}
          onSelectionChange={(k) => setTab(String(k))}
          className="no-drag"
        >
          <Tabs.ListContainer>
            <Tabs.List aria-label="navigation">
              {tabs.map((tb, i) => (
                <Tabs.Tab key={tb.key} id={tb.key}>
                  {i > 0 && <Tabs.Separator />}
                  {tb.label}
                  <Tabs.Indicator />
                </Tabs.Tab>
              ))}
            </Tabs.List>
          </Tabs.ListContainer>
        </Tabs>
        <div className="flex items-center justify-end gap-2">
          {isWindows ? (
            <WinControls />
          ) : (
            <>
              {updateBadge}
              <StatusChip state={headerState} />
            </>
          )}
        </div>
      </header>

      {/* 内容区：唯一滚动容器。顶/左/右边距等宽（px-5 / pt-5）。 */}
      <main className="cb-scroll flex-1 overflow-y-auto overflow-x-hidden px-5 pb-6 pt-5">
        {!status ? (
          <div className="mt-10 flex items-center justify-center gap-2 text-foreground-secondary">
            <Spinner /> {t("loading")}
          </div>
        ) : (
          <>
            {tab === "overview" && <OverviewPage status={status} onChange={refresh} />}
            {tab === "settings" && (
              <SettingsPage status={status} theme={theme} setTheme={setTheme} onChange={refresh} />
            )}
            {tab === "about" && <AboutPage status={status} onChange={refresh} />}
          </>
        )}
      </main>
    </div>
  );
}

// WinControls 是 Windows 无边框窗口的最小化/最大化/关闭按钮，整合进顶栏右侧，
// 与 macOS 交通灯位置对称。点击调用 Wails 运行时的当前窗口方法。
function WinControls() {
  const cls =
    "no-drag inline-grid h-[52px] w-[44px] place-items-center text-foreground-secondary outline-none transition hover:bg-default-100/70";
  return (
    <div className="-mr-3 flex items-stretch">
      <button className={cls} aria-label="minimize" onClick={() => void Window.Minimise()}>
        <svg width="11" height="11" viewBox="0 0 11 11" aria-hidden>
          <rect x="1" y="5" width="9" height="1" fill="currentColor" />
        </svg>
      </button>
      <button className={cls} aria-label="maximize" onClick={() => void Window.ToggleMaximise()}>
        <svg width="11" height="11" viewBox="0 0 11 11" aria-hidden>
          <rect x="1.5" y="1.5" width="8" height="8" fill="none" stroke="currentColor" strokeWidth="1" />
        </svg>
      </button>
      <button
        className={`${cls} hover:bg-danger hover:text-white`}
        aria-label="close"
        onClick={() => void Window.Close()}
      >
        <svg width="11" height="11" viewBox="0 0 11 11" aria-hidden>
          <path d="M1.5 1.5 L9.5 9.5 M9.5 1.5 L1.5 9.5" stroke="currentColor" strokeWidth="1.2" />
        </svg>
      </button>
    </div>
  );
}
