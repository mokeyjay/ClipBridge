// API 客户端：封装与服务端 Web 端口的 JSON 交互。会话用 HttpOnly Cookie，
// 修改类请求自动从 cb_csrf Cookie 取值放入 X-CSRF-Token 头（双提交 CSRF）。

const API = "/api/v1";

// SubjectType 区分管理员与普通用户两类 Web 主体。
export type SubjectType = "admin" | "user";

// Me 是当前登录主体。
export interface Me {
  subject_type: SubjectType;
  subject_id: string;
  username: string;
}

// ApiError 携带稳定错误码，UI 据此分支，不依赖文案。
export class ApiError extends Error {
  code: string;
  status: number;
  constructor(status: number, code: string, message: string) {
    super(message || code);
    this.code = code;
    this.status = status;
  }
}

// readCookie 读取指定名称的 Cookie 值。
function readCookie(name: string): string {
  const prefix = name + "=";
  for (const part of document.cookie.split("; ")) {
    if (part.startsWith(prefix)) return part.slice(prefix.length);
  }
  return "";
}

// mutating 判断方法是否为状态变更（需要 CSRF 头）。
function mutating(method: string): boolean {
  return method === "POST" || method === "PATCH" || method === "PUT" || method === "DELETE";
}

// request 是底层请求方法，处理 JSON、CSRF 头与错误码解析。
async function request<T>(method: string, path: string, body?: unknown): Promise<T> {
  const headers: Record<string, string> = {};
  if (body !== undefined) headers["Content-Type"] = "application/json";
  if (mutating(method)) headers["X-CSRF-Token"] = readCookie("cb_csrf");

  const resp = await fetch(API + path, {
    method,
    headers,
    credentials: "same-origin",
    body: body === undefined ? undefined : JSON.stringify(body),
  });

  const text = await resp.text();
  const data = text ? JSON.parse(text) : null;
  if (!resp.ok) {
    const err = data?.error ?? {};
    throw new ApiError(resp.status, err.code ?? "UNKNOWN", err.message ?? "");
  }
  return data as T;
}

// 以下为各接口的类型化封装。

// Sessions 报告当前同时持有的登录态（管理员/用户均可存在），供右上角一键切换。
export interface Sessions {
  admin?: Me;
  user?: Me;
}

export const auth = {
  me: () => request<Sessions>("GET", "/auth/me"),
  login: (username: string, password: string) =>
    request<Me>("POST", "/auth/login", { username, password }),
  // role 指定要退出的身份；不传则两者都退出。
  logout: (role?: "admin" | "user") =>
    request<unknown>("POST", `/auth/logout${role ? `?role=${role}` : ""}`),
  changePassword: (current_password: string, new_password: string) =>
    request<unknown>("PATCH", "/auth/password", { current_password, new_password }),
};

export interface ServerSettings {
  server_id: string;
  server_name: string;
  max_sync_size_bytes: number;
  allowed_types: ContentType[];
  ciphertext_ttl_seconds: number;
  sync_log_retention_days: number;
}

export interface AdminStats {
  user_count: number;
  device_count: number;
  online_device_count: number;
}

export interface SyncLogView {
  id: string;
  username?: string;
  source_device_name?: string;
  target_device_name?: string;
  event_type: string;
  content_type?: string;
  ciphertext_size_bytes?: number;
  result: string;
  error_code?: string;
  created_at: string;
}

export interface SyncLogPage {
  logs: SyncLogView[];
  total: number;
  page: number;
  page_size: number;
}

export interface AuditLogView {
  id: string;
  actor_type: string;
  actor_name: string;
  action: string;
  target_type?: string;
  target_name?: string;
  detail?: string;
  created_at: string;
}

export interface AuditLogPage {
  logs: AuditLogView[];
  total: number;
  page: number;
  page_size: number;
}

export interface UserView {
  id: string;
  username: string;
  status: string;
  created_at: string;
}

export interface DeviceView {
  id: string;
  name: string;
  platform: string;
  client_version: string;
  status: string;
  key_fingerprint: string;
  online: boolean;
  last_seen_at?: string;
  created_at: string;
}

export const admin = {
  getSettings: () => request<ServerSettings>("GET", "/admin/settings"),
  updateSettings: (patch: Partial<Pick<ServerSettings, "server_name" | "max_sync_size_bytes" | "allowed_types" | "sync_log_retention_days">>) =>
    request<ServerSettings>("PATCH", "/admin/settings", patch),
  stats: () => request<AdminStats>("GET", "/admin/stats"),
  syncLogs: (opts: { page?: number; result?: string; q?: string } = {}) => {
    const p = new URLSearchParams();
    if (opts.page) p.set("page", String(opts.page));
    if (opts.result) p.set("result", opts.result);
    if (opts.q) p.set("q", opts.q);
    const qs = p.toString();
    return request<SyncLogPage>("GET", `/admin/sync-logs${qs ? `?${qs}` : ""}`);
  },
  // 操作日志：管理员/用户关键操作的审计记录（不含敏感值）。
  auditLogs: (opts: { page?: number; actor?: string; q?: string } = {}) => {
    const p = new URLSearchParams();
    if (opts.page) p.set("page", String(opts.page));
    if (opts.actor) p.set("actor", opts.actor);
    if (opts.q) p.set("q", opts.q);
    const qs = p.toString();
    return request<AuditLogPage>("GET", `/admin/audit-logs${qs ? `?${qs}` : ""}`);
  },
  listUsers: () => request<{ users: UserView[] }>("GET", "/admin/users"),
  createUser: (username: string, password: string) =>
    request<UserView>("POST", "/admin/users", { username, password }),
  updateUser: (id: string, patch: { username?: string; status?: string }) =>
    request<UserView>("PATCH", `/admin/users/${id}`, patch),
  resetUserPassword: (id: string) =>
    request<{ password: string }>("POST", `/admin/users/${id}/reset-password`),
  listUserDevices: (id: string) =>
    request<{ devices: DeviceView[] }>("GET", `/admin/users/${id}/devices`),
  updateDevice: (id: string, patch: { name?: string; status?: string }) =>
    request<DeviceView>("PATCH", `/admin/devices/${id}`, patch),
  deleteDevice: (id: string) => request<unknown>("DELETE", `/admin/devices/${id}`),
  updateProfile: (username: string) =>
    request<{ username: string }>("PATCH", "/admin/profile", { username }),
};

export type ContentType = "text" | "image" | "file" | "rich_text";

export interface UserSettings {
  max_sync_size_bytes: number;
  allowed_types: ContentType[];
  max_auto_upload_size_bytes: number;
  max_auto_download_size_bytes: number;
  file_ttl_days: number;
}

export interface PendingRequest {
  request_id: string;
  device_name: string;
  platform: string;
  client_version: string;
  key_fingerprint: string;
  created_at: string;
}

export interface CurrentPairingCode {
  active: boolean;
  expires_at?: string;
  server_name: string;
  server_fingerprint_sha256: string;
  pending_requests: PendingRequest[];
}

export interface CreatedPairingCode {
  code: string;
  expires_at: string;
  server_name: string;
  server_fingerprint_sha256: string;
}

export const user = {
  getSettings: () => request<UserSettings>("GET", "/user/settings"),
  updateSettings: (patch: Partial<UserSettings>) =>
    request<UserSettings>("PATCH", "/user/settings", patch),
  listDevices: () => request<{ devices: DeviceView[] }>("GET", "/user/devices"),
  updateDevice: (id: string, patch: { name?: string; status?: string }) =>
    request<DeviceView>("PATCH", `/user/devices/${id}`, patch),
  revokeDevice: (id: string) => request<unknown>("DELETE", `/user/devices/${id}`),
  createPairingCode: () => request<CreatedPairingCode>("POST", "/pairing-codes"),
  getCurrentPairingCode: () => request<CurrentPairingCode>("GET", "/pairing-codes/current"),
  cancelPairingCode: () => request<unknown>("DELETE", "/pairing-codes/current"),
  confirmRequest: (id: string) => request<DeviceView>("POST", `/pairing-requests/${id}/confirm`),
  rejectRequest: (id: string) => request<unknown>("POST", `/pairing-requests/${id}/reject`),
};
