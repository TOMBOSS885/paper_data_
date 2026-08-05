var PaperKBSync = (() => {
  const PLUGIN_ID = "paper-kb-sync@your-domain.example";
  const PREF_ROOT = "extensions.paper-kb-sync.";
  let rootURI = "";
  let observerID = null;
  let preferencePaneError = "";
  const windows = new Set();

  function pref(name) {
    return Zotero.Prefs.get(PREF_ROOT + name, true) || "";
  }

  function setPref(name, value) {
    Zotero.Prefs.set(PREF_ROOT + name, value, true);
  }

  function newUUID() {
    return Services.uuid.generateUUID().toString().slice(1, -1);
  }

  function serverURL() {
    return String(pref("serverURL")).replace(/\/$/, "");
  }

  function externalLibraryKey(libraryID) {
    return pref("externalLibraryKey") || `local:${ensureClientInstanceId()}:${libraryID}`;
  }

  function getCredential() {
    const host = serverURL();
    if (!host || !Services.logins) return "";
    try {
      const logins = Services.logins.findLogins({}, host, null, "Paper KB Sync");
      return logins.length ? logins[0].password : "";
    } catch (e) {
      Zotero.debug(`Paper KB Sync credential read failed: ${e}`);
      return "";
    }
  }

  function saveCredential(token) {
    const host = serverURL();
    if (!host || !Services.logins) throw new Error("先配置服务器地址");
    const LoginInfo = Components.Constructor(
      "@mozilla.org/login-manager/loginInfo;1", null,
      "init", [Ci.nsILoginInfo]
    );
    const info = new LoginInfo(host, null, "Paper KB Sync", "", token, "", "");
    const old = Services.logins.findLogins({}, host, null, "Paper KB Sync");
    for (const login of old) Services.logins.removeLogin(login);
    Services.logins.addLogin(info);
  }

  function ensureClientInstanceId() {
    let id = pref("clientInstanceId");
    if (!id) {
      id = newUUID();
      setPref("clientInstanceId", id);
    }
    return id;
  }

  async function request(method, path, options = {}) {
    const token = getCredential();
    if (!token) throw new Error("未配置同步令牌，请在插件设置中保存 token");
    const headers = new Headers(options.headers || {});
    headers.set("Authorization", `Bearer ${token}`);
    headers.set("X-PKB-Client-Version", "0.1.0");
    let body = options.body;
    const isFormData = typeof FormData !== "undefined" && body instanceof FormData;
    if (body && !isFormData && typeof body !== "string") {
      headers.set("Content-Type", "application/json");
      body = JSON.stringify(body);
    }
    const xhr = await Zotero.HTTP.request(method, serverURL() + path, {
      headers: Object.fromEntries(headers.entries()),
      body,
      responseType: "json",
      timeout: options.timeout || 30000,
      successCodes: false,
    });
    if (xhr.status < 200 || xhr.status >= 300) {
      const code = xhr.response?.error?.code || `http_${xhr.status}`;
      throw new Error(`${code}: ${xhr.response?.error?.message || xhr.statusText}`);
    }
    return xhr.response?.data ?? xhr.response;
  }

  async function sha256Hex(text) {
    const bytes = new TextEncoder().encode(text);
    const digest = await crypto.subtle.digest("SHA-256", bytes);
    return Array.from(new Uint8Array(digest), b => b.toString(16).padStart(2, "0")).join("");
  }

  function itemType(item) {
    try { return Zotero.ItemTypes.getName(item.itemTypeID); } catch (_) { return "journalArticle"; }
  }

  function field(item, name) {
    try { return item.getField(name) || ""; } catch (_) { return ""; }
  }

  async function snapshotItem(item) {
    const authors = item.getCreators().filter(c => c.creatorType === "author").map(c => {
      if (c.name) return c.name;
      return `${c.firstName || ""} ${c.lastName || ""}`.trim();
    });
    const yearMatch = String(field(item, "date")).match(/\b(\d{4})\b/);
    const metadata = {
      itemType: itemType(item),
      title: field(item, "title"),
      abstract: field(item, "abstractNote"),
      authors,
      doi: field(item, "DOI"),
      journal: field(item, "publicationTitle") || field(item, "bookTitle"),
      year: yearMatch ? Number(yearMatch[1]) : 0,
      tags: item.getTags().filter(t => !t.type).map(t => t.tag).sort(),
      extra: {},
    };
    const metadataHash = await sha256Hex(JSON.stringify(metadata));
    let filePath = "";
    let filename = "";
    for (const attachmentID of item.getAttachments()) {
      const attachment = Zotero.Items.get(attachmentID);
      if (attachment.attachmentContentType !== "application/pdf") continue;
      const path = await attachment.getFilePathAsync();
      if (path && await attachment.fileExists()) {
        filePath = path;
        filename = attachment.attachmentFilename || "paper.pdf";
        break;
      }
    }
    let fileSize = 0;
    if (filePath) {
      try { fileSize = (await IOUtils.stat(filePath)).size || 0; } catch (_) {}
    }
    return {
      externalLibraryKey: externalLibraryKey(item.libraryID),
      itemKey: item.key,
      localVersion: String(item.dateModified || ""),
      metadata,
      metadataHash,
      filePath,
      filename,
      fileSize,
      item,
    };
  }

  async function selectedSnapshots() {
    const pane = Zotero.getActiveZoteroPane();
    const seen = new Set();
    const out = [];
    for (const selected of pane.getSelectedItems()) {
      let item = selected;
      if (item.isAttachment() && item.parentID) item = Zotero.Items.get(item.parentID);
      if (!item || item.isNote() || item.isAttachment() || seen.has(item.id)) continue;
      seen.add(item.id);
      out.push(await snapshotItem(item));
    }
    return out;
  }

  async function createSession(snapshots) {
    const items = snapshots.map(s => ({
      externalLibraryKey: s.externalLibraryKey,
      itemKey: s.itemKey,
      localVersion: s.localVersion,
      metadata: s.metadata,
      metadataHash: s.metadataHash,
      fileSha256: "",
      fileSize: s.fileSize,
    }));
    return request("POST", "/api/sync/v1/sessions", {
      body: {
        clientInstanceId: ensureClientInstanceId(),
        displayName: "Zotero",
        externalLibraryKey: items[0]?.externalLibraryKey || externalLibraryKey(Zotero.getActiveZoteroPane().getSelectedLibraryID()),
        items,
      },
    });
  }

  async function uploadSnapshot(snapshot) {
    Components.utils.importGlobalProperties(["File", "FormData"]);
    const form = new FormData();
    form.append("externalLibraryKey", snapshot.externalLibraryKey);
    form.append("itemKey", snapshot.itemKey);
    form.append("metadata", JSON.stringify(snapshot.metadata));
    if (snapshot.filePath) {
      let file = File.createFromFileName(snapshot.filePath);
      if (file.then) file = await file;
      form.append("file", file, snapshot.filename || "paper.pdf");
    }
    return request("POST", "/api/sync/v1/papers", { body: form, timeout: 120000 });
  }

  async function linkImportedItem(serverPaperID, item) {
    return request("POST", "/api/sync/v1/links", {
      body: { paperId: serverPaperID, externalLibraryKey: externalLibraryKey(item.libraryID), itemKey: item.key },
    });
  }

  async function pullRow(row, localSnapshot) {
    const server = row.server;
    const md = server.metadata;
    const pane = Zotero.getActiveZoteroPane();
    const item = localSnapshot?.item || new Zotero.Item(md.itemType || "journalArticle");
    if (!localSnapshot) item.libraryID = pane.getSelectedLibraryID();
    const fields = { title: md.title, abstractNote: md.abstract, DOI: md.doi, publicationTitle: md.journal };
    for (const [key, value] of Object.entries(fields)) item.setField(key, value || "");
    item.setField("date", md.year ? String(md.year) : "");
    item.setCreators((md.authors || []).map(name => ({ name, creatorType: "author" })));
    item.setTags((md.tags || []).map(tag => ({ tag })));
    item.addRelation("papersync:paper", `${serverURL()}/api/sync/v1/papers/${server.paperId}`);
    await item.saveTx();
    if (server.file?.available) {
      const tempPath = PathUtils.join(Zotero.getTempDirectory().path, `paper-kb-${newUUID()}.pdf`);
      await Zotero.HTTP.download(`${serverURL()}/api/sync/v1/papers/${server.paperId}/file`, tempPath, {
        headers: { Authorization: `Bearer ${getCredential()}` },
      });
      await Zotero.Attachments.importFromFile({ file: tempPath, parentItemID: item.id, contentType: "application/pdf", title: server.file.originalName || "paper.pdf" });
      await IOUtils.remove(tempPath, { ignoreAbsent: true });
    }
    await linkImportedItem(server.paperId, item);
    return item;
  }

  async function openSyncDialog(window) {
    if (!serverURL() || !getCredential()) {
      window.alert("请先在 Zotero 设置中配置 Paper KB 服务器地址和同步令牌");
      return;
    }
    const snapshots = await selectedSnapshots();
    const session = await createSession(snapshots);
    const byKey = new Map(snapshots.map(s => [s.itemKey, s]));
    const io = {
      rows: session.diff.items || [],
      upload: async row => uploadSnapshot(byKey.get(row.local.itemKey)),
      pull: async row => {
        const item = await pullRow(row, byKey.get(row.local?.itemKey));
        const refreshed = await snapshotItem(item);
        byKey.set(refreshed.itemKey, refreshed);
        const index = snapshots.findIndex(s => s.itemKey === refreshed.itemKey);
        if (index >= 0) snapshots[index] = refreshed;
        else snapshots.push(refreshed);
        return refreshed.itemKey;
      },
      refresh: async () => (await createSession(snapshots)).diff.items || [],
    };
    window.openDialog(rootURI + "content/sync.xhtml", "paper-kb-sync-dialog", "chrome,dialog=no,resizable=yes", io);
  }

  function onMainWindowLoad({ window }) {
    if (windows.has(window)) return;
    windows.add(window);
    const doc = window.document;
    const menu = doc.getElementById("menu_ToolsPopup");
    if (!menu) return;
    const item = doc.createXULElement("menuitem");
    item.id = "paper-kb-sync-menuitem";
    item.setAttribute("label", "Paper KB 同步");
    item.addEventListener("command", () => openSyncDialog(window));
    menu.appendChild(item);
    window.paperKBSyncMenuItem = item;
    setPref("startupStatus", preferencePaneError ? `menu-ready; ${preferencePaneError}` : "ready");
  }

  function onMainWindowUnload({ window }) {
    window.paperKBSyncMenuItem?.remove();
    windows.delete(window);
  }

  async function startup({ rootURI: uri }) {
    rootURI = uri;
    try {
      ensureClientInstanceId();
      setPref("startupStatus", "loading");
      await Zotero.PreferencePanes.register({
        pluginID: PLUGIN_ID,
        id: "paper-kb-sync-preferences",
        src: rootURI + "preferences.xhtml",
        scripts: [rootURI + "preferences.js"],
        image: rootURI + "icons/paper-kb-sync.svg",
        label: "Paper KB Sync",
      });
      setPref("startupStatus", "preference-pane-registered");
    } catch (error) {
      preferencePaneError = String(error);
      setPref("startupStatus", `preference-pane-error: ${preferencePaneError}`);
      Zotero.logError(error);
    }
    observerID = Zotero.Notifier.registerObserver({ notify() {} }, ["item", "collection", "file"], PLUGIN_ID);
  }

  function shutdown() {
    for (const window of Array.from(windows)) onMainWindowUnload({ window });
    if (observerID !== null) Zotero.Notifier.unregisterObserver(observerID);
    observerID = null;
  }

  return { startup, shutdown, onMainWindowLoad, onMainWindowUnload, openSyncDialog, saveCredential, setPref };
})();
