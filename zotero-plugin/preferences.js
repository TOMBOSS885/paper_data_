var PaperKBSyncPrefs = (() => {
  const root = "extensions.paper-kb-sync.";
  function value(id) { return document.getElementById(id).value.trim(); }
  function setStatus(message) { document.getElementById("paper-kb-sync-status").value = message; }
  function init() {
    // The bootstrap startup path creates the persistent client ID before this pane opens.
  }
  async function save() {
    const server = value("paper-kb-sync-server").replace(/\/$/, "");
    const token = value("paper-kb-sync-token");
    if (!/^https:\/\//i.test(server) && !/^http:\/\/(localhost|127\.0\.0\.1)(:|\/|$)/i.test(server)) { setStatus("生产环境必须使用 HTTPS"); return; }
    if (!token) { setStatus("请输入令牌"); return; }
    Zotero.Prefs.set(root + "serverURL", server, true);
    const LoginInfo = Components.Constructor("@mozilla.org/login-manager/loginInfo;1", null, "init", [Ci.nsILoginInfo]);
    const old = Services.logins.findLogins({}, server, null, "Paper KB Sync");
    for (const login of old) Services.logins.removeLogin(login);
    Services.logins.addLogin(new LoginInfo(server, null, "Paper KB Sync", "", token, "", ""));
    try {
      const response = await Zotero.HTTP.request("GET", server + "/api/sync/v1/capabilities", { headers: { Authorization: `Bearer ${token}` }, responseType: "json", timeout: 15000 });
      if (response.status >= 200 && response.status < 300) setStatus("连接成功"); else setStatus("连接失败");
    } catch (error) { setStatus(`连接失败：${error}`); }
  }
  return { init, save };
})();
