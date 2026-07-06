import { useEffect, useRef, useState } from "react";
import { Button, Card, Checkbox, CheckboxGroup, Description, Input, Label, Modal, Tabs, TextField } from "@heroui/react";
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
        <Tabs.Panel id="pairing"><Pairing /></Tabs.Panel>
        <Tabs.Panel id="policy"><SyncPolicy /></Tabs.Panel>
        <Tabs.Panel id="account"><Account me={me} /></Tabs.Panel>
      </ConsoleShell>
      {/* 全局：检测到新的待确认设备时弹窗（贯穿所有 tab） */}
      <PendingWatcher />
    </Tabs>
  );
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

// PendingWatcher 轮询待确认设备，出现新请求时弹窗（确认/拒绝），不再常驻列表。
function PendingWatcher() {
  const { t } = useI18n();
  const [selected, setSelected] = useState<PendingRequest | null>(null);
  const seen = useRef<Set<string>>(new Set());
  const firstLoad = useRef(true);

  useEffect(() => {
    const tick = () =>
      void user.getCurrentPairingCode().then((c) => {
        const pending = c.pending_requests ?? [];
        // 首次加载只记录已有项，避免一进页面就弹历史请求。
        if (firstLoad.current) {
          pending.forEach((p) => seen.current.add(p.request_id));
          firstLoad.current = false;
          return;
        }
        // 清掉已不在待确认列表中的 id（已处理/已过期），避免 seen 无限增长。
        const ids = new Set(pending.map((p) => p.request_id));
        seen.current.forEach((rid) => ids.has(rid) || seen.current.delete(rid));
        // 已有弹窗时不开新弹窗、也不提前标记已读，留到当前处理完后再弹下一个。
        // 弹窗本身即提示，不再额外弹 toast（否则确认后 toast 仍滞留，造成「第一个弹窗
        // 还显示在那里」的困惑）。
        if (selected) return;
        const fresh = pending.find((p) => !seen.current.has(p.request_id));
        if (fresh) {
          seen.current.add(fresh.request_id);
          setSelected(fresh);
        }
      }).catch(() => {});
    tick();
    const id = setInterval(tick, 3000);
    return () => clearInterval(id);
  }, [selected]);

  const confirm = async (id: string) => {
    setSelected(null); // 立即关闭弹窗，不等待网络往返（避免请求慢/挂起时弹窗滞留）
    try {
      await user.confirmRequest(id);
      toastOK(t("pairingConfirmed"));
    } catch (e) {
      toastErr(t("opFailed"), errText(e, t));
    }
  };
  const reject = async (id: string) => {
    setSelected(null);
    try {
      await user.rejectRequest(id);
      toastOK(t("pairingRejected"));
    } catch (e) {
      toastErr(t("opFailed"), errText(e, t));
    }
  };

  return (
    <Modal>
      <Modal.Backdrop isOpen={selected !== null} onOpenChange={(o) => !o && setSelected(null)}>
        <Modal.Container>
          <Modal.Dialog>
            <Modal.CloseTrigger />
            <Modal.Header><Modal.Heading>{t("newPairingRequest")}</Modal.Heading></Modal.Header>
            <Modal.Body>
              {selected && (
                <div className="flex flex-col gap-1">
                  <Row label={t("username")} value={selected.device_name} />
                  <Row label={t("platform")} value={`${selected.platform} ${selected.client_version}`} />
                  <Row label={t("requestTime")} value={formatTime(selected.created_at)} />
                  <Row label={t("keyFingerprint")} value={<code className="mono text-xs break-all">{selected.key_fingerprint}</code>} />
                </div>
              )}
            </Modal.Body>
            <Modal.Footer>
              {selected && <Button variant="danger-soft" onPress={() => void reject(selected.request_id)}>{t("reject")}</Button>}
              {selected && <Button onPress={() => void confirm(selected.request_id)}>{t("confirm")}</Button>}
            </Modal.Footer>
          </Modal.Dialog>
        </Modal.Container>
      </Modal.Backdrop>
    </Modal>
  );
}

// Pairing 生成配对码并倒计时展示（待确认设备由全局弹窗处理）。
function Pairing() {
  const { t } = useI18n();
  const [current, setCurrent] = useState<CurrentPairingCode | null>(null);
  const [created, setCreated] = useState<CreatedPairingCode | null>(null);
  const [remaining, setRemaining] = useState(0);

  const reload = () => void user.getCurrentPairingCode().then(setCurrent).catch(() => {});
  useEffect(() => {
    reload();
    const id = setInterval(reload, 3000);
    return () => clearInterval(id);
  }, []);

  const expiresAt = created?.expires_at ?? current?.expires_at;
  useEffect(() => {
    const tick = () => setRemaining(secondsUntil(expiresAt));
    tick();
    const id = setInterval(tick, 1000);
    return () => clearInterval(id);
  }, [expiresAt]);

  const generate = async () => {
    try {
      setCreated(await user.createPairingCode());
      toastOK(t("codeCreated"));
      reload();
    } catch (e) {
      toastErr(t("opFailed"), errText(e, t));
    }
  };
  const cancel = async () => {
    try {
      await user.cancelPairingCode();
      setCreated(null);
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
                  className="group relative rounded-2xl border border-default-200 bg-default-50/50 px-4 py-3 transition data-[selected=true]:border-accent data-[selected=true]:bg-accent/10"
                >
                  <Checkbox.Control className="absolute right-3 top-3 size-5 rounded-md">
                    <Checkbox.Indicator />
                  </Checkbox.Control>
                  <Checkbox.Content>
                    <Label className="font-medium">{t(opt.titleKey)}</Label>
                    <Description className="text-xs text-foreground-secondary">{t(opt.descKey)}</Description>
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
