param([string]$BinaryDirectory = (Join-Path $PSScriptRoot '..\target\release'))
$ErrorActionPreference = 'Stop'
$source = (Resolve-Path -LiteralPath $BinaryDirectory).Path
$installer = (Resolve-Path -LiteralPath (Join-Path $PSScriptRoot '..\install.ps1')).Path
$root = Join-Path ([IO.Path]::GetTempPath()) ('git-next-install-' + [Guid]::NewGuid().ToString('N'))
$prefix = Join-Path $root 'prefix'
$commands = Join-Path $root 'commands'
$originalGit = (Get-Command git -CommandType Application | Select-Object -First 1).Source
$originalHash = (Get-FileHash -LiteralPath $originalGit -Algorithm SHA256).Hash
$savedHome = $env:HOME
$savedProfile = $env:USERPROFILE
$pathBefore = [Environment]::GetEnvironmentVariable('Path', 'User')
try {
    New-Item -ItemType Directory -Path $root | Out-Null
    $env:HOME = Join-Path $root 'home'
    $env:USERPROFILE = $env:HOME
    New-Item -ItemType Directory -Path $env:HOME | Out-Null
    $rejected = $false
    try { & $installer -Prefix $prefix -CommandDirectory $commands -ReplaceGit -NoShortcut -NoPathUpdate }
    catch {
        $rejected = $_.Exception.Message -like '*replacement is blocked*'
        if (-not $rejected) { throw }
    }
    if (-not $rejected -or (Test-Path -LiteralPath $prefix) -or (Test-Path -LiteralPath $commands)) { throw 'Replacement gate made changes or did not reject.' }
    & $installer -Prefix $prefix -CommandDirectory $commands -NoShortcut -NoPathUpdate -SourceBinary (Join-Path $source 'git-next.exe') -SourceTerminal (Join-Path $source 'git-terminal.exe')
    $manifest = Get-Content -LiteralPath (Join-Path $prefix 'installation.json') -Raw | ConvertFrom-Json
    if ([IO.File]::ReadAllBytes((Join-Path $prefix 'installation.json')) -contains 13) { throw 'Installation metadata must use LF.' }
    if ($manifest.product -ne 'git-next' -or $manifest.system_git_replaced -ne $false -or $manifest.replacement_ready -ne $false) { throw 'Incorrect installation identity.' }
    $installed = Join-Path $commands 'git-next.exe'
    if ((Get-FileHash -LiteralPath $installed -Algorithm SHA256).Hash -ne $manifest.sha256) { throw 'Installed checksum mismatch.' }
    if (@($manifest.helpers).Count -ne 2) { throw 'Native LFS helper manifest is incomplete.' }
    foreach ($helper in $manifest.helpers) {
        if ((Get-FileHash -LiteralPath $helper.command -Algorithm SHA256).Hash -ne $helper.sha256) { throw 'Installed LFS helper checksum mismatch.' }
    }
    $lfsVersion = (& (Join-Path $commands 'git-next-lfs.exe') --version) -join "`n"
    if ($LASTEXITCODE -ne 0 -or $lfsVersion -notmatch 'git-next native') { throw 'Installed LFS helper identity is wrong.' }
    $identity = (& (Join-Path $prefix 'preview\bin\git.exe') --version --json) | ConvertFrom-Json
    if ($LASTEXITCODE -ne 0 -or $identity.product -ne 'git-next') { throw 'git-named preview did not identify git-next.' }
    & (Join-Path $commands 'git-terminal.exe') --print-setup | Out-Null
    if ($LASTEXITCODE -ne 0) { throw 'Installed terminal failed.' }
    & $installer -Prefix $prefix -CommandDirectory $commands -NoShortcut -NoPathUpdate -Uninstall
    if ((Test-Path -LiteralPath $installed) -or (Test-Path -LiteralPath (Join-Path $commands 'git-terminal.exe'))) { throw 'Owned commands were not removed.' }
    foreach ($helper in $manifest.helpers) { if (Test-Path -LiteralPath $helper.command) { throw 'Owned helper was not removed.' } }
    & $installer -Prefix $prefix -CommandDirectory $commands -NoShortcut -NoPathUpdate -SourceBinary (Join-Path $source 'git-next.exe') -SourceTerminal (Join-Path $source 'git-terminal.exe')
    $modified = [IO.File]::Open($installed, [IO.FileMode]::Append)
    $modified.WriteByte(0)
    $modified.Dispose()
    $changedHelper = Join-Path $commands 'git-next-lfs.exe'
    $modifiedHelper = [IO.File]::Open($changedHelper, [IO.FileMode]::Append)
    $modifiedHelper.WriteByte(0)
    $modifiedHelper.Dispose()
    & $installer -Prefix $prefix -CommandDirectory $commands -NoShortcut -NoPathUpdate -Uninstall
    if (-not (Test-Path -LiteralPath $installed)) { throw 'A modified command was deleted.' }
    if (-not (Test-Path -LiteralPath $changedHelper)) { throw 'A modified helper was deleted.' }
    if ((Get-FileHash -LiteralPath $originalGit -Algorithm SHA256).Hash -ne $originalHash) { throw 'Original Git was changed.' }
    if ([Environment]::GetEnvironmentVariable('Path', 'User') -ne $pathBefore) { throw 'Isolated acceptance changed user PATH.' }
    @{status='pass'; preview_install=$true; identity=$true; replacement_refused_without_writes=$true; checksum_uninstall=$true; changed_command_preserved=$true; original_git_unchanged=$true; user_path_unchanged=$true} | ConvertTo-Json
} finally {
    $env:HOME = $savedHome
    $env:USERPROFILE = $savedProfile
    $absolute = [IO.Path]::GetFullPath($root)
    if ($absolute.StartsWith([IO.Path]::GetFullPath([IO.Path]::GetTempPath()), [StringComparison]::OrdinalIgnoreCase) -and (Split-Path -Leaf $absolute).StartsWith('git-next-install-')) {
        Remove-Item -LiteralPath $absolute -Recurse -Force -ErrorAction SilentlyContinue
    }
}
