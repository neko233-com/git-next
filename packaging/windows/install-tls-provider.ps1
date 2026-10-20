param(
    [ValidateSet('windows-x64','windows-arm64')][string]$Platform = 'windows-x64',
    [string]$Archive,
    [switch]$Quiet,
    [switch]$Machine
)
Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'
$productRoot = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))
Import-Module (Join-Path $productRoot 'updater/Update.Core.psm1') -Force -DisableNameChecking
$catalog = Get-Content -LiteralPath (Join-Path $PSScriptRoot 'tls-providers.lock.json') -Raw -Encoding UTF8 | ConvertFrom-Json
if ($catalog.schema_version -ne 1 -or $catalog.component -cne 'tls-openssl' -or $catalog.bundled_in_git_next_release -ne $false) { throw 'Invalid independent TLS provider catalog.' }
$pin = $catalog.platforms.PSObject.Properties[$Platform].Value
if (-not $pin -or $pin.archive_sha256 -notmatch '^[a-f0-9]{64}$' -or $pin.entrypoint -cne ($pin.native_prefix + '/bin/libcurl-openssl-4.dll')) { throw 'Invalid TLS provider pin.' }
if ($pin.archive_url -notmatch '^https://github\.com/git-for-windows/git/releases/download/v2\.54\.0\.windows\.1/Git-2\.54\.0-(64-bit|arm64)\.tar\.bz2$') { throw 'Unexpected independent provider source.' }
if ($Machine) {
    if (-not (Test-GnAdministrator)) { throw 'Machine TLS preparation requires an administrator.' }
    $package = Get-Content -LiteralPath (Join-Path $productRoot 'package.json') -Raw -Encoding UTF8 | ConvertFrom-Json
    $scope = Get-GnScopeContext $productRoot $package
    if (-not $scope.machine) { throw 'Machine TLS preparation requires the registered machine MSI.' }
    $componentHome = $scope.components
    Initialize-GnMachineDirectory $componentHome
} else { $componentHome = if ($env:GIT_NEXT_COMPONENT_HOME) { $env:GIT_NEXT_COMPONENT_HOME } else { Join-Path $env:LOCALAPPDATA 'git-next/components' } }
if (-not [IO.Path]::IsPathRooted($componentHome)) { throw 'GIT_NEXT_COMPONENT_HOME must be an absolute directory.' }
$componentHome = [IO.Path]::GetFullPath($componentHome).TrimEnd('\','/')
$platformRoot = Assert-GnOwnedPath $componentHome (Join-Path $componentHome ('tls-openssl/' + $Platform))
if ($Machine) { Initialize-GnMachineDirectory $platformRoot } else { [IO.Directory]::CreateDirectory($platformRoot) | Out-Null }
$destination = Assert-GnOwnedPath $platformRoot (Join-Path $platformRoot $pin.archive_sha256)
$lockPath = Assert-GnOwnedPath $platformRoot (Join-Path $platformRoot '.install.lock')
$lock = $null
$deadline = [DateTime]::UtcNow.AddMinutes(3)
while (-not $lock) {
    try { $lock = Open-GnExclusiveLock $lockPath }
    catch [IO.IOException] {
        if ([DateTime]::UtcNow -gt $deadline) { throw 'TLS component preparation is busy.' }
        Start-Sleep -Milliseconds 250
    }
}
$stage = $null
$client = $null
$temporaryDownload = $null
$held = [Collections.Generic.List[IDisposable]]::new()
try {
    $entries = @($pin.files.PSObject.Properties)
    foreach ($entry in $entries) {
        if ($entry.Name -notmatch '^[a-zA-Z0-9_./+-]+$' -or $entry.Name.Contains('..') -or $entry.Value.sha256 -notmatch '^[a-f0-9]{64}$') { throw 'Unsafe TLS provider entry.' }
        if (-not $entry.Name.StartsWith($pin.native_prefix + '/', [StringComparison]::Ordinal) -or $entry.Value.archive_member -cne $entry.Name) { throw 'Unexpected TLS provider archive member.' }
    }
    if ([IO.Directory]::Exists($destination)) {
        if ($Machine) { Assert-GnProtectedObject $destination }
        foreach ($entry in $entries) {
            $file = Assert-GnOwnedPath $destination (Join-Path $destination $entry.Name)
            if ($Machine) { Assert-GnProtectedObject $file }
            if (-not [IO.File]::Exists($file) -or (Get-FileHash -LiteralPath $file -Algorithm SHA256).Hash.ToLowerInvariant() -cne $entry.Value.sha256) { throw 'Existing TLS component was modified; preserve it and prepare a clean component location.' }
        }
        if (-not $Quiet) { Write-Output 'TLS provider is already prepared.' }
        exit 0
    }
    if ($Machine -and $Archive) {
        $downloads = Assert-GnOwnedPath $componentHome (Join-Path $componentHome 'downloads')
        Initialize-GnMachineDirectory $downloads
        $temporaryDownload = Assert-GnOwnedPath $downloads (Join-Path $downloads ([Guid]::NewGuid().ToString('N') + '.offline'))
        $inputStream = [IO.File]::OpenRead([IO.Path]::GetFullPath($Archive))
        $outputStream = $null
        try { $outputStream = Open-GnNewFile $temporaryDownload; $inputStream.CopyTo($outputStream); $outputStream.Flush($true) }
        finally { if ($outputStream) { $outputStream.Dispose() }; $inputStream.Dispose() }
        $Archive = $temporaryDownload
    }
    if (-not $Archive) {
        $downloads = Assert-GnOwnedPath $componentHome (Join-Path $componentHome 'downloads')
        if ($Machine) { Initialize-GnMachineDirectory $downloads } else { [IO.Directory]::CreateDirectory($downloads) | Out-Null }
        $Archive = Assert-GnOwnedPath $downloads (Join-Path $downloads ($pin.archive_sha256 + '.tar.bz2'))
        if (-not [IO.File]::Exists($Archive)) {
            $temporaryDownload = Assert-GnOwnedPath $downloads (Join-Path $downloads ([Guid]::NewGuid().ToString('N') + '.download'))
            $client = New-GnHttpClient
            $plan = [pscustomobject]@{ Url = $pin.archive_url; Entry = [pscustomobject]@{ size = $pin.archive_size; sha256 = $pin.archive_sha256 } }
            Save-GnVerifiedPackage $client $plan $temporaryDownload | Out-Null
            [IO.File]::Move($temporaryDownload, $Archive)
            $temporaryDownload = $null
        }
    }
    $Archive = [IO.Path]::GetFullPath($Archive)
    if ($Machine) { Assert-GnProtectedObject $Archive }
    if (-not [IO.File]::Exists($Archive) -or (Get-Item -LiteralPath $Archive).Length -ne $pin.archive_size -or (Get-FileHash -LiteralPath $Archive -Algorithm SHA256).Hash.ToLowerInvariant() -cne $pin.archive_sha256) { throw 'Independent TLS archive checksum mismatch.' }
    $stage = Assert-GnOwnedPath $platformRoot (Join-Path $platformRoot ('.stage-' + [Guid]::NewGuid().ToString('N')))
    if ($Machine) { Initialize-GnMachineDirectory $stage -Private } else { [IO.Directory]::CreateDirectory($stage) | Out-Null }
    $tar = Join-Path $env:SystemRoot 'System32/tar.exe'
    if (-not [IO.File]::Exists($tar)) { throw 'The Windows archive tool is unavailable.' }
    $arguments = @('-x','-f',$Archive,'-C',$stage,'--') + @($entries | ForEach-Object { $_.Value.archive_member }) + @($pin.native_prefix + '/share/licenses')
    $tarMessages = & $tar @arguments 2>&1
    if ($LASTEXITCODE -ne 0) { throw 'Could not extract the independent TLS component.' }
    if ($Machine) {
        foreach ($item in @(Get-ChildItem -LiteralPath $stage -Force -Recurse)) { Set-GnMachinePublicObject $item.FullName }
    }
    foreach ($entry in $entries) {
        $source = Assert-GnOwnedPath $stage (Join-Path $stage $entry.Value.archive_member)
        if ($Machine) { $held.Add([IO.File]::Open($source,[IO.FileMode]::Open,[IO.FileAccess]::Read,([IO.FileShare]::Read -bor [IO.FileShare]::Delete))) }
        if (-not [IO.File]::Exists($source) -or (Get-Item -LiteralPath $source).Length -ne $entry.Value.size -or (Get-FileHash -LiteralPath $source -Algorithm SHA256).Hash.ToLowerInvariant() -cne $entry.Value.sha256) { throw 'Extracted TLS component checksum mismatch.' }
    }
    Write-GnJson (Join-Path $stage 'provider.json') ([pscustomobject]@{ schema_version=1; platform=$Platform; archive_sha256=$pin.archive_sha256; private_component=$true })
    # Both resolved directories stay under this component's owned platform root.
    $stage = Assert-GnOwnedPath $platformRoot ([IO.Path]::GetFullPath($stage))
    $destination = Assert-GnOwnedPath $platformRoot ([IO.Path]::GetFullPath($destination))
    if ($Machine) { Set-GnMachinePublicObject $stage }
    [IO.Directory]::Move($stage, $destination)
    $stage = $null
    if (-not $Quiet) { Write-Output 'Independent TLS provider prepared.' }
} finally {
    foreach ($handle in $held) { $handle.Dispose() }
    if ($client) { $client.Dispose() }
    if ($temporaryDownload -and [IO.File]::Exists($temporaryDownload)) { [IO.File]::Delete($temporaryDownload) }
    if ($stage -and [IO.Directory]::Exists($stage)) {
        $resolved = Assert-GnOwnedPath $platformRoot ([IO.Path]::GetFullPath($stage))
        if ([IO.Path]::GetFileName($resolved) -notmatch '^\.stage-[a-f0-9]{32}$') { throw 'Unexpected TLS staging directory; cleanup refused.' }
        Remove-Item -LiteralPath $resolved -Recurse -Force
    }
    if ($lock) { $lock.Dispose() }
}
