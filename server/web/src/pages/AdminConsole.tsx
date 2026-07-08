import { useEffect, useState } from "react";
import { Button, Card, Checkbox, CheckboxGroup, Description, Input, Label, ListBox, Modal, Select, Table, Tabs, TextField } from "@heroui/react";
import { RiArrowLeftSLine, RiArrowRightSLine, RiSearchLine } from "@remixicon/react";
import { admin, type AdminStats, type AuditLogPage, type ContentType, type DeviceView, type Me, type ServerSettings, type SyncLogPage, type UserView } from "../api";
import { useI18n } from "../i18n";
import { toastErr, toastOK, errText } from "../notify";
import { ConsoleShell, OnlineDot, Row, StatusChip } from "../components/Shell";
import { bytesToMiB, formatBytes, formatTime, mibToBytes, relativeTime } from "../util";

const usernameOK = (s: string) => /^[A-Za-z0-9_.-]{3,32}$/.test(s.trim());
const passwordOK = (s: string) => s.length >= 8;

// AdminConsole 是管理员后台：总览、用户管理、实例设置、同步日志。
export function AdminConsole({ me }: { me: Me }) {
  const { t } = useI18n();
  const [tab, setTab] = useState("overview");
  return (
    <Tabs selectedKey={tab} onSelectionChange={(k) => setTab(String(k))}>
      <ConsoleShell
        subtitle={`${t("admin")} · ${me.username}`}
        center={
          <Tabs.ListContainer>
            <Tabs.List aria-label="admin">
              <Tabs.Tab id="overview">{t("overview")}<Tabs.Indicator /></Tabs.Tab>
              <Tabs.Tab id="users"><Tabs.Separator />{t("users")}<Tabs.Indicator /></Tabs.Tab>
              <Tabs.Tab id="logs"><Tabs.Separator />{t("syncLogs")}<Tabs.Indicator /></Tabs.Tab>
              <Tabs.Tab id="audit"><Tabs.Separator />{t("auditLogs")}<Tabs.Indicator /></Tabs.Tab>
              <Tabs.Tab id="settings"><Tabs.Separator />{t("instanceSettings")}<Tabs.Indicator /></Tabs.Tab>
            </Tabs.List>
          </Tabs.ListContainer>
        }
      >
        <Tabs.Panel id="overview"><AdminOverview /></Tabs.Panel>
        <Tabs.Panel id="users"><AdminUsers /></Tabs.Panel>
        <Tabs.Panel id="logs"><SyncLogs /></Tabs.Panel>
        <Tabs.Panel id="audit"><AuditLogs /></Tabs.Panel>
        <Tabs.Panel id="settings"><AdminSettings /></Tabs.Panel>
      </ConsoleShell>
    </Tabs>
  );
}

// Stat 是一个强调数值的小卡片。
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

// AdminOverview 展示实例摘要、用户/设备计数与在线设备数。
function AdminOverview() {
  const { t } = useI18n();
  const [s, setS] = useState<ServerSettings | null>(null);
  const [stats, setStats] = useState<AdminStats | null>(null);

  useEffect(() => {
    void admin.getSettings().then(setS).catch(() => {});
    const load = () => void admin.stats().then(setStats).catch(() => {});
    load();
    const id = setInterval(load, 5000);
    return () => clearInterval(id);
  }, []);

  if (!s) return <Loading t={t} />;
  return (
    <div className="mt-4 grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
      <Stat label={t("userCount")} value={String(stats?.user_count ?? "—")} />
      <Stat label={t("deviceCount")} value={String(stats?.device_count ?? "—")} />
      <Stat label={t("onlineDeviceCount")} value={String(stats?.online_device_count ?? "—")} />
      <Stat label={t("maxSyncSize")} value={formatBytes(s.max_sync_size_bytes)} />
      <div className="sm:col-span-2 lg:col-span-4">
        <Card variant="default" className="surface-card rounded-2xl">
          <Card.Content className="px-5 py-3">
            <Row label={t("serverId")} value={<code className="mono text-xs">{s.server_id}</code>} />
            <Row label={t("serverName")} value={s.server_name} />
            <Row label={t("ciphertextTTL")} value={`${s.ciphertext_ttl_seconds}s`} />
            <Row label={t("logRetention")} value={`${s.sync_log_retention_days} ${t("days")}`} />
          </Card.Content>
        </Card>
      </div>
    </div>
  );
}

// AdminUsers 管理用户与按用户的设备。
function AdminUsers() {
  const { t } = useI18n();
  const [users, setUsers] = useState<UserView[]>([]);
  const [viewing, setViewing] = useState<UserView | null>(null);
  const [newName, setNewName] = useState("");
  const [newPw, setNewPw] = useState("");
  const [reveal, setReveal] = useState<{ username: string; password: string } | null>(null);
  const [confirmReset, setConfirmReset] = useState<UserView | null>(null);

  const reload = () => void admin.listUsers().then((r) => setUsers(r.users)).catch(() => {});
  useEffect(reload, []);

  const create = async () => {
    if (!usernameOK(newName)) return toastErr(t("opFailed"), t("usernameRule"));
    if (!passwordOK(newPw)) return toastErr(t("opFailed"), t("passwordRule"));
    try {
      await admin.createUser(newName.trim(), newPw);
      setNewName("");
      setNewPw("");
      toastOK(t("userCreated"));
      reload();
    } catch (e) {
      toastErr(t("opFailed"), errText(e, t));
    }
  };

  const toggle = async (u: UserView) => {
    try {
      await admin.updateUser(u.id, { status: u.status === "active" ? "disabled" : "active" });
      toastOK(t("opSuccess"));
      reload();
    } catch (e) {
      toastErr(t("opFailed"), errText(e, t));
    }
  };

  const resetPw = async (u: UserView) => {
    try {
      const { password } = await admin.resetUserPassword(u.id);
      setConfirmReset(null);
      setReveal({ username: u.username, password });
    } catch (e) {
      toastErr(t("opFailed"), errText(e, t));
    }
  };

  if (viewing) return <AdminUserDevices user={viewing} onBack={() => setViewing(null)} />;

  return (
    <div className="mt-4 flex flex-col gap-4">
      <Card variant="default" className="surface-card rounded-2xl">
        <Card.Header className="px-5 pt-5"><Card.Title>{t("createUser")}</Card.Title></Card.Header>
        <Card.Content className="px-5 pb-5">
          <div className="flex flex-wrap items-end gap-3">
            <TextField value={newName} onChange={setNewName} className="flex-1">
              <Label>{t("username")}</Label>
              <Input placeholder={t("username")} />
            </TextField>
            <TextField value={newPw} onChange={setNewPw} type="password" className="flex-1">
              <Label>{t("password")}</Label>
              <Input type="password" placeholder={t("password")} />
            </TextField>
            <Button onPress={() => void create()}>{t("create")}</Button>
          </div>
          <p className="mt-2 text-xs text-foreground-secondary">{t("usernameRule")} · {t("passwordRule")}</p>
        </Card.Content>
      </Card>

      <Card variant="default" className="surface-card rounded-2xl">
        <Card.Content className="p-2">
          <Table>
            <Table.ScrollContainer>
              <Table.Content aria-label={t("users")}>
                <Table.Header>
                  <Table.Column>{t("username")}</Table.Column>
                  <Table.Column>{t("status")}</Table.Column>
                  <Table.Column>{t("createdAt")}</Table.Column>
                  <Table.Column> </Table.Column>
                </Table.Header>
                <Table.Body>
                  {users.map((u) => (
                    <Table.Row key={u.id}>
                      <Table.Cell>{u.username}</Table.Cell>
                      <Table.Cell><StatusChip status={u.status} /></Table.Cell>
                      <Table.Cell>{formatTime(u.created_at)}</Table.Cell>
                      <Table.Cell>
                        <div className="flex flex-wrap justify-end gap-2">
                          <Button size="sm" variant="secondary" onPress={() => setViewing(u)}>{t("viewDevices")}</Button>
                          <Button size="sm" variant="outline" onPress={() => setConfirmReset(u)}>{t("resetPassword")}</Button>
                          <Button size="sm" variant={u.status === "active" ? "danger-soft" : "secondary"} onPress={() => void toggle(u)}>
                            {u.status === "active" ? t("disable") : t("enable")}
                          </Button>
                        </div>
                      </Table.Cell>
                    </Table.Row>
                  ))}
                </Table.Body>
              </Table.Content>
            </Table.ScrollContainer>
          </Table>
        </Card.Content>
      </Card>

      <ConfirmModal
        open={confirmReset !== null}
        title={t("resetConfirmTitle")}
        body={`${confirmReset?.username ?? ""} — ${t("resetConfirmBody")}`}
        confirmLabel={t("resetPassword")}
        danger
        onConfirm={() => confirmReset && void resetPw(confirmReset)}
        onClose={() => setConfirmReset(null)}
      />
      <PasswordRevealModal reveal={reveal} onClose={() => setReveal(null)} />
    </div>
  );
}

// ConfirmModal 是通用二次确认对话框。
export function ConfirmModal({
  open,
  title,
  body,
  confirmLabel,
  danger,
  onConfirm,
  onClose,
}: {
  open: boolean;
  title: string;
  body: string;
  confirmLabel: string;
  danger?: boolean;
  onConfirm: () => void;
  onClose: () => void;
}) {
  const { t } = useI18n();
  // 受控弹窗：Modal.Backdrop 直接作根节点（外包 <Modal> 会与 isOpen 状态冲突导致关不掉）。
  return (
    <Modal.Backdrop isOpen={open} onOpenChange={(o) => !o && onClose()}>
      <Modal.Container>
        <Modal.Dialog>
          <Modal.CloseTrigger />
          <Modal.Header><Modal.Heading>{title}</Modal.Heading></Modal.Header>
          <Modal.Body><p className="text-sm text-foreground-secondary">{body}</p></Modal.Body>
          <Modal.Footer>
            <Button variant="tertiary" onPress={onClose}>{t("cancel")}</Button>
            <Button variant={danger ? "danger" : "primary"} onPress={onConfirm}>{confirmLabel}</Button>
          </Modal.Footer>
        </Modal.Dialog>
      </Modal.Container>
    </Modal.Backdrop>
  );
}

// PasswordRevealModal 一次性展示重置后的新密码，并提供复制。
function PasswordRevealModal({ reveal, onClose }: { reveal: { username: string; password: string } | null; onClose: () => void }) {
  const { t } = useI18n();
  // 受控弹窗：Modal.Backdrop 直接作根节点（外包 <Modal> 会与 isOpen 状态冲突导致关不掉）。
  return (
    <Modal.Backdrop isOpen={reveal !== null} onOpenChange={(o) => !o && onClose()}>
      <Modal.Container>
        <Modal.Dialog>
          <Modal.CloseTrigger />
          <Modal.Header><Modal.Heading>{t("newPasswordTitle")}</Modal.Heading></Modal.Header>
          <Modal.Body>
            {reveal && (
              <div className="flex flex-col gap-3">
                <p className="text-sm text-foreground-secondary">{reveal.username}</p>
                <div className="flex items-center gap-2">
                  <code className="mono flex-1 break-all rounded-xl bg-default-100 px-3 py-2 text-sm">{reveal.password}</code>
                  <Button size="sm" variant="secondary" onPress={() => void navigator.clipboard?.writeText(reveal.password).then(() => toastOK(t("copied")))}>
                    {t("copy")}
                  </Button>
                </div>
              </div>
            )}
          </Modal.Body>
          <Modal.Footer>
            <Button onPress={onClose}>{t("done")}</Button>
          </Modal.Footer>
        </Modal.Dialog>
      </Modal.Container>
    </Modal.Backdrop>
  );
}

// AdminUserDevices 列出并管理某用户的设备。
function AdminUserDevices({ user, onBack }: { user: UserView; onBack: () => void }) {
  const { t } = useI18n();
  const [devices, setDevices] = useState<DeviceView[]>([]);
  const reload = () => void admin.listUserDevices(user.id).then((r) => setDevices(r.devices)).catch(() => {});
  useEffect(reload, [user.id]);

  const onAction = async (d: DeviceView, action: DeviceAction) => {
    try {
      if (action === "delete") await admin.deleteDevice(d.id);
      else if (action === "revoke") await admin.updateDevice(d.id, { status: "revoked" });
      else await admin.updateDevice(d.id, { status: action === "disable" ? "disabled" : "active" });
      toastOK(t("deviceUpdated"));
      reload();
    } catch (e) {
      toastErr(t("opFailed"), errText(e, t));
    }
  };

  return (
    <div className="mt-4 flex flex-col gap-4">
      <div className="flex items-center gap-3">
        <Button size="sm" variant="ghost" onPress={onBack}>← {t("back")}</Button>
        <span className="font-medium text-foreground">{user.username}</span>
      </div>
      <Card variant="default" className="surface-card rounded-2xl">
        <Card.Content className="p-2"><DeviceTable devices={devices} onAction={onAction} allowRevoke /></Card.Content>
      </Card>
    </div>
  );
}

export type DeviceAction = "enable" | "disable" | "revoke" | "delete";

// DeviceTable 是设备列表的共用展示（管理员与用户后台复用），含在线状态与删除。
export function DeviceTable({
  devices,
  onAction,
  allowRevoke,
}: {
  devices: DeviceView[];
  onAction: (d: DeviceView, action: DeviceAction) => void;
  allowRevoke?: boolean;
}) {
  const { t, lang } = useI18n();
  return (
    <Table>
      <Table.ScrollContainer>
        <Table.Content aria-label={t("devices")}>
          <Table.Header>
            <Table.Column>{t("username")}</Table.Column>
            <Table.Column>{t("platform")}</Table.Column>
            <Table.Column>{t("status")}</Table.Column>
            <Table.Column>{t("lastSeen")}</Table.Column>
            <Table.Column>{t("keyFingerprint")}</Table.Column>
            <Table.Column> </Table.Column>
          </Table.Header>
          <Table.Body>
            {devices.map((d) => (
              <Table.Row key={d.id}>
                <Table.Cell>{d.name}</Table.Cell>
                <Table.Cell>{d.platform} {d.client_version}</Table.Cell>
                <Table.Cell><StatusChip status={d.status} /></Table.Cell>
                <Table.Cell><OnlineDot online={d.online} label={d.online ? t("online") : relativeTime(d.last_seen_at, lang)} title={formatTime(d.last_seen_at)} /></Table.Cell>
                <Table.Cell><code className="mono text-xs">{d.key_fingerprint}</code></Table.Cell>
                <Table.Cell>
                  <div className="flex flex-wrap justify-end gap-2">
                    {d.status === "revoked" ? (
                      <Button size="sm" variant="danger" onPress={() => onAction(d, "delete")}>{t("delete")}</Button>
                    ) : (
                      <>
                        <Button size="sm" variant={d.status === "active" ? "danger-soft" : "secondary"} onPress={() => onAction(d, d.status === "active" ? "disable" : "enable")}>
                          {d.status === "active" ? t("disable") : t("enable")}
                        </Button>
                        {allowRevoke && (
                          <Button size="sm" variant="danger" onPress={() => onAction(d, "revoke")}>{t("revoke")}</Button>
                        )}
                      </>
                    )}
                  </div>
                </Table.Cell>
              </Table.Row>
            ))}
          </Table.Body>
        </Table.Content>
      </Table.ScrollContainer>
    </Table>
  );
}

// SyncLogs 展示同步日志（无明文）：含用户名/设备名，支持搜索、按结果筛选与翻页。
function SyncLogs() {
  const { t, lang } = useI18n();
  const [data, setData] = useState<SyncLogPage | null>(null);
  const [page, setPage] = useState(1);
  const [result, setResult] = useState<"all" | "success" | "failure">("all");
  const [q, setQ] = useState("");

  // 搜索/筛选变化时回到第 1 页（带轻量防抖）。
  useEffect(() => {
    setPage(1);
  }, [q, result]);

  // 日志按需加载：搜索输入防抖；不再自动轮询刷新（翻页/筛选/搜索时重新拉取）。
  useEffect(() => {
    const load = () =>
      void admin
        .syncLogs({ page, result: result === "all" ? undefined : result, q: q.trim() || undefined })
        .then(setData)
        .catch(() => {});
    const id = setTimeout(load, q ? 300 : 0); // 输入搜索时防抖
    return () => clearTimeout(id);
  }, [page, result, q]);

  const logs = data?.logs ?? [];
  const total = data?.total ?? 0;
  const pageSize = data?.page_size ?? 20;
  const totalPages = Math.max(1, Math.ceil(total / pageSize));
  const deviceLabel = (l: SyncLogPage["logs"][number]) =>
    [l.source_device_name, l.target_device_name].filter(Boolean).join(" → ") || "—";

  return (
    <div className="mt-4 flex flex-col gap-3">
      <div className="flex flex-wrap items-end gap-3">
        <TextField value={q} onChange={setQ} className="min-w-[220px] flex-1" aria-label={t("syncLogs")}>
          <Input placeholder={`${t("username")} / ${t("devices")} / ${t("event")}`} />
        </TextField>
        <Select aria-label={t("result")} selectedKey={result} onSelectionChange={(k) => setResult(k as typeof result)} className="w-[150px]">
          <Select.Trigger><Select.Value /><Select.Indicator /></Select.Trigger>
          <Select.Popover>
            <ListBox>
              <ListBox.Item id="all" textValue={t("all")}>{t("all")}<ListBox.ItemIndicator /></ListBox.Item>
              <ListBox.Item id="success" textValue={t("success")}>{t("success")}<ListBox.ItemIndicator /></ListBox.Item>
              <ListBox.Item id="failure" textValue={t("failure")}>{t("failure")}<ListBox.ItemIndicator /></ListBox.Item>
            </ListBox>
          </Select.Popover>
        </Select>
        <div className="ml-auto flex items-center gap-1 self-center">
          <span title={t("back")} className="inline-flex">
            <Button size="sm" variant="ghost" isDisabled={page <= 1} onPress={() => setPage((p) => Math.max(1, p - 1))} aria-label="prev">
              <RiArrowLeftSLine size={18} />
            </Button>
          </span>
          <span className="min-w-[64px] text-center text-sm text-foreground-secondary">{page} / {totalPages}</span>
          <span title={t("done")} className="inline-flex">
            <Button size="sm" variant="ghost" isDisabled={page >= totalPages} onPress={() => setPage((p) => Math.min(totalPages, p + 1))} aria-label="next">
              <RiArrowRightSLine size={18} />
            </Button>
          </span>
        </div>
      </div>

      <Card variant="default" className="surface-card rounded-2xl">
        <Card.Content className="p-2">
          {logs.length === 0 ? (
            <p className="flex items-center justify-center gap-2 px-3 py-8 text-center text-sm text-foreground-secondary">
              <RiSearchLine size={16} /> {t("noLogs")}
            </p>
          ) : (
            <Table>
              <Table.ScrollContainer>
                <Table.Content aria-label={t("syncLogs")}>
                  <Table.Header>
                    <Table.Column>{t("time")}</Table.Column>
                    <Table.Column>{t("user")}</Table.Column>
                    <Table.Column>{t("devices")}</Table.Column>
                    <Table.Column>{t("event")}</Table.Column>
                    <Table.Column>{t("syncTypes")}</Table.Column>
                    <Table.Column>{t("size")}</Table.Column>
                    <Table.Column>{t("result")}</Table.Column>
                  </Table.Header>
                  <Table.Body>
                    {logs.map((l) => (
                      <Table.Row key={l.id}>
                        <Table.Cell><span title={formatTime(l.created_at)}>{relativeTime(l.created_at, lang)}</span></Table.Cell>
                        <Table.Cell>{l.username || "—"}</Table.Cell>
                        <Table.Cell>{deviceLabel(l)}</Table.Cell>
                        <Table.Cell>{l.event_type}</Table.Cell>
                        <Table.Cell>{l.content_type ? t(l.content_type as "text") : "—"}</Table.Cell>
                        <Table.Cell>{l.ciphertext_size_bytes ? formatBytes(l.ciphertext_size_bytes) : "—"}</Table.Cell>
                        <Table.Cell>
                          <span className={l.result === "success" ? "text-success" : "text-danger"}>
                            {l.result === "success" ? t("success") : t("failure")}
                            {l.error_code ? ` · ${l.error_code}` : ""}
                          </span>
                        </Table.Cell>
                      </Table.Row>
                    ))}
                  </Table.Body>
                </Table.Content>
              </Table.ScrollContainer>
            </Table>
          )}
        </Card.Content>
      </Card>
    </div>
  );
}

// 操作日志动作码的双语标签；未知动作码回退原样展示。
const AUDIT_ACTION_LABELS: Record<string, { zh: string; en: string }> = {
  "user.create": { zh: "创建用户", en: "Create user" },
  "user.rename": { zh: "重命名用户", en: "Rename user" },
  "user.enable": { zh: "启用用户", en: "Enable user" },
  "user.disable": { zh: "禁用用户", en: "Disable user" },
  "user.reset_password": { zh: "重置用户密码", en: "Reset user password" },
  "user.update_settings": { zh: "更新同步策略", en: "Update sync policy" },
  "admin.rename": { zh: "修改管理员用户名", en: "Rename admin" },
  "account.change_password": { zh: "修改密码", en: "Change password" },
  "device.rename": { zh: "重命名设备", en: "Rename device" },
  "device.enable": { zh: "启用设备", en: "Enable device" },
  "device.disable": { zh: "禁用设备", en: "Disable device" },
  "device.revoke": { zh: "吊销设备", en: "Revoke device" },
  "device.delete": { zh: "删除设备", en: "Delete device" },
  "settings.update": { zh: "修改实例设置", en: "Update instance settings" },
  "pairing.code_create": { zh: "生成配对码", en: "Create pairing code" },
  "pairing.confirm": { zh: "确认配对", en: "Confirm pairing" },
  "pairing.reject": { zh: "拒绝配对", en: "Reject pairing" },
};

// AuditLogs 展示操作日志（管理员操作与用户关键操作，不含敏感值），
// 支持按操作者类型筛选、搜索与翻页，交互与同步日志页一致。
function AuditLogs() {
  const { t, lang } = useI18n();
  const [data, setData] = useState<AuditLogPage | null>(null);
  const [page, setPage] = useState(1);
  const [actor, setActor] = useState<"all" | "admin" | "user">("all");
  const [q, setQ] = useState("");

  // 搜索/筛选变化时回到第 1 页。
  useEffect(() => {
    setPage(1);
  }, [q, actor]);

  // 按需加载：搜索输入防抖；翻页/筛选/搜索时重新拉取。
  useEffect(() => {
    const load = () =>
      void admin
        .auditLogs({ page, actor: actor === "all" ? undefined : actor, q: q.trim() || undefined })
        .then(setData)
        .catch(() => {});
    const id = setTimeout(load, q ? 300 : 0);
    return () => clearTimeout(id);
  }, [page, actor, q]);

  const logs = data?.logs ?? [];
  const total = data?.total ?? 0;
  const pageSize = data?.page_size ?? 20;
  const totalPages = Math.max(1, Math.ceil(total / pageSize));
  // actionLabel 把动作码译为界面语言，未知码原样返回。
  const actionLabel = (a: string) => AUDIT_ACTION_LABELS[a]?.[lang] ?? a;

  return (
    <div className="mt-4 flex flex-col gap-3">
      <div className="flex flex-wrap items-end gap-3">
        <TextField value={q} onChange={setQ} className="min-w-[220px] flex-1" aria-label={t("auditLogs")}>
          <Input placeholder={`${t("actorCol")} / ${t("actionCol")} / ${t("targetCol")}`} />
        </TextField>
        <Select aria-label={t("actorCol")} selectedKey={actor} onSelectionChange={(k) => setActor(k as typeof actor)} className="w-[150px]">
          <Select.Trigger><Select.Value /><Select.Indicator /></Select.Trigger>
          <Select.Popover>
            <ListBox>
              <ListBox.Item id="all" textValue={t("all")}>{t("all")}<ListBox.ItemIndicator /></ListBox.Item>
              <ListBox.Item id="admin" textValue={t("admin")}>{t("admin")}<ListBox.ItemIndicator /></ListBox.Item>
              <ListBox.Item id="user" textValue={t("user")}>{t("user")}<ListBox.ItemIndicator /></ListBox.Item>
            </ListBox>
          </Select.Popover>
        </Select>
        <div className="ml-auto flex items-center gap-1 self-center">
          <span className="inline-flex">
            <Button size="sm" variant="ghost" isDisabled={page <= 1} onPress={() => setPage((p) => Math.max(1, p - 1))} aria-label="prev">
              <RiArrowLeftSLine size={18} />
            </Button>
          </span>
          <span className="min-w-[64px] text-center text-sm text-foreground-secondary">{page} / {totalPages}</span>
          <span className="inline-flex">
            <Button size="sm" variant="ghost" isDisabled={page >= totalPages} onPress={() => setPage((p) => Math.min(totalPages, p + 1))} aria-label="next">
              <RiArrowRightSLine size={18} />
            </Button>
          </span>
        </div>
      </div>

      <Card variant="default" className="surface-card rounded-2xl">
        <Card.Content className="p-2">
          {logs.length === 0 ? (
            <p className="flex items-center justify-center gap-2 px-3 py-8 text-center text-sm text-foreground-secondary">
              <RiSearchLine size={16} /> {t("noAuditLogs")}
            </p>
          ) : (
            <Table>
              <Table.ScrollContainer>
                <Table.Content aria-label={t("auditLogs")}>
                  <Table.Header>
                    <Table.Column>{t("time")}</Table.Column>
                    <Table.Column>{t("actorCol")}</Table.Column>
                    <Table.Column>{t("actionCol")}</Table.Column>
                    <Table.Column>{t("targetCol")}</Table.Column>
                    <Table.Column>{t("detailCol")}</Table.Column>
                  </Table.Header>
                  <Table.Body>
                    {logs.map((l) => (
                      <Table.Row key={l.id}>
                        <Table.Cell><span title={formatTime(l.created_at)}>{relativeTime(l.created_at, lang)}</span></Table.Cell>
                        <Table.Cell>
                          {(l.actor_type === "admin" ? t("admin") : t("user")) + (l.actor_name ? ` · ${l.actor_name}` : "")}
                        </Table.Cell>
                        <Table.Cell>{actionLabel(l.action)}</Table.Cell>
                        <Table.Cell>{l.target_name || "—"}</Table.Cell>
                        <Table.Cell className="text-foreground-secondary">{l.detail || "—"}</Table.Cell>
                      </Table.Row>
                    ))}
                  </Table.Body>
                </Table.Content>
              </Table.ScrollContainer>
            </Table>
          )}
        </Card.Content>
      </Card>
    </div>
  );
}

// 实例级同步类型选项（与用户端一致的四种）。
const INSTANCE_TYPE_OPTIONS: { value: ContentType; titleKey: "text" | "image" | "file" | "rich_text"; descKey: "textDesc" | "imageDesc" | "fileDesc" | "richTextDesc" }[] = [
  { value: "text", titleKey: "text", descKey: "textDesc" },
  { value: "image", titleKey: "image", descKey: "imageDesc" },
  { value: "file", titleKey: "file", descKey: "fileDesc" },
  { value: "rich_text", titleKey: "rich_text", descKey: "richTextDesc" },
];

// AdminSettings 维护实例配置（自助注册已移除）。
function AdminSettings() {
  const { t } = useI18n();
  const [s, setS] = useState<ServerSettings | null>(null);
  const [maxMiB, setMaxMiB] = useState("");

  useEffect(() => {
    void admin.getSettings().then((v) => {
      setS(v);
      setMaxMiB(String(bytesToMiB(v.max_sync_size_bytes)));
    }).catch(() => {});
  }, []);

  if (!s) return <Loading t={t} />;

  const save = async () => {
    try {
      const updated = await admin.updateSettings({
        server_name: s.server_name,
        max_sync_size_bytes: mibToBytes(Number(maxMiB) || 0),
        allowed_types: s.allowed_types,
        sync_log_retention_days: s.sync_log_retention_days,
      });
      setS(updated);
      setMaxMiB(String(bytesToMiB(updated.max_sync_size_bytes)));
      toastOK(t("settingsSaved"));
    } catch (e) {
      toastErr(t("opFailed"), errText(e, t));
    }
  };

  return (
    <Card variant="default" className="surface-card mt-4 rounded-2xl">
      <Card.Content className="px-5 py-5">
        <div className="flex max-w-4xl flex-col gap-4">
          <TextField value={s.server_name} onChange={(v) => setS({ ...s, server_name: v })} className="max-w-md">
            <Label>{t("serverName")}</Label>
            <Input />
          </TextField>
          <TextField value={maxMiB} onChange={(v) => setMaxMiB(v.replace(/[^\d.]/g, ""))} className="max-w-md">
            <Label>{t("maxSyncSize")} (MiB)</Label>
            <Input inputMode="decimal" />
          </TextField>
          <TextField value={String(s.sync_log_retention_days)} onChange={(v) => setS({ ...s, sync_log_retention_days: Number(v.replace(/[^\d]/g, "")) || 0 })} className="max-w-md">
            <Label>{t("logRetention")}</Label>
            <Input inputMode="numeric" />
          </TextField>
          <div>
            <p className="mb-1 text-sm font-medium text-foreground">{t("syncTypes")}</p>
            <p className="mb-2 text-xs text-foreground-secondary">{t("instanceTypesHint")}</p>
            <CheckboxGroup
              aria-label={t("syncTypes")}
              className="grid grid-cols-2 gap-3 sm:grid-cols-4"
              value={s.allowed_types as string[]}
              onChange={(vals: string[]) => setS({ ...s, allowed_types: vals as ContentType[] })}
            >
              {INSTANCE_TYPE_OPTIONS.map((opt) => (
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
          <p className="rounded-xl bg-warning/10 px-3 py-2 text-sm text-warning">{t("sizeChangeHint")}</p>
          <div><Button onPress={() => void save()}>{t("save")}</Button></div>
        </div>
      </Card.Content>
    </Card>
  );
}

function Loading({ t }: { t: (k: "loading") => string }) {
  return <p className="mt-6 text-foreground-secondary">{t("loading")}</p>;
}
