import { useState, type CSSProperties, type ComponentType, type ReactNode } from "react";
import {
  RiArrowDownLine,
  RiArrowLeftLine,
  RiArrowRightLine,
  RiArrowUpLine,
  RiCheckLine,
  RiCloseLine,
  RiComputerLine,
  RiErrorWarningLine,
  RiFileCopyLine,
  RiFileLine,
  RiFileTextLine,
  RiFolderLine,
  RiImageLine,
  RiInboxLine,
  RiInformationLine,
  RiLinkM,
  RiLoader4Line,
  RiMoonLine,
  RiNotification3Line,
  RiNotificationOffLine,
  RiPauseLine,
  RiPlayLine,
  RiPlugLine,
  RiRefreshLine,
  RiServerLine,
  RiShieldCheckLine,
  RiSparkling2Line,
  RiSunLine,
  RiText,
  RiTimeLine,
  type RemixiconComponentType,
} from "@remixicon/react";
import { Browser } from "@wailsio/runtime";
import { useI18n } from "../i18n";

// 统一图标层：第三方图标库 Remix Icon。页面仍按语义 name 引用，这里集中映射，
// 保持 Icon(name/size/className) API 不变。
const ICONS: Record<string, RemixiconComponentType> = {
  about: RiInformationLine,
  link: RiLinkM,
  shield: RiShieldCheckLine,
  shieldAlert: RiErrorWarningLine,
  server: RiServerLine,
  copy: RiFileCopyLine,
  check: RiCheckLine,
  checkSm: RiCheckLine,
  text: RiText,
  image: RiImageLine,
  file: RiFileLine,
  richtext: RiFileTextLine,
  arrowUp: RiArrowUpLine,
  arrowDown: RiArrowDownLine,
  arrowLeft: RiArrowLeftLine,
  arrowRight: RiArrowRightLine,
  bell: RiNotification3Line,
  bellOff: RiNotificationOffLine,
  sun: RiSunLine,
  moon: RiMoonLine,
  auto: RiComputerLine,
  refresh: RiRefreshLine,
  pause: RiPauseLine,
  play: RiPlayLine,
  inbox: RiInboxLine,
  x: RiCloseLine,
  sparkle: RiSparkling2Line,
  plug: RiPlugLine,
  folder: RiFolderLine,
  clock: RiTimeLine,
};

export function Icon({ name, size = 16, className = "", style }: { name: string; size?: number; className?: string; style?: CSSProperties }) {
  const Cmp = (ICONS[name] ?? RiInformationLine) as ComponentType<{ size?: number; className?: string; style?: CSSProperties }>;
  return <Cmp size={size} className={className} style={style} />;
}

export function Spinner({ size = 15 }: { size?: number }) {
  return <RiLoader4Line size={size} className="animate-spin" />;
}

// SectionTitle 是卡片左上角外部的分组标题（按 xq.md 适当放大、不再用极小号大写）。
export function SectionTitle({ children }: { children: ReactNode }) {
  return <div className="mb-1.5 ml-1 text-[13px] font-semibold text-foreground">{children}</div>;
}

// Surface 是分组卡片容器（白底卡片）。
export function Surface({ children, className = "" }: { children: ReactNode; className?: string }) {
  return <div className={`cb-card overflow-hidden rounded-2xl ${className}`}>{children}</div>;
}

// InfoRow 是分组卡片里的一行：标签（含副标题）+ 右侧值/控件。相邻行带分隔线。
export function InfoRow({
  label,
  sub,
  children,
  align = "center",
  wrapValue = false,
}: {
  label: ReactNode;
  sub?: ReactNode;
  children?: ReactNode;
  align?: "center" | "start";
  // wrapValue: 值可能是很长的不可断字符串（如错误路径），让值占据剩余宽度并换行，
  // 标签保持自然宽度（避免标签被挤成竖排）。
  wrapValue?: boolean;
}) {
  return (
    <div
      className={`cb-divider-b flex gap-3 px-3.5 py-2 last:border-b-0 ${
        align === "start" ? "items-start" : "items-center"
      }`}
    >
      <div className={wrapValue ? "shrink-0" : "min-w-0 flex-1"}>
        <div className="text-[13px] text-foreground">{label}</div>
        {sub && <div className="mt-0.5 text-[11px] leading-snug text-foreground-secondary">{sub}</div>}
      </div>
      {children != null && (
        <div
          className={`flex items-center gap-2 text-[13px] text-foreground-secondary ${
            wrapValue ? "min-w-0 flex-1 justify-end break-all text-right" : "shrink-0 justify-end"
          }`}
        >
          {children}
        </div>
      )}
    </div>
  );
}

// StatusChip 在顶栏右侧展示连接状态（圆点 + 文案）。
const DOT_CLASS: Record<string, string> = {
  connected: "bg-success",
  connecting: "bg-accent animate-pulse",
  paused: "bg-warning",
  unpaired: "bg-default-400",
  error: "bg-danger",
};
export function StatusChip({ state }: { state: string }) {
  const { t } = useI18n();
  const labelKey: Record<string, "status_connected" | "status_connecting" | "status_paused" | "status_unpaired" | "status_error"> = {
    connected: "status_connected",
    connecting: "status_connecting",
    paused: "status_paused",
    unpaired: "status_unpaired",
    error: "status_error",
  };
  return (
    <span className="inline-flex items-center gap-1.5 rounded-full bg-default-100 px-2.5 py-1 text-[12px] font-medium text-foreground-secondary">
      <span className={`size-[7px] rounded-full ${DOT_CLASS[state] ?? DOT_CLASS.unpaired}`} />
      {t(labelKey[state] ?? "status_unpaired")}
    </span>
  );
}

// UpdateBadge 是顶栏的「发现新版本」入口：绿色向上箭头 + 文案，点击用系统默认
// 浏览器打开对应版本的 release 页面。仅当后端检测到更新时由调用方渲染。
export function UpdateBadge({ version, url }: { version: string; url: string }) {
  const { t } = useI18n();
  return (
    <button
      type="button"
      title={t("update_hint").replace("{v}", version || "")}
      onClick={() => {
        if (url) void Browser.OpenURL(url);
      }}
      className="no-drag inline-flex items-center gap-1 rounded-full bg-success/12 px-2.5 py-1 text-[12px] font-medium text-success transition hover:bg-success/20"
    >
      <Icon name="arrowUp" size={13} />
      {t("update_new")}
    </button>
  );
}

// RadioDot 是单选指示器（普通 radio 外观）。
function RadioDot({ selected }: { selected: boolean }) {
  return (
    <span
      className={`grid size-[18px] place-items-center rounded-full border-[1.5px] transition ${
        selected ? "border-accent" : "border-default-300"
      }`}
    >
      {selected && <span className="size-[9px] rounded-full bg-accent" />}
    </span>
  );
}

// CheckBadge 是多选指示器（方形对勾）。
function CheckBadge({ selected }: { selected: boolean }) {
  return (
    <span
      className={`grid size-[18px] place-items-center rounded-md border-[1.5px] transition ${
        selected ? "border-accent bg-accent text-white" : "border-default-300"
      }`}
    >
      {selected && <Icon name="check" size={12} />}
    </span>
  );
}

type RadioOpt = { key: string; title: string; sub?: string; icon?: string };

// CardRadioGroup 是卡片式单选（自绘，完全掌控白底/描边/底色/指示器对齐）。
export function CardRadioGroup({
  options,
  value,
  onChange,
}: {
  options: RadioOpt[];
  value: string;
  onChange: (k: string) => void;
}) {
  return (
    <div role="radiogroup" className="grid grid-cols-3 gap-2.5">
      {options.map((o) => {
        const sel = value === o.key;
        return (
          <button
            key={o.key}
            type="button"
            role="radio"
            aria-checked={sel}
            onClick={() => onChange(o.key)}
            className={`no-drag relative flex flex-col items-start rounded-2xl border px-3 py-2.5 pr-9 text-left transition ${
              sel ? "border-accent bg-accent/10" : "cb-card hover:border-default-300"
            }`}
          >
            <span className="absolute right-3 top-1/2 -translate-y-1/2">
              <RadioDot selected={sel} />
            </span>
            <div className="flex items-center gap-1.5">
              {o.icon && <Icon name={o.icon} size={16} className={sel ? "text-accent" : "text-foreground-secondary"} />}
              <span className="text-[12.5px] font-medium text-foreground">{o.title}</span>
            </div>
            {o.sub && <span className="mt-0.5 text-[11px] leading-snug text-foreground-secondary">{o.sub}</span>}
          </button>
        );
      })}
    </div>
  );
}

type CheckOpt = { value: string; title: string; icon: string };

// CardCheckGroup 是卡片式多选（图标+名称居中，淡底图标）。
export function CardCheckGroup({
  options,
  value,
  onChange,
  disabled,
}: {
  options: CheckOpt[];
  value: string[];
  onChange: (v: string[]) => void;
  disabled?: boolean;
}) {
  const toggle = (k: string) => {
    if (disabled) return;
    onChange(value.includes(k) ? value.filter((x) => x !== k) : [...value, k]);
  };
  return (
    <div className={`grid grid-cols-2 gap-2.5 sm:grid-cols-4 ${disabled ? "pointer-events-none opacity-50" : ""}`}>
      {options.map((o) => {
        const sel = value.includes(o.value);
        return (
          <button
            key={o.value}
            type="button"
            role="checkbox"
            aria-checked={sel}
            onClick={() => toggle(o.value)}
            className={`no-drag relative flex flex-col items-center gap-2 rounded-2xl border px-2 py-3 text-center transition ${
              sel ? "border-accent bg-accent/10" : "cb-card hover:border-default-300"
            }`}
          >
            <span className="absolute right-2 top-2">
              <CheckBadge selected={sel} />
            </span>
            <span className="grid size-11 place-items-center rounded-xl bg-accent/10 text-accent">
              <Icon name={o.icon} size={24} />
            </span>
            <span className="text-[14px] font-medium text-foreground">{o.title}</span>
          </button>
        );
      })}
    </div>
  );
}

// CopyButton 复制文本并短暂显示对勾反馈。
export function CopyButton({ text, onCopied }: { text: string; onCopied?: () => void }) {
  const [done, setDone] = useState(false);
  return (
    <button
      type="button"
      title={text}
      className="no-drag inline-grid size-6 place-items-center rounded-md border border-default-200 bg-default-100 text-foreground-secondary transition hover:bg-default-200 hover:text-foreground"
      onClick={() => {
        try {
          navigator.clipboard?.writeText(text);
        } catch {
          /* ignore */
        }
        setDone(true);
        onCopied?.();
        setTimeout(() => setDone(false), 1400);
      }}
    >
      <Icon name={done ? "checkSm" : "copy"} size={13} />
    </button>
  );
}
