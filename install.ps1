param(
    [string]$Commit,
    [string]$SourceBinary,
    [string]$SourceTerminal,
    [switch]$AddContextMenu,
    [string]$CommandDirectory = (Join-Path $HOME '.local\bin'),
    [switch]$NoShortcut,
    [switch]$NoPathUpdate,
    [string]$Prefix = (Join-Path $env:LOCALAPPDATA 'Programs\git-next'),
    [switch]$ReplaceGit,
    [switch]$Uninstall
)
$ErrorActionPreference = 'Stop'
$userBin = [IO.Path]::GetFullPath($CommandDirectory)
$command = Join-Path $userBin 'git-next.exe'
$terminal = Join-Path $userBin 'git-terminal.exe'
$shortcut = if ($NoShortcut) { $null } else { Join-Path ([Environment]::GetFolderPath('StartMenu')) 'Programs\Git Terminal.lnk' }
$manifest = Join-Path $Prefix 'installation.json'
if ($Uninstall) {
    if (-not (Test-Path -LiteralPath $manifest)) { throw 'No git-next installation manifest found.' }
    $saved = Get-Content -LiteralPath $manifest -Raw | ConvertFrom-Json
    if ($saved.command -ne $command) { throw 'Manifest command directory does not match; pass the original -CommandDirectory.' }
    if ((Test-Path -LiteralPath $command) -and (Get-FileHash -LiteralPath $command -Algorithm SHA256).Hash -eq $saved.sha256) {
        Remove-Item -LiteralPath $command
    }
    if ($saved.terminal_sha256 -and (Test-Path -LiteralPath $terminal) -and (Get-FileHash -LiteralPath $terminal -Algorithm SHA256).Hash -eq $saved.terminal_sha256) { Remove-Item -LiteralPath $terminal }
    if ($saved.shortcut -and (Test-Path -LiteralPath $saved.shortcut)) {
        $wsh = New-Object -ComObject WScript.Shell
        if ($wsh.CreateShortcut($saved.shortcut).TargetPath -eq $terminal) { Remove-Item -LiteralPath $saved.shortcut }
    }
    foreach ($location in @('HKCU:\Software\Classes\Directory\shell\GitNextTerminal','HKCU:\Software\Classes\Directory\Background\shell\GitNextTerminal')) {
        if (Test-Path -LiteralPath $location) {
            $configured = (Get-Item -LiteralPath ($location + '\command')).GetValue('')
            if ($configured.StartsWith('"' + $terminal + '"')) { Remove-Item -LiteralPath $location -Recurse }
        }
    }
    Write-Host 'git-next preview command removed. Repositories, Git configuration and the original Git are preserved.'
    return
}
if ($ReplaceGit) {
    throw 'System replacement is blocked: network push, LFS, complete login/credential helpers, merge/rebase and ecosystem compatibility have not passed. Run git-next doctor --json for the current blockers.'
}
New-Item -ItemType Directory -Force -Path $Prefix | Out-Null
if (-not $SourceBinary) {
    if (-not (Get-Command cargo -ErrorAction SilentlyContinue)) { throw 'Install the stable Rust toolchain and its native compiler first: https://rustup.rs/' }
    if (-not $Commit) {
        $Commit = (Invoke-RestMethod -Uri 'https://api.github.com/repos/neko233-com/git-next/commits/main' -Headers @{'User-Agent'='git-next-installer'}).sha
    }
    if ($Commit -notmatch '^[a-f0-9]{40}$') { throw 'A full official commit SHA is required.' }
    $env:CARGO_NET_RETRY = '1'
    $env:CARGO_HTTP_TIMEOUT = '30'
    $privateGitConfig = Join-Path $Prefix 'installer-empty-gitconfig'
    [IO.File]::WriteAllText($privateGitConfig, '')
    $savedGitEnv = @{}
    foreach ($key in @('GIT_CONFIG_GLOBAL','GIT_CONFIG_NOSYSTEM','GIT_CONFIG_COUNT','GIT_TERMINAL_PROMPT')) {
        $savedGitEnv[$key] = [Environment]::GetEnvironmentVariable($key, 'Process')
    }
    $env:GIT_CONFIG_GLOBAL = $privateGitConfig
    $env:GIT_CONFIG_NOSYSTEM = '1'
    $env:GIT_CONFIG_COUNT = '0'
    $env:GIT_TERMINAL_PROMPT = '0'
    $sources = @('https://github.com/neko233-com/git-next.git', 'https://ghfast.top/https://github.com/neko233-com/git-next.git', 'https://ghproxy.net/https://github.com/neko233-com/git-next.git')
    $success = $false
    try {
        foreach ($source in $sources) {
            & cargo install --config 'net.git-fetch-with-cli=false' --git $source --rev $Commit --locked --bin git-next --bin git-terminal --root $Prefix --force
            if ($LASTEXITCODE -eq 0) { $success = $true; break }
        }
    } finally {
        foreach ($key in $savedGitEnv.Keys) { [Environment]::SetEnvironmentVariable($key, $savedGitEnv[$key], 'Process') }
    }
    if (-not $success) { throw 'All source transports failed. No existing Git was modified.' }
    $SourceBinary = Join-Path $Prefix 'bin\git-next.exe'
}
$SourceBinary = (Resolve-Path -LiteralPath $SourceBinary).Path
if (-not $SourceTerminal) { $SourceTerminal = Join-Path (Split-Path -Parent $SourceBinary) 'git-terminal.exe' }
$SourceTerminal = (Resolve-Path -LiteralPath $SourceTerminal).Path
$identity = (& $SourceBinary version --json) | ConvertFrom-Json
if ($LASTEXITCODE -ne 0 -or $identity.product -ne 'git-next' -or $identity.external_git_fallback -ne $false -or $identity.external_ssh_fallback -ne $false) { throw 'Source binary is not a recognized self-contained git-next client.' }
& $SourceTerminal --version
if ($LASTEXITCODE -ne 0) { throw 'git-terminal validation failed.' }
$compatibility = (& $SourceBinary compatibility --json) | ConvertFrom-Json
if ($LASTEXITCODE -ne 0 -or $compatibility.product -ne 'git-next' -or $compatibility.external_git_fallback -ne $false -or $compatibility.external_ssh_fallback -ne $false) { throw 'Unexpected client compatibility report.' }
New-Item -ItemType Directory -Force -Path $userBin | Out-Null
New-Item -ItemType Directory -Force -Path (Join-Path $Prefix 'preview\bin') | Out-Null
if (Test-Path -LiteralPath $command) {
    Copy-Item -LiteralPath $command -Destination (Join-Path $Prefix ('git-next.previous.' + [DateTime]::UtcNow.ToString('yyyyMMddHHmmssfff') + '.exe'))
}
$pending = $command + '.new'
Copy-Item -LiteralPath $SourceBinary -Destination $pending -Force
Move-Item -LiteralPath $pending -Destination $command -Force
Copy-Item -LiteralPath $SourceBinary -Destination (Join-Path $Prefix 'preview\bin\git.exe') -Force
Copy-Item -LiteralPath $SourceTerminal -Destination ($terminal + '.new') -Force
Move-Item -LiteralPath ($terminal + '.new') -Destination $terminal -Force
if (-not $NoShortcut) {
    $wsh = New-Object -ComObject WScript.Shell
    $link = $wsh.CreateShortcut($shortcut)
    if (-not (Test-Path -LiteralPath $shortcut) -or $link.TargetPath -eq $terminal) {
        $link.TargetPath = $terminal
        $link.WorkingDirectory = $HOME
        $link.Description = 'Git Terminal with isolated git-next preview'
        $link.Save()
    }
}
if ($AddContextMenu) {
    foreach ($location in @('HKCU:\Software\Classes\Directory\shell\GitNextTerminal','HKCU:\Software\Classes\Directory\Background\shell\GitNextTerminal')) {
        New-Item -Path ($location + '\command') -Force | Out-Null
        Set-Item -LiteralPath $location -Value 'Open Git Terminal here'
        Set-Item -LiteralPath ($location + '\command') -Value ('"' + $terminal + '" --cwd "%V"')
    }
}
$hash = (Get-FileHash -LiteralPath $command -Algorithm SHA256).Hash
if ($hash -ne (Get-FileHash -LiteralPath $SourceBinary -Algorithm SHA256).Hash) { throw 'Installed command checksum mismatch.' }
@{schema_version=2; product='git-next'; version=$identity.version; command=$command; preview_git=(Join-Path $Prefix 'preview\bin\git.exe'); sha256=$hash; terminal=$terminal; terminal_sha256=(Get-FileHash -LiteralPath $terminal -Algorithm SHA256).Hash; shortcut=$(if ($NoShortcut) {$null} else {$shortcut}); source_commit=$Commit; system_git_replaced=$false; replacement_ready=$compatibility.complete_git_parity} | ConvertTo-Json | Set-Content -LiteralPath $manifest -Encoding utf8
$currentPath = [Environment]::GetEnvironmentVariable('Path', 'User')
if (-not $NoPathUpdate -and ($currentPath -split ';') -notcontains $userBin) {
    $updatedPath = if ([string]::IsNullOrEmpty($currentPath)) { $userBin } else { $currentPath.TrimEnd(';') + ';' + $userBin }
    [Environment]::SetEnvironmentVariable('Path', $updatedPath, 'User')
}
Write-Host "Installed preview: $command"
Write-Host 'Open a new terminal to use git-next. The git-named preview stays outside PATH; system Git has not been replaced.'
