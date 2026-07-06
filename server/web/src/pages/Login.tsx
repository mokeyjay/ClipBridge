import { useState } from "react";
import { Button, Card, Input, Label, TextField } from "@heroui/react";
import { auth } from "../api";
import { useI18n } from "../i18n";
import { toastErr, errText } from "../notify";
import { TopBar } from "../App";

// Login 提供管理员/用户统一登录。自助注册已移除（用户由管理员添加）。onCancel 用于
// 「登录其它身份」时返回当前已登录的控制台。onLoggedIn 收到刚登录的身份类型，便于
// 上层切到该身份的控制台（避免管理员已登录时再登录普通用户却停留在管理员界面）。
export function Login({ onLoggedIn, onCancel }: { onLoggedIn: (role: "admin" | "user") => void; onCancel?: () => void }) {
  const { t } = useI18n();
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [busy, setBusy] = useState(false);

  const submit = async () => {
    setBusy(true);
    try {
      const me = await auth.login(username, password);
      onLoggedIn(me.subject_type);
    } catch (e) {
      toastErr(t("loginFailed"), errText(e, t));
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="flex min-h-full flex-col">
      <div className="flex justify-end p-4">
        <TopBar />
      </div>
      <div className="flex flex-1 items-center justify-center p-4">
        <div className="w-full max-w-sm">
          <div className="mb-6 text-center">
            <h1 className="font-display text-3xl font-bold text-foreground">{t("appName")}</h1>
            <p className="mt-1 text-sm text-foreground-secondary">端到端加密 · 自托管剪贴板同步</p>
          </div>
          <Card className="surface-card rounded-2xl">
            <Card.Content className="p-6">
              <form
                className="flex flex-col gap-4"
                onSubmit={(e) => {
                  e.preventDefault();
                  if (!busy) void submit();
                }}
              >
                <TextField value={username} onChange={setUsername} isRequired autoFocus>
                  <Label>{t("username")}</Label>
                  <Input placeholder={t("username")} autoComplete="username" />
                </TextField>
                <TextField value={password} onChange={setPassword} isRequired type="password">
                  <Label>{t("password")}</Label>
                  <Input placeholder={t("password")} type="password" autoComplete="current-password" />
                </TextField>
                <Button type="submit" isDisabled={busy} className="mt-1">
                  {t("signIn")}
                </Button>
                {onCancel && (
                  <Button variant="ghost" size="sm" onPress={onCancel}>
                    {t("back")}
                  </Button>
                )}
              </form>
            </Card.Content>
          </Card>
        </div>
      </div>
    </div>
  );
}
