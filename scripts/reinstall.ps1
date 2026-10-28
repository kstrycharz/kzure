#requires -Version 7.0
param([switch]$WipeData)
$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest
if (-not $WipeData) { throw 'This permanently deletes this Kzure installation and all workload data. Run ./scripts/reinstall.ps1 -WipeData only when a complete reset is intended.' }
$workspacePath = [IO.Path]::GetFullPath((Split-Path $PSScriptRoot -Parent)).TrimEnd('\','/')
Set-Location -LiteralPath $workspacePath
$privatePath = [IO.Path]::GetFullPath((Join-Path $workspacePath '.local'))
$environmentPath = [IO.Path]::GetFullPath((Join-Path $workspacePath '.env'))
if ([IO.Path]::GetDirectoryName($privatePath) -ne $workspacePath -or [IO.Path]::GetFileName($privatePath) -ne '.local' -or [IO.Path]::GetDirectoryName($environmentPath) -ne $workspacePath) { throw 'Refusing to wipe paths outside this checkout' }
if ((Test-Path -LiteralPath $privatePath) -and ((Get-Item -LiteralPath $privatePath -Force).Attributes -band [IO.FileAttributes]::ReparsePoint)) { throw 'Refusing to wipe a linked private directory' }
if (-not (Test-Path -LiteralPath $environmentPath)) { throw 'No current installation configuration exists. Use scripts/start.ps1 for a new installation.' }
$configuration = @{}
Get-Content -LiteralPath $environmentPath | ForEach-Object { if ($_ -match '^([^=]+)=(.*)$') { $configuration[$Matches[1]]=$Matches[2] } }
$oldInstallation = $configuration['KZURE_INSTALL_ID']
if ($oldInstallation -notmatch '^[a-f0-9]{32}$') { throw 'Cannot safely identify the current Kzure installation' }
function Docker-Read([string[]]$Arguments) {
    $result = & docker @Arguments
    if ($LASTEXITCODE -ne 0) { throw ('Docker inventory failed: ' + $Arguments[0]) }
    return @($result | Where-Object { $_ -and $_.Trim() })
}
function Docker-Run([string[]]$Arguments) {
    & docker @Arguments
    if ($LASTEXITCODE -ne 0) { throw ('Docker operation failed: ' + $Arguments[0]) }
}
# Snapshot exact identities before stopping services. Validate every deletion
# against labels, and refuse a Compose project belonging to another checkout.
$composeContainers = @(Docker-Read @('ps','-aq','--filter','label=com.docker.compose.project=kzure'))
foreach ($containerId in $composeContainers) {
    $labels = (Docker-Read @('inspect','--type','container','--format','{{json .Config.Labels}}',$containerId)) -join '' | ConvertFrom-Json -AsHashtable
    $ownerPath = $labels['com.docker.compose.project.working_dir']
    if (-not $ownerPath -or [IO.Path]::GetFullPath($ownerPath).TrimEnd('\','/') -ne $workspacePath) { throw 'A different checkout owns the kzure Compose project; reset refused' }
}
$ownedContainers = @(Docker-Read @('ps','-aq','--filter',"label=io.kzure.installation=$oldInstallation"))
$ownedVolumes = @(Docker-Read @('volume','ls','-q','--filter',"label=io.kzure.installation=$oldInstallation"))
$ownedNetworks = @(Docker-Read @('network','ls','-q','--filter',"label=io.kzure.installation=$oldInstallation"))
Write-Host "Resetting this checkout's Kzure services and $($ownedContainers.Count) workload/helper containers, $($ownedVolumes.Count) owned data volumes, $($ownedNetworks.Count) owned networks."
Docker-Run @('compose','-f','compose.yaml','-f','compose.https.yaml','stop')
foreach ($containerId in $ownedContainers) {
    $labels = (Docker-Read @('inspect','--type','container','--format','{{json .Config.Labels}}',$containerId)) -join '' | ConvertFrom-Json -AsHashtable
    if ($labels['io.kzure.installation'] -ne $oldInstallation) { throw 'Container ownership changed; reset stopped' }
    Docker-Run @('rm','-f',$containerId)
}
Docker-Run @('compose','-f','compose.yaml','-f','compose.https.yaml','down','--volumes','--remove-orphans')
$policyImage = 'kzure/network-policy:0.1.0'
Docker-Run @('run','--rm','--network','host','--cap-drop','ALL','--cap-add','NET_ADMIN','--security-opt','no-new-privileges:true','--env',"KZURE_INSTALL_ID=$oldInstallation",'--mount',"type=bind,source=$workspacePath/scripts/reset-network-policy.sh,target=/reset-policy.sh,readonly",'--entrypoint','/bin/sh',$policyImage,'/reset-policy.sh')
foreach ($networkId in $ownedNetworks) {
    $labels = (Docker-Read @('network','inspect','--format','{{json .Labels}}',$networkId)) -join '' | ConvertFrom-Json -AsHashtable
    if ($labels['io.kzure.installation'] -ne $oldInstallation) { throw 'Network ownership changed; reset stopped' }
    Docker-Run @('network','rm',$networkId)
}
foreach ($volumeName in $ownedVolumes) {
    $labels = (Docker-Read @('volume','inspect','--format','{{json .Labels}}',$volumeName)) -join '' | ConvertFrom-Json -AsHashtable
    if ($labels['io.kzure.installation'] -ne $oldInstallation) { throw 'Volume ownership changed; reset stopped' }
    Docker-Run @('volume','rm',$volumeName)
}
Docker-Run @('compose','-f','compose.yaml','-f','compose.https.yaml','down','--volumes','--remove-orphans')
foreach ($kind in @('container','volume','network')) {
    $remaining = @(if ($kind -eq 'container') { Docker-Read @('ps','-aq','--filter',"label=io.kzure.installation=$oldInstallation") } else { Docker-Read @($kind,'ls','-q','--filter',"label=io.kzure.installation=$oldInstallation") })
    if ($remaining.Count -gt 0) { throw 'Old installation objects remain; private configuration has been preserved for inspection' }
    $composeRemaining = @(if ($kind -eq 'container') { Docker-Read @('ps','-aq','--filter','label=com.docker.compose.project=kzure') } else { Docker-Read @($kind,'ls','-q','--filter','label=com.docker.compose.project=kzure') })
    if ($composeRemaining.Count -gt 0) { throw 'Compose objects remain; private configuration has been preserved for inspection' }
}
# The full absolute paths above were verified against this checkout. No source
# files, external directories, images or unrelated Docker projects are pruned.
Remove-Item -LiteralPath $environmentPath -Force
if (Test-Path -LiteralPath $privatePath) { Remove-Item -LiteralPath $privatePath -Recurse -Force }
& node scripts/configure.mjs
if ($LASTEXITCODE -ne 0) { throw 'Fresh configuration failed' }
& node scripts/images.mjs
if ($LASTEXITCODE -ne 0) { throw 'Fresh catalog build failed' }
Docker-Run @('compose','build')
& node scripts/configure-https.mjs
if ($LASTEXITCODE -ne 0) { throw 'Fresh local certificates failed' }
Docker-Run @('compose','-f','compose.yaml','-f','compose.https.yaml','up','-d','--wait','--wait-timeout','120')
& node scripts/verify-fresh-install.mjs
if ($LASTEXITCODE -ne 0) { throw 'Fresh installation verification failed' }
Write-Host 'Kzure is freshly installed. Private administrator credentials: .local/admin-credentials.txt'
