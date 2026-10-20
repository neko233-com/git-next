# Copyright 2026 git-next contributors. MIT license.
param([Parameter(Mandatory)][string]$Package,[Parameter(Mandatory)][string]$UpgradePackage,[Parameter(Mandatory)][string]$Output)
$ErrorActionPreference='Stop'
$script=(Resolve-Path -LiteralPath (Join-Path $PSScriptRoot 'msi_acceptance.ps1')).Path
$base=(Resolve-Path -LiteralPath $Package).Path
$upgrade=(Resolve-Path -LiteralPath $UpgradePackage).Path
$result=[IO.Path]::GetFullPath($Output)
if (Test-Path -LiteralPath $result) { throw 'Use a new machine fixture result.' }
$code='try { & '+"'"+$script.Replace("'","''")+"' -Package '"+$base.Replace("'","''")+"' -UpgradePackage '"+$upgrade.Replace("'","''")+"' *> '"+$result.Replace("'","''")+"'; exit 0 } catch { "+'$_ | Out-String | Add-Content -LiteralPath '+"'"+$result.Replace("'","''")+"'; exit 1 }"
$encoded=[Convert]::ToBase64String([Text.Encoding]::Unicode.GetBytes($code))
$process=Start-Process -FilePath (Join-Path $env:SystemRoot 'System32/WindowsPowerShell/v1.0/powershell.exe') -Verb RunAs -WindowStyle Hidden -ArgumentList @('-NoProfile','-ExecutionPolicy','Bypass','-EncodedCommand',$encoded) -Wait -PassThru
if (Test-Path -LiteralPath $result) { Get-Content -LiteralPath $result }
if ($process.ExitCode -ne 0) { throw 'Elevated machine MSI fixture failed; inspect its result log.' }
