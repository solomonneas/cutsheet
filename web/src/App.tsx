import { useCallback, useEffect, useState } from "react";
import { NavLink, Outlet } from "react-router-dom";
import { ApiError, apiGet, getToken } from "./api";

export type AuthState = "checking" | "open" | "token" | "denied" | "unreachable";

// authStateChanged is dispatched (window event) by Settings after the token
// changes so the topbar chip re-probes without a reload.
export const AUTH_CHANGED_EVENT = "cutsheet-auth-changed";

export function useAuthState(): [AuthState, () => void] {
  const [state, setState] = useState<AuthState>("checking");

  const probe = useCallback(() => {
    setState("checking");
    apiGet("/devices")
      .then(() => {
        setState(getToken() ? "token" : "open");
      })
      .catch((err) => {
        if (err instanceof ApiError && err.status === 401) {
          setState("denied");
        } else {
          setState("unreachable");
        }
      });
  }, []);

  useEffect(() => {
    probe();
    window.addEventListener(AUTH_CHANGED_EVENT, probe);
    return () => window.removeEventListener(AUTH_CHANGED_EVENT, probe);
  }, [probe]);

  return [state, probe];
}

const AUTH_CHIP: Record<AuthState, { label: string; cls: string; title: string }> = {
  checking: { label: "...", cls: "", title: "Checking API access" },
  open: {
    label: "OPEN ACCESS",
    cls: "auth-open",
    title: "No API tokens exist; localhost requests are unauthenticated. Create one with: cutsheet token create",
  },
  token: { label: "TOKEN AUTH", cls: "auth-token", title: "Authenticated with a bearer token" },
  denied: {
    label: "UNAUTHORIZED",
    cls: "auth-denied",
    title: "The API rejected this client. Set a valid token in Settings.",
  },
  unreachable: { label: "API DOWN", cls: "auth-denied", title: "The API is not responding" },
};

export default function App() {
  const [auth] = useAuthState();
  const chip = AUTH_CHIP[auth];

  return (
    <>
      <header className="topbar">
        <NavLink to="/" className="brand">
          cut<span className="brand-accent">sheet</span>
        </NavLink>
        <nav className="topnav">
          <NavLink to="/" end>
            Timeline
          </NavLink>
          <NavLink to="/devices">Devices</NavLink>
          <NavLink to="/settings">Settings</NavLink>
        </nav>
        <span className={`auth-chip ${chip.cls}`} title={chip.title}>
          {chip.label}
        </span>
      </header>
      <main>
        <Outlet />
      </main>
    </>
  );
}
