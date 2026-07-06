import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { Toast } from "@heroui/react";
import "./globals.css";
import { App } from "./App.tsx";

// 入口：HeroUI v3 无需 Provider；挂载 Toast.Provider 支持全局通知。
createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <Toast.Provider placement="bottom end" />
    <App />
  </StrictMode>,
);
