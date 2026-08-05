# Paper KB Sync for Zotero 7

This directory contains the Zotero 7 plugin source. The packaged plugin is
created at `dist/paper-kb-sync-0.1.9.xpi`.

## Install

1. In Zotero 7, open `Tools -> Plugins`.
2. Drag `paper-kb-sync-0.1.0.xpi` into the Plugins window and restart Zotero.
3. Open `Edit -> Settings -> Paper KB Sync`.
4. Enter the public HTTPS URL of this Paper KB deployment and paste a sync token.
5. Select normal Zotero items, then use `Tools -> Paper KB Sync` to upload or
   compare them. Leave the selection empty and open the same command to browse
   and pull server-only papers.

The sync window always compares before changing data. It shows local-only,
server-only, matching, and changed rows. Select rows and choose upload or pull.
Only one stored PDF attachment per item is transferred in this version. Notes,
annotations, snapshots, linked URLs, and supplementary attachments are skipped.

## Build the XPI

From the repository root on Windows:

```powershell
.\zotero-plugin\build-xpi.ps1
```

The XPI must have `manifest.json` and `bootstrap.js` at its archive root.

## Development

Use a separate Zotero profile and data directory. Create a text file in the
profile `extensions` directory named `paper-kb-sync@your-domain.example`; its
only content is the absolute path of this `zotero-plugin` directory. Restart
Zotero with `-purgecaches -ZoteroDebugText -jsconsole` after source changes.
