import { createRoot } from "react-dom/client";
import { App } from "./app.tsx";
// Only the Oat files the client uses; 00-base.css first, it declares the layer order (client contracts §10.4).
import "@knadh/oat/css/00-base.css";
import "@knadh/oat/css/01-theme.css";
import "@knadh/oat/css/form.css";
import "@knadh/oat/css/button.css";
import "@knadh/oat/css/table.css";
import "@knadh/oat/css/animations.css";
import "./app.css";

// No StrictMode: its doubled effects in development would hand the callback's one-time pending
// entry to the first run and refuse the second (client contracts §9.3 item 1).
const root = document.getElementById("root");
if (root !== null) createRoot(root).render(<App />);
