# Copyright 2026 git-next contributors. MIT license.
param(
    [ValidateSet('Check','Apply','RequestApply','Legacy','Scheduled','Enable','Disable','Status','Register','Unregister','RollbackRegistration','GuardRemoval','InitializeRuntimeConfiguration','RollbackRuntimeConfiguration','CommitRuntimeConfiguration')]
    [string]$Mode = 'Check',
    [AllowEmptyString()][ValidateSet('','Auto','Check','Apply','Enable','Disable')][string]$Request = 'Auto',
    [switch]$Yes,[switch]$Quiet,[switch]$Gui,[switch]$Json
)
Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'
# A custom action run by the installer has no console: its output is discarded, so a
# failure reaches the user as a bare `Error 1722` with no reason. Every unhandled error
# is recorded here, next to the other product state, before the failure is signalled.
trap {
    try {
        # A machine installation writes to the protected program data root; a user
        # installation has no access there and uses the local application data root.
        # Both are tried, so a failure is recorded whichever scope is being installed.
        $candidates = @(
            (Join-Path $env:ProgramData 'git-next\logs'),
            (Join-Path ([Environment]::GetFolderPath('LocalApplicationData')) 'git-next\logs')
        )
        $entry = '{0} mode={1} scope-hint={2} {3}{4}{5}' -f (Get-Date).ToString('o'), $Mode, $env:GITNEXT_LOG_SCOPE, $_.Exception.Message, [Environment]::NewLine, $_.ScriptStackTrace
        foreach ($directory in $candidates) {
            try {
                [void][IO.Directory]::CreateDirectory($directory)
                [IO.File]::AppendAllText((Join-Path $directory ('msi-action-' + $Mode + '.log')), $entry + [Environment]::NewLine)
                break
            } catch {}
        }
    } catch {}
    exit 1
}
Import-Module (Join-Path $PSScriptRoot 'Update.Core.psm1') -Force
$installRoot = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))
$packagePath = Assert-GnOwnedPath $installRoot (Join-Path $installRoot 'package.json')
$installed = Get-Content -LiteralPath $packagePath -Raw | ConvertFrom-Json
if ($installed.schema_version -ne 1 -or $installed.product -cne 'git-next' -or $installed.platform -notin @('windows-x64','windows-arm64') -or $installed.install_kind -cne 'msi') { throw 'This updater requires its owned MSI installation.' }
$context = Get-GnScopeContext $installRoot $installed
function Write-GnPublicCheck($Plan) {
    $value = [ordered]@{schema_version=1;status='checked';version=$Plan.Version;installed_version=$installed.version;install_scope=$context.scope;newer=[bool]$Plan.Newer;metadata_trust=$Plan.MetadataTrust}
    if ($Json) { $value | ConvertTo-Json }
    elseif ($Plan.Newer) { Write-Output ((Get-GnMessage 'git-next update available: {0}.') -f $Plan.Version) }
    else { Write-Output (Get-GnMessage 'git-next is current.') }
}
function Write-GnPublicState($State, [string]$Status = '') {
    if (-not $Status) { $Status = if ($State.result -eq 'never') { 'idle' } else { $State.result } }
    $value = [ordered]@{schema_version=1;status=$Status;version=$State.version;installed_version=$installed.version;install_scope=$context.scope;enabled=[bool]$State.enabled}
    if ($Json) { $value | ConvertTo-Json }
    else { Write-Output ((Get-GnMessage 'git-next update status: {0}.') -f $Status) }
}
if ($Mode -in @('InitializeRuntimeConfiguration','RollbackRuntimeConfiguration','CommitRuntimeConfiguration')) {
    if ($context.machine -and -not (Test-GnAdministrator)) { throw 'This machine update operation requires the protected worker or an administrator.' }
    $configurationTransaction = Join-Path $context.updates 'runtime-configuration-msi'
    switch ($Mode) {
        'InitializeRuntimeConfiguration' { Initialize-GnRuntimeConfiguration $installRoot $installed }
        'RollbackRuntimeConfiguration' { Restore-GnRuntimeConfigTransaction $installRoot $installed $configurationTransaction; Complete-GnRuntimeConfigTransaction $installRoot $installed $configurationTransaction }
        'CommitRuntimeConfiguration' { Complete-GnRuntimeConfigTransaction $installRoot $installed $configurationTransaction }
    }
    return
}
if (-not $Request) { $Request = 'Auto' }
if ($Request -ne 'Auto') {
    if ($Mode -ne 'Scheduled') { throw 'An update request is only accepted by the registered worker.' }
    $Mode = $Request
}
if ($Mode -eq 'GuardRemoval') {
    $takeover = Assert-GnOwnedPath $installRoot (Join-Path $installRoot 'migration/takeover.ps1')
    if ([IO.File]::Exists($takeover)) { & $takeover -Mode GuardRemoval }
    return
}
$stateRoot = $context.updates
[void](Assert-GnOwnedPath $context.root (Join-Path $stateRoot 'state.json'))
if ($Mode -eq 'Legacy') {
    $publicClient = New-GnHttpClient
    try { $legacyPlan = Get-GnOfficialPlan $publicClient $installed }
    finally { $publicClient.Dispose() }
    if (-not $legacyPlan.Newer) {
        if (-not $Quiet) { Write-Output (Get-GnMessage 'git-next is current.') }
        exit 0
    }
    if (-not $Yes) {
        $prompt = (Get-GnMessage 'Update git-next to {0}?') -f $legacyPlan.Version
        if ($Gui) {
            Add-Type -AssemblyName System.Windows.Forms
            $Yes = [Windows.Forms.MessageBox]::Show($prompt,'git-next',[Windows.Forms.MessageBoxButtons]::YesNo,[Windows.Forms.MessageBoxIcon]::Question) -eq [Windows.Forms.DialogResult]::Yes
        } else { $Yes = (Read-Host $prompt) -match '^(?i:y|yes|是|好)$' }
        if (-not $Yes) { exit 1 }
    }
    $Mode = 'RequestApply'
}
if ($Mode -eq 'RequestApply') {
    $service = New-Object -ComObject Schedule.Service; [void]$service.Connect()
    $task = $service.GetFolder('\').GetTask($context.task_name)
    if ($task.Definition.RegistrationInfo.Description -cne ('git-next automatic update: ' + [IO.Path]::GetFullPath($installRoot))) { throw 'An unrelated task owns the git-next updater name.' }
    [void]$task.Run('Apply')
    if ($null -ne (Get-Variable legacyPlan -ErrorAction SilentlyContinue)) {
        if (-not $Quiet) { Write-Output (Get-GnMessage 'git-next update was queued.') }
        exit 2
    }
    [pscustomobject]@{schema_version=1;status='queued';request='Apply';install_scope=$context.scope;task=$context.task_name} | ConvertTo-Json
    return
}
if ($context.machine -and -not (Test-GnAdministrator)) {
    if ($Mode -eq 'Check') {
        $publicClient = New-GnHttpClient
        try { Write-GnPublicCheck (Get-GnOfficialPlan $publicClient $installed) }
        finally { $publicClient.Dispose() }
        return
    }
    if ($Mode -eq 'Status') {
        $publicState = Assert-GnOwnedPath $stateRoot (Join-Path $stateRoot 'state.json')
        Assert-GnScopedFile $publicState
        if ([IO.File]::Exists($publicState)) { Write-GnPublicState (Get-Content -LiteralPath $publicState -Raw -Encoding UTF8 | ConvertFrom-Json) }
        else { Write-GnPublicState @{enabled=$true;result='never';version=$installed.version} }
        return
    }
    if ($Mode -eq 'Apply') {
        $service = New-Object -ComObject Schedule.Service; [void]$service.Connect()
        $task = $service.GetFolder('\').GetTask($context.task_name)
        if ($task.Definition.RegistrationInfo.Description -cne ('git-next automatic update: ' + [IO.Path]::GetFullPath($installRoot))) { throw 'An unrelated task owns the git-next updater name.' }
        [void]$task.Run($Mode)
        [pscustomobject]@{schema_version=1;status='queued';request=$Mode;install_scope='machine';task=$context.task_name} | ConvertTo-Json
        return
    }
    throw 'This machine update operation requires the protected worker or an administrator.'
}
if ($context.machine) { Initialize-GnMachineDirectory $stateRoot }
else { [void][IO.Directory]::CreateDirectory($stateRoot) }
$statePath = Assert-GnOwnedPath $stateRoot (Join-Path $stateRoot 'state.json')
if ($Mode -in @('Register','Unregister','RollbackRegistration')) {
    # Registering the update task is an optional integration, not part of the file
    # installation. Failing the whole install because a scheduled task could not be
    # created destroys five minutes of correctly copied files and reports a bare
    # installer error; the failure is recorded instead, and `git install verify`
    # reports the missing task with the command that repairs it.
    try {
        Set-GnUpdateTask $installRoot $stateRoot $installed.platform $Mode $context
    } catch {
        if ($Mode -eq 'Unregister') { return }
        $directory = if ($context.machine) { $context.root } else { $context.root }
        try {
            [void][IO.Directory]::CreateDirectory((Join-Path $directory 'logs'))
            $entry = '{0} mode={1} registration failed: {2}{3}{4}' -f (Get-Date).ToString('o'), $Mode, $_.Exception.Message, [Environment]::NewLine, $_.ScriptStackTrace
            [IO.File]::AppendAllText((Join-Path $directory 'logs\updater-registration.log'), $entry + [Environment]::NewLine)
        } catch {}
        Write-Output ('git-next: the update task could not be registered: ' + $_.Exception.Message)
    }
    return
}
$lockPath = Assert-GnOwnedPath $stateRoot (Join-Path $stateRoot 'updater.lock')
$lock = $null
$client = $null
$download = $null
try {
    try { $lock = Open-GnExclusiveLock $lockPath }
    catch [IO.IOException] { if ($Mode -eq 'Scheduled') { return }; throw 'Another git-next updater is running.' }
    $state = @{ schema_version=1; enabled=$true; last_attempt_utc=$null; last_success_utc=$null; result='never'; version=$installed.version }
    if (Test-Path -LiteralPath $statePath) {
        Assert-GnScopedFile $statePath
        $saved = Get-Content -LiteralPath $statePath -Raw | ConvertFrom-Json
        if ($saved.schema_version -ne 1) { throw 'Unsupported updater state version.' }
        foreach ($key in @('enabled','last_attempt_utc','last_success_utc','result','version')) { $state[$key] = $saved.$key }
    }
    if ($Mode -eq 'Status') { Write-GnPublicState $state; return }
    if ($Mode -in @('Enable','Disable')) { $state.enabled = $Mode -eq 'Enable'; Write-GnJson $statePath $state; Write-GnPublicState $state 'configured'; return }
    if ($Mode -eq 'Scheduled') {
        if (-not $state.enabled) { return }
        $retryHours = if ($state.result -in @('current','updated','available')) { 20 } else { 1 }
        if ($state.last_attempt_utc -and [DateTime]::Parse($state.last_attempt_utc).ToUniversalTime() -gt [DateTime]::UtcNow.AddHours(-$retryHours)) { return }
    }
    if ($Request -eq 'Apply') {
        # Let the requesting git process exit before the ordinary busy check.
        for ($attempt=0;$attempt -lt 20 -and (Test-GnBusy $installRoot);$attempt++) { Start-Sleep -Milliseconds 250 }
    }
    $state.last_attempt_utc = [DateTime]::UtcNow.ToString('o')
    $state.result = 'checking'
    Write-GnJson $statePath $state
    Assert-GnInstalledFiles $installRoot $installed
    $client = New-GnHttpClient
    $plan = Get-GnOfficialPlan $client $installed
    if ($Mode -eq 'Check') { Write-GnPublicCheck $plan; $state.result = if ($plan.Newer) { 'available' } else { 'current' }; Write-GnJson $statePath $state; return }
    if (-not $plan.Newer) { $state.result = 'current'; Write-GnJson $statePath $state; if ($Mode -ne 'Scheduled') { Write-GnPublicState $state }; return }
    if (Test-GnBusy $installRoot) { $state.result = 'deferred-busy'; Write-GnJson $statePath $state; if ($Json) { $state | ConvertTo-Json }; return }
    $download = Assert-GnOwnedPath $stateRoot (Join-Path $stateRoot ([Guid]::NewGuid().ToString('N') + '.msi'))
    $transport = Save-GnVerifiedPackage $client $plan $download
    Assert-GnMsi $download $plan.Entry
    if (Test-GnBusy $installRoot) { $state.result = 'deferred-busy'; Write-GnJson $statePath $state; if ($Json) { $state | ConvertTo-Json }; return }
    # The MSI upgrade transaction restores the old installation on failure.
    $log = Assert-GnOwnedPath $stateRoot (Join-Path $stateRoot 'msi-upgrade.log')
    $msiexec = Join-Path $env:SystemRoot 'System32\msiexec.exe'
    $arguments = '/i "' + $download + '" /qn /norestart REBOOT=ReallySuppress MSIRESTARTMANAGERCONTROL=Disable /l*v "' + $log + '"'
    $process = Start-Process -FilePath $msiexec -ArgumentList $arguments -WindowStyle Hidden -Wait -PassThru
    if ($process.ExitCode -ne 0) { throw ('MSI upgrade did not complete without a restart; exit ' + $process.ExitCode + '. Inspect the owned MSI log.') }
    $newPackage = Get-Content -LiteralPath $packagePath -Raw | ConvertFrom-Json
    if ($newPackage.version -cne $plan.Version -or $newPackage.product_code -cne $plan.Entry.product_code) { throw 'Installed package identity differs from the verified release.' }
    Assert-GnInstalledFiles $installRoot $newPackage
    $state.version = $plan.Version
    $state.last_success_utc = [DateTime]::UtcNow.ToString('o')
    $state.result = 'updated'
    Write-GnJson $statePath $state
    if ($Json) { $state | ConvertTo-Json } elseif ($Mode -ne 'Scheduled') { Write-Output ((Get-GnMessage 'Updated git-next to {0} through {1}.') -f $plan.Version,$transport.Host) }
} catch {
    if ($null -ne (Get-Variable state -ErrorAction SilentlyContinue)) {
        $state.result = 'failed'
        Write-GnJson $statePath $state
    }
    # Do not persist remote bodies, credential-bearing URLs or exception strings.
    if ($Mode -ne 'Scheduled') { throw (Get-GnMessage $_.Exception.Message) }
    exit 1
} finally {
    if ($download -and [IO.File]::Exists($download)) { [IO.File]::Delete($download) }
    if ($client) { $client.Dispose() }
    if ($lock) { $lock.Dispose() }
}
