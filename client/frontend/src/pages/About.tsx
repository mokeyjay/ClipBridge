import { useCallback, useEffect, useState } from "react";
import { Button, Modal } from "@heroui/react";
import { App as Svc, type AboutDTO, type PeerDTO, type StatusDTO } from "../api";
import { useI18n } from "../i18n";
import { toastOK, toastErr, humanError } from "../notify";
import { fpHeadTail } from "../util";
import { CopyButton, Icon, InfoRow, SectionTitle, Surface } from "../components/common";

// AboutPage：身份信息 + 设备互验 + 诊断信息 + 危险操作。
// 设备互验按 prd/03 §5.3 补齐：展示同用户全部设备的公钥指纹，
// 引导用户跨设备对照，一致即确认对端身份（信任根不依赖服务端）。
export function AboutPage({ status, onChange }: { status: StatusDTO; onChange: () => void }) {
  const { t } = useI18n();
  const [about, setAbout] = useState<AboutDTO | null>(null);
  const [showReset, setShowReset] = useState(false);

  const load = useCallback(() => {
    Svc.About().then(setAbout).catch(() => {});
  }, []);
  useEffect(load, [load]);

  const reset = async () => {
    try {
      await Svc.Unpair();
      toastOK(t("toast_reset_done"));
    } catch (e) {
      toastErr(humanError(e));
    }
    setShowReset(false);
    onChange();
    load();
  };

  const idRows: { label: string; val: string }[] = about
    ? [
        { label: t("ab_device_uuid"), val: about.device_id },
        { label: t("ab_user_uuid"), val: about.user_id },
        { label: t("ab_server_uuid"), val: about.server_id },
      ]
    : [];

  return (
    <div className="flex flex-col gap-4">
      {/* 身份信息 */}
      <div>
        <SectionTitle>{t("ab_identity")}</SectionTitle>
        <Surface>
          {idRows.map((r) => (
            <InfoRow key={r.label} label={r.label}>
              <span className="mono">{r.val || "—"}</span>
              {r.val && <CopyButton text={r.val} onCopied={() => toastOK(t("toast_copied"))} />}
            </InfoRow>
          ))}
          <InfoRow label={t("ab_pubkey")}>
            <span className="mono">{fpHeadTail(about?.key_fingerprint || "")}</span>
            {about?.key_fingerprint && <CopyButton text={about.key_fingerprint} onCopied={() => toastOK(t("toast_copied"))} />}
          </InfoRow>
        </Surface>
      </div>

      {/* 设备互验（prd/03 §5.3）*/}
      <div>
        <SectionTitle>{t("ab_verify")}</SectionTitle>
        <PeerList connected={status.connected} />
      </div>

      {/* 诊断信息 */}
      <div>
        <SectionTitle>{t("ab_diag")}</SectionTitle>
        <Surface>
          <InfoRow label={t("ab_version")}>
            <span className="mono">{about?.version || "—"}</span>
          </InfoRow>
          <InfoRow label={t("ab_lasterr")} align="start" wrapValue={!!status.last_error}>
            <span className={status.last_error ? "text-danger" : "text-foreground-secondary"}>
              {status.last_error || t("ab_lasterr_none")}
            </span>
          </InfoRow>
        </Surface>
      </div>

      {/* 危险操作 */}
      <div>
        <SectionTitle>{t("ab_danger")}</SectionTitle>
        <Surface>
          <InfoRow label={t("ab_reset")} sub={t("reset_warn1")}>
            <Button size="sm" variant="danger-soft" className="no-drag" onPress={() => setShowReset(true)}>
              <Icon name="refresh" size={14} />
              {t("ab_reset")}
            </Button>
          </InfoRow>
        </Surface>
      </div>

      <ResetModal show={showReset} onClose={() => setShowReset(false)} onReset={() => void reset()} />
    </div>
  );
}

// PeerList 展示同用户全部设备的公钥指纹与本机 TOFU 信任状态。
// 引导语提示用户在两台设备上核对同一指纹；「指纹变化」的设备在概览页有
// 对应的告警横幅与信任入口。
function PeerList({ connected }: { connected: boolean }) {
  const { t } = useI18n();
  const [peers, setPeers] = useState<PeerDTO[] | null>(null);

  useEffect(() => {
    if (!connected) {
      setPeers(null);
      return;
    }
    let stop = false;
    const tick = () => Svc.Peers().then((p) => !stop && setPeers(p)).catch(() => {});
    tick();
    const id = setInterval(tick, 10000); // 低频刷新在线状态/新设备
    return () => {
      stop = true;
      clearInterval(id);
    };
  }, [connected]);

  const stateChip = (p: PeerDTO) => {
    const map: Record<string, { key: Parameters<typeof t>[0]; cls: string }> = {
      self: { key: "peer_self", cls: "bg-accent/10 text-accent" },
      trusted: { key: "peer_trusted", cls: "bg-success/10 text-success" },
      mismatch: { key: "peer_mismatch", cls: "bg-danger/10 text-danger" },
      unseen: { key: "peer_unseen", cls: "bg-foreground/10 text-foreground-secondary" },
    };
    const m = map[p.trust_state] ?? map.unseen;
    return <span className={`rounded-full px-2 py-0.5 text-[10.5px] font-medium ${m.cls}`}>{t(m.key)}</span>;
  };

  return (
    <>
      <p className="mb-1.5 ml-1 text-[11.5px] leading-relaxed text-foreground-secondary">{t("ab_verify_sub")}</p>
      <Surface>
        {!connected ? (
          <div className="px-3.5 py-4 text-center text-[12px] text-foreground-secondary">{t("peers_need_conn")}</div>
        ) : peers === null ? (
          <div className="px-3.5 py-4 text-center text-[12px] text-foreground-secondary">…</div>
        ) : peers.length <= 1 ? (
          <div className="px-3.5 py-4 text-center text-[12px] text-foreground-secondary">{t("peers_empty")}</div>
        ) : (
          peers.map((p) => (
            <div key={p.device_id} className="cb-divider-b flex items-center gap-3 px-3.5 py-2.5 last:border-b-0">
              <span
                className={`mt-0.5 inline-block size-2 shrink-0 rounded-full ${p.online ? "bg-success" : "bg-default-300"}`}
                title={p.online ? t("peer_online") : t("peer_offline")}
              />
              <div className="min-w-0 flex-1">
                <div className="flex items-center gap-2">
                  <span className="truncate-1 text-[13px] font-medium text-foreground">{p.name || p.device_id}</span>
                  {stateChip(p)}
                </div>
                <div className="mono mt-0.5 break-all text-[11px] leading-relaxed text-foreground-secondary">
                  {p.key_fingerprint}
                </div>
              </div>
              <CopyButton text={p.key_fingerprint} onCopied={() => toastOK(t("toast_copied"))} />
            </div>
          ))
        )}
      </Surface>
    </>
  );
}

// ResetModal 是重置客户端的二次确认弹窗（从 AboutPage 中拆出保持组件精简）。
function ResetModal({ show, onClose, onReset }: { show: boolean; onClose: () => void; onReset: () => void }) {
  const { t } = useI18n();
  return (
    <div>
      <Modal>
        <Modal.Backdrop isOpen={show} onOpenChange={(o) => !o && onClose()}>
          <Modal.Container>
            <Modal.Dialog>
              <Modal.CloseTrigger />
              <Modal.Header>
                <div className="mx-auto mb-2 grid size-12 place-items-center rounded-full bg-danger/12 text-danger">
                  <Icon name="shieldAlert" size={24} />
                </div>
                <Modal.Heading className="text-center">{t("reset_title")}</Modal.Heading>
              </Modal.Header>
              <Modal.Body>
                <p className="text-center text-[12.5px] text-foreground-secondary">{t("reset_intro")}</p>
                <ul className="mt-3 flex flex-col gap-1 rounded-xl bg-default-100/60 px-3.5 py-3 text-[12px] text-foreground-secondary">
                  <li>· {t("reset_warn1")}</li>
                  <li>· {t("reset_warn2")}</li>
                  <li>· {t("reset_warn3")}</li>
                </ul>
              </Modal.Body>
              <Modal.Footer>
                <Button variant="tertiary" className="no-drag" onPress={onClose}>
                  {t("btn_cancel")}
                </Button>
                <Button variant="danger" className="no-drag" onPress={onReset}>
                  {t("reset_confirm")}
                </Button>
              </Modal.Footer>
            </Modal.Dialog>
          </Modal.Container>
        </Modal.Backdrop>
      </Modal>
    </div>
  );
}
