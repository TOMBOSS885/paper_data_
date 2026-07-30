# Paper Knowledge Base API

Run locally after MySQL is available:

```powershell
Copy-Item ..\doc\env.example .env
$env:JWT_SECRET = 'replace-with-a-random-secret-at-least-32-bytes'
go run .\cmd\server
```

The server runs migrations when `AUTO_MIGRATE=true` (the development default). Production should run the versioned migration step once and set `AUTO_MIGRATE=false`. Files are written below `UPLOAD_DIR` with UUID keys and mode `0600`.

Migration `006_drop_citation_formats.sql` removes the retired citation-format feature while preserving migration continuity for existing deployments.

The API uses `pkb_session` and `pkb_csrf` cookies. All authenticated state-changing requests require an `X-CSRF-Token` header matching the CSRF cookie.

Set `SETUP_SECRET` to a separate random secret before first deployment. `GET /api/setup/status` intentionally returns only `initialized`; send this secret as `setupNonce` to `POST /api/setup/admin` through the protected setup interface.
