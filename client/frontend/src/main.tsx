import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { Toast } from "@heroui/react";
import "./globals.css";
import { App } from "./App.tsx";
import { I18nProvider } from "./i18n";

// 入口：HeroUI v3 无需 Provider；挂载全局 Toast 与 i18n。
createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <Toast.Provider placement="top" />
    <I18nProvider>
      <App />
    </I18nProvider>
  </StrictMode>,
);
