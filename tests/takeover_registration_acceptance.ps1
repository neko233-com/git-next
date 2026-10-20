# Copyright 2026 git-next contributors. MIT license.
$ErrorActionPreference = 'Stop'
Import-Module (Join-Path $PSScriptRoot '../packaging/windows/Update.Core.psm1') -Force -DisableNameChecking
$module = Import-Module (Join-Path $PSScriptRoot '../packaging/windows/Takeover.Core.psm1') -Force -DisableNameChecking -PassThru
$fixture = Join-Path ([IO.Path]::GetTempPath()) ('git-next-uninstall-' + [Guid]::NewGuid().ToString('N'))
[void][IO.Directory]::CreateDirectory($fixture)
try {
    foreach ($name in @('unins000.exe','unins001.exe')) {
        $path = Join-Path $fixture $name
        [IO.File]::WriteAllText($path,'fixture')
        $actual = & $module { param($root,$command) Get-GnRegisteredUninstaller $root $command } $fixture ('"'+$path+'"')
        if ($actual -cne $path) { throw 'Registered filename was not preserved.' }
    }
    foreach ($bad in @((Join-Path $fixture '../unins000.exe'),(Join-Path $fixture 'missing.exe'),((Join-Path $fixture 'unins001.exe')+' /silent'))) {
        $rejected = $false
        try { & $module { param($root,$command) Get-GnRegisteredUninstaller $root $command } $fixture $bad | Out-Null } catch { $rejected = $true }
        if (-not $rejected) { throw 'Unsafe uninstaller registration was accepted.' }
    }
    Write-Output '{"status":"pass","cases":5}'
} finally {
    $resolved = [IO.Path]::GetFullPath($fixture)
    if ([IO.Path]::GetDirectoryName($resolved).TrimEnd('\') -ine ([IO.Path]::GetTempPath()).TrimEnd('\') -or [IO.Path]::GetFileName($resolved) -notmatch '^git-next-uninstall-[a-f0-9]{32}$') { throw 'Unexpected fixture cleanup path.' }
    Remove-Item -LiteralPath $resolved -Recurse -Force
}
