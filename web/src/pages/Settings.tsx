import { useState, type FormEvent } from "react";
import { AUTH_CHANGED_EVENT, useAuthState } from "../App";
import { getToken, setToken } from "../api";
import { useToast } from "../toast";

export default function Settings() {
  const [tokenInput, setTokenInput] = useState(getToken());
  const [auth, reprobe] = useAuthState();
  const toast = useToast();

  const save = (e: FormEvent) => {
    e.preventDefault();
    setToken(tokenInput.trim());
    window.dispatchEvent(new Event(AUTH_CHANGED_EVENT));
    reprobe();
    toast.push("success", tokenInput.trim() ? "API token saved" : "API token cleared");
  };

  const clear = () => {
    setTokenInput("");
    setToken("");
    window.dispatchEvent(new Event(AUTH_CHANGED_EVENT));
    reprobe();
    toast.push("info", "API token cleared");
  };

  return (
    <>
      <div className="page-head">
        <h1>Settings</h1>
      </div>

      {auth === "open" && (
        <div className="banner banner-open">
          Open access: no API tokens exist, so this server accepts unauthenticated requests
          from localhost. To lock it down, create a token on the server
          (<code>cutsheet token create --data-dir ... --name ui</code>) and paste it below;
          from that moment every request requires a token, including from localhost.
        </div>
      )}
      {auth === "token" && (
        <div className="banner banner-ok">
          Authenticated: requests are sent with the stored bearer token and the API accepts
          them.
        </div>
      )}
      {auth === "denied" && (
        <div className="banner banner-err">
          The API rejected this client (401).{" "}
          {getToken()
            ? "The stored token is invalid or was revoked; paste a current one."
            : "Tokens exist on this server; paste one below to authenticate."}
        </div>
      )}
      {auth === "unreachable" && (
        <div className="banner banner-err">
          The API is not responding. Check that <code>cutsheet serve</code> is running.
        </div>
      )}

      <div className="panel">
        <h2>API token</h2>
        <form className="token-row" onSubmit={save}>
          <input
            type="password"
            className="mono"
            value={tokenInput}
            onChange={(e) => setTokenInput(e.target.value)}
            placeholder="cst_..."
            autoComplete="off"
          />
          <button className="btn-primary" type="submit">
            Save
          </button>
          <button type="button" onClick={clear} disabled={!getToken() && !tokenInput}>
            Clear
          </button>
        </form>
        <p className="form-hint">
          Stored in this browser's localStorage and sent as an Authorization: Bearer header
          on every API request. Tokens are minted on the server with{" "}
          <code>cutsheet token create</code> and shown exactly once.
        </p>
      </div>
    </>
  );
}
