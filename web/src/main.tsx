import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { createBrowserRouter, RouterProvider } from "react-router-dom";
import App from "./App";
import { ToastProvider } from "./toast";
import Timeline from "./pages/Timeline";
import Devices from "./pages/Devices";
import ChangeDetailPage from "./pages/ChangeDetail";
import Settings from "./pages/Settings";
import "./styles.css";

const router = createBrowserRouter([
  {
    path: "/",
    element: <App />,
    children: [
      { index: true, element: <Timeline /> },
      { path: "devices", element: <Devices /> },
      { path: "changes/:id", element: <ChangeDetailPage /> },
      { path: "settings", element: <Settings /> },
    ],
  },
]);

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <ToastProvider>
      <RouterProvider router={router} />
    </ToastProvider>
  </StrictMode>,
);
