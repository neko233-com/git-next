# Copyright 2026 git-next contributors. MIT license.
param(
    [ValidateSet('Plan','Apply','Rollback','GuardRemoval')][string]$Mode = 'Plan',
    [string]$OriginalGitRoot,
    [string]$Transaction,
    [string]$TlsArchive,
    [string]$StateRoot,
    [string]$OriginalSnapshot,
    [string]$OriginalSnapshotSha256
)
Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'
$installRoot = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))
Import-Module (Join-Path $installRoot 'updater/Update.Core.psm1') -Force -DisableNameChecking
Import-Module (Join-Path $PSScriptRoot 'Takeover.Core.psm1') -Force -DisableNameChecking
$updateLock = $null
try {
if ($Mode -eq 'GuardRemoval') { Assert-GnTakeoverRemoval $installRoot; return }
if ($Mode -in @('Apply','Rollback')) {
    if ($Mode -eq 'Rollback') {
        if (-not $Transaction) { throw 'Pass the exact protected migration transaction directory.' }
        $context = Get-GnTakeoverRecoveryContext $Transaction
        $installRoot = $context.plan.install_root
    } else {
        $package = Get-Content -LiteralPath (Assert-GnOwnedPath $installRoot (Join-Path $installRoot 'package.json')) -Raw -Encoding UTF8 | ConvertFrom-Json
        if ($package.schema_version -ne 1 -or $package.product -cne 'git-next' -or $package.platform -notin @('windows-x64','windows-arm64')) { throw 'The complete native MSI installation is required.' }
        if (-not $package.complete_git_parity -or -not $package.release_eligible) { throw 'Takeover requires the accepted complete release; the candidate remains separate.' }
        $context = Get-GnScopeContext $installRoot $package
    }
    if ($context.machine -and -not (Test-GnAdministrator)) { throw 'Replacing the selected machine installation requires one elevated migration run.' }
    $localState = $context.updates
    $lockPath = Assert-GnOwnedPath $localState (Join-Path $localState 'updater.lock')
    if ($context.machine) { Initialize-GnMachineDirectory $localState }
    else { [void][IO.Directory]::CreateDirectory($localState) }
    try { $updateLock = Open-GnExclusiveLock $lockPath }
    catch [IO.IOException] { throw 'Another installation or update transaction is running.' }
}
if ($Mode -eq 'Rollback') {
    if (-not $Transaction) { throw 'Pass the exact protected migration transaction directory.' }
    $runner = [IO.Path]::GetFullPath((Join-Path $Transaction 'recovery-tools/migration/takeover.ps1'))
    if ($context.plan.PSObject.Properties['same_directory'] -and $context.plan.same_directory -and $context.plan.status -ne 'rolled-back' -and $PSCommandPath -ine $runner) {
        $prepared = Restore-GnTakeover $Transaction -PrepareOnly
        if ($prepared -ine $runner) { throw 'The protected rollback runner does not match this transaction.' }
        if ($updateLock) { $updateLock.Dispose(); $updateLock = $null }
        $hostDirectory = if (-not [Environment]::Is64BitProcess -and [Environment]::Is64BitOperatingSystem) { 'Sysnative' } else { 'System32' }
        $nativeHost = Join-Path $env:SystemRoot ($hostDirectory+'/WindowsPowerShell/v1.0/powershell.exe')
        & $nativeHost -NoLogo -NoProfile -NonInteractive -ExecutionPolicy Bypass -WindowStyle Hidden -File $runner -Mode Rollback -Transaction $Transaction
        if ($LASTEXITCODE -ne 0) { throw 'The protected rollback did not complete; its transaction and original backup are retained.' }
        return
    }
    Restore-GnTakeover $Transaction
    [pscustomobject]@{schema_version=1;product='git-next';status='rolled-back';repositories_modified=$false;credentials_exported=$false} | ConvertTo-Json
    return
}
if (-not $OriginalGitRoot) { throw 'Pass the exact original Git installation directory.' }
$plan = Get-GnTakeoverPlan $installRoot $OriginalGitRoot $OriginalSnapshot $OriginalSnapshotSha256
if ($Mode -eq 'Plan') {
    [pscustomobject]@{schema_version=1;product='git-next';status='planned';version=$plan.version;platform=$plan.platform;install_scope=$plan.install_scope;original_scope=$(if($plan.hive -eq 'LocalMachine'){'machine'}else{'user'});complete_git_parity=$plan.complete_git_parity;release_eligible=$plan.release_eligible;bridge_count=@($plan.bridges).Count;retained_data_files=@($plan.retained).Count;unmapped_executable_count=@($plan.unmapped).Count;changes_applied=$false;repositories_modified=$false;credential_vault_exported=$false} | ConvertTo-Json
    return
}
if (-not $StateRoot) { $StateRoot = $context.migrations }
$result = Start-GnTakeover $plan $StateRoot $TlsArchive
[pscustomobject]@{schema_version=1;product='git-next';status='active';transaction=$result;repositories_modified=$false;credentials_exported=$false;runtime_accepted=$false} | ConvertTo-Json
} catch {
    $message = $_.Exception.Message
    try { $message = Get-GnMessage $message } catch { }
    throw $message
}
finally { if ($updateLock) { $updateLock.Dispose() } }
