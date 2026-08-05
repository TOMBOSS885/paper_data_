$ErrorActionPreference = 'Stop'

$pluginRoot = Split-Path -Parent $PSScriptRoot
$stage = Join-Path ([System.IO.Path]::GetTempPath()) ("paper-kb-sync-xpi-" + [guid]::NewGuid().ToString('N'))
$dist = Join-Path $PSScriptRoot 'dist'
$version = (Get-Content -Raw (Join-Path $PSScriptRoot 'manifest.json') | ConvertFrom-Json).version
$zip = Join-Path $dist "paper-kb-sync-$version.zip"
$xpi = Join-Path $dist "paper-kb-sync-$version.xpi"

New-Item -ItemType Directory -Path $stage | Out-Null
New-Item -ItemType Directory -Path $dist -Force | Out-Null
Copy-Item (Join-Path $PSScriptRoot 'manifest.json'), (Join-Path $PSScriptRoot 'bootstrap.js'), (Join-Path $PSScriptRoot 'prefs.js') -Destination $stage
Copy-Item (Join-Path $PSScriptRoot 'preferences.xhtml'), (Join-Path $PSScriptRoot 'preferences.js') -Destination $stage
Copy-Item (Join-Path $PSScriptRoot 'src'), (Join-Path $PSScriptRoot 'content'), (Join-Path $PSScriptRoot 'locale'), (Join-Path $PSScriptRoot 'icons') -Destination $stage -Recurse
Add-Type -AssemblyName System.IO.Compression
Add-Type -AssemblyName System.IO.Compression.FileSystem
if (Test-Path -LiteralPath $zip) { Remove-Item -LiteralPath $zip -Force }
$archive = [System.IO.Compression.ZipFile]::Open($zip, [System.IO.Compression.ZipArchiveMode]::Create)
try {
    Get-ChildItem -LiteralPath $stage -Recurse -File | ForEach-Object {
        $relative = $_.FullName.Substring($stage.Length).TrimStart('\', '/').Replace('\', '/')
        [System.IO.Compression.ZipFileExtensions]::CreateEntryFromFile($archive, $_.FullName, $relative, [System.IO.Compression.CompressionLevel]::Optimal) | Out-Null
    }
} finally {
    $archive.Dispose()
}
Move-Item -LiteralPath $zip -Destination $xpi -Force
Get-Item -LiteralPath $xpi | Select-Object FullName, Length, LastWriteTime
