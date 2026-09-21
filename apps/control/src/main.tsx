import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import App from "./App";
import { embeddedClient, isPhone } from "./device";
import "./base.css";
import "./library.css";
import "./phone-player.css";

document.documentElement.dataset.device = isPhone ? "phone" : "standard";
if (embeddedClient) document.documentElement.dataset.client = embeddedClient;

const root = document.getElementById("root");
if (!root) throw new Error("Application root element was not found.");

createRoot(root).render(
  <StrictMode>
    <App />
  </StrictMode>,
);
