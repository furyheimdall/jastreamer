import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import App from "./App";
import "./base.css";
import "./library.css";

const root = document.getElementById("root");
if (!root) throw new Error("앱을 표시할 요소를 찾지 못했습니다.");

createRoot(root).render(
  <StrictMode>
    <App />
  </StrictMode>,
);
