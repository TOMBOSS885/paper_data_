$ErrorActionPreference = 'Stop'

$Root = Split-Path -Parent $MyInvocation.MyCommand.Path
$EnvFile = Join-Path $Root '.env'
$ComposeFile = Join-Path $Root 'deploy\docker-compose.yml'

if (-not (Get-Command docker -ErrorAction SilentlyContinue)) {
    throw 'Docker Engine is not installed. Install Docker and Compose v2 first.'
}

docker compose version | Out-Null

if (-not (Test-Path $EnvFile)) {
    Copy-Item (Join-Path $Root '.env.example') $EnvFile
    throw 'Created .env. Configure MYSQL_PASSWORD, JWT_SECRET, and SETUP_SECRET, then run .\deploy.ps1 again.'
}

$EnvText = Get-Content -Raw $EnvFile
if ($EnvText -match 'replace_with_') {
    throw '.env still contains example values. Configure MYSQL_PASSWORD, JWT_SECRET, and SETUP_SECRET first.'
}

docker compose --env-file $EnvFile -f $ComposeFile config --quiet
docker compose --env-file $EnvFile -f $ComposeFile up -d --build --remove-orphans

$PortLine = Get-Content $EnvFile | Where-Object { $_ -match '^HTTP_PORT=' } | Select-Object -Last 1
$HttpPort = if ($PortLine) { ($PortLine -split '=', 2)[1].Trim() } else { '80' }

Write-Host 'Waiting for services to become healthy...'
for ($i = 0; $i -lt 30; $i++) {
    try {
        $Response = Invoke-WebRequest -UseBasicParsing "http://127.0.0.1:$HttpPort/api/health" -TimeoutSec 3
        if ($Response.StatusCode -eq 200) {
            Write-Host "Deployment completed: http://SERVER_ADDRESS:$HttpPort/setup"
            Write-Host 'Enter SETUP_SECRET from .env during first-time setup to create the administrator.'
            exit 0
        }
    } catch {}
    Start-Sleep -Seconds 3
}

docker compose --env-file $EnvFile -f $ComposeFile logs --tail=100 api web
throw 'Services did not become ready. Inspect the logs above.'
