param(
    [Parameter(Mandatory=$true)][string]$ProductRoot,
    [Parameter(Mandatory=$true)][string]$Archive,
    [Parameter(Mandatory=$true)][string]$Output
)
$ErrorActionPreference = 'Stop'
$ProductRoot = [IO.Path]::GetFullPath($ProductRoot)
$package = Get-Content -LiteralPath (Join-Path $ProductRoot 'package.json') -Raw | ConvertFrom-Json
$script = Join-Path $ProductRoot 'components/install-tls-provider.ps1'
$catalog = Get-Content -LiteralPath (Join-Path $ProductRoot 'components/tls-providers.lock.json') -Raw | ConvertFrom-Json
$pin = $catalog.platforms.PSObject.Properties[$package.platform].Value
$machine = $package.install_scope -eq 'machine'
Import-Module (Join-Path $ProductRoot 'updater/Update.Core.psm1') -Force -DisableNameChecking
$scope = Get-GnScopeContext $ProductRoot $package
$destination = Join-Path $scope.components ('tls-openssl/'+$package.platform+'/'+$pin.archive_sha256)
$fresh = -not [IO.Directory]::Exists($destination)
$arguments = @('-NoLogo','-NoProfile','-NonInteractive','-ExecutionPolicy','Bypass','-File',$script,'-Platform',$package.platform,'-Archive',[IO.Path]::GetFullPath($Archive),'-Quiet')
if ($machine) { $arguments += '-Machine' }
for ($round=0; $round -lt 2; $round++) {
    & (Join-Path $env:SystemRoot 'System32/WindowsPowerShell/v1.0/powershell.exe') @arguments
    if ($LASTEXITCODE -ne 0) { throw ('TLS preparation/idempotence failed in round '+$round) }
}
$verified = 0
foreach ($entry in $pin.files.PSObject.Properties) {
    $path = Assert-GnOwnedPath $destination (Join-Path $destination $entry.Name)
    if ($machine) { Assert-GnProtectedObject $path }
    if ((Get-FileHash -LiteralPath $path).Hash.ToLowerInvariant() -cne $entry.Value.sha256 -or (Get-Item -LiteralPath $path).Length -ne $entry.Value.size) { throw 'Published TLS component differs from its pin.' }
    $verified++
}
if (@(Get-ChildItem -LiteralPath ([IO.Path]::GetDirectoryName($destination)) -Directory -Force | Where-Object Name -like '.stage-*').Count) { throw 'TLS staging directory leaked.' }
[pscustomobject]@{status='pass';platform=$package.platform;scope=$package.install_scope;fresh_component_prepared=$fresh;idempotent_preparation=$true;verified_files=$verified;script_sha256=(Get-FileHash -LiteralPath $script).Hash.ToLowerInvariant();archive_sha256=$pin.archive_sha256;protected_machine_acl_verified=$machine} | ConvertTo-Json | Set-Content -LiteralPath $Output -Encoding UTF8
