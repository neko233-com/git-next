param(
    [Parameter(Mandatory)][string]$Archive,
    [string]$BinaryDirectory = (Join-Path $PSScriptRoot '..\target\release')
)
$ErrorActionPreference = 'Stop'
$Archive = (Resolve-Path -LiteralPath $Archive).Path
$source = (Resolve-Path -LiteralPath $BinaryDirectory).Path
$installer = (Resolve-Path -LiteralPath (Join-Path $PSScriptRoot '..\install.ps1')).Path
$supportInstaller = (Resolve-Path -LiteralPath (Join-Path $PSScriptRoot '..\install-credential-support.ps1')).Path
$root = Join-Path ([IO.Path]::GetTempPath()) ('git-next-credential-install-' + [Guid]::NewGuid().ToString('N'))
$prefix = Join-Path $root 'prefix'
$commands = Join-Path $root 'commands'
$savedHome = $env:HOME
$savedProfile = $env:USERPROFILE
$savedPath = $env:PATH
$savedCompiler = $env:GIT_NEXT_FIXTURE_RUSTC
$originalGit = (Get-Command git -CommandType Application | Select-Object -First 1).Source
$originalHash = (Get-FileHash -LiteralPath $originalGit -Algorithm SHA256).Hash
try {
    $env:PATH = (Join-Path $HOME '.cargo\bin') + ';' + $env:PATH
    $env:GIT_NEXT_FIXTURE_RUSTC = (& (Join-Path $HOME '.cargo\bin\rustup.exe') which rustc) -join ''
    if ($LASTEXITCODE -ne 0) { throw 'Fixture compiler discovery failed.' }
    New-Item -ItemType Directory -Path (Join-Path $root 'home') | Out-Null
    $devtool = $env:GIT_NEXT_TOOL
    if (-not $devtool) {
        $devtool = Join-Path $root 'git-next-tool.exe'
        & go -C (Join-Path $PSScriptRoot '..\tools\devtool') build -trimpath -o $devtool .
        if ($LASTEXITCODE -ne 0) { throw 'Go fixture tool build failed.' }
    }
    $devtool = (Resolve-Path -LiteralPath $devtool).Path
    $env:HOME = Join-Path $root 'home'
    $env:USERPROFILE = $env:HOME
    & $installer -Prefix $prefix -CommandDirectory $commands -NoShortcut -NoPathUpdate -SourceBinary (Join-Path $source 'git-next.exe') -SourceTerminal (Join-Path $source 'git-terminal.exe')
    $bad = Join-Path $root 'bad.zip'
    [IO.File]::WriteAllText($bad, 'invalid fixture archive')
    $rejected = $false
    try { & $supportInstaller -Archive $bad -Prefix $prefix -CommandDirectory $commands }
    catch { $rejected = $_.Exception.Message -like '*checksum mismatch*' }
    if (-not $rejected -or (Test-Path -LiteralPath (Join-Path $prefix 'support'))) { throw 'An invalid archive was accepted or changed support files.' }
    & $supportInstaller -Archive $Archive -Prefix $prefix -CommandDirectory $commands
    $manifestPath = Join-Path $prefix 'credential-support.json'
    $manifest = Get-Content -LiteralPath $manifestPath -Raw | ConvertFrom-Json
    if (-not $manifest.signature_verified -or $manifest.config_changed -or $manifest.login_reset -or @($manifest.files).Count -ne 52) { throw 'Incomplete support manifest.' }
    foreach ($entry in $manifest.files) {
        if ((Get-FileHash -LiteralPath (Join-Path $manifest.directory $entry.name) -Algorithm SHA256).Hash -ne $entry.sha256) { throw 'Managed support file hash mismatch.' }
    }
    foreach ($entry in $manifest.locators) {
        $bytes = [IO.File]::ReadAllBytes($entry.path)
        if ($bytes -contains 13 -or [Text.Encoding]::UTF8.GetString($bytes) -notmatch 'git-next') { throw 'Support locator is not LF JSON.' }
    }
    $helper = Join-Path $manifest.directory 'git-credential-manager.exe'
    & $devtool gcm-acceptance (Join-Path $commands 'git-next.exe') --helper $helper --installed-support
    if ($LASTEXITCODE -ne 0) { throw 'Installed credential support failed native HTTPS acceptance.' }
    $before = [IO.File]::ReadAllBytes($manifestPath)
    $locatorBefore = @{}
    foreach ($entry in $manifest.locators) { $locatorBefore[$entry.path] = (Get-FileHash -LiteralPath $entry.path -Algorithm SHA256).Hash }
    $failurePath = $manifestPath + '.new'
    New-Item -ItemType Directory -Path $failurePath | Out-Null
    $rejected = $false
    try { & $supportInstaller -Archive $Archive -Prefix $prefix -CommandDirectory $commands }
    catch { $rejected = $true }
    if (-not $rejected -or [Convert]::ToBase64String([IO.File]::ReadAllBytes($manifestPath)) -ne [Convert]::ToBase64String($before)) { throw 'Failed upgrade changed the published manifest.' }
    foreach ($entry in $manifest.locators) {
        if ((Get-FileHash -LiteralPath $entry.path -Algorithm SHA256).Hash -ne $locatorBefore[$entry.path]) { throw 'Failed upgrade did not restore a locator.' }
    }
    if ((Get-FileHash -LiteralPath $helper -Algorithm SHA256).Hash -ne ($manifest.files | Where-Object name -eq 'git-credential-manager.exe').sha256) { throw 'Failed upgrade lost the previous helper.' }
    Remove-Item -LiteralPath $failurePath
    & $supportInstaller -Archive $Archive -Prefix $prefix -CommandDirectory $commands
    $preserved = Join-Path $manifest.directory 'NOTICE'
    [IO.File]::AppendAllText($preserved, "`nfixture local change`n")
    & $supportInstaller -Prefix $prefix -CommandDirectory $commands -Uninstall
    if (-not (Test-Path -LiteralPath $preserved) -or (Test-Path -LiteralPath $helper)) { throw 'Uninstall removed modified files or retained an unchanged helper.' }
    foreach ($entry in $manifest.locators) { if (Test-Path -LiteralPath $entry.path) { throw 'Uninstall left a matching locator.' } }
    if ((Get-FileHash -LiteralPath $originalGit -Algorithm SHA256).Hash -ne $originalHash) { throw 'Original Git changed.' }
    Write-Host 'PASS: signature/checksum, isolated install, LF locators, native HTTPS credentials, failed-upgrade rollback, upgrade and conservative uninstall.'
} finally {
    $env:HOME = $savedHome
    $env:USERPROFILE = $savedProfile
    $env:PATH = $savedPath
    $env:GIT_NEXT_FIXTURE_RUSTC = $savedCompiler
    if (Test-Path -LiteralPath $root) {
        $resolved = (Resolve-Path -LiteralPath $root).Path
        $temporary = [IO.Path]::GetFullPath([IO.Path]::GetTempPath())
        if (-not $resolved.StartsWith($temporary, [StringComparison]::OrdinalIgnoreCase) -or (Split-Path -Leaf $resolved) -notlike 'git-next-credential-install-*') { throw 'Unexpected fixture cleanup path.' }
        Remove-Item -LiteralPath $resolved -Recurse -Force
    }
}
