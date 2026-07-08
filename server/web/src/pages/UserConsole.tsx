import { useCallback, useEffect, useState } from "react";
import { Button, Card, Checkbox, CheckboxGroup, Description, Input, Label, Tabs, TextField } from "@heroui/react";
import {
  user,
  auth,
  type ContentType,
  type CreatedPairingCode,
  type CurrentPairingCode,
  type DeviceView,
  type Me,
  type PendingRequest,
  type UserSettings,
} from "../api";
import { useI18n } from "../i18n";
import { toastErr, toastOK, errText } from "../notify";
import { ConsoleShell, OnlineDot, Row } from "../components/Shell";
import { DeviceTable, type DeviceAction } from "./AdminConsole";
import { bytesToMiB, formatBytes, formatTime, mibToBytes, mmss, secondsUntil } from "../util";

// UserConsole 是用户后台：总览、设备、配对、同步策略、账号。新待确认设备会弹窗提醒。
export function UserConsole({ me }: { me: Me }) {
  const { t } = useI18n();
  const [tab, setTab] = useState("overview");
  // 单点管理配对码/待确认设备：配对 tab 与全局弹窗共用这份状态，避免各自轮询 /current。
  const pairing = usePairing();
  return (
    <Tabs selectedKey={tab} onSelectionChange={(k) => setTab(String(k))}>
      <ConsoleShell
        subtitle={`${t("user")} · ${me.username}`}
        center={
          <Tabs.ListContainer>
            <Tabs.List aria-label="user">
              <Tabs.Tab id="overview">{t("overview")}<Tabs.Indicator /></Tabs.Tab>
              <Tabs.Tab id="devices"><Tabs.Separator />{t("devices")}<Tabs.Indicator /></Tabs.Tab>
              <Tabs.Tab id="pairing"><Tabs.Separator />{t("pairing")}<Tabs.Indicator /></Tabs.Tab>
              <Tabs.Tab id="policy"><Tabs.Separator />{t("syncPolicy")}<Tabs.Indicator /></Tabs.Tab>
              <Tabs.Tab id="account"><Tabs.Separator />{t("account")}<Tabs.Indicator /></Tabs.Tab>
            </Tabs.List>
          </Tabs.ListContainer>
        }
      >
        <Tabs.Panel id="overview"><UserOverview /></Tabs.Panel>
        <Tabs.Panel id="devices"><UserDevices /></Tabs.Panel>
        <Tabs.Panel id="pairing">
          <Pairing
            data={pairing.data}
            created={pairing.created}
            rememberCreated={pairing.rememberCreated}
            clearCreated={pairing.clearCreated}
            reload={pairing.reload}
          />
        </Tabs.Panel>
        <Tabs.Panel id="policy"><SyncPolicy /></Tabs.Panel>
        <Tabs.Panel id="account"><Account me={me} /></Tabs.Panel>
      </ConsoleShell>
      {/* 全局：检测到新的待确认设备时弹窗（贯穿所有 tab）；确认成功后跳到设备页。
          待确认列表来自共享的 usePairing（仅在存在有效配对码时才轮询）。 */}
      <PendingModal
        pending={pairing.data?.pending_requests ?? []}
        reload={pairing.reload}
        onConfirmed={() => setTab("devices")}
      />
    </Tabs>
  );
}

// usePairing 单点管理当前配对码与待确认设备。关键：只有在存在有效配对码时才轮询
// /current——新设备只能针对有效配对码发起请求，无有效码时轮询毫无意义。挂载时先
// 拉一次以恢复刷新前仍有效的配对码；有效期间每 3s 轮询一次（待确认设备 + 服务信息）。
function usePairing() {
  const [data, setData] = useState<CurrentPairingCode | null>(null);
  const [created, setCreated] = useState<CreatedPairingCode | null>(null);
  // reload 拉取当前有效配对码与待确认请求，并在服务端已无有效码时清掉本地明文码。
  const reload = useCallback(() => user.getCurrentPairingCode().then((next) => {
    setData(next);
    if (!next.active) setCreated(null);
  }).catch(() => {}), []);
  // rememberCreated 保存刚生成的 6 位明文码，并同步填充共享的 current 状态。
  const rememberCreated = useCallback((next: CreatedPairingCode) => {
    setCreated(next);
    setData((prev) => ({
      active: true,
      expires_at: next.expires_at,
      server_name: next.server_name,
      server_fingerprint_sha256: next.server_fingerprint_sha256,
      pending_requests: prev?.pending_requests ?? [],
    }));
  }, []);
  // clearCreated 在取消或过期时清理本地明文码。
  const clearCreated = useCallback(() => setCreated(null), []);
  useEffect(() => {
    void reload();
  }, [reload]);
  useEffect(() => {
    if (!created) return;
    const ms = Math.max(0, new Date(created.expires_at).getTime() - Date.now() + 1000);
    const id = setTimeout(() => setCreated(null), ms);
    return () => clearTimeout(id);
  }, [created]);
  const active = !!data?.active || !!created;
  useEffect(() => {
    if (!active) return; // 无有效配对码：不轮询
    const id = setInterval(() => void reload(), 3000);
    return () => clearInterval(id);
  }, [active, reload]);
  return { data, created, rememberCreated, clearCreated, reload };
}

function Stat({ label, value }: { label: string; value: string }) {
  return (
    <Card variant="default" className="surface-card rounded-2xl">
      <Card.Content className="px-5 py-4">
        <p className="text-sm text-foreground-secondary">{label}</p>
        <p className="font-display mt-1 text-2xl font-semibold text-foreground">{value}</p>
      </Card.Content>
    </Card>
  );
}

// UserOverview 展示设备数、当前在线设备与策略摘要。
function UserOverview() {
  const { t } = useI18n();
  const [devices, setDevices] = useState<DeviceView[]>([]);
  const [s, setS] = useState<UserSettings | null>(null);
  useEffect(() => {
    const load = () => void user.listDevices().then((r) => setDevices(r.devices)).catch(() => {});
    load();
    const id = setInterval(load, 5000);
    void user.getSettings().then(setS).catch(() => {});
    return () => clearInterval(id);
  }, []);
  const online = devices.filter((d) => d.online);
  return (
    <div className="mt-4 grid gap-4 sm:grid-cols-2">
      <Stat label={t("deviceCount")} value={String(devices.length)} />
      <Stat label={t("onlineDeviceCount")} value={String(online.length)} />
      <div className="sm:col-span-2">
        <Card variant="default" className="surface-card rounded-2xl">
          <Card.Header className="px-5 pt-5"><Card.Title>{t("currentOnlineDevices")}</Card.Title></Card.Header>
          <Card.Content className="px-5 pb-4">
            {online.length === 0 ? (
              <p className="text-sm text-foreground-secondary">{t("noOnlineDevices")}</p>
            ) : (
              <div className="flex flex-col">
                {online.map((d) => (
                  <div key={d.id} className="flex items-center justify-between gap-4 border-b border-default-200 py-2 last:border-0">
                    <span className="text-sm font-medium text-foreground">{d.name}</span>
                    <span className="flex items-center gap-3 text-xs text-foreground-secondary">
                      <span>{d.platform} {d.client_version}</span>
                      <OnlineDot online label={t("online")} />
                    </span>
                  </div>
                ))}
              </div>
            )}
          </Card.Content>
        </Card>
      </div>
      {s && (
        <div className="sm:col-span-2">
          <Card variant="default" className="surface-card rounded-2xl">
            <Card.Content className="px-5 py-3">
              <Row label={t("maxSyncSize")} value={formatBytes(s.max_sync_size_bytes)} />
              <Row label={t("syncTypes")} value={s.allowed_types.map((x) => t(x)).join("、")} />
            </Card.Content>
          </Card>
        </div>
      )}
    </div>
  );
}

// UserDevices 管理自己的设备（含吊销 / 删除已吊销记录）。
function UserDevices() {
  const { t } = useI18n();
  const [devices, setDevices] = useState<DeviceView[]>([]);
  const reload = () => void user.listDevices().then((r) => setDevices(r.devices)).catch(() => {});
  useEffect(reload, []);
  const onAction = async (d: DeviceView, action: DeviceAction) => {
    try {
      if (action === "revoke" || action === "delete") await user.revokeDevice(d.id);
      else await user.updateDevice(d.id, { status: action === "disable" ? "disabled" : "active" });
      toastOK(t("deviceUpdated"));
      reload();
    } catch (e) {
      toastErr(t("opFailed"), errText(e, t));
    }
  };
  return (
    <Card variant="default" className="surface-card mt-4 rounded-2xl">
      <Card.Content className="p-2"><DeviceTable devices={devices} onAction={onAction} allowRevoke /></Card.Content>
    </Card>
  );
}

const modalExitMs = 160;

// PendingModal 展示待确认设备并逐个确认/拒绝。pending 来自上层共享的 usePairing，
// 本组件不再自行轮询（避免与配对 tab 重复请求 /current）。
// onConfirmed 在确认成功后触发（跳转到设备页）；reload 用于操作后即时刷新列表。
//
// 当前 HeroUI/React Aria Modal 在本项目组合里会额外挂出一个无按钮 portal，
// 因此这里用业务专属受控覆盖层，手动控制进出场动画和关闭入口。
function PendingModal({
  pending,
  reload,
  onConfirmed,
}: {
  pending: PendingRequest[];
  reload: () => void;
  onConfirmed: () => void;
}) {
  const { t } = useI18n();
  // handled：已在本地处理（确认/拒绝/关闭）的 request_id，立即从队列隐藏，不等下次轮询。
  const [handled, setHandled] = useState<Set<string>>(() => new Set());
  const [busy, setBusy] = useState(false);
  const [shown, setShown] = useState<PendingRequest | null>(null);
  const [closing, setClosing] = useState(false);

  // 已不在待确认列表中的 handled id（已过期/被他处处理）及时清掉，避免无限增长。
  useEffect(() => {
    setHandled((h) => {
      if (h.size === 0) return h;
      const ids = new Set(pending.map((p) => p.request_id));
      const next = new Set<string>();
      h.forEach((id) => ids.has(id) && next.add(id));
      return next.size === h.size ? h : next;
    });
  }, [pending]);

  // 当前要处理的请求 = 第一个尚未被本地处理的待确认请求。
  const current = pending.find((p) => !handled.has(p.request_id)) ?? null;

  // current 变化时保留上一帧内容播放退场动画，避免弹窗瞬间消失。
  useEffect(() => {
    if (current) {
      setShown(current);
      setClosing(false);
      return;
    }
    if (!shown) return;
    setClosing(true);
    const id = setTimeout(() => {
      setShown(null);
      setClosing(false);
    }, modalExitMs);
    return () => clearTimeout(id);
  }, [current, shown]);

  // 处理一个请求：同一设备可能被重复提交（同指纹多请求），把同指纹的待确认项一并
  // 标记为已处理，避免确认其一后另一个又弹出来。失败则回退，下一轮可重试。
  const act = async (req: PendingRequest, run: () => Promise<unknown>, okMsg: string) => {
    const sameFp = pending.filter((p) => p.key_fingerprint === req.key_fingerprint).map((p) => p.request_id);
    setBusy(true);
    setHandled((h) => new Set([...h, ...sameFp]));
    try {
      await run();
      toastOK(okMsg);
      reload(); // 即时刷新共享列表（不必等下一次 3s 轮询）
    } catch (e) {
      toastErr(t("opFailed"), errText(e, t));
      setHandled((h) => {
        const n = new Set(h);
        sameFp.forEach((id) => n.delete(id));
        return n;
      });
    } finally {
      setBusy(false);
    }
  };

  // confirm 确认当前设备，成功后跳转到设备页查看新设备。
  const confirm = (req: PendingRequest) =>
    void act(req, async () => {
      await user.confirmRequest(req.request_id);
      onConfirmed();
    }, t("pairingConfirmed"));
  // reject 拒绝当前设备，并从本地弹窗队列隐藏同指纹重复请求。
  const reject = (req: PendingRequest) =>
    void act(req, () => user.rejectRequest(req.request_id), t("pairingRejected"));
  // 关闭当前设备：仅本地隐藏当前待确认项，不发确认也不发拒绝。
  const dismiss = () => {
    const req = current ?? shown;
    if (!req) return;
    const sameFp = pending.filter((p) => p.key_fingerprint === req.key_fingerprint).map((p) => p.request_id);
    setHandled((h) => new Set([...h, ...sameFp]));
  };

  const display = current ?? shown;
  if (!display) return null;
  return (
    <div
      className={`cb-pairing-modal fixed inset-0 z-50 flex items-center justify-center bg-black/45 px-4 py-8 ${closing ? "cb-pairing-modal--closing" : ""}`}
      role="presentation"
      aria-hidden={closing}
    >
      <section
        className="cb-pairing-modal__dialog relative w-full max-w-3xl rounded-[2rem] bg-background px-6 py-6 text-foreground shadow-2xl sm:px-8 sm:py-7"
        role="dialog"
        aria-modal="true"
        aria-labelledby="pending-pairing-title"
      >
        <button
          aria-label="Close"
          className="absolute right-6 top-6 flex size-12 items-center justify-center rounded-full bg-default-100 text-foreground-secondary transition hover:bg-default-200 hover:text-foreground disabled:opacity-50"
          disabled={busy || closing}
          type="button"
          onClick={dismiss}
        >
          <span className="text-4xl leading-none">×</span>
        </button>
        <h2 id="pending-pairing-title" className="font-display pr-16 text-2xl font-semibold text-foreground">
          {t("newPairingRequest")}
        </h2>
        <div className="mt-7 flex flex-col gap-1">
          <Row label={t("username")} value={display.device_name} />
          <Row label={t("platform")} value={`${display.platform} ${display.client_version}`} />
          <Row label={t("requestTime")} value={formatTime(display.created_at)} />
          <Row label={t("keyFingerprint")} value={<code className="mono text-xs break-all">{display.key_fingerprint}</code>} />
        </div>
        <div className="mt-8 flex justify-end gap-3">
          <Button variant="danger-soft" isDisabled={busy || closing || !current} onPress={() => current && reject(current)}>
            {t("reject")}
          </Button>
          <Button isDisabled={busy || closing || !current} onPress={() => current && confirm(current)}>
            {t("confirm")}
          </Button>
        </div>
      </section>
    </div>
  );
}

// Pairing 生成配对码并倒计时展示（待确认设备由全局弹窗处理）。
// current/reload 来自上层共享的 usePairing——本组件不再自行轮询 /current。
// created 为本地态：新生成配对码时才拿得到 6 位明文（/current 出于安全不再回传明文）。
function Pairing({
  data: current,
  created,
  rememberCreated,
  clearCreated,
  reload,
}: {
  data: CurrentPairingCode | null;
  created: CreatedPairingCode | null;
  rememberCreated: (created: CreatedPairingCode) => void;
  clearCreated: () => void;
  reload: () => void;
}) {
  const { t } = useI18n();
  const [remaining, setRemaining] = useState(0);

  const expiresAt = created?.expires_at ?? current?.expires_at;
  useEffect(() => {
    const tick = () => setRemaining(secondsUntil(expiresAt));
    tick();
    const id = setInterval(tick, 1000);
    return () => clearInterval(id);
  }, [expiresAt]);

  // generate 创建新配对码，并把只返回一次的明文码提升到共享状态保存。
  const generate = async () => {
    try {
      rememberCreated(await user.createPairingCode());
      toastOK(t("codeCreated"));
      reload();
    } catch (e) {
      toastErr(t("opFailed"), errText(e, t));
    }
  };
  // cancel 取消当前配对码，并同步清空本地保存的明文码。
  const cancel = async () => {
    try {
      await user.cancelPairingCode();
      clearCreated();
      toastOK(t("codeCancelled"));
      reload();
    } catch (e) {
      toastErr(t("opFailed"), errText(e, t));
    }
  };

  const active = created || current?.active;
  return (
    <div className="mt-4 flex flex-col gap-4">
      <Card variant="default" className="surface-card rounded-2xl">
        <Card.Header className="px-5 pt-5"><Card.Title>{t("pairing")}</Card.Title></Card.Header>
        <Card.Content className="px-5 pb-5">
          {!active ? (
            <div className="flex flex-col items-start gap-3">
              <p className="text-sm text-foreground-secondary">{t("noActiveCode")}</p>
              <Button onPress={() => void generate()}>{t("connectNewDevice")}</Button>
            </div>
          ) : (
            <div className="flex flex-col gap-4">
              {created && (
                <div className="rounded-2xl bg-accent/10 px-4 py-5 text-center">
                  <p className="text-xs uppercase tracking-wide text-foreground-secondary">{t("pairingCode")}</p>
                  <p className="code-hero mt-2 text-5xl text-foreground">{created.code}</p>
                </div>
              )}
              <div className="rounded-xl bg-default-100/50 px-4 py-1">
                <Row label={t("expiresIn")} value={<span className="mono">{mmss(remaining)}</span>} />
                <Row label={t("serverName")} value={current?.server_name ?? created?.server_name} />
                <Row label={t("serverFingerprint")} value={<code className="mono text-xs break-all">{current?.server_fingerprint_sha256 ?? created?.server_fingerprint_sha256}</code>} />
              </div>
              <Button variant="tertiary" onPress={() => void cancel()}>{t("cancel")}</Button>
            </div>
          )}
        </Card.Content>
      </Card>
    </div>
  );
}

const TYPE_OPTIONS: { value: ContentType; titleKey: "text" | "image" | "file" | "rich_text"; descKey: "textDesc" | "imageDesc" | "fileDesc" | "richTextDesc" }[] = [
  { value: "text", titleKey: "text", descKey: "textDesc" },
  { value: "image", titleKey: "image", descKey: "imageDesc" },
  { value: "file", titleKey: "file", descKey: "fileDesc" },
  { value: "rich_text", titleKey: "rich_text", descKey: "richTextDesc" },
];

// SyncPolicy 维护用户级默认同步策略模板（文件有效期已移至客户端本地配置）。
function SyncPolicy() {
  const { t } = useI18n();
  const [s, setS] = useState<UserSettings | null>(null);
  const [maxMiB, setMaxMiB] = useState("");
  const [upMiB, setUpMiB] = useState("");
  const [downMiB, setDownMiB] = useState("");

  useEffect(() => {
    void user.getSettings().then((v) => {
      setS(v);
      setMaxMiB(String(bytesToMiB(v.max_sync_size_bytes)));
      setUpMiB(String(bytesToMiB(v.max_auto_upload_size_bytes)));
      setDownMiB(String(bytesToMiB(v.max_auto_download_size_bytes)));
    }).catch(() => {});
  }, []);
  if (!s) return <p className="mt-6 text-foreground-secondary">{t("loading")}</p>;

  const save = async () => {
    try {
      const updated = await user.updateSettings({
        max_sync_size_bytes: mibToBytes(Number(maxMiB) || 0),
        allowed_types: s.allowed_types,
        max_auto_upload_size_bytes: mibToBytes(Number(upMiB) || 0),
        max_auto_download_size_bytes: mibToBytes(Number(downMiB) || 0),
        file_ttl_days: s.file_ttl_days,
      });
      setS(updated);
      toastOK(t("settingsSaved"));
    } catch (e) {
      toastErr(t("opFailed"), errText(e, t));
    }
  };

  return (
    <Card variant="default" className="surface-card mt-4 rounded-2xl">
      <Card.Content className="px-5 py-5">
        <div className="flex flex-col gap-5">
          <div>
            <p className="mb-2 text-sm font-medium text-foreground">{t("syncTypes")}</p>
            <CheckboxGroup
              aria-label={t("syncTypes")}
              className="grid grid-cols-2 gap-3 sm:grid-cols-4"
              value={s.allowed_types as string[]}
              onChange={(vals: string[]) => setS({ ...s, allowed_types: vals as ContentType[] })}
            >
              {TYPE_OPTIONS.map((opt) => (
                <Checkbox
                  key={opt.value}
                  value={opt.value}
                  variant="secondary"
                  className="group relative min-h-24 w-full rounded-2xl border border-default-200 bg-default-50/50 px-4 py-3 transition data-[selected=true]:border-accent data-[selected=true]:bg-accent/10"
                >
                  <Checkbox.Control className="absolute right-3 top-3 size-5 rounded-md">
                    <Checkbox.Indicator />
                  </Checkbox.Control>
                  <Checkbox.Content className="flex min-h-16 w-full flex-col items-start justify-center gap-1 pr-10 text-left">
                    <Label className="block text-base font-medium leading-tight">{t(opt.titleKey)}</Label>
                    <Description className="block text-sm leading-snug text-foreground-secondary">{t(opt.descKey)}</Description>
                  </Checkbox.Content>
                </Checkbox>
              ))}
            </CheckboxGroup>
          </div>
          <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
            <TextField value={maxMiB} onChange={(v) => setMaxMiB(v.replace(/[^\d.]/g, ""))}><Label>{t("maxSyncSize")} (MiB)</Label><Input inputMode="decimal" /></TextField>
            <TextField value={upMiB} onChange={(v) => setUpMiB(v.replace(/[^\d.]/g, ""))}><Label>{t("maxAutoUpload")} (MiB)</Label><Input inputMode="decimal" /></TextField>
            <TextField value={downMiB} onChange={(v) => setDownMiB(v.replace(/[^\d.]/g, ""))}><Label>{t("maxAutoDownload")} (MiB)</Label><Input inputMode="decimal" /></TextField>
          </div>
          <p className="rounded-xl bg-default-100/50 px-3 py-2 text-sm text-foreground-secondary">{t("policyHint")}</p>
          <div><Button onPress={() => void save()}>{t("save")}</Button></div>
        </div>
      </Card.Content>
    </Card>
  );
}

// Account 修改密码（退出登录已移至顶栏）。
function Account({ me }: { me: Me }) {
  const { t } = useI18n();
  const [cur, setCur] = useState("");
  const [next, setNext] = useState("");

  const change = async () => {
    if (next.length < 8) return toastErr(t("opFailed"), t("passwordRule"));
    try {
      await auth.changePassword(cur, next);
      setCur("");
      setNext("");
      toastOK(t("passwordChanged"));
    } catch (e) {
      toastErr(t("opFailed"), errText(e, t));
    }
  };

  return (
    <Card variant="default" className="surface-card mt-4 rounded-2xl">
      <Card.Content className="px-5 py-5">
        <div className="flex max-w-md flex-col gap-4">
          <Row label={t("username")} value={me.username} />
          <TextField value={cur} onChange={setCur} type="password"><Label>{t("currentPassword")}</Label><Input type="password" /></TextField>
          <TextField value={next} onChange={setNext} type="password"><Label>{t("newPassword")}</Label><Input type="password" /></TextField>
          <p className="text-xs text-foreground-secondary">{t("passwordRule")}</p>
          <div><Button onPress={() => void change()}>{t("changePassword")}</Button></div>
        </div>
      </Card.Content>
    </Card>
  );
}
