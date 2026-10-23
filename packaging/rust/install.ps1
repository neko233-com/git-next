# Copyright 2026 git-next contributors. MIT license.
# Installs the Rust git-next package under the user profile.
# It does not download Git for Windows and does not write into a registered official Git directory.
[CmdletBinding()]
param(
    [string]$Destination
)
Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

$packageRoot = Split-Path -Parent $PSCommandPath
$payload = Join-Path $packageRoot 'bin'
$git = Join-Path $payload 'git.exe'
if (-not [IO.File]::Exists($git)) { throw 'bin\git.exe is missing from this Rust package.' }

if (-not $Destination) { $Destination = Join-Path $env:LOCALAPPDATA 'Programs\git-next' }
$destination = [IO.Path]::GetFullPath($Destination)
$local = [IO.Path]::GetFullPath($env:LOCALAPPDATA)
if (-not $destination.StartsWith($local + '\', [StringComparison]::OrdinalIgnoreCase)) {
    throw 'Choose a directory inside the user local application folder.'
}

function Get-OfficialGitRoots {
    foreach ($hive in @([Microsoft.Win32.RegistryHive]::LocalMachine, [Microsoft.Win32.RegistryHive]::CurrentUser)) {
        foreach ($view in @([Microsoft.Win32.RegistryView]::Registry64, [Microsoft.Win32.RegistryView]::Registry32)) {
            $base = [Microsoft.Win32.RegistryKey]::OpenBaseKey($hive, $view)
            try {
                $key = $base.OpenSubKey('Software\Microsoft\Windows\CurrentVersion\Uninstall\Git_is1')
                if ($key) {
                    try {
                        $location = [string]$key.GetValue('InstallLocation')
                        if ($location) { [IO.Path]::GetFullPath($location).TrimEnd('\') }
                    } finally { $key.Dispose() }
                }
            } finally { $base.Dispose() }
        }
    }
}
foreach ($root in @(Get-OfficialGitRoots | Select-Object -Unique)) {
    if ($destination.TrimEnd('\') -eq $root) { throw 'Refusing to write into a registered official Git directory.' }
}

$bin = Join-Path $destination 'bin'
[void][IO.Directory]::CreateDirectory($bin)
Get-ChildItem -LiteralPath $payload -Force | ForEach-Object {
    Copy-Item -LiteralPath $_.FullName -Destination (Join-Path $bin $_.Name) -Recurse -Force
}
if (-not [IO.File]::Exists((Join-Path $bin 'git-next.exe'))) {
    Copy-Item -LiteralPath (Join-Path $bin 'git.exe') -Destination (Join-Path $bin 'git-next.exe')
}
foreach ($name in @('LICENSE', 'LICENSE.git-terminal', 'compatibility.json', 'package.json', 'README.md')) {
    $source = Join-Path $packageRoot $name
    if ([IO.File]::Exists($source)) { Copy-Item -LiteralPath $source -Destination (Join-Path $destination $name) -Force }
}

$userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
$entries = @()
if ($userPath) {
    $entries = @($userPath.Split(';') | Where-Object { $_ -and ($_.TrimEnd('\') -ine $bin.TrimEnd('\')) })
}
[Environment]::SetEnvironmentVariable('Path', ((@($bin) + $entries) -join ';'), 'User')

[pscustomobject]@{
    installed = $destination
    git = (Join-Path $bin 'git.exe')
    official_git = $false
    mingw = $false
    bash = $false
    perl = $false
    python = $false
} | ConvertTo-Json -Compress
