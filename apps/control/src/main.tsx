import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import App from "./App";
import { isPhone } from "./device";
import "./base.css";
import "./library.css";
import "./phone-player.css";

document.documentElement.dataset.device = isPhone ? "phone" : "standard";

const root = document.getElementById("root");
if (!root) throw new Error("Application root element was not found.");

createRoot(root).render(
  <StrictMode>
    <App />
  </StrictMode>,
);
