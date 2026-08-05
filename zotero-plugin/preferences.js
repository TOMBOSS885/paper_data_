var PaperKBSyncPrefs = (() => {
  "use strict";

  const root = "extensions.paper-kb-sync.";
  const loginRealm = "Paper KB Sync";
  const { Services } = ChromeUtils.importESModule("resource://gre/modules/Services.sys.mjs");
  let saving = false;

  function element(id) {
    return document.getElementById(id);
  }

  function value(id) {
    return String(element(id).value || "").trim();
  }

  function setStatus(message, kind = "info") {
    const status = element("paper-kb-sync-status");
    status.textContent = message;
    status.setAttribute("data-state", kind);
    status.style.color = kind === "error" ? "#d9534f" : kind === "success" ? "#3d9b52" : "";
    status.style.marginTop = "8px";
    status.style.minHeight = "1.4em";
  }

  function normalizeServerURL(input) {
    let url;
    try {
      url = new URL(input);
    } catch (_) {
      throw new Error("服务器地址不是有效的 URL");
    }
    const localHTTP = url.protocol === "http:" && (url.hostname === "localhost" || url.hostname === "127.0.0.1");
    if (url.protocol !== "https:" && !localHTTP) {
      throw new Error("生产服务器必须使用 HTTPS");
    }
    if (url.username || url.password || url.search || url.hash) {
      throw new Error("服务器地址不能包含账号、查询参数或片段");
    }
    url.pathname = url.pathname.replace(/\/+$/, "");
    return url.toString().replace(/\/$/, "");
  }

  function credentialOrigin(server) {
    return new URL(server).origin;
  }

  function matchingCredentials(origin) {
    return Services.logins.findLogins(origin, null, loginRealm);
  }

  function replaceCredential(server, token) {
    const origin = credentialOrigin(server);
    const LoginInfo = Components.Constructor(
      "@mozilla.org/login-manager/loginInfo;1",
      null,
      "init",
      [Ci.nsILoginInfo]
    );
    const existing = matchingCredentials(origin);
    for (const login of existing) Services.logins.removeLogin(login);
    Services.logins.addLogin(new LoginInfo(origin, null, loginRealm, "paper-kb-sync", token, "", ""));
  }

  async function verify(server, token) {
    const response = await Zotero.HTTP.request("GET", `${server}/api/sync/v1/capabilities`, {
      headers: { Authorization: `Bearer ${token}` },
      responseType: "json",
      timeout: 15000,
      successCodes: false,
    });
    if (response.status < 200 || response.status >= 300) {
      const error = response.response?.error;
      throw new Error(error?.message || `服务器返回 HTTP ${response.status}`);
    }
  }

  async function save() {
    if (saving) return;
    saving = true;
    const button = element("paper-kb-sync-save");
    button.disabled = true;
    try {
      const server = normalizeServerURL(value("paper-kb-sync-server"));
      const token = value("paper-kb-sync-token");
      if (!token) throw new Error("请输入同步令牌");
      setStatus("正在测试服务器连接…");
      await verify(server, token);
      Zotero.Prefs.set(root + "serverURL", server, true);
      replaceCredential(server, token);
      element("paper-kb-sync-token").value = "";
      setStatus("连接成功，服务器地址和同步令牌已保存。", "success");
    } catch (error) {
      const detail = error?.message || String(error);
      setStatus(`保存失败：${detail}`, "error");
      Zotero.logError(error);
    } finally {
      saving = false;
      button.disabled = false;
    }
  }

  function init() {
    const button = element("paper-kb-sync-save");
    if (!button || button.dataset.paperKbSyncBound === "true") return;
    button.dataset.paperKbSyncBound = "true";
    const startSave = () => { void save(); };
    button.addEventListener("command", startSave);
    button.addEventListener("click", startSave);
    setStatus("输入服务器地址和同步令牌后，点击保存并测试连接。");
  }

  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", init, { once: true });
  } else {
    init();
  }
  return { init, save };
})();
