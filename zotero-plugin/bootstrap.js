var { Services } = ChromeUtils.importESModule("resource://gre/modules/Services.sys.mjs");
var pluginRootURI = null;
var pluginMain = null;

async function startup({ rootURI }, reason) {
  await Zotero.initializationPromise;
  pluginRootURI = rootURI;
  let scope = {
    Zotero,
    Services,
    Cc: Components.classes,
    Ci: Components.interfaces,
    Components,
    ChromeUtils,
    PathUtils: globalThis.PathUtils,
    IOUtils: globalThis.IOUtils,
    rootURI,
  };
  Services.scriptloader.loadSubScript(rootURI + "src/main.js", scope);
  pluginMain = scope.PaperKBSync;
  await pluginMain.startup({ rootURI });
  for (let window of Zotero.getMainWindows()) pluginMain.onMainWindowLoad({ window });
}

function shutdown() {
  if (pluginMain) pluginMain.shutdown();
  pluginMain = null;
  pluginRootURI = null;
}

function install() {}
function uninstall() {}

function onMainWindowLoad({ window }) {
  if (pluginMain) pluginMain.onMainWindowLoad({ window });
}

function onMainWindowUnload({ window }) {
  if (pluginMain) pluginMain.onMainWindowUnload({ window });
}
