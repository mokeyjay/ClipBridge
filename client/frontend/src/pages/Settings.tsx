import { useEffect, useState } from "react";
import { Button, InputGroup, ListBox, Select, Switch, TextField } from "@heroui/react";
import { App as Svc, type SizeSettingDTO, type StatusDTO } from "../api";
import { useI18n, type LangPref, type TKey } from "../i18n";
import { toastOK, toastErr, humanError } from "../notify";
import { bytesToMiB, mibToBytes, type Theme } from "../util";
import { CardCheckGroup, CardRadioGroup, Icon, InfoRow, SectionTitle, Surface } from "../components/common";

const DIR_TO_KEY: Record<string, string> = { bidirectional: "both", upload_only: "up", download_only: "down" };
const KEY_TO_DIR: Record<string, string> = { both: "bidirectional", up: "upload_only", down: "download_only" };

// 裸开关（仅滑块），用于行尾与「跟随账号默认」。
function Toggle({ on, onChange, isDisabled }: { on: boolean; onChange: (v: boolean) => void; isDisabled?: boolean }) {
  return (
    <Switch isSelected={on} onChange={onChange} isDisabled={isDisabled} className="no-drag">
      <Switch.Control>
        <Switch.Thumb />
      </Switch.Control>
    </Switch>
  );
}

export function SettingsPage({
  status,
  theme,
  setTheme,
  onChange,
}: {
  status: StatusDTO;
  theme: Theme;
  setTheme: (t: Theme) => void;
  onChange: () => void;
}) {
  const { t } = useI18n();

  // guard：开关 / 单选等变更后只刷新状态，不弹 toast（控件本身即时反馈，弹窗反而吵）。
  const guard = (p: Promise<unknown>) => p.then(onChange).catch((e) => toastErr(humanError(e)));
  // guardSaved：仅用于「输入框」类设置（尺寸、文件时长）——输入提交后无即时视觉反馈，
  // 弹「设置已保存」确认更友好。
  const guardSaved = (p: Promise<unknown>) =>
    p.then(() => {
      onChange();
      toastOK(t("toast_saved"));
    }).catch((e) => toastErr(humanError(e)));

  const dirOpts: CardOpt[] = [
    { key: "both", title: t("dir_both"), sub: t("dir_both_s"), icon: "refresh" },
    { key: "up", title: t("dir_up"), sub: t("dir_up_s"), icon: "arrowUp" },
    { key: "down", title: t("dir_down"), sub: t("dir_down_s"), icon: "arrowDown" },
  ];
  const notifyOpts: CardOpt[] = [
    { key: "quiet", title: t("nt_quiet"), sub: t("nt_quiet_s"), icon: "bellOff" },
    { key: "default", title: t("nt_default"), sub: t("nt_default_s"), icon: "bell" },
    { key: "verbose", title: t("nt_verbose"), sub: t("nt_verbose_s"), icon: "sparkle" },
  ];
  const themeOpts: CardOpt[] = [
    { key: "light", title: t("th_light"), icon: "sun" },
    { key: "dark", title: t("th_dark"), icon: "moon" },
    { key: "system", title: t("th_auto"), icon: "auto" },
  ];

  const testNotify = async () => {
    try {
      await Svc.TestNotification();
      toastOK(t("toast_notify_ok"));
    } catch (e) {
      toastErr(humanError(e));
    }
  };

  return (
    <div className="flex flex-col gap-5">
      {/* 本机开关 + 语言（无分组标题） */}
      <Surface>
        <InfoRow label={t("s_pause")} sub={t("s_pause_sub")}>
          <Toggle on={status.paused} onChange={(v) => void guard(Svc.SetPaused(v))} />
        </InfoRow>
        <InfoRow label={t("s_autostart")} sub={t("s_autostart_sub")}>
          <Toggle on={status.autostart} onChange={(v) => void guard(Svc.SetAutostart(v))} />
        </InfoRow>
        <InfoRow label={t("s_lang")}>
          <LanguageSelect />
        </InfoRow>
      </Surface>

      {/* 同步方向 */}
      <CardRadio
        title={t("s_direction")}
        options={dirOpts}
        value={DIR_TO_KEY[status.direction] || "both"}
        onChange={(k) => void guard(Svc.SetDirection(KEY_TO_DIR[k]))}
      />

      {/* 通知策略：测试通知按钮放在小标题右侧，按字号缩小 */}
      <CardRadio
        title={t("s_notify")}
        options={notifyOpts}
        value={status.notify_policy || "default"}
        onChange={(k) => void guard(Svc.SetNotifyPolicy(k))}
        titleAction={
          <Button
            size="sm"
            variant="ghost"
            className="no-drag h-6 min-h-0 gap-1 px-2 text-[11.5px]"
            onPress={() => void testNotify()}
          >
            <Icon name="bell" size={12} />
            {t("test_notify")}
          </Button>
        }
      />

      {/* 主题 */}
      <CardRadio
        title={t("s_theme")}
        options={themeOpts}
        value={theme}
        onChange={(k) => setTheme(k as Theme)}
      />

      {/* 窗口材质（仅 Windows；Windows 10 无论选择何值都回退普通窗口） */}
      {status.platform === "windows" && (
        <CardRadio
          title={t("s_backdrop")}
          options={[
            { key: "mica", title: t("bd_mica"), sub: t("bd_mica_s"), icon: "auto" },
            { key: "acrylic", title: t("bd_acrylic"), sub: t("bd_acrylic_s"), icon: "sparkle" },
          ]}
          value={status.windows_backdrop || "mica"}
          onChange={(k) => void guard(Svc.SetWindowsBackdrop(k))}
        />
      )}

      {/* 同步策略覆盖 */}
      <div>
        <SectionTitle>{t("set_policy")}</SectionTitle>
        <Surface className="p-3.5">
          <div className="flex flex-col gap-4">
            <SyncTypes status={status} onChange={onChange} />
            <OverridableSize
              labelKey="s_maxsize"
              subKey="s_maxsize_sub"
              setting={status.max_sync_size}
              apply={(inherit, bytes, notify) => void (notify ? guardSaved : guard)(Svc.SetMaxSyncSize(inherit, bytes))}
            />
            <OverridableSize
              labelKey="s_autosize_up"
              subKey="s_autosize_sub"
              setting={status.auto_upload}
              apply={(inherit, bytes, notify) => void (notify ? guardSaved : guard)(Svc.SetAutoUploadSize(inherit, bytes))}
            />
            <OverridableSize
              labelKey="s_autosize_down"
              subKey="s_autosize_sub"
              setting={status.auto_download}
              apply={(inherit, bytes, notify) => void (notify ? guardSaved : guard)(Svc.SetAutoDownloadSize(inherit, bytes))}
            />
          </div>
        </Surface>
      </div>

      {/* 接收文件 + 语言 */}
      <div>
        <SectionTitle>{t("s_recv")}</SectionTitle>
        <Surface>
          <ReceivedFolder status={status} onChange={onChange} />
          <FileRetention status={status} onChange={onChange} />
        </Surface>
      </div>
    </div>
  );
}

// ---------- 卡片式单选 ----------
type CardOpt = { key: string; title: string; sub?: string; icon?: string };

function CardRadio({
  title,
  options,
  value,
  onChange,
  titleAction,
}: {
  title: string;
  options: CardOpt[];
  value: string;
  onChange: (k: string) => void;
  titleAction?: React.ReactNode;
}) {
  return (
    <div>
      <div className="mb-1.5 ml-1 flex min-h-[24px] items-center justify-between gap-2">
        <span className="text-[13px] font-semibold text-foreground">{title}</span>
        {titleAction}
      </div>
      <CardRadioGroup options={options} value={value} onChange={onChange} />
    </div>
  );
}

// ---------- 同步类型（继承 / 本机多选） ----------
const TYPE_OPTS: { value: string; titleKey: TKey; icon: string }[] = [
  { value: "text", titleKey: "type_text", icon: "text" },
  { value: "image", titleKey: "type_image", icon: "image" },
  { value: "file", titleKey: "type_file", icon: "file" },
  { value: "rich_text", titleKey: "type_rich", icon: "richtext" },
];

function SyncTypes({ status, onChange }: { status: StatusDTO; onChange: () => void }) {
  const { t } = useI18n();
  const s = status.sync_types;
  const inherit = s.inherit;
  const shown = inherit ? s.inherited ?? [] : s.override ?? [];

  const apply = (vals: string[]) => Svc.SetSyncTypes(false, vals).then(onChange).catch((e) => toastErr(humanError(e)));
  const toggleInherit = (v: boolean) => {
    const p = v ? Svc.SetSyncTypes(true, []) : Svc.SetSyncTypes(false, s.inherited ?? ["text", "image", "file", "rich_text"]);
    p.then(onChange).catch((e) => toastErr(humanError(e)));
  };

  const options = TYPE_OPTS.map((o) => ({ value: o.value, title: t(o.titleKey), icon: o.icon }));

  return (
    <div className="flex flex-col gap-2.5">
      <div className="flex items-center justify-between gap-3">
        <span className="text-[13px] font-medium text-foreground">{t("s_types")}</span>
        <InheritSwitch inherit={inherit} onChange={toggleInherit} />
      </div>
      <CardCheckGroup options={options} value={shown as string[]} onChange={(vals) => void apply(vals)} disabled={inherit} />
    </div>
  );
}

// ---------- 可继承尺寸 ----------
function OverridableSize({
  labelKey,
  subKey,
  setting,
  apply,
}: {
  labelKey: TKey;
  subKey: TKey;
  setting: SizeSettingDTO;
  // notify=true 表示来自输入框提交（应弹「设置已保存」）；继承开关不传（不弹）。
  apply: (inherit: boolean, bytes: number, notify?: boolean) => void;
}) {
  const { t } = useI18n();
  const [mib, setMib] = useState("");
  const inheritedMiB = bytesToMiB(setting.inherited_bytes);

  // 覆盖态用本地覆盖值回填；继承态输入禁用，直接展示账号默认（实际生效）值。
  useEffect(() => {
    if (!setting.inherit) setMib(String(bytesToMiB(setting.override_bytes)));
  }, [setting.inherit, setting.override_bytes]);

  const commit = () => {
    const v = Number(mib);
    if (!v || v <= 0) return;
    apply(false, mibToBytes(v), true);
  };

  return (
    <div className="flex flex-col gap-2">
      <div className="flex items-start justify-between gap-3">
        <div className="min-w-0">
          <div className="text-[13px] font-medium text-foreground">{t(labelKey)}</div>
          <div className="mt-0.5 text-[11.5px] leading-snug text-foreground-secondary">{t(subKey)}</div>
        </div>
        <InheritSwitch inherit={setting.inherit} onChange={(v) => apply(v, mibToBytes(Number(mib) || inheritedMiB))} />
      </div>
      <TextField
        value={setting.inherit ? String(inheritedMiB) : mib}
        onChange={(v) => setMib(v.replace(/[^\d.]/g, ""))}
        isDisabled={setting.inherit}
        aria-label={t(labelKey)}
      >
        <InputGroup>
          <InputGroup.Input
            className="no-drag"
            inputMode="decimal"
            placeholder={String(inheritedMiB)}
            onBlur={commit}
            onKeyDown={(e) => {
              if (e.key === "Enter") commit();
            }}
          />
          <InputGroup.Suffix>MiB</InputGroup.Suffix>
        </InputGroup>
      </TextField>
    </div>
  );
}

// 「跟随账号默认」开关 + 文案。
function InheritSwitch({ inherit, onChange }: { inherit: boolean; onChange: (v: boolean) => void }) {
  const { t } = useI18n();
  return (
    <div className="flex shrink-0 items-center gap-2">
      <span className="text-[11.5px] text-foreground-secondary">{t("inherit_account")}</span>
      <Toggle on={inherit} onChange={onChange} />
    </div>
  );
}

// ---------- 语言下拉（自动 / 中文 / English） ----------
function LanguageSelect() {
  const { t, pref, setPref } = useI18n();
  return (
    <Select
      aria-label={t("s_lang")}
      selectedKey={pref}
      onSelectionChange={(k) => setPref(String(k) as LangPref)}
      className="w-[150px]"
    >
      <Select.Trigger className="no-drag">
        <Select.Value />
        <Select.Indicator />
      </Select.Trigger>
      <Select.Popover>
        <ListBox>
          <ListBox.Item id="auto" textValue={t("lang_auto")}>
            {t("lang_auto")}
            <ListBox.ItemIndicator />
          </ListBox.Item>
          <ListBox.Item id="zh" textValue="中文">
            中文
            <ListBox.ItemIndicator />
          </ListBox.Item>
          <ListBox.Item id="en" textValue="English">
            English
            <ListBox.ItemIndicator />
          </ListBox.Item>
        </ListBox>
      </Select.Popover>
    </Select>
  );
}

// ---------- 接收目录（InputGroup + 浏览按钮，无保存） ----------
function ReceivedFolder({ status, onChange }: { status: StatusDTO; onChange: () => void }) {
  const { t } = useI18n();
  const browse = async () => {
    try {
      const dir = await Svc.PickReceivedDir();
      if (dir) {
        toastOK(t("toast_saved"));
        onChange();
      }
    } catch (e) {
      toastErr(humanError(e));
    }
  };
  return (
    <InfoRow label={t("s_folder")} sub={t("s_folder_sub")} align="start">
      <TextField value={status.received_dir || ""} isReadOnly aria-label={t("s_folder")} className="w-[320px] max-w-full">
        <InputGroup>
          <InputGroup.Prefix>
            <Icon name="folder" size={15} />
          </InputGroup.Prefix>
          <InputGroup.Input className="mono no-drag text-[11.5px]" />
          <InputGroup.Suffix className="p-0.5">
            <Button size="sm" variant="ghost" className="no-drag" onPress={() => void browse()}>
              {t("btn_browse")}
            </Button>
          </InputGroup.Suffix>
        </InputGroup>
      </TextField>
    </InfoRow>
  );
}

// ---------- 文件保持时长（纯本地） ----------
function FileRetention({ status, onChange }: { status: StatusDTO; onChange: () => void }) {
  const { t } = useI18n();
  const [days, setDays] = useState(String(status.file_ttl_days || status.inherited_file_ttl_days || 7));

  useEffect(() => {
    setDays(String(status.file_ttl_days || status.inherited_file_ttl_days || 7));
  }, [status.file_ttl_days, status.inherited_file_ttl_days]);

  const commit = () => {
    let n = Number(days);
    if (!n || n < 1) n = 1;
    if (n > 365) n = 365;
    Svc.SetFileRetention(false, n)
      .then(() => {
        onChange();
        toastOK(t("toast_saved"));
      })
      .catch((e) => toastErr(humanError(e)));
  };

  return (
    <InfoRow label={t("s_filettl")} sub={t("s_filettl_sub")}>
      <TextField value={days} onChange={(v) => setDays(v.replace(/[^\d]/g, ""))} aria-label={t("s_filettl")} className="w-[120px]">
        <InputGroup>
          <InputGroup.Input
            className="no-drag"
            inputMode="numeric"
            onBlur={commit}
            onKeyDown={(e) => {
              if (e.key === "Enter") commit();
            }}
          />
          <InputGroup.Suffix>{t("unit_days")}</InputGroup.Suffix>
        </InputGroup>
      </TextField>
    </InfoRow>
  );
}
