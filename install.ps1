param(
    [string]$Commit,
    [string]$SourceBinary,
    [string]$Prefix = (Join-Path $env:LOCALAPPDATA 'Programs\git-next'),
    [switch]$ReplaceGit,
    [switch]$Uninstall
)
$ErrorActionPreference = 'Stop'
$userBin = Join-Path $HOME '.local\bin'
$command = Join-Path $userBin 'git-next.exe'
$manifest = Join-Path $Prefix 'installation.json'
if ($Uninstall) {
    if (-not (Test-Path -LiteralPath $manifest)) { throw 'No git-next installation manifest found.' }
    $saved = Get-Content -LiteralPath $manifest -Raw | ConvertFrom-Json
    if ((Test-Path -LiteralPath $command) -and (Get-FileHash -LiteralPath $command -Algorithm SHA256).Hash -eq $saved.sha256) {
        Remove-Item -LiteralPath $command
    }
    Write-Host 'git-next preview command removed. Repositories, Git configuration and the original Git are preserved.'
    return
}
if ($ReplaceGit) {
    throw 'System replacement is blocked: this experimental version has not passed full Git compatibility. Use the preview until push, checkout/pull, merge, rebase, LFS and ecosystem gates pass.'
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
            & cargo install --config 'net.git-fetch-with-cli=false' --git $source --rev $Commit --locked --bin git-next --root $Prefix --force
            if ($LASTEXITCODE -eq 0) { $success = $true; break }
        }
    } finally {
        foreach ($key in $savedGitEnv.Keys) { [Environment]::SetEnvironmentVariable($key, $savedGitEnv[$key], 'Process') }
    }
    if (-not $success) { throw 'All source transports failed. No existing Git was modified.' }
    $SourceBinary = Join-Path $Prefix 'bin\git-next.exe'
}
$SourceBinary = (Resolve-Path -LiteralPath $SourceBinary).Path
$compatibility = (& $SourceBinary compatibility --json) | ConvertFrom-Json
if ($LASTEXITCODE -ne 0 -or $compatibility.external_git_fallback -or $compatibility.external_ssh_fallback) { throw 'Unexpected client compatibility report.' }
New-Item -ItemType Directory -Force -Path $userBin | Out-Null
New-Item -ItemType Directory -Force -Path (Join-Path $Prefix 'preview\bin') | Out-Null
if (Test-Path -LiteralPath $command) {
    Copy-Item -LiteralPath $command -Destination (Join-Path $Prefix ('git-next.previous.' + [DateTime]::UtcNow.ToString('yyyyMMddHHmmssfff') + '.exe'))
}
$pending = $command + '.new'
Copy-Item -LiteralPath $SourceBinary -Destination $pending -Force
Move-Item -LiteralPath $pending -Destination $command -Force
Copy-Item -LiteralPath $SourceBinary -Destination (Join-Path $Prefix 'preview\bin\git.exe') -Force
$hash = (Get-FileHash -LiteralPath $command -Algorithm SHA256).Hash
@{schema_version=1; command=$command; preview_git=(Join-Path $Prefix 'preview\bin\git.exe'); sha256=$hash; source_commit=$Commit; system_git_replaced=$false} | ConvertTo-Json | Set-Content -LiteralPath $manifest -Encoding utf8
$currentPath = [Environment]::GetEnvironmentVariable('Path', 'User')
if (($currentPath -split ';') -notcontains $userBin) {
    $updatedPath = if ([string]::IsNullOrEmpty($currentPath)) { $userBin } else { $currentPath.TrimEnd(';') + ';' + $userBin }
    [Environment]::SetEnvironmentVariable('Path', $updatedPath, 'User')
}
Write-Host "Installed preview: $command"
Write-Host 'Open a new terminal to use git-next. The git-named preview stays outside PATH; system Git has not been replaced.'
