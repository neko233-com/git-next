# Copyright 2026 git-next contributors. MIT license.
param([Parameter(Mandatory)][string]$Package, [string]$UpgradePackage)
$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest
$workspace = Split-Path -Parent $PSScriptRoot
Import-Module (Join-Path $workspace 'packaging/windows/Update.Core.psm1') -Force
Add-Type -Path (Join-Path $PSScriptRoot 'windows_file_snapshot.cs')
$basePath = (Resolve-Path -LiteralPath $Package).Path
$receiptPath = [IO.Path]::ChangeExtension($basePath,'.receipt.json')
$base = Get-Content -LiteralPath $receiptPath -Raw | ConvertFrom-Json
$machine = $base.install_scope -ceq 'machine'
$pathScope = if ($machine) { 'Machine' } else { 'User' }
$dataFolder = if ($machine) { 'CommonApplicationData' } else { 'LocalApplicationData' }
$programsFolder = if ($machine) { 'CommonPrograms' } else { 'Programs' }
if ((Get-FileHash -LiteralPath $basePath -Algorithm SHA256).Hash.ToLowerInvariant() -cne $base.sha256) { throw 'Base MSI differs from its local packaging receipt.' }
Assert-GnMsi $basePath $base
$upgrade = $null
if ($UpgradePackage) {
    $upgradePath = (Resolve-Path -LiteralPath $UpgradePackage).Path
    $upgrade = Get-Content -LiteralPath ([IO.Path]::ChangeExtension($upgradePath,'.receipt.json')) -Raw | ConvertFrom-Json
    if ((Get-FileHash -LiteralPath $upgradePath -Algorithm SHA256).Hash.ToLowerInvariant() -cne $upgrade.sha256 -or $upgrade.upgrade_code -cne $base.upgrade_code -or -not (Test-GnNewer $upgrade.version $base.version)) { throw 'Upgrade fixture is not a pinned newer MSI from the same product family.' }
    Assert-GnMsi $upgradePath $upgrade
}
$installer = New-Object -ComObject WindowsInstaller.Installer
if ($installer.ProductState($base.product_code) -ne -1 -or ($upgrade -and $installer.ProductState($upgrade.product_code) -ne -1)) { throw 'Lifecycle fixture requires absent MSI product identities; preserve existing installations.' }
$sid = [Security.Principal.WindowsIdentity]::GetCurrent().User.Value
$taskName = 'git-next-update-' + $base.platform + '-' + $(if($machine){'machine'}else{$sid})
$service = New-Object -ComObject Schedule.Service
$service.Connect()
$tasks = $service.GetFolder('\')
$priorTask = $null
try { $priorTask = $tasks.GetTask($taskName) } catch { if (-not (Test-GnMissingTaskError $_)) { throw } }
if ($priorTask) { throw 'An updater task already exists; use an isolated Windows user for this lifecycle batch.' }
$stateDirectory = Join-Path ([Environment]::GetFolderPath($dataFolder)) ('git-next\updates\' + $base.platform)
if (Test-Path -LiteralPath $stateDirectory) { throw 'Existing updater state must be preserved; use an isolated Windows user for this lifecycle batch.' }
$configurationState = Join-Path ([Environment]::GetFolderPath($dataFolder)) ('git-next\config\runtime-defaults-' + $base.platform + '.json')
if (Test-Path -LiteralPath $configurationState) { throw 'Existing runtime configuration state must be preserved; use an isolated fixture state.' }
$startMenu = Join-Path ([Environment]::GetFolderPath($programsFolder)) ('git-next (' + $base.platform + ')')
if (Test-Path -LiteralPath $startMenu) { throw 'Existing Start menu integration must be preserved.' }
$registry = $(if($machine){'HKLM:'}else{'HKCU:'}) + '\Software\git-next\MSI\' + $base.platform
if (Test-Path -LiteralPath $registry) { throw 'Existing MSI registry state must be preserved.' }
$temporary = Join-Path ([IO.Path]::GetTempPath()) ('git-next-msi-test-' + [Guid]::NewGuid().ToString('N'))
[void][IO.Directory]::CreateDirectory($temporary)
$installRoot = Join-Path $temporary 'installation'
if ($machine) { $installRoot = Join-Path ([Environment]::GetFolderPath('ProgramFiles')) ('git-next-acceptance-' + [Guid]::NewGuid().ToString('N') + '\installation') }
$profileRoot = Join-Path $temporary 'profile'
[void][IO.Directory]::CreateDirectory($profileRoot)
$oldHome = $env:HOME
$oldProfile = $env:USERPROFILE
$oldSsh = $env:SSH_ROOT_PATH
$oldPath = [Environment]::GetEnvironmentVariable('Path',$pathScope)
$references = @((Get-Command git -CommandType Application).Source | Select-Object -Unique)
$referenceHashes = @{}
foreach ($reference in $references) { $referenceHashes[$reference] = (Get-FileHash -LiteralPath $reference -Algorithm SHA256).Hash }
$checks = [Collections.Generic.List[string]]::new()
$attempted = $false
$failureProduct = $null
$msiexec = Join-Path $env:SystemRoot 'System32/msiexec.exe'
function Check([string]$Name,[bool]$Condition) { if (-not $Condition) { throw ('MSI acceptance failed: ' + $Name) }; $checks.Add($Name) }
function Msi([string]$Arguments,[string]$LogName,[bool]$Success = $true,[string]$FailureMarker = '') {
    $log = Join-Path $temporary $LogName
    $command = $Arguments + ' /qn /norestart REBOOT=ReallySuppress MSIRESTARTMANAGERCONTROL=Disable /l*v "' + $log + '"'
    $process = Start-Process -FilePath $msiexec -ArgumentList $command -WindowStyle Hidden -Wait -PassThru
    if ($Success -and $process.ExitCode -ne 0) {
        $diagnostics = Join-Path $workspace ('.evidence/msi-failure-' + [Guid]::NewGuid().ToString('N'))
        [void][IO.Directory]::CreateDirectory($diagnostics)
        Copy-Item -LiteralPath $log -Destination (Join-Path $diagnostics $LogName)
        throw ('MSI operation failed with exit ' + $process.ExitCode + '; retained diagnostic log: ' + (Join-Path $diagnostics $LogName))
    }
    if (-not $Success -and $process.ExitCode -eq 0) { throw 'Injected failing MSI unexpectedly succeeded.' }
    if (-not $Success -and $FailureMarker -and ([IO.File]::ReadAllText($log)).IndexOf($FailureMarker,[StringComparison]::Ordinal) -lt 0) { throw ('The expected MSI failure point was not reached: ' + $FailureMarker + '; log: ' + $log) }
    return $process.ExitCode
}
function Snapshot {
    $manifest = Get-Content -LiteralPath (Join-Path $installRoot 'package.json') -Raw | ConvertFrom-Json
    $expected = [Collections.Generic.Dictionary[string,string]]::new([StringComparer]::Ordinal)
    foreach ($entry in $manifest.files.PSObject.Properties) { $expected.Add($entry.Name,$entry.Value) }
    $files = [GitNext.Acceptance.Files]::Snapshot($installRoot,$expected)
    return ($files | ConvertTo-Json -Compress)
}
function CheckTerminalShortcut {
    $path=Join-Path $startMenu 'Git Terminal.lnk'
    Check 'owned terminal shortcut exists' (Test-Path -LiteralPath $path)
    $shell=New-Object -ComObject WScript.Shell
    $shortcut=$shell.CreateShortcut($path)
    $manifest=Get-Content -LiteralPath (Join-Path $installRoot 'package.json') -Raw | ConvertFrom-Json
    $native= [bool]$manifest.files.PSObject.Properties['bin/git-terminal.exe']
    $expected=Join-Path $installRoot $(if($native){'bin\git-terminal.exe'}else{'runtime\git-bash.exe'})
    Check 'terminal shortcut points to the installed runtime' ($shortcut.TargetPath -ieq $expected)
    if($native){Check 'terminal shortcut preserves home launch arguments' ($shortcut.Arguments -ceq '--cd-to-home')}
    [void][Runtime.InteropServices.Marshal]::FinalReleaseComObject($shell)
}

try {
    $env:HOME=$profileRoot
    $env:USERPROFILE=$profileRoot
    $env:SSH_ROOT_PATH=(Join-Path $profileRoot '.ssh')
    $attempted = $true
    [void](Msi ('/i "' + $basePath + '" INSTALLFOLDER="' + $installRoot + '"') 'install.log')
    Check 'base product registered' ($installer.ProductState($base.product_code) -eq 5)
    Assert-GnInstalledFiles $installRoot (Get-Content -LiteralPath (Join-Path $installRoot 'package.json') -Raw | ConvertFrom-Json)
    $task = $tasks.GetTask($taskName)
    if ($machine) {
        Check 'owned protected SYSTEM updater task' ($task.Definition.Principal.RunLevel -eq 1 -and $task.Definition.Principal.LogonType -eq 5 -and $task.Definition.Principal.UserId -in @('SYSTEM','S-1-5-18') -and $task.Definition.RegistrationInfo.Description -ceq ('git-next automatic update: ' + $installRoot))
    } else {
        Check 'owned least-privilege interactive updater task' ($task.Definition.Principal.RunLevel -eq 0 -and $task.Definition.Principal.LogonType -eq 3 -and $task.Definition.RegistrationInfo.Description -ceq ('git-next automatic update: ' + $installRoot))
    }
    # No fixture task may perform a real network update during lifecycle checks.
    $task.Enabled=$false
    $before = Snapshot
    $native = Join-Path $installRoot 'bin/git-next.exe'
    $version = & $native version --json | ConvertFrom-Json
    Check 'actual installed product identity' ($LASTEXITCODE -eq 0 -and $version.product -ceq 'git-next' -and $version.version -ceq $base.version)
    Check 'preview git remains outside user PATH' (([Environment]::GetEnvironmentVariable('Path','User') -split ';') -notcontains (Join-Path $installRoot 'preview/bin'))
    [void](Msi ('/fomus ' + $base.product_code) 'repair.log')
    Check 'repair preserves actual payload bytes' ((Snapshot) -ceq $before)
    CheckTerminalShortcut
    $task = $tasks.GetTask($taskName)
    $task.Enabled=$false
    # IRegisteredTask caches Xml; reacquire after changing Enabled.
    $taskBefore = $tasks.GetTask($taskName).Xml
    if ($upgrade) {
        foreach ($relative in @('package.json','git-next-distribution.json','runtime/defaults/manifest.json')) {
            $metadata = Join-Path $installRoot $relative
            [IO.File]::SetLastWriteTimeUtc($metadata,([IO.File]::GetCreationTimeUtc($metadata)).AddMinutes(2))
        }
        Check 'rollback-style manifest timestamps preserve installed bytes' ((Snapshot) -ceq $before)
        # Metadata-only failure fixture exercises the real MSI rollback transaction.
        $failing = Join-Path $temporary 'failing-upgrade.msi'
        Copy-Item -LiteralPath $upgradePath -Destination $failing
        $database = $installer.OpenDatabase($failing,1)
        $failureType = 34 + 1024 + $(if($machine){2048}else{0})
        $failureCommand = '"[System64Folder]cmd.exe" /d /c "exit 42"'
        $view = $database.OpenView("INSERT INTO ``CustomAction`` (``Action``,``Type``,``Source``,``Target``) VALUES ('GitNextInjectedFailure',$failureType,'System64Folder','$failureCommand')")
        [void]$view.Execute(); [void]$view.Close(); [void][Runtime.InteropServices.Marshal]::FinalReleaseComObject($view)
        $lookup = $database.OpenView("SELECT ``Sequence`` FROM ``InstallExecuteSequence`` WHERE ``Action``='RegisterUpdater'")
        [void]$lookup.Execute(); $row = $lookup.Fetch(); $sequence = $row.IntegerData(1) + 1
        [void][Runtime.InteropServices.Marshal]::FinalReleaseComObject($row)
        [void]$lookup.Close(); [void][Runtime.InteropServices.Marshal]::FinalReleaseComObject($lookup)
        $view = $database.OpenView("INSERT INTO ``InstallExecuteSequence`` (``Action``,``Condition``,``Sequence``) VALUES ('GitNextInjectedFailure','1',$sequence)")
        [void]$view.Execute(); [void]$view.Close(); [void][Runtime.InteropServices.Marshal]::FinalReleaseComObject($view)
        [void]$database.Commit()
        [void][Runtime.InteropServices.Marshal]::FinalReleaseComObject($database)
        [GC]::Collect(); [GC]::WaitForPendingFinalizers()
        $failureProduct = $upgrade.product_code
        [void](Msi ('/i "' + $failing + '" INSTALLFOLDER="' + $installRoot + '"') 'rollback.log' $false 'GitNextInjectedFailure returned actual error code 42')
        $oldState = $installer.ProductState($base.product_code)
        $newState = $installer.ProductState($upgrade.product_code)
        $taskAfter = $tasks.GetTask($taskName).Xml
        if ($oldState -ne 5 -or $newState -ne -1 -or $taskAfter -cne $taskBefore) {
            $diagnostics = Join-Path $workspace ('.evidence/msi-rollback-' + [Guid]::NewGuid().ToString('N'))
            [void][IO.Directory]::CreateDirectory($diagnostics)
            Copy-Item -LiteralPath (Join-Path $temporary 'rollback.log') -Destination $diagnostics
            [IO.File]::WriteAllText((Join-Path $diagnostics 'task-before.xml'),$taskBefore)
            [IO.File]::WriteAllText((Join-Path $diagnostics 'task-after.xml'),$taskAfter)
            Write-Output ('Rollback states: old=' + $oldState + ', new=' + $newState + '; diagnostics: ' + $diagnostics)
        }
        Check 'failed upgrade restores old registration' ($oldState -eq 5 -and $newState -eq -1)
        Check 'failed upgrade restores actual old bytes' ((Snapshot) -ceq $before)
        CheckTerminalShortcut
        Check 'failed upgrade restores updater task' ($taskAfter -ceq $taskBefore)
        [void](Msi ('/i "' + $upgradePath + '" INSTALLFOLDER="' + $installRoot + '"') 'upgrade.log')
        $task = $tasks.GetTask($taskName); $task.Enabled=$false
        Check 'newer product replaces old registration' ($installer.ProductState($upgrade.product_code) -eq 5 -and $installer.ProductState($base.product_code) -eq -1)
        [void](Snapshot)
        Assert-GnInstalledFiles $installRoot (Get-Content -LiteralPath (Join-Path $installRoot 'package.json') -Raw | ConvertFrom-Json)
        $version = & $native version --json | ConvertFrom-Json
        Check 'upgraded native bytes identify requested version' ($LASTEXITCODE -eq 0 -and $version.version -ceq $upgrade.version)
        CheckTerminalShortcut
        [void](Msi ('/i "' + $basePath + '" INSTALLFOLDER="' + $installRoot + '"') 'downgrade.log' $false 'A newer git-next version is already installed.')
        Check 'downgrade leaves newer product registered' ($installer.ProductState($upgrade.product_code) -eq 5)
    }
    $active = if ($upgrade) { $upgrade.product_code } else { $base.product_code }
    [void](Msi ('/x ' + $active) 'uninstall.log')
    Check 'product registration removed' ($installer.ProductState($active) -eq -1)
    $foundTask = $false
    try { [void]$tasks.GetTask($taskName); $foundTask=$true } catch { if (-not (Test-GnMissingTaskError $_)) { throw } }
    Check 'owned task removed' (-not $foundTask)
    Check 'selected scope PATH restored exactly' ([Environment]::GetEnvironmentVariable('Path',$pathScope) -ceq $oldPath)
    foreach ($reference in $references) {
        Check ('original Git bytes preserved: ' + $reference) ((Get-FileHash -LiteralPath $reference -Algorithm SHA256).Hash -ceq $referenceHashes[$reference])
    }
    Check 'no SSH profile files created' (-not (Test-Path -LiteralPath $env:SSH_ROOT_PATH))
    [pscustomobject]@{status=$(if ($upgrade) {'pass'} else {'incomplete'});scope=('actual Windows '+$base.install_scope+' MSI install/repair/upgrade/injected-failure rollback/uninstall; original Git retained');base_sha256=$base.sha256;upgrade_sha256=$(if($upgrade){$upgrade.sha256}else{$null});checks=$checks;package_complete_git_parity=$(if($upgrade){$upgrade.complete_git_parity}else{$base.complete_git_parity})} | ConvertTo-Json -Depth 5
    if (-not $upgrade) { throw 'A separately built newer native package is required for complete upgrade/rollback acceptance.' }
} catch {
    $diagnostics = Join-Path $workspace ('.evidence/msi-lifecycle-' + [Guid]::NewGuid().ToString('N'))
    [void][IO.Directory]::CreateDirectory($diagnostics)
    Get-ChildItem -LiteralPath $temporary -File -Filter '*.log' | Copy-Item -Destination $diagnostics
    Write-Output ('Lifecycle diagnostic logs: ' + $diagnostics)
    Write-Output ($_ | Out-String)
    throw
} finally {
    $cleanupErrors = [Collections.Generic.List[string]]::new()
    if ($attempted) {
        foreach ($code in @($failureProduct,$(if($upgrade){$upgrade.product_code}),$base.product_code) | Where-Object { $_ } | Select-Object -Unique) {
            if ($installer.ProductState($code) -ge 0) {
                try { [void](Msi ('/x ' + $code) 'cleanup.log') } catch { $cleanupErrors.Add('Owned MSI product cleanup failed: ' + $code) }
            }
        }
    }
    $env:HOME=$oldHome; $env:USERPROFILE=$oldProfile; $env:SSH_ROOT_PATH=$oldSsh
    if ([Environment]::GetEnvironmentVariable('Path',$pathScope) -cne $oldPath) { $cleanupErrors.Add('Selected scope PATH differs after MSI cleanup; preserve logs and inspect.') }
    if (-not $cleanupErrors.Count) {
        if (Test-Path -LiteralPath $configurationState) {
            $configuration = Get-Content -LiteralPath $configurationState -Raw -Encoding UTF8 | ConvertFrom-Json
            if ($configuration.product -cne 'git-next' -or $configuration.platform -cne $base.platform -or $configuration.install_scope -cne $base.install_scope -or [IO.Path]::GetFullPath($configuration.install_root) -ine [IO.Path]::GetFullPath($installRoot)) { throw 'Runtime configuration state is not owned by this fixture.' }
            Remove-Item -LiteralPath $configurationState -Force
        }
        if (Test-Path -LiteralPath $stateDirectory) {
            $expectedRoot = Join-Path ([Environment]::GetFolderPath($dataFolder)) 'git-next\updates'
            [void](Assert-GnOwnedPath $expectedRoot (Join-Path $stateDirectory 'cleanup.marker'))
            $unexpected = @(Get-ChildItem -LiteralPath $stateDirectory -Recurse | Where-Object { $_.PSIsContainer -or $_.Name -notin @('task-transaction.json','state.json','updater.lock','owner.json','policy.json') })
            if ($unexpected.Count) { throw 'Unexpected updater-state files appeared; preserve them and inspect fixture logs.' }
            Remove-Item -LiteralPath $stateDirectory -Recurse -Force
        }
        if (Test-Path -LiteralPath $startMenu) { throw 'Owned Start menu integration remains after uninstall; preserve the fixture and investigate.' }
        $resolved = [IO.Path]::GetFullPath($temporary)
        $allowed = [IO.Path]::GetFullPath([IO.Path]::GetTempPath()).TrimEnd('\') + '\git-next-msi-test-'
        if (-not $resolved.StartsWith($allowed,[StringComparison]::OrdinalIgnoreCase)) { throw 'Unexpected MSI cleanup directory.' }
        Remove-Item -LiteralPath $resolved -Recurse -Force
        if ($machine) {
            $fixtureParent = [IO.Path]::GetDirectoryName($installRoot)
            $allowedParent = [IO.Path]::GetFullPath([Environment]::GetFolderPath('ProgramFiles')).TrimEnd('\') + '\git-next-acceptance-'
            if (-not $fixtureParent.StartsWith($allowedParent,[StringComparison]::OrdinalIgnoreCase)) { throw 'Unexpected machine fixture cleanup directory.' }
            if (Test-Path -LiteralPath $fixtureParent) { Remove-Item -LiteralPath $fixtureParent -Recurse -Force }
        }
    }
    [void][Runtime.InteropServices.Marshal]::FinalReleaseComObject($installer)
    if ($cleanupErrors.Count) { throw ($cleanupErrors -join '; ') }
}
