import { createContext, useCallback, useContext, useEffect, useState, type ReactNode } from "react";
import { Button, Spinner } from "@heroui/react";
import { RiMoonLine, RiSunLine, RiComputerLine, RiTranslate2 } from "@remixicon/react";
import { auth, type Sessions } from "./api";
import { I18nProvider, useI18n } from "./i18n";
import { applyTheme, type Theme } from "./util";
import { Login } from "./pages/Login";
import { AdminConsole } from "./pages/AdminConsole";
import { UserConsole } from "./pages/UserConsole";

type View = "admin" | "user";

// Auth 暴露当前同时持有的登录态与一键切换/登录/退出能力。
interface AuthCtx {
  sessions: Sessions;
  view: View;
  setView: (v: View) => void;
  refresh: () => Promise<Sessions>;
  logout: (role: View) => Promise<void>;
  startAddLogin: () => void;
}
const AuthContext = createContext<AuthCtx | null>(null);
export function useAuth(): AuthCtx {
  const ctx = useContext(AuthContext);
  if (!ctx) throw new Error("useAuth outside provider");
  return ctx;
}

// IconButton 是带 hover 原生提示（title）的纯图标按钮，用于节约顶栏空间。
export function IconButton({
  label,
  onPress,
  icon: I,
  variant = "ghost",
}: {
  label: string;
  onPress: () => void;
  icon: typeof RiSunLine;
  variant?: "ghost" | "secondary" | "outline";
}) {
  return (
    <span title={label} className="inline-flex">
      <Button variant={variant} size="sm" onPress={onPress} aria-label={label} className="px-2">
        <I size={18} />
      </Button>
    </span>
  );
}

// TopBar 提供语言与主题切换，登录页与控制台共用。iconOnly 时为纯图标 + hover 提示。
export function TopBar({ right, iconOnly }: { right?: ReactNode; iconOnly?: boolean }) {
  const { t, lang, setLang } = useI18n();
  const [theme, setTheme] = useState<Theme>(() => (localStorage.getItem("cb-theme") as Theme) || "system");
  useEffect(() => {
    applyTheme(theme);
    localStorage.setItem("cb-theme", theme);
  }, [theme]);
  const cycleTheme = () => setTheme((p) => (p === "light" ? "dark" : p === "dark" ? "system" : "light"));
  const ThemeIcon = theme === "light" ? RiSunLine : theme === "dark" ? RiMoonLine : RiComputerLine;
  const toggleLang = () => setLang(lang === "zh" ? "en" : "zh");
  return (
    <div className="flex items-center gap-1">
      {right}
      {iconOnly ? (
        <IconButton label={`${t("language")} · ${lang === "zh" ? "EN" : "中"}`} onPress={toggleLang} icon={RiTranslate2} />
      ) : (
        <Button variant="ghost" size="sm" onPress={toggleLang} aria-label={t("language")}>
          <RiTranslate2 size={16} />
          {lang === "zh" ? "中" : "EN"}
        </Button>
      )}
      <IconButton label={t("theme")} onPress={cycleTheme} icon={ThemeIcon} />
    </div>
  );
}

// Root 引导双会话：加载两类登录态，按 view 路由到对应控制台，支持一键切换/追加登录。
function Root() {
  const { t } = useI18n();
  const [sessions, setSessions] = useState<Sessions>({});
  const [view, setView] = useState<View | null>(null);
  const [loading, setLoading] = useState(true);
  const [adding, setAdding] = useState(false); // 「登录其它身份」覆盖层

  const refresh = useCallback(async (): Promise<Sessions> => {
    const s = await auth.me().catch(() => ({}) as Sessions);
    setSessions(s);
    setView((cur) => {
      if (cur && s[cur]) return cur;
      if (s.admin) return "admin";
      if (s.user) return "user";
      return null;
    });
    return s;
  }, []);

  useEffect(() => {
    void refresh().finally(() => setLoading(false));
  }, [refresh]);

  const logout = useCallback(
    async (role: View) => {
      await auth.logout(role).catch(() => {});
      await refresh();
    },
    [refresh],
  );

  if (loading) {
    return (
      <div className="flex h-full items-center justify-center">
        <Spinner aria-label={t("loading")} />
      </div>
    );
  }

  const noSession = !sessions.admin && !sessions.user;
  if (noSession || adding) {
    return (
      <Login
        onLoggedIn={async (role) => {
          await refresh();
          // Switch to the identity just signed in — without this, adding a user
          // login while an admin session exists would keep showing the admin view.
          setView(role);
          setAdding(false);
        }}
        onCancel={adding ? () => setAdding(false) : undefined}
      />
    );
  }

  const active: View = view ?? (sessions.admin ? "admin" : "user");
  const ctx: AuthCtx = {
    sessions,
    view: active,
    setView,
    refresh,
    logout,
    startAddLogin: () => setAdding(true),
  };

  return (
    <AuthContext.Provider value={ctx}>
      {active === "admin" && sessions.admin ? (
        <AdminConsole me={sessions.admin} />
      ) : sessions.user ? (
        <UserConsole me={sessions.user} />
      ) : null}
    </AuthContext.Provider>
  );
}

// App 注入 i18n 并渲染根。
export function App() {
  return (
    <I18nProvider>
      <Root />
    </I18nProvider>
  );
}
