$ErrorActionPreference = 'Stop'
Set-Location (Split-Path $PSScriptRoot -Parent)
node scripts/configure.mjs
if ($LASTEXITCODE -ne 0) { throw 'Configuration failed' }
node scripts/images.mjs
if ($LASTEXITCODE -ne 0) { throw 'Catalog image build failed' }
docker compose build
if ($LASTEXITCODE -ne 0) { throw 'Platform image build failed' }
docker compose up -d --wait
if ($LASTEXITCODE -ne 0) { throw 'Platform startup failed; inspect docker compose logs' }
Write-Host 'Kzure is running at http://localhost:8088'
Write-Host 'Your administrator credentials are in .local/admin-credentials.txt'
