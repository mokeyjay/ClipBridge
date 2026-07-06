import { useEffect, useState, type ReactNode } from "react";
import { Alert, Button, Input, Label, Modal, TextField } from "@heroui/react";
import { App as Svc, type HistoryDTO, type StatusDTO } from "../api";
import { useI18n, type TKey } from "../i18n";
import { toastOK, toastErr, humanError } from "../notify";
import { contentTypeKey, formatBytes, relTime, absTime } from "../util";
import { Icon, Spinner, Surface } from "../components/common";

// OverviewPage：未配对显示连接向导，已配对显示状态总览 + 同步记录。
export function OverviewPage({ status, onChange }: { status: StatusDTO; onChange: () => void }) {
  if (!status.paired) return <ConnectFlow onPaired={onChange} />;
  return <PairedOverview status={status} onChange={onChange} />;
}

// ---------- 连接向导 ----------
function ConnectFlow({ onPaired }: { onPaired: () => void }) {
  const { t } = useI18n();
  const [step, setStep] = useState(1);
  const [addr, setAddr] = useState("https://127.0.0.1:8443");
  const [code, setCode] = useState("");
  const [name, setName] = useState("");
  const [fp, setFp] = useState("");
  const [errAddr, setErrAddr] = useState("");
  const [errCode, setErrCode] = useState("");
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    Svc.SuggestedDeviceName().then((n) => setName((p) => p || n)).catch(() => {});
  }, []);

  const doConnect = async () => {
    let ok = true;
    if (!/^https:\/\/.+/.test(addr.trim())) {
      setErrAddr(t("err_addr"));
      ok = false;
    } else setErrAddr("");
    if (!/^\d{6}$/.test(code.trim())) {
      setErrCode(t("err_code"));
      ok = false;
    } else setErrCode("");
    if (!ok) return;
    setBusy(true);
    try {
      setFp(await Svc.BeginPair(addr.trim()));
      setStep(2);
    } catch (e) {
      toastErr(humanError(e));
    } finally {
      setBusy(false);
    }
  };

  const doTrust = async () => {
    setBusy(true);
    try {
      await Svc.Pair(addr.trim(), fp, code.trim(), name.trim() || "我的设备");
      toastOK(t("toast_paired"));
      onPaired();
    } catch (e) {
      toastErr(humanError(e));
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="mx-auto flex w-full max-w-[440px] flex-col gap-4">
      <StepIndicator step={step} />

      {step === 1 ? (
        <>
          <Hero icon="plug" title={t("connect_title")} sub={t("connect_sub")} />
          <Surface className="p-3.5">
            <div className="flex flex-col gap-3">
              <TextField value={addr} onChange={setAddr} isInvalid={!!errAddr}>
                <Label className="text-[12px] text-foreground-secondary">{t("f_address")}</Label>
                <Input className="mono no-drag" placeholder="https://主机:8443" />
                {errAddr && <FieldErr>{errAddr}</FieldErr>}
              </TextField>
              <div className="flex gap-3">
                <TextField
                  className="flex-1"
                  value={code}
                  onChange={(v) => setCode(v.replace(/[^\d]/g, "").slice(0, 6))}
                  isInvalid={!!errCode}
                >
                  <Label className="text-[12px] text-foreground-secondary">{t("f_code")}</Label>
                  <Input className="mono no-drag tracking-[0.18em]" inputMode="numeric" placeholder={t("ph_code")} />
                  {errCode && <FieldErr>{errCode}</FieldErr>}
                </TextField>
                <TextField className="flex-1" value={name} onChange={setName}>
                  <Label className="text-[12px] text-foreground-secondary">{t("f_devicename")}</Label>
                  <Input className="no-drag" />
                </TextField>
              </div>
            </div>
          </Surface>
          <Button className="no-drag w-full" onPress={() => void doConnect()} isDisabled={busy}>
            {busy ? <Spinner /> : <Icon name="link" size={15} />}
            {t("connect_btn")}
          </Button>
        </>
      ) : (
        <>
          <Hero icon="shield" title={t("verify_title")} sub={t("verify_sub")} />
          <div>
            <div className="mb-1.5 ml-1 text-[13px] font-semibold text-foreground">{t("cert_fp")}</div>
            <div className="mono break-all rounded-2xl border border-default-200 bg-default-100/50 px-3.5 py-3 text-[13px] leading-relaxed">
              {fp}
            </div>
          </div>
          <div className="flex gap-3">
            <Button variant="outline" className="no-drag flex-1" onPress={() => setStep(1)} isDisabled={busy}>
              <Icon name="arrowLeft" size={15} />
              {t("btn_back")}
            </Button>
            <Button className="no-drag flex-1" onPress={() => void doTrust()} isDisabled={busy}>
              {busy ? <Spinner /> : <Icon name="shield" size={15} />}
              {t("btn_trust")}
            </Button>
          </div>
          {busy && (
            <Alert status="accent">
              <Alert.Indicator />
              <Alert.Content>
                <Alert.Title>{t("pairing_wait")}</Alert.Title>
                <Alert.Description>{t("pairing_wait_sub")}</Alert.Description>
              </Alert.Content>
            </Alert>
          )}
        </>
      )}
    </div>
  );
}

function StepIndicator({ step }: { step: number }) {
  return (
    <div className="flex items-center justify-center gap-2">
      <span
        className={`grid size-5 place-items-center rounded-full text-[11px] font-semibold ${
          step > 1 ? "bg-accent/15 text-accent" : "bg-accent text-accent-foreground"
        }`}
      >
        {step > 1 ? "✓" : "1"}
      </span>
      <span className={`h-px w-7 ${step > 1 ? "bg-accent" : "bg-default-300"}`} />
      <span
        className={`grid size-5 place-items-center rounded-full text-[11px] font-semibold ${
          step === 2 ? "bg-accent text-accent-foreground" : "bg-default-100 text-foreground-secondary"
        }`}
      >
        2
      </span>
    </div>
  );
}

function Hero({ icon, title, sub }: { icon: string; title: string; sub: string }) {
  return (
    <div className="flex flex-col items-center text-center">
      <div className="grid size-13 place-items-center rounded-2xl bg-accent/12 text-accent">
        <Icon name={icon} size={26} />
      </div>
      <h2 className="mt-2.5 text-[17px] font-semibold text-foreground">{title}</h2>
      <p className="mx-auto mt-1 max-w-[340px] text-[11.5px] leading-relaxed text-foreground-secondary">{sub}</p>
    </div>
  );
}

function FieldErr({ children }: { children: ReactNode }) {
  return <div className="ml-0.5 mt-1 text-[11.5px] text-danger">{children}</div>;
}

// ---------- 已配对总览 ----------
function PairedOverview({ status, onChange }: { status: StatusDTO; onChange: () => void }) {
  const { t, lang } = useI18n();
  const [history, setHistory] = useState<HistoryDTO[]>([]);
  const [page, setPage] = useState(1); // 同步记录分页（客户端分页，第 1 页为最新）

  useEffect(() => {
    const tick = () => Svc.RecentHistory().then((h) => setHistory([...h].reverse())).catch(() => {});
    tick();
    const id = setInterval(tick, 2000);
    return () => clearInterval(id);
  }, []);

  const resume = async () => {
    await Svc.SetPaused(false);
    onChange();
  };
  const connText = status.paused ? t("status_paused") : status.connected ? t("status_connected") : t("status_connecting");

  // 服务器卡片：主标题显示实例名（拿到前回退为端点），小字显示实际接入端点。
  const endpoint = serverShort(status.server_url);
  const serverName = (status.server_name || "").trim();

  // 同步记录分页：每页 8 条；页码随列表变化自适应钳制。
  const pageSize = 8;
  const totalPages = Math.max(1, Math.ceil(history.length / pageSize));
  const curPage = Math.min(page, totalPages);
  const pageItems = history.slice((curPage - 1) * pageSize, curPage * pageSize);

  return (
    <div className="flex flex-col gap-3.5">
      {status.server_fp_mismatch && <ServerFPMismatchBanner status={status} onChange={onChange} />}

      {(status.peer_mismatches ?? []).map((m) => (
        <PeerMismatchBanner key={m.device_id} m={m} onChange={onChange} />
      ))}

      {status.paused && (
        <Alert status="warning">
          <Alert.Indicator />
          <Alert.Content>
            <Alert.Title>{t("paused_banner_t")}</Alert.Title>
            <Alert.Description>{t("paused_banner_s")}</Alert.Description>
          </Alert.Content>
          <Button size="sm" variant="outline" className="no-drag" onPress={() => void resume()}>
            <Icon name="play" size={14} />
            {t("resume")}
          </Button>
        </Alert>
      )}

      {status.permission_warning && (
        <Alert status="warning">
          <Alert.Indicator />
          <Alert.Content>
            <Alert.Title>{t("warn_cred_t")}</Alert.Title>
            <Alert.Description>{status.permission_warning}</Alert.Description>
          </Alert.Content>
        </Alert>
      )}

      {/* 状态卡：三张等高，数值同尺寸，灰色小字底部对齐 */}
      <div className="grid grid-cols-3 gap-2.5">
        <Stat
          icon={status.paused ? "pause" : "shield"}
          iconClass={status.paused ? "text-warning" : "text-accent"}
          label={t("ov_conn")}
        >
          {connText}
        </Stat>
        <Stat icon="refresh" label={t("ov_count")}>
          <span className="inline-flex items-center gap-2.5">
            <span className="inline-flex items-center gap-1">
              <Icon name="arrowUp" size={13} className="text-accent" />
              {status.upload_count}
            </span>
            <span className="inline-flex items-center gap-1">
              <Icon name="arrowDown" size={13} className="text-foreground-secondary" />
              {status.download_count}
            </span>
          </span>
        </Stat>
        <Stat icon="server" label={serverName ? endpoint : t("ov_server")}>
          <span className="truncate-1 block" title={serverName || endpoint}>{serverName || endpoint}</span>
        </Stat>
      </div>

      {/* 同步记录 */}
      <div>
        <div className="mb-1.5 ml-1 flex items-center justify-between">
          <span className="text-[13px] font-semibold text-foreground">{t("sync_records")}</span>
          {history.length > pageSize && (
            <span className="flex items-center gap-1">
              <button
                type="button"
                aria-label="prev"
                disabled={curPage <= 1}
                onClick={() => setPage(curPage - 1)}
                className="no-drag inline-grid size-6 place-items-center rounded-md text-foreground-secondary transition hover:bg-default-100 hover:text-foreground disabled:opacity-30 disabled:hover:bg-transparent"
              >
                <Icon name="arrowLeft" size={14} />
              </button>
              <span className="min-w-[44px] text-center text-[11px] tabular-nums text-foreground-secondary">{curPage} / {totalPages}</span>
              <button
                type="button"
                aria-label="next"
                disabled={curPage >= totalPages}
                onClick={() => setPage(curPage + 1)}
                className="no-drag inline-grid size-6 place-items-center rounded-md text-foreground-secondary transition hover:bg-default-100 hover:text-foreground disabled:opacity-30 disabled:hover:bg-transparent"
              >
                <Icon name="arrowRight" size={14} />
              </button>
            </span>
          )}
        </div>
        <Surface>
          {history.length === 0 ? (
            <div className="flex flex-col items-center gap-2 px-5 py-8 text-center">
              <Icon name="inbox" size={30} className="text-default-400" />
              <div className="text-[13px] font-medium text-foreground">{t("rec_empty_t")}</div>
              <div className="max-w-[280px] text-[12px] text-foreground-secondary">{t("rec_empty_s")}</div>
            </div>
          ) : (
            pageItems.map((r, i) => <RecordRow key={(curPage - 1) * pageSize + i} r={r} lang={lang} />)
          )}
        </Surface>
      </div>
    </div>
  );
}

// FPRow 以等宽字体展示一条「标签 + 指纹」，供告警横幅内核对使用。
function FPRow({ label, fp }: { label: string; fp: string }) {
  return (
    <div className="mt-1.5">
      <div className="text-[11px] text-foreground-secondary">{label}</div>
      <div className="mono break-all text-[11.5px] leading-relaxed">{fp || "—"}</div>
    </div>
  );
}

// ServerFPMismatchBanner：服务器证书指纹变化的引导式重置横幅。
// 展示新旧指纹供与 Web 后台核对，「信任新指纹」需二次确认；绝不静默接受。
function ServerFPMismatchBanner({ status, onChange }: { status: StatusDTO; onChange: () => void }) {
  const { t } = useI18n();
  const [confirm, setConfirm] = useState(false);
  const [busy, setBusy] = useState(false);

  const trust = async () => {
    setBusy(true);
    try {
      await Svc.TrustServerFingerprint();
      toastOK(t("toast_fp_trusted"));
      setConfirm(false);
      onChange();
    } catch (e) {
      toastErr(humanError(e));
    } finally {
      setBusy(false);
    }
  };

  return (
    <>
      <Alert status="danger">
        <Alert.Indicator />
        <Alert.Content>
          <Alert.Title>{t("fp_banner_t")}</Alert.Title>
          <Alert.Description>
            {t("fp_banner_s")}
            <FPRow label={t("fp_old")} fp={status.server_fingerprint} />
            <FPRow label={t("fp_new")} fp={status.new_server_fingerprint} />
            <div className="mt-1.5 text-[11px] text-foreground-secondary">{t("fp_repair_hint")}</div>
          </Alert.Description>
        </Alert.Content>
        <Button
          size="sm"
          variant="danger-soft"
          className="no-drag shrink-0"
          isDisabled={!status.new_server_fingerprint}
          onPress={() => setConfirm(true)}
        >
          <Icon name="shield" size={14} />
          {t("fp_trust_btn")}
        </Button>
      </Alert>

      <Modal>
        <Modal.Backdrop isOpen={confirm} onOpenChange={(o) => !o && setConfirm(false)}>
          <Modal.Container>
            <Modal.Dialog>
              <Modal.CloseTrigger />
              <Modal.Header>
                <div className="mx-auto mb-2 grid size-12 place-items-center rounded-full bg-danger/12 text-danger">
                  <Icon name="shieldAlert" size={24} />
                </div>
                <Modal.Heading className="text-center">{t("fp_trust_title")}</Modal.Heading>
              </Modal.Header>
              <Modal.Body>
                <p className="text-center text-[12.5px] text-foreground-secondary">{t("fp_trust_body")}</p>
                <div className="mono mt-3 break-all rounded-xl bg-default-100/60 px-3.5 py-3 text-[12px] leading-relaxed">
                  {status.new_server_fingerprint}
                </div>
              </Modal.Body>
              <Modal.Footer>
                <Button variant="tertiary" className="no-drag" onPress={() => setConfirm(false)} isDisabled={busy}>
                  {t("btn_cancel")}
                </Button>
                <Button variant="danger" className="no-drag" onPress={() => void trust()} isDisabled={busy}>
                  {busy ? <Spinner /> : <Icon name="shield" size={14} />}
                  {t("fp_trust_confirm")}
                </Button>
              </Modal.Footer>
            </Modal.Dialog>
          </Modal.Container>
        </Modal.Backdrop>
      </Modal>
    </>
  );
}

// PeerMismatchBanner：某台对端设备公钥指纹变化的持续告警横幅。
// 提示「对端可能已重新配对」这一正常原因，信任前需二次确认。
function PeerMismatchBanner({
  m,
  onChange,
}: {
  m: { device_id: string; device_name: string; trusted_fingerprint: string; new_fingerprint: string };
  onChange: () => void;
}) {
  const { t } = useI18n();
  const [confirm, setConfirm] = useState(false);
  const [busy, setBusy] = useState(false);

  const trust = async () => {
    setBusy(true);
    try {
      await Svc.TrustPeer(m.device_id);
      toastOK(t("toast_peer_trusted"));
      setConfirm(false);
      onChange();
    } catch (e) {
      toastErr(humanError(e));
    } finally {
      setBusy(false);
    }
  };

  return (
    <>
      <Alert status="warning">
        <Alert.Indicator />
        <Alert.Content>
          <Alert.Title>{`${m.device_name || m.device_id} · ${t("peer_banner_t")}`}</Alert.Title>
          <Alert.Description>
            {t("peer_banner_s")}
            <FPRow label={t("peer_old")} fp={m.trusted_fingerprint} />
            <FPRow label={t("peer_new")} fp={m.new_fingerprint} />
          </Alert.Description>
        </Alert.Content>
        <Button size="sm" variant="outline" className="no-drag shrink-0" onPress={() => setConfirm(true)}>
          <Icon name="shield" size={14} />
          {t("peer_trust_btn")}
        </Button>
      </Alert>

      <Modal>
        <Modal.Backdrop isOpen={confirm} onOpenChange={(o) => !o && setConfirm(false)}>
          <Modal.Container>
            <Modal.Dialog>
              <Modal.CloseTrigger />
              <Modal.Header>
                <div className="mx-auto mb-2 grid size-12 place-items-center rounded-full bg-warning/15 text-warning">
                  <Icon name="shieldAlert" size={24} />
                </div>
                <Modal.Heading className="text-center">{t("peer_trust_title")}</Modal.Heading>
              </Modal.Header>
              <Modal.Body>
                <p className="text-center text-[12.5px] text-foreground-secondary">{t("peer_trust_body")}</p>
                <div className="mono mt-3 break-all rounded-xl bg-default-100/60 px-3.5 py-3 text-[12px] leading-relaxed">
                  {m.new_fingerprint}
                </div>
              </Modal.Body>
              <Modal.Footer>
                <Button variant="tertiary" className="no-drag" onPress={() => setConfirm(false)} isDisabled={busy}>
                  {t("btn_cancel")}
                </Button>
                <Button className="no-drag" onPress={() => void trust()} isDisabled={busy}>
                  {busy ? <Spinner /> : <Icon name="shield" size={14} />}
                  {t("peer_trust_btn")}
                </Button>
              </Modal.Footer>
            </Modal.Dialog>
          </Modal.Container>
        </Modal.Backdrop>
      </Modal>
    </>
  );
}

function Stat({
  icon,
  iconClass = "text-accent",
  label,
  children,
}: {
  icon: string;
  iconClass?: string;
  label: string;
  children: ReactNode;
}) {
  return (
    <div className="cb-card flex h-[84px] flex-col rounded-2xl p-3">
      <Icon name={icon} size={17} className={iconClass} />
      <div className="mt-1 flex-1 text-[16px] font-semibold leading-tight text-foreground">{children}</div>
      <div className="text-[11px] text-foreground-secondary">{label}</div>
    </div>
  );
}

function RecordRow({ r, lang }: { r: HistoryDTO; lang: "zh" | "en" }) {
  const { t } = useI18n();
  const up = r.direction === "upload";
  const typeName = t(contentTypeKey(r.content_type));
  const icon = ({ text: "text", image: "image", file: "file", rich_text: "richtext" } as Record<string, string>)[r.content_type] ?? "file";
  // 三态：成功 / 忽略（被策略或方向拦截、无在线目标、待确认等）/ 失败。
  const st = r.status || (r.ok ? "ok" : "failed");
  const ignored = st === "ignored";
  const failed = st === "failed";
  // 图标底色按状态/方向着色：失败=红，忽略=中性灰底，上传=蓝，下载=绿。
  // 注意：HeroUI v3 没有 default-100/200 数值刻度（那些类是无效的，导致此前忽略态
  // 图标毫无底色），故中性色改用真实的 foreground token 加透明度。
  const iconTone = failed
    ? "bg-danger/10 text-danger"
    : ignored
      ? "bg-foreground/10 text-foreground-secondary"
      : up
        ? "bg-accent/10 text-accent"
        : "bg-success/10 text-success";

  return (
    <div className="cb-divider-b flex items-center gap-3 px-3.5 py-2.5 last:border-b-0">
      <div className={`relative grid size-9 shrink-0 place-items-center rounded-xl ${iconTone}`}>
        <Icon name={icon} size={18} />
        <span className="absolute -bottom-0.5 -right-0.5 grid size-[14px] place-items-center rounded-full bg-[var(--cb-card-bg)] text-foreground-secondary">
          <Icon name={up ? "arrowUp" : "arrowDown"} size={10} className={up ? "text-accent" : "text-success"} />
        </span>
      </div>

      <div className="min-w-0 flex-1">
        <div className="truncate-1 text-[13px] font-medium text-foreground">{r.summary || typeName}</div>
        <div className="mt-0.5 flex items-center gap-1.5 text-[11px] text-foreground-secondary">
          <span>{up ? t("up") : t("down")}</span>
          <span className="opacity-40">·</span>
          <span>{typeName}</span>
          {r.size_bytes > 0 && (
            <>
              <span className="opacity-40">·</span>
              <span>{formatBytes(r.size_bytes)}</span>
            </>
          )}
          {(failed || ignored) && r.detail && (
            <span className={`truncate-1 ${failed ? "text-danger" : "text-foreground-secondary"}`}>· {t(r.detail as TKey)}</span>
          )}
        </div>
      </div>

      <div className="shrink-0 text-right">
        <div className="text-[11px] text-foreground-secondary" title={absTime(r.at)}>{relTime(r.at, lang)}</div>
        <div
          className={`mt-0.5 text-[11px] font-semibold ${
            failed ? "text-danger" : ignored ? "text-foreground-secondary" : "text-success"
          }`}
        >
          {failed ? t("res_failed") : ignored ? t("res_ignored") : t("res_success")}
        </div>
      </div>
    </div>
  );
}

function serverShort(url: string): string {
  return url.replace(/^https?:\/\//, "").replace(/\/$/, "") || "—";
}
