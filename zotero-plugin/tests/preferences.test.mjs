import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";
import vm from "node:vm";

const source = await readFile(new URL("../preferences.js", import.meta.url), "utf8");

function makeElement(value = "") {
  const listeners = new Map();
  return {
    value,
    textContent: "",
    disabled: false,
    dataset: {},
    style: {},
    attributes: new Map(),
    addEventListener(type, listener) { listeners.set(type, listener); },
    dispatch(type) { return listeners.get(type)?.(); },
    setAttribute(name, value) { this.attributes.set(name, value); },
  };
}

function loadPreferencePage({ server = "https://papers.example.com", token = "", status = 200 } = {}) {
  const elements = {
    "paper-kb-sync-server": makeElement(server),
    "paper-kb-sync-token": makeElement(token),
    "paper-kb-sync-save": makeElement(),
    "paper-kb-sync-status": makeElement(),
  };
  const savedPreferences = [];
  const savedLogins = [];
  const errors = [];
  const context = {
    URL,
    Promise,
    setTimeout,
    document: {
      readyState: "complete",
      getElementById(id) { return elements[id] ?? null; },
      addEventListener() { throw new Error("unexpected document event registration"); },
    },
    ChromeUtils: { importESModule() { return { Services: context.Services }; } },
    Components: {
      Constructor() {
        return function LoginInfo(origin, formActionOrigin, httpRealm, username, password) {
          return { origin, formActionOrigin, httpRealm, username, password };
        };
      },
    },
    Ci: { nsILoginInfo: Symbol("nsILoginInfo") },
    Services: {
      logins: {
        findLogins() { return []; },
        removeLogin() {},
        addLogin(login) { savedLogins.push(login); },
      },
    },
    Zotero: {
      HTTP: {
        async request() {
          return status === 200 ? { status: 200, response: { data: {} } } : {
            status,
            response: { error: { message: "token rejected" } },
          };
        },
      },
      Prefs: { set(key, value, global) { savedPreferences.push({ key, value, global }); } },
      logError(error) { errors.push(error); },
    },
  };
  vm.runInNewContext(source, context, { filename: "preferences.js" });
  return { elements, savedPreferences, savedLogins, errors, api: context.PaperKBSyncPrefs };
}

test("preference page reports an empty token instead of failing silently", async () => {
  const page = loadPreferencePage();
  await page.api.save();

  assert.equal(page.elements["paper-kb-sync-status"].textContent, "保存失败：请输入同步令牌");
  assert.equal(page.elements["paper-kb-sync-save"].disabled, false);
  assert.equal(page.savedPreferences.length, 0);
  assert.equal(page.savedLogins.length, 0);
});

test("preference page reports a rejected server token and does not save it", async () => {
  const page = loadPreferencePage({ token: "test-token", status: 401 });
  await page.api.save();

  assert.equal(page.elements["paper-kb-sync-status"].textContent, "保存失败：token rejected");
  assert.equal(page.savedPreferences.length, 0);
  assert.equal(page.savedLogins.length, 0);
});

test("preference page saves a verified token and exposes a success message", async () => {
  const page = loadPreferencePage({ server: "https://papers.example.com/", token: "test-token" });
  await page.api.save();

  assert.equal(page.elements["paper-kb-sync-status"].textContent, "连接成功，服务器地址和同步令牌已保存。");
  assert.deepEqual(page.savedPreferences, [{
    key: "extensions.paper-kb-sync.serverURL",
    value: "https://papers.example.com",
    global: true,
  }]);
  assert.equal(page.savedLogins.length, 1);
  assert.equal(page.savedLogins[0].origin, "https://papers.example.com");
  assert.equal(page.savedLogins[0].password, "test-token");
  assert.equal(page.elements["paper-kb-sync-token"].value, "");
});
