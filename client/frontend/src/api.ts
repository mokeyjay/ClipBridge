// API 桥接层：复用 Wails 生成的类型化绑定，并封装事件订阅。
import * as GuiService from "../bindings/github.com/mokeyjay/clipbridge/client/internal/guiservice";
import type { StatusDTO, AboutDTO, HistoryDTO, SizeSettingDTO, TypesSettingDTO, PeerDTO, PeerMismatchDTO } from "../bindings/github.com/mokeyjay/clipbridge/client/internal/guiservice";
import { Events } from "@wailsio/runtime";

export const App = GuiService.App;
export type { StatusDTO, AboutDTO, HistoryDTO, SizeSettingDTO, TypesSettingDTO, PeerDTO, PeerMismatchDTO };

// onStatus 订阅后端推送的状态更新；后端通过 emit("status", StatusDTO) 推送。
export function onStatus(cb: (s: StatusDTO) => void): () => void {
  return Events.On("status", (e: { data: unknown }) => {
    const data = Array.isArray(e.data) ? e.data[0] : e.data;
    cb(data as StatusDTO);
  });
}
