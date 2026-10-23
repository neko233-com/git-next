# Copyright 2026 git-next contributors. MIT license.
Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

function Assert-GnNoRepository([IO.DirectoryInfo]$Directory) {
    $head = Join-Path $Directory.FullName 'HEAD'
    if ([IO.File]::Exists($head) -and ((Get-Item -LiteralPath $head -Force).Attributes -band [IO.FileAttributes]::ReparsePoint)) { throw 'Migration refuses reparse points in the selected installation.' }
    $isHead = [IO.File]::Exists($head) -and ([IO.FileInfo]::new($head)).Length -le 4096
    if ($isHead) { $isHead = [IO.File]::ReadAllText($head).Trim() -match '^(ref: refs/[^\r\n]+|[a-fA-F0-9]{40}|[a-fA-F0-9]{64})$' }
    $metadata = $Directory.Name -ieq '.git' -and $isHead
    $bare = $isHead -and [IO.Directory]::Exists((Join-Path $Directory.FullName 'objects')) -and [IO.Directory]::Exists((Join-Path $Directory.FullName 'refs'))
    if ($metadata -or $bare) { throw 'The selected installation contains Git repository metadata; migration preserves it without installing or removing files.' }
}

function Get-GnTree([string]$Root) {
    $Root = [IO.Path]::GetFullPath($Root).TrimEnd('\','/')
    [void](Assert-GnOwnedPath $Root (Join-Path $Root '.ownership-check'))
    $pending = [Collections.Generic.Stack[IO.DirectoryInfo]]::new()
    $pending.Push([IO.DirectoryInfo]::new($Root))
    $result = [Collections.Generic.List[object]]::new()
    [long]$bytes = 0
    while ($pending.Count) {
        $directory = $pending.Pop()
        Assert-GnNoRepository $directory
        foreach ($item in $directory.GetFileSystemInfos()) {
            if ($item.Attributes -band [IO.FileAttributes]::ReparsePoint) { throw 'Migration refuses reparse points in the selected installation.' }
            if ($item.Attributes -band [IO.FileAttributes]::Directory) { $pending.Push($item); continue }
            if ($item.Name -ieq '.git' -and $item.Length -le 4096 -and [IO.File]::ReadAllText($item.FullName) -match '^gitdir: [^\r\n]+\r?\n?$') { throw 'The selected installation contains a Git worktree pointer; migration preserves it without following it.' }
            $relative = $item.FullName.Substring($Root.Length + 1).Replace('\','/')
            [void](Assert-GnOwnedPath $Root $item.FullName)
            $bytes += $item.Length
            if ($result.Count -ge 100000 -or $bytes -gt 4GB -or $item.Length -gt 1GB) { throw 'The selected installation exceeds the migration capacity limit.' }
            $stream = [IO.File]::OpenRead($item.FullName)
            try { $pe = $stream.ReadByte() -eq 77 -and $stream.ReadByte() -eq 90 }
            finally { $stream.Dispose() }
            $subsystem = 0
            if ($relative.EndsWith('.exe',[StringComparison]::OrdinalIgnoreCase)) {
                if (-not $pe) { throw 'An installation executable is not a valid PE image.' }
                $stream = [IO.File]::OpenRead($item.FullName)
                $reader = [IO.BinaryReader]::new($stream)
                try {
                    $stream.Position = 0x3c
                    $offset = $reader.ReadUInt32()
                    if ($offset -gt $stream.Length - 94) { throw 'Invalid executable header.' }
                    $stream.Position = $offset
                    if ($reader.ReadUInt32() -ne 0x4550) { throw 'Invalid executable header.' }
                    $stream.Position = $offset + 24 + 68
                    $subsystem = $reader.ReadUInt16()
                } finally { $reader.Dispose(); $stream.Dispose() }
            }
            $access = (Get-Acl -LiteralPath $item.FullName).GetSecurityDescriptorSddlForm([Security.AccessControl.AccessControlSections]::Access)
            $result.Add([pscustomobject]@{relative=$relative;sha256=(Get-FileHash -LiteralPath $item.FullName -Algorithm SHA256).Hash.ToLowerInvariant();bytes=$item.Length;pe=$pe;subsystem=$subsystem;attributes=[int]$item.Attributes;access=$access})
        }
    }
    return $result.ToArray()
}

function Get-GnConfigDirectory {
    if ($env:GIT_NEXT_CONFIG_HOME) { $path = $env:GIT_NEXT_CONFIG_HOME }
    elseif ($env:XDG_CONFIG_HOME) { $path = Join-Path $env:XDG_CONFIG_HOME 'git-next' }
    else {
        $profilePath = if ($env:HOME) { $env:HOME } else { $env:USERPROFILE }
        if (-not $profilePath) { throw 'The user configuration directory is unavailable.' }
        $path = Join-Path $profilePath '.config/git-next'
    }
    if (-not [IO.Path]::IsPathRooted($path)) { throw 'The user configuration directory must be absolute.' }
    return [IO.Path]::GetFullPath($path).TrimEnd('\','/')
}

function New-GnPrivateDirectory([string]$Path,[switch]$Machine) {
    if ([IO.Directory]::Exists($Path) -or [IO.File]::Exists($Path)) { throw 'A private migration directory must be newly created.' }
    if ($Machine) { Initialize-GnMachineDirectory $Path -Private; return }
    [void][IO.Directory]::CreateDirectory($Path)
    $security = [Security.AccessControl.DirectorySecurity]::new()
    $security.SetAccessRuleProtection($true,$false)
    $sid = [Security.Principal.WindowsIdentity]::GetCurrent().User
    $security.SetOwner($sid)
    foreach ($identity in @($sid,[Security.Principal.SecurityIdentifier]::new('S-1-5-18'),[Security.Principal.SecurityIdentifier]::new('S-1-5-32-544'))) {
        $rule = [Security.AccessControl.FileSystemAccessRule]::new($identity,'FullControl','ContainerInherit,ObjectInherit','None','Allow')
        $security.AddAccessRule($rule)
    }
    Set-Acl -LiteralPath $Path -AclObject $security
}

function Get-GnRegistrySnapshot([string]$Hive,[string]$Path) {
    $base = [Microsoft.Win32.RegistryKey]::OpenBaseKey([Microsoft.Win32.RegistryHive]::$Hive,[Microsoft.Win32.RegistryView]::Registry64)
    $records = [Collections.Generic.List[object]]::new()
    try {
        $key = $base.OpenSubKey($Path)
        if (-not $key) { return [pscustomobject]@{hive=$Hive;path=$Path;exists=$false;keys=@()} }
        $key.Dispose()
        $pending = [Collections.Generic.Stack[string]]::new(); $pending.Push('')
        while ($pending.Count) {
            $relative = $pending.Pop()
            $full = $Path + $(if ($relative) {'\'+$relative} else {''})
            $key = $base.OpenSubKey($full)
            try {
                $values = @()
                foreach ($name in $key.GetValueNames()) {
                    $kind = $key.GetValueKind($name).ToString()
                    $value = $key.GetValue($name,$null,[Microsoft.Win32.RegistryValueOptions]::DoNotExpandEnvironmentNames)
                    if ($kind -eq 'Binary' -or $kind -eq 'None') { $value = [Convert]::ToBase64String($value) }
                    elseif ($kind -eq 'QWord') { $value = $value.ToString([Globalization.CultureInfo]::InvariantCulture) }
                    $values += [pscustomobject]@{name=$name;kind=$kind;value=$value}
                }
                $records.Add([pscustomobject]@{relative=$relative;values=$values})
                if ($records.Count -gt 1024) { throw 'Unexpected installation registry size.' }
                foreach ($child in $key.GetSubKeyNames()) { $pending.Push($(if ($relative) {$relative+'\'+$child} else {$child})) }
            } finally { $key.Dispose() }
        }
        return [pscustomobject]@{hive=$Hive;path=$Path;exists=$true;keys=$records.ToArray()}
    } finally { $base.Dispose() }
}

function Restore-GnRegistrySnapshot($Snapshot) {
    $base = [Microsoft.Win32.RegistryKey]::OpenBaseKey([Microsoft.Win32.RegistryHive]::$($Snapshot.hive),[Microsoft.Win32.RegistryView]::Registry64)
    try {
        # Selected keys belong to this transaction; unrelated registry trees are untouched.
        $base.DeleteSubKeyTree($Snapshot.path,$false)
        if (-not $Snapshot.exists) { return }
        foreach ($record in $Snapshot.keys) {
            $key = $base.CreateSubKey($Snapshot.path + $(if ($record.relative) {'\'+$record.relative} else {''}))
            try {
                foreach ($entry in $record.values) {
                    $kind = [Microsoft.Win32.RegistryValueKind]::$($entry.kind)
                    $value = $entry.value
                    if ($entry.kind -in @('Binary','None')) { $value = [Convert]::FromBase64String($value) }
                    elseif ($entry.kind -eq 'QWord') { $value = [long]::Parse($value,[Globalization.CultureInfo]::InvariantCulture) }
                    elseif ($entry.kind -eq 'DWord') { $value = [int]$value }
                    elseif ($entry.kind -eq 'MultiString') { $value = [string[]]$value }
                    $key.SetValue($entry.name,$value,$kind)
                }
            } finally { $key.Dispose() }
        }
    } finally { $base.Dispose() }
}

function Get-GnOldUpdateTasks([string]$OriginalRoot) {
    $service = New-Object -ComObject Schedule.Service; [void]$service.Connect()
    $folder = $service.GetFolder('\')
    $sid = [Security.Principal.WindowsIdentity]::GetCurrent().User.Value
    $result = @()
    foreach ($task in $folder.GetTasks(1)) {
        foreach ($action in $task.Definition.Actions) {
            if ($action.Type -ne 0) { continue }
            $text = $action.Path + ' ' + $action.Arguments
            if ($text.IndexOf($OriginalRoot,[StringComparison]::OrdinalIgnoreCase) -lt 0 -or $text -notmatch 'git-update-git-for-windows|git-for-windows-updater') { continue }
            if ($task.Definition.Principal.LogonType -ne 3 -or $task.Definition.Principal.UserId -notin @($sid,[Security.Principal.WindowsIdentity]::GetCurrent().Name)) { throw 'The legacy updater task requires a different account; migration was not started.' }
            $result += [pscustomobject]@{name=$task.Name;xml=$task.Xml;sid=$sid}
            break
        }
    }
    return $result
}

function Set-GnOldUpdateTasks($Tasks,[bool]$Restore) {
    if (-not @($Tasks).Count) { return }
    $service = New-Object -ComObject Schedule.Service; [void]$service.Connect(); $folder = $service.GetFolder('\')
    Set-GnOldUpdateTasksAtFolder $folder $Tasks $Restore
}

function Set-GnOldUpdateTasksAtFolder($Folder,$Tasks,[bool]$Restore) {
    foreach ($record in $Tasks) {
        $current = $null
        try { $current = $Folder.GetTask($record.name) } catch { if (-not (Test-GnMissingTaskError $_)) { throw } }
        if ($current) {
            $identities = @($record.sid)
            if ($record.sid -ceq [Security.Principal.WindowsIdentity]::GetCurrent().User.Value) { $identities += [Security.Principal.WindowsIdentity]::GetCurrent().Name }
            if ($current.Xml -cne $record.xml -or $current.Definition.Principal.LogonType -ne 3 -or $current.Definition.Principal.UserId -cnotin $identities) { throw 'A legacy updater task changed outside this takeover; its current task was preserved.' }
        }
        if ($Restore) {
            # TASK_CREATE rejects a task that appeared after the lookup; it never replaces it.
            if (-not $current) { [void]$Folder.RegisterTask($record.name,$record.xml,2,$record.sid,$null,3,$null) }
        } elseif ($current) { [void]$Folder.DeleteTask($record.name,0) }
    }
}

function Get-GnRelatedIntegration([string]$OriginalRoot) {
    $files = [Collections.Generic.List[IO.FileInfo]]::new()
    $shell = New-Object -ComObject WScript.Shell
    foreach ($folder in @([Environment]::GetFolderPath('Programs'),[Environment]::GetFolderPath('CommonPrograms'))) {
        $gitFolder = Join-Path $folder 'Git'
        if ([IO.Directory]::Exists($gitFolder)) { foreach ($file in @(Get-ChildItem -LiteralPath $gitFolder -Filter '*.lnk' -Recurse -File)) { $files.Add($file) } }
    }
    foreach ($folder in @([Environment]::GetFolderPath('DesktopDirectory'),[Environment]::GetFolderPath('CommonDesktopDirectory'))) {
        if ([IO.Directory]::Exists($folder)) { foreach ($file in @(Get-ChildItem -LiteralPath $folder -Filter '*.lnk' -File)) { $files.Add($file) } }
    }
    foreach ($folder in @([Environment]::GetFolderPath('LocalApplicationData'),[Environment]::GetFolderPath('CommonApplicationData'))) {
        $fragment = Join-Path $folder 'Microsoft/Windows Terminal/Fragments/Git'
        if ([IO.Directory]::Exists($fragment)) { foreach ($file in @(Get-ChildItem -LiteralPath $fragment -Filter '*.json' -File)) { $files.Add($file) } }
    }
    $result = @(); $seen = [Collections.Generic.HashSet[string]]::new([StringComparer]::OrdinalIgnoreCase)
    foreach ($file in $files) {
        if (-not $seen.Add($file.FullName)) { continue }
        if ($file.Length -gt 2MB -or $file.Attributes -band [IO.FileAttributes]::ReparsePoint) { continue }
        $retiredGui = $false
        $retiredStartMenu = $false
        if ($file.Extension -ieq '.lnk') {
            $target = [Environment]::ExpandEnvironmentVariables($shell.CreateShortcut($file.FullName).TargetPath)
            if (-not $target.StartsWith($OriginalRoot+'\',[StringComparison]::OrdinalIgnoreCase)) { continue }
            $retiredGui = [IO.Path]::GetFileName($target) -in @('git-gui.exe','gitk.exe')
            foreach ($menu in @([Environment]::GetFolderPath('Programs'),[Environment]::GetFolderPath('CommonPrograms'))) {
                if ($file.FullName.StartsWith((Join-Path $menu 'Git')+'\',[StringComparison]::OrdinalIgnoreCase)) { $retiredStartMenu = $true }
            }
        } else {
            $text = [IO.File]::ReadAllText($file.FullName).Replace('\\','\').Replace('/','\')
            if ($text.IndexOf($OriginalRoot,[StringComparison]::OrdinalIgnoreCase) -lt 0) { continue }
        }
        $relative = ([BitConverter]::ToString([Security.Cryptography.SHA256]::Create().ComputeHash([Text.Encoding]::UTF8.GetBytes($file.FullName)))).Replace('-','').ToLowerInvariant()
        $access = (Get-Acl -LiteralPath $file.FullName).GetSecurityDescriptorSddlForm([Security.AccessControl.AccessControlSections]::Access)
        $result += [pscustomobject]@{source=$file.FullName;relative=$relative;sha256=(Get-FileHash -LiteralPath $file.FullName -Algorithm SHA256).Hash.ToLowerInvariant();attributes=[int]$file.Attributes;access=$access;retired_gui=$retiredGui;retired_start_menu=$retiredStartMenu}
        if ($result.Count -gt 128) { throw 'Unexpected number of legacy integration files.' }
    }
    return $result
}

function Restore-GnFileAccess([string]$Path,[string]$Access) {
    $security = [Security.AccessControl.FileSecurity]::new()
    $security.SetSecurityDescriptorSddlForm($Access,[Security.AccessControl.AccessControlSections]::Access)
    Set-Acl -LiteralPath $Path -AclObject $security
}

function Copy-GnRelatedIntegration([string]$Backup,$Records,[bool]$Restore,[switch]$ExcludeRetiredGui) {
    foreach ($record in $Records) {
        $stored = Assert-GnOwnedPath $Backup (Join-Path $Backup $record.relative)
        $source = if ($Restore) { $stored } else { $record.source }
        $target = if ($Restore) { $record.source } else { $stored }
        [void](Assert-GnOwnedPath ([IO.Path]::GetDirectoryName($record.source)) $record.source)
        if ($Restore -and $ExcludeRetiredGui -and (($record.PSObject.Properties['retired_gui'] -and $record.retired_gui) -or ($record.PSObject.Properties['retired_start_menu'] -and $record.retired_start_menu))) {
            if ([IO.File]::Exists($target)) {
                if ((Get-FileHash -LiteralPath $target -Algorithm SHA256).Hash.ToLowerInvariant() -cne $record.sha256) { throw 'A legacy GUI shortcut changed; preserve it for review.' }
                Remove-Item -LiteralPath $target -Force
            }
            continue
        }
        if ((Get-FileHash -LiteralPath $source -Algorithm SHA256).Hash.ToLowerInvariant() -cne $record.sha256) { throw 'A legacy integration source changed; the file was preserved.' }
        if ([IO.File]::Exists($target)) {
            if ((Get-FileHash -LiteralPath $target -Algorithm SHA256).Hash.ToLowerInvariant() -cne $record.sha256) { throw 'A legacy integration destination changed; the file was preserved.' }
            continue
        }
        [void][IO.Directory]::CreateDirectory([IO.Path]::GetDirectoryName($target))
        [IO.File]::Copy($source,$target,$false)
        [IO.File]::SetAttributes($target,[IO.FileAttributes]$record.attributes)
        if ($Restore) { Restore-GnFileAccess $target $record.access }
    }
}

function Assert-GnRegistryCanRestore($Snapshot) {
    $current = Get-GnRegistrySnapshot $Snapshot.hive $Snapshot.path
    $keys = @{}
    foreach ($record in $Snapshot.keys) { $keys[$record.relative] = $record }
    foreach ($record in $current.keys) {
        if (-not $keys.ContainsKey($record.relative)) { throw 'A legacy registry key changed outside this migration; rollback preserved it.' }
        $values = @{}
        foreach ($entry in $keys[$record.relative].values) { $values[$entry.name] = $entry }
        foreach ($entry in $record.values) {
            if (-not $values.ContainsKey($entry.name)) { throw 'A legacy registry value changed outside this migration; rollback preserved it.' }
            $old = $values[$entry.name]
            $ours = $Snapshot.path -ceq 'Software\GitForWindows' -and $entry.name -ceq 'CurrentVersion' -and $entry.value -ceq '2.54.0.git-next'
            if (-not $ours -and (($entry | ConvertTo-Json -Depth 5 -Compress) -cne ($old | ConvertTo-Json -Depth 5 -Compress))) { throw 'A legacy registry value changed outside this migration; rollback preserved it.' }
        }
    }
}

function Get-GnRegisteredUninstaller([string]$Root,[string]$Command) {
    $path = $Command.Trim()
    if ($path.StartsWith('"') -and $path.EndsWith('"')) { $path = $path.Substring(1,$path.Length-2) }
    if (-not [IO.Path]::IsPathRooted($path) -or [IO.Path]::GetFileName($path) -notmatch '^unins[0-9]+\.exe$') { throw 'The registered Git uninstaller is invalid.' }
    $path = Assert-GnOwnedPath $Root ([IO.Path]::GetFullPath($path))
    if ([IO.Path]::GetDirectoryName($path).TrimEnd('\','/') -ine $Root.TrimEnd('\','/') -or -not [IO.File]::Exists($path)) { throw 'The registered Git uninstaller is outside the selected installation or missing.' }
    return $path
}

function Get-GnMigrationFileIndex($Records) {
    $index = [Collections.Generic.Dictionary[string,object]]::new([StringComparer]::Ordinal)
    foreach ($record in @($Records)) {
        $relative = $record.relative.Replace('\','/')
        if ($index.ContainsKey($relative)) { $index[$relative] = $null }
        else { $index.Add($relative,$record) }
    }
    return ,$index
}

function Get-GnSameDirectoryStale($Package,[string]$PackageSha256,$Files) {
    if ($PackageSha256 -cnotmatch '^[a-f0-9]{64}$') { throw 'The selected package identity is invalid.' }
    $owned = @{'package.json'=$PackageSha256}
    foreach ($property in $Package.files.PSObject.Properties) { $owned[$property.Name.Replace('\','/')] = $property.Value }
    $stale = [Collections.Generic.List[object]]::new()
    foreach ($file in @($Files)) {
        if (-not $owned.ContainsKey($file.relative.Replace('\','/'))) { $stale.Add($file) }
    }
    return $stale.ToArray()
}

function Restore-GnSameDirectoryPayload($Plan,$Package,[string]$Backup) {
    $snapshot = Get-GnMigrationFileIndex $Plan.files
    $owned = [Collections.Generic.Dictionary[string,string]]::new([StringComparer]::Ordinal)
    foreach ($property in $Package.files.PSObject.Properties) { $owned.Add($property.Name,$property.Value) }
    $owned['package.json'] = $Plan.package_sha256
    foreach ($relative in $owned.Keys) {
        if (-not $snapshot.ContainsKey($relative) -or -not $snapshot[$relative] -or $snapshot[$relative].sha256 -cne $owned[$relative]) { throw 'The protected snapshot lacks an exact MSI-owned file.' }
        $target = Assert-GnOwnedPath $Plan.install_root (Join-Path $Plan.install_root $relative)
        if ([IO.File]::Exists($target)) {
            if ((Get-FileHash -LiteralPath $target -Algorithm SHA256).Hash.ToLowerInvariant() -cne $owned[$relative]) { throw 'An MSI-owned file changed during takeover; it was preserved.' }
            continue
        }
        if ([IO.Directory]::Exists($target)) { throw 'An MSI-owned file was replaced by a directory; it was preserved.' }
        Copy-GnMigrationFile $Backup $Plan.install_root $snapshot[$relative] -RestoreAccess
    }
    Assert-GnInstalledFiles $Plan.install_root $Package
}

function Remove-GnSameDirectoryStale($Plan) {
    $packagePath = Assert-GnOwnedPath $Plan.install_root (Join-Path $Plan.install_root 'package.json')
    if ((Get-FileHash -LiteralPath $packagePath -Algorithm SHA256).Hash.ToLowerInvariant() -cne $Plan.package_sha256) { throw 'The selected candidate changed during takeover.' }
    $stale = Get-GnMigrationFileIndex $Plan.stale
    foreach ($file in @(Get-GnTree $Plan.original_root)) {
        $relative = $file.relative.Replace('\','/')
        if ($relative -ieq 'package.json' -or -not $stale.ContainsKey($relative) -or -not $stale[$relative] -or $stale[$relative].sha256 -cne $file.sha256) { continue }
        $target = Assert-GnOwnedPath $Plan.original_root (Join-Path $Plan.original_root $file.relative)
        if ((Get-FileHash -LiteralPath $target -Algorithm SHA256).Hash.ToLowerInvariant() -cne $stale[$relative].sha256) { continue }
        Remove-Item -LiteralPath $target -Force
    }
}

function Restore-GnTakeoverRoot($Plan) {
    if ([IO.Directory]::Exists($Plan.original_root)) {
        [void](Assert-GnOwnedPath $Plan.original_root (Join-Path $Plan.original_root '.ownership-check'))
        if ((Get-Acl -LiteralPath $Plan.original_root).Sddl -cne $Plan.root_acl) { throw 'The original installation root access changed; it was preserved.' }
        return
    }
    if ([IO.File]::Exists($Plan.original_root)) { throw 'The original installation root was replaced by a file.' }
    if (-not ('GitNext.ScopeNative' -as [type])) { Add-Type -Path (Join-Path $PSScriptRoot '../updater/Scope.Native.cs') }
    [GitNext.ScopeNative]::CreateProtectedDirectory($Plan.original_root,$Plan.root_acl)
    [void](Assert-GnOwnedPath $Plan.original_root (Join-Path $Plan.original_root '.ownership-check'))
    if ((Get-Acl -LiteralPath $Plan.original_root).Sddl -cne $Plan.root_acl) { throw 'The original root was created concurrently with different access; recovery preserved it.' }
}

function Get-GnTakeoverRegistry([string]$Hive) {
    $paths = @('Software\GitForWindows','Software\Microsoft\Windows\CurrentVersion\Uninstall\Git_is1',
        'Software\Classes\Applications\git.exe','Software\Classes\Applications\git-gui.exe','Software\Classes\Applications\gitk.exe',
        'Software\Classes\Directory\shell\git_gui','Software\Classes\Directory\shell\git_shell',
        'Software\Classes\Directory\Background\shell\git_gui','Software\Classes\Directory\Background\shell\git_shell',
        'Software\Classes\LibraryFolder\Background\shell\git_gui','Software\Classes\LibraryFolder\Background\shell\git_shell',
        'Software\Classes\.git','Software\Classes\.gitignore','Software\Classes\.gitattributes','Software\Classes\.gitmodules',
        'Software\Classes\.sh','Software\Classes\git_auto_file','Software\Classes\sh_auto_file')
    foreach ($path in $paths) { Get-GnRegistrySnapshot $Hive $path }
    foreach ($path in @('Console\Git Bash','Console\Git CMD')) { Get-GnRegistrySnapshot 'CurrentUser' $path }
}

function Get-GnOriginalGitRegistration([string]$Root) {
    foreach ($hive in @('CurrentUser','LocalMachine')) {
        $base = [Microsoft.Win32.RegistryKey]::OpenBaseKey([Microsoft.Win32.RegistryHive]::$hive,[Microsoft.Win32.RegistryView]::Registry64)
        try {
            $key = $base.OpenSubKey('Software\Microsoft\Windows\CurrentVersion\Uninstall\Git_is1')
            if (-not $key) { continue }
            try { $location = $key.GetValue('InstallLocation'); $command = $key.GetValue('UninstallString') } finally { $key.Dispose() }
            if ($location -and [IO.Path]::GetFullPath($location).TrimEnd('\','/') -ieq $Root) {
                return [pscustomobject]@{hive=$hive;uninstaller=(Get-GnRegisteredUninstaller $Root $command)}
            }
        } finally { $base.Dispose() }
    }
    throw 'The old uninstaller registration does not match the selected directory.'
}

function New-GnPreinstallSnapshot([string]$OriginalGitRoot,[string]$SnapshotRoot,[string]$CandidatePackagePath,[string]$CandidateMsiSha256) {
    $OriginalGitRoot = [IO.Path]::GetFullPath($OriginalGitRoot).TrimEnd('\','/')
    $SnapshotRoot = [IO.Path]::GetFullPath($SnapshotRoot).TrimEnd('\','/')
    $CandidatePackagePath = [IO.Path]::GetFullPath($CandidatePackagePath)
    if ($CandidateMsiSha256 -cnotmatch '^[a-f0-9]{64}$' -or $SnapshotRoot -ieq $OriginalGitRoot -or $SnapshotRoot.StartsWith($OriginalGitRoot+'\',[StringComparison]::OrdinalIgnoreCase)) { throw 'The pre-MSI snapshot identity or location is invalid.' }
    if ([IO.Directory]::Exists($SnapshotRoot) -or [IO.File]::Exists($SnapshotRoot)) { throw 'A pre-MSI snapshot must use a new private directory.' }
    [void](Assert-GnOwnedPath ([IO.Path]::GetDirectoryName($CandidatePackagePath)) $CandidatePackagePath)
    $package = Get-Content -LiteralPath $CandidatePackagePath -Raw -Encoding UTF8 | ConvertFrom-Json
    if ($package.schema_version -ne 1 -or $package.product -cne 'git-next' -or $package.install_kind -cne 'msi' -or $package.platform -notin @('windows-x64','windows-arm64') -or -not $package.complete_git_parity -or -not $package.release_eligible) { throw 'A verified complete MSI candidate is required for the preinstall snapshot.' }
    $scope = Get-GnInstallScope $package
    $machine = $scope -eq 'machine'
    if ($machine -and -not (Test-GnAdministrator)) { throw 'The pre-MSI machine snapshot requires an administrator.' }
    if (Test-GnBusy $OriginalGitRoot) { throw 'The original Git is busy; no installation has started.' }
    if ([IO.File]::Exists((Join-Path $OriginalGitRoot 'package.json'))) { throw 'The original Git directory is already overlaid by an MSI; a pre-MSI snapshot cannot be invented afterwards.' }
    $registration = Get-GnOriginalGitRegistration $OriginalGitRoot
    if (($registration.hive -eq 'LocalMachine') -ne $machine) { throw 'The original Git and candidate MSI installation scopes differ.' }
    $sid = [Security.Principal.WindowsIdentity]::GetCurrent().User.Value
    if ($machine) {
        Assert-GnProtectedObject ([IO.Path]::GetDirectoryName($SnapshotRoot))
        if (-not ('GitNext.ScopeNative' -as [type])) { Add-Type -Path (Join-Path $PSScriptRoot '../updater/Scope.Native.cs') }
        [GitNext.ScopeNative]::CreateProtectedDirectory($SnapshotRoot,'O:BAG:BAD:P(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)')
        Assert-GnProtectedObject $SnapshotRoot
    } else { New-GnPrivateDirectory $SnapshotRoot }
    $files = @(Get-GnTree $OriginalGitRoot)
    $index = Get-GnMigrationFileIndex $files
    if (-not $index.ContainsKey('cmd/git.exe') -or -not $index['cmd/git.exe']) { throw 'The original Git snapshot lacks its exact command entry.' }
    $related = @(Get-GnRelatedIntegration $OriginalGitRoot)
    $snapshot = [pscustomobject]@{schema_version=1;product='git-next';kind='pre-msi-original-git';user_sid=$sid;install_scope=$scope;platform=$package.platform;original_root=$OriginalGitRoot;candidate_package_sha256=(Get-FileHash -LiteralPath $CandidatePackagePath -Algorithm SHA256).Hash.ToLowerInvariant();candidate_msi_sha256=$CandidateMsiSha256;original_git_sha256=$index['cmd/git.exe'].sha256;files=$files;registry=@(Get-GnTakeoverRegistry $registration.hive);related=$related;old_update_tasks=@(Get-GnOldUpdateTasks $OriginalGitRoot);user_path=[Environment]::GetEnvironmentVariable('Path','User');machine_path=[Environment]::GetEnvironmentVariable('Path','Machine');root_acl=(Get-Acl -LiteralPath $OriginalGitRoot).Sddl;hive=$registration.hive;uninstaller_relative=[IO.Path]::GetFileName($registration.uninstaller);uninstaller_sha256=(Get-FileHash -LiteralPath $registration.uninstaller -Algorithm SHA256).Hash.ToLowerInvariant()}
    $original = Join-Path $SnapshotRoot 'original'
    [void][IO.Directory]::CreateDirectory($original)
    foreach ($file in $files) { Copy-GnMigrationFile $OriginalGitRoot $original $file }
    $integration = Join-Path $SnapshotRoot 'integration'
    [void][IO.Directory]::CreateDirectory($integration)
    Copy-GnRelatedIntegration $integration $related $false
    if (Test-GnBusy $OriginalGitRoot) { throw 'The original Git became busy while snapshotting; no MSI installation may start.' }
    foreach ($file in $files) {
        $source = Assert-GnOwnedPath $OriginalGitRoot (Join-Path $OriginalGitRoot $file.relative)
        if ((Get-FileHash -LiteralPath $source -Algorithm SHA256).Hash.ToLowerInvariant() -cne $file.sha256) { throw 'The original Git changed while snapshotting; no MSI installation may start.' }
    }
    $after = @(Get-GnTree $OriginalGitRoot)
    $afterIndex = Get-GnMigrationFileIndex $after
    if ($afterIndex.Count -ne $index.Count) { throw 'The original Git file set changed while snapshotting; no MSI installation may start.' }
    foreach ($name in $index.Keys) {
        if (-not $afterIndex.ContainsKey($name) -or -not $afterIndex[$name] -or ($index[$name] | ConvertTo-Json -Depth 5 -Compress) -cne ($afterIndex[$name] | ConvertTo-Json -Depth 5 -Compress)) { throw 'The original Git file or access metadata changed while snapshotting.' }
    }
    $registryAfter = @(Get-GnTakeoverRegistry $registration.hive)
    $relatedAfter = @(Get-GnRelatedIntegration $OriginalGitRoot)
    $tasksAfter = @(Get-GnOldUpdateTasks $OriginalGitRoot)
    if (($snapshot.registry | ConvertTo-Json -Depth 20 -Compress) -cne ($registryAfter | ConvertTo-Json -Depth 20 -Compress) -or ($snapshot.related | ConvertTo-Json -Depth 10 -Compress) -cne ($relatedAfter | ConvertTo-Json -Depth 10 -Compress) -or ($snapshot.old_update_tasks | ConvertTo-Json -Depth 10 -Compress) -cne ($tasksAfter | ConvertTo-Json -Depth 10 -Compress) -or $snapshot.user_path -cne [Environment]::GetEnvironmentVariable('Path','User') -or $snapshot.machine_path -cne [Environment]::GetEnvironmentVariable('Path','Machine') -or $snapshot.root_acl -cne (Get-Acl -LiteralPath $OriginalGitRoot).Sddl -or $snapshot.candidate_package_sha256 -cne (Get-FileHash -LiteralPath $CandidatePackagePath -Algorithm SHA256).Hash.ToLowerInvariant()) { throw 'Pre-MSI integration or candidate metadata changed while snapshotting.' }
    $manifest = Join-Path $SnapshotRoot 'snapshot.json'
    Write-GnJson $manifest $snapshot
    if ($machine) { Assert-GnProtectedObject $manifest }
    return $manifest
}

function Read-GnOriginalSnapshot([string]$Manifest,[string]$ExpectedSha256,[string]$OriginalRoot,[switch]$AllowOperator) {
    $Manifest = [IO.Path]::GetFullPath($Manifest)
    $root = [IO.Path]::GetDirectoryName($Manifest)
    [void](Assert-GnOwnedPath $root $Manifest)
    Assert-GnScopedFile $Manifest
    if (([IO.FileInfo]::new($Manifest)).Length -gt 64MB -or $ExpectedSha256 -cnotmatch '^[a-f0-9]{64}$' -or (Get-FileHash -LiteralPath $Manifest -Algorithm SHA256).Hash.ToLowerInvariant() -cne $ExpectedSha256) { throw 'The protected original snapshot manifest changed.' }
    $snapshot = Get-Content -LiteralPath $Manifest -Raw -Encoding UTF8 | ConvertFrom-Json
    $validKind = $snapshot.kind -ceq 'pre-msi-original-git' -or ($AllowOperator -and $snapshot.kind -ceq 'operator-recovery-original-git')
    if ($snapshot.schema_version -ne 1 -or $snapshot.product -cne 'git-next' -or -not $validKind -or $snapshot.user_sid -cne [Security.Principal.WindowsIdentity]::GetCurrent().User.Value -or [IO.Path]::GetFullPath($snapshot.original_root).TrimEnd('\','/') -ine $OriginalRoot -or $snapshot.install_scope -notin @('user','machine') -or $snapshot.platform -notin @('windows-x64','windows-arm64') -or $snapshot.candidate_package_sha256 -cnotmatch '^[a-f0-9]{64}$' -or $snapshot.candidate_msi_sha256 -cnotmatch '^[a-f0-9]{64}$' -or @($snapshot.files).Count -gt 100000 -or $snapshot.original_git_sha256 -cnotmatch '^[a-f0-9]{64}$') { throw 'The protected original snapshot identity is invalid.' }
    if ($root.TrimEnd('\','/') -ieq $OriginalRoot -or $root.StartsWith($OriginalRoot+'\',[StringComparison]::OrdinalIgnoreCase)) { throw 'The original snapshot must be outside the installation.' }
    Assert-GnTakeoverPrivateObject $root ($snapshot.install_scope -eq 'machine')
    Assert-GnTakeoverPrivateObject $Manifest ($snapshot.install_scope -eq 'machine')
    $index = Get-GnMigrationFileIndex $snapshot.files
    [long]$total = 0
    foreach ($file in $snapshot.files) {
        if (-not $index[$file.relative] -or $file.relative.Contains('\') -or $file.relative.StartsWith('/') -or $file.sha256 -cnotmatch '^[a-f0-9]{64}$' -or $file.bytes -lt 0 -or $file.bytes -gt 1GB) { throw 'The original snapshot file inventory is invalid.' }
        [void](Assert-GnOwnedPath (Join-Path $root 'original') (Join-Path $root ('original/'+$file.relative)))
        $total += $file.bytes
        if ($total -gt 4GB) { throw 'The original snapshot exceeds the recovery capacity limit.' }
    }
    if (-not $index.ContainsKey('cmd/git.exe') -or -not $index['cmd/git.exe'] -or $index['cmd/git.exe'].sha256 -cne $snapshot.original_git_sha256) { throw 'The original snapshot command identity is invalid.' }
    return $snapshot
}

function Assert-GnTakeoverPrivateObject([string]$Path,[bool]$Machine) {
    if ($Machine) { Assert-GnProtectedObject $Path; return }
    if (-not (Test-Path -LiteralPath $Path)) { throw 'A private takeover object is missing.' }
    if ((Get-Item -LiteralPath $Path -Force).Attributes -band [IO.FileAttributes]::ReparsePoint) { throw 'A private takeover object is a reparse point.' }
    $acl = Get-Acl -LiteralPath $Path
    $trusted = @([Security.Principal.WindowsIdentity]::GetCurrent().User.Value,'S-1-5-18','S-1-5-32-544')
    if ($acl.GetOwner([Security.Principal.SecurityIdentifier]).Value -notin $trusted) { throw 'A private takeover object has a different owner.' }
    $write = [Security.AccessControl.FileSystemRights]::WriteData -bor [Security.AccessControl.FileSystemRights]::AppendData -bor [Security.AccessControl.FileSystemRights]::WriteExtendedAttributes -bor [Security.AccessControl.FileSystemRights]::WriteAttributes -bor [Security.AccessControl.FileSystemRights]::Delete -bor [Security.AccessControl.FileSystemRights]::ChangePermissions -bor [Security.AccessControl.FileSystemRights]::TakeOwnership
    foreach ($rule in $acl.GetAccessRules($true,$true,[Security.Principal.SecurityIdentifier])) {
        if ($rule.PropagationFlags -band [Security.AccessControl.PropagationFlags]::InheritOnly) { continue }
        if ($rule.AccessControlType -eq [Security.AccessControl.AccessControlType]::Allow -and ($rule.FileSystemRights -band $write) -and $rule.IdentityReference.Value -notin $trusted) { throw 'A private takeover object permits another user to write.' }
    }
}

function Get-GnTakeoverPlan([string]$InstallRoot,[string]$OriginalGitRoot,[string]$OriginalSnapshot='',[string]$OriginalSnapshotSha256='') {
    $InstallRoot = [IO.Path]::GetFullPath($InstallRoot).TrimEnd('\','/')
    $OriginalGitRoot = [IO.Path]::GetFullPath($OriginalGitRoot).TrimEnd('\','/')
    # git-default-v2 installs into the official Git directory itself. Nesting is still rejected
    # because the protected backup must be reproducible; identity is the intended overlay case.
    if ($InstallRoot.StartsWith($OriginalGitRoot+'\',[StringComparison]::OrdinalIgnoreCase) -or $OriginalGitRoot.StartsWith($InstallRoot+'\',[StringComparison]::OrdinalIgnoreCase)) { throw 'The new and old installations must be separate.' }
    $SameDirectory = $InstallRoot -ieq $OriginalGitRoot
    $packagePath = Assert-GnOwnedPath $InstallRoot (Join-Path $InstallRoot 'package.json')
    $package = Get-Content -LiteralPath $packagePath -Raw -Encoding UTF8 | ConvertFrom-Json
    $packageSha256 = (Get-FileHash -LiteralPath $packagePath -Algorithm SHA256).Hash.ToLowerInvariant()
    $originalBinding = $null
    if ($OriginalSnapshot) {
        $OriginalSnapshot = [IO.Path]::GetFullPath($OriginalSnapshot)
        $snapshotHash = $OriginalSnapshotSha256
        $snapshot = Read-GnOriginalSnapshot $OriginalSnapshot $snapshotHash $OriginalGitRoot
        if ($snapshot.candidate_package_sha256 -cne $packageSha256 -or $snapshot.platform -cne $package.platform -or $snapshot.install_scope -cne (Get-GnInstallScope $package)) { throw 'The pre-MSI snapshot belongs to a different candidate.' }
        $originalBinding = [pscustomobject]@{manifest=$OriginalSnapshot;sha256=$snapshotHash;candidate_package_sha256=$snapshot.candidate_package_sha256;candidate_msi_sha256=$snapshot.candidate_msi_sha256;original_git_sha256=$snapshot.original_git_sha256;kind=$snapshot.kind}
    }
    Assert-GnInstalledFiles $InstallRoot $package
    if ($package.platform -notin @('windows-x64','windows-arm64') -or $package.engine -cne 'source-built-git-next-native-core') { throw 'The complete native MSI installation is required.' }
    foreach ($name in @('migration/git-next-bridge.exe','migration/git-next-bridge-gui.exe')) {
        if (-not $package.files.PSObject.Properties[$name]) { throw 'The MSI lacks its owned migration bridge.' }
    }
    if (-not [IO.File]::Exists((Join-Path $OriginalGitRoot 'cmd/git.exe'))) { throw 'The selected old installation is not the supported Git for Windows installer layout.' }
    $registration = Get-GnOriginalGitRegistration $OriginalGitRoot
    $hive = $registration.hive
    $uninstaller = $registration.uninstaller
    $files = @(Get-GnTree $OriginalGitRoot)
    $nativePrefix = if ($package.platform -eq 'windows-arm64') { 'clangarm64' } else { 'mingw64' }
    $coreHashes = @($files | Where-Object { $_.relative -in @('cmd/git.exe','bin/git.exe',($nativePrefix+'/bin/git.exe'),($nativePrefix+'/libexec/git-core/git.exe')) } | ForEach-Object sha256)
    $builtins = if ($package.PSObject.Properties['builtins']) { @($package.builtins) } else { @() }
    $bridges = @(); $retained = @(); $unmapped = @(); $stale = @()
    if ($SameDirectory) {
        # The MSI already placed the real payload in the official directory, so no bridge
        # forwarder is produced. Everything still owned by the official uninstaller is stale
        # and must be gone once its uninstaller has run.
        $stale = @(Get-GnSameDirectoryStale $package $packageSha256 $files)
    } else {
        foreach ($file in $files) {
            if ($file.relative -match '(^|/)unins[0-9]+\.' -or $file.relative -match 'git-update-git-for-windows|git-for-windows-updater' -or $file.relative -in @('mingw64/bin/wintoast.exe','clangarm64/bin/wintoast.exe')) { continue }
            if (-not $file.pe) { $retained += $file; continue }
            if (-not $file.relative.EndsWith('.exe',[StringComparison]::OrdinalIgnoreCase)) { continue }
            $target = 'runtime/' + $file.relative
            if ($file.relative -eq 'git-bash.exe' -and $package.files.PSObject.Properties['bin/git-terminal.exe']) { $target = 'bin/git-terminal.exe' }
            if ($file.relative -in @('cmd/git.exe','bin/git.exe')) { $target = $file.relative }
            if (-not $package.files.PSObject.Properties[$target] -and $file.relative -match '^cmd/(git-(?:receive-pack|upload-pack|upload-archive)|scalar)\.exe$') {
                $command = $Matches[1]
                $target = if ($command -eq 'scalar') { 'runtime/'+$nativePrefix+'/bin/scalar.exe' } else { 'runtime/'+$nativePrefix+'/libexec/git-core/'+$command+'.exe' }
            }
            if (-not $package.files.PSObject.Properties[$target] -and $file.relative -in @('cmd/git-lfs.exe','cmd/git-credential-manager.exe')) { $target = 'runtime/'+$nativePrefix+'/bin/'+[IO.Path]::GetFileName($file.relative) }
            if (-not $package.files.PSObject.Properties[$target] -and $coreHashes -contains $file.sha256 -and $file.relative -match '^(cmd|mingw64/bin|clangarm64/bin|mingw64/libexec/git-core|clangarm64/libexec/git-core)/git-([a-z][a-z0-9-]*)\.exe$' -and $builtins -contains $Matches[2]) {
                $target = 'runtime/'+$nativePrefix+'/libexec/git-core/git-'+$Matches[2]+'.exe'
            }
            if (-not $package.files.PSObject.Properties[$target]) { $unmapped += $file.relative; continue }
            if ($file.subsystem -notin @(2,3)) { throw 'A legacy executable has an unsupported subsystem.' }
            $bridgeName = if ($file.subsystem -eq 2) { 'migration/git-next-bridge-gui.exe' } else { 'migration/git-next-bridge.exe' }
            $bridges += [pscustomobject]@{relative=$file.relative;target=$target;bridge=$bridgeName;sha256=$package.files.PSObject.Properties[$bridgeName].Value}
        }
        if (-not @($bridges | Where-Object { $_.relative -ceq 'cmd/git.exe' }).Count) { throw 'The default legacy Git command has no owned mapping.' }
    }
    $scopeContext = Get-GnScopeContext $InstallRoot $package
    $configDirectory = if ($scopeContext.machine) { $scopeContext.config } else { Get-GnConfigDirectory }
    $installScope = $scopeContext.scope
    $systemConfig = Assert-GnOwnedPath $configDirectory (Join-Path $configDirectory 'system.gitconfig')
    $pointer = Assert-GnOwnedPath $configDirectory (Join-Path $configDirectory 'migration.json')
    if ([IO.File]::Exists($pointer)) { throw 'A prior migration transaction exists; resume or roll it back first.' }
    if ([IO.File]::Exists($systemConfig)) { throw 'A prior system-config migration exists; resume or roll it back first.' }
    if ((Get-FileHash -LiteralPath $packagePath -Algorithm SHA256).Hash.ToLowerInvariant() -cne $packageSha256) { throw 'The selected package changed during planning.' }
    return [pscustomobject]@{schema_version=1;product='git-next';status='planned';same_directory=$SameDirectory;install_root=$InstallRoot;install_scope=$installScope;original_root=$OriginalGitRoot;original_snapshot=$originalBinding;hive=$hive;platform=$package.platform;version=$package.version;package_sha256=$packageSha256;release_eligible=$package.release_eligible;complete_git_parity=$package.complete_git_parity;uninstaller_relative=[IO.Path]::GetFileName($uninstaller);uninstaller_sha256=(Get-FileHash -LiteralPath $uninstaller -Algorithm SHA256).Hash.ToLowerInvariant();files=$files;bridges=$bridges;retained=$retained;stale=$stale;unmapped=$unmapped;system_config=$systemConfig;pointer=$pointer;registry=@();related=@(Get-GnRelatedIntegration $OriginalGitRoot);old_update_tasks=@();user_path=$null;machine_path=$null;root_acl=$null;system_config_sha256=$null}
}

function Copy-GnMigrationFile([string]$SourceRoot,[string]$TargetRoot,$Record,[string]$AllowedTargetSha256 = '',[switch]$RestoreAccess) {
    $source = Assert-GnOwnedPath $SourceRoot (Join-Path $SourceRoot $Record.relative)
    $target = Assert-GnOwnedPath $TargetRoot (Join-Path $TargetRoot $Record.relative)
    if ((Get-FileHash -LiteralPath $source -Algorithm SHA256).Hash.ToLowerInvariant() -cne $Record.sha256) { throw 'Migration source bytes changed; no replacement was made.' }
    $replaceExisting = $false
    if ([IO.File]::Exists($target)) {
        Assert-GnScopedFile $target
        $current = (Get-FileHash -LiteralPath $target -Algorithm SHA256).Hash.ToLowerInvariant()
        if ($current -ceq $Record.sha256) { return }
        if (-not $AllowedTargetSha256 -or $current -cne $AllowedTargetSha256) { throw 'A migration destination changed; it was preserved without replacement.' }
        $replaceExisting = $true
        [IO.File]::SetAttributes($target,[IO.FileAttributes]::Normal)
    }
    New-GnOutputParent $target
    $inputStream = [IO.File]::OpenRead($source)
    $outputStream = $null
    try {
        if ($replaceExisting) {
            [void](Assert-GnOwnedPath $TargetRoot $target)
            $outputStream = [IO.File]::Open($target,[IO.FileMode]::Open,[IO.FileAccess]::ReadWrite,[IO.FileShare]::None)
            $algorithm = [Security.Cryptography.SHA256]::Create()
            try { $lockedHash = ([BitConverter]::ToString($algorithm.ComputeHash($outputStream))).Replace('-','').ToLowerInvariant() } finally { $algorithm.Dispose() }
            if ($lockedHash -cne $AllowedTargetSha256) { throw 'A migration destination changed; it was preserved without replacement.' }
            $outputStream.Position = 0; $outputStream.SetLength(0)
        } else { $outputStream = Open-GnNewFile $target }
        $inputStream.CopyTo($outputStream); $outputStream.Flush($true)
    }
    finally { if ($outputStream) { $outputStream.Dispose() }; $inputStream.Dispose() }
    [IO.File]::SetAttributes($target,[IO.FileAttributes]$Record.attributes)
    if ($RestoreAccess) { Restore-GnFileAccess $target $Record.access }
    if ((Get-FileHash -LiteralPath $target -Algorithm SHA256).Hash.ToLowerInvariant() -cne $Record.sha256) { throw 'Migration file copy verification failed.' }
}

function Set-GnBridgeRegistration($Plan,$Record,[bool]$Remove) {
    $path = Assert-GnOwnedPath $Plan.original_root (Join-Path $Plan.original_root $Record.relative)
    if (-not $Remove) {
        # A forwarder whose target resolves to its own file starts a copy of itself on every
        # invocation, and each copy does the same. The mapping is refused before it is stored.
        $target = Assert-GnOwnedPath $Plan.install_root (Join-Path $Plan.install_root $Record.target)
        if ([IO.Path]::GetFullPath($target).TrimEnd('\') -ieq [IO.Path]::GetFullPath($path).TrimEnd('\')) { throw 'A legacy bridge would forward to its own entry point.' }
    }
    $base = [Microsoft.Win32.RegistryKey]::OpenBaseKey([Microsoft.Win32.RegistryHive]::$($Plan.hive),[Microsoft.Win32.RegistryView]::Registry64)
    $name = 'Software\git-next\Bridges\' + $path
    try {
        if ($Remove) {
            $key = $base.OpenSubKey($name)
            if ($key) {
                try { if ($key.GetValue('Transaction') -cne $Plan.transaction_id) { throw 'An unrelated migration owns the bridge registration.' } } finally { $key.Dispose() }
                $base.DeleteSubKeyTree($name,$false)
            }
        } else {
            $key = $base.CreateSubKey($name)
            try {
                if ($key.GetValue('Transaction') -and $key.GetValue('Transaction') -cne $Plan.transaction_id) { throw 'Another migration owns the legacy bridge.' }
                $key.SetValue('Platform',$Plan.platform); $key.SetValue('RelativePath',$Record.target.Replace('/','\')); $key.SetValue('Transaction',$Plan.transaction_id)
            } finally { $key.Dispose() }
        }
    } finally { $base.Dispose() }
}

function Write-GnTakeoverJournal([string]$Transaction,$Plan) { Write-GnJson (Assert-GnOwnedPath $Transaction (Join-Path $Transaction 'transaction.json')) $Plan }

function Restore-GnMigrationPath($Plan,[string]$Scope) {
    $Original = if ($Scope -eq 'Machine') { $Plan.machine_path } else { $Plan.user_path }
    $OriginalRoot = $Plan.original_root
    $CandidateBin = Join-Path $Plan.install_root 'bin'
    $current = [Environment]::GetEnvironmentVariable('Path',$Scope)
    $parts = [Collections.Generic.List[string]]::new()
    foreach ($part in @($current -split ';')) { if ($part) { $parts.Add($part) } }
    $originalParts = @($Original -split ';' | Where-Object { $_ })
    $originalExpanded = @($originalParts | ForEach-Object { [Environment]::ExpandEnvironmentVariables($_).Replace('/','\').TrimEnd('\') })
    $introduced = [Collections.Generic.List[string]]::new()
    $mapping = Get-GnMigrationPathMapping $Plan
    foreach ($entry in $originalExpanded) {
        if (-not $entry.StartsWith($OriginalRoot+'\',[StringComparison]::OrdinalIgnoreCase)) { continue }
        $relative = $entry.Substring($OriginalRoot.Length+1).Replace('\','/')
        if ($mapping.ContainsKey($relative)) { $introduced.Add((Join-Path $Plan.install_root $mapping[$relative]).TrimEnd('\')) }
    }
    $primaryScope = if ($Plan.install_scope -eq 'machine') { 'Machine' } else { 'User' }
    if ($Scope -eq $primaryScope) { $introduced.Add($CandidateBin.TrimEnd('\')) }
    for ($i=$parts.Count-1;$i -ge 0;$i--) {
        $expanded = [Environment]::ExpandEnvironmentVariables($parts[$i]).Replace('/','\').TrimEnd('\')
        if ($expanded -in $introduced -and $expanded -notin $originalExpanded) { $parts.RemoveAt($i) }
    }
    $originalBinIndex = -1
    for ($i = 0; $i -lt $originalParts.Count; $i++) {
        $entry = $originalParts[$i]
        if ($Scope -eq $primaryScope -and $originalExpanded[$i] -ieq $CandidateBin.TrimEnd('\','/')) { $originalBinIndex = $i; continue }
        $expanded = [Environment]::ExpandEnvironmentVariables($entry).TrimEnd('\','/')
        if ($expanded -ine $OriginalRoot -and -not $expanded.StartsWith($OriginalRoot+'\',[StringComparison]::OrdinalIgnoreCase)) { continue }
        if (-not @($parts | Where-Object { $_ -ieq $entry }).Count) { $parts.Insert([Math]::Min($i,$parts.Count),$entry) }
    }
    if ($Scope -eq $primaryScope) {
        for ($i = $parts.Count - 1; $i -ge 0; $i--) { if ($parts[$i].TrimEnd('\','/') -ieq $CandidateBin.TrimEnd('\','/')) { $parts.RemoveAt($i) } }
        if ($originalBinIndex -ge 0) { $parts.Insert([Math]::Min($originalBinIndex,$parts.Count),$originalParts[$originalBinIndex]) }
    }
    $restored = $parts.ToArray() -join ';'
    if ($restored -cne $current) { [Environment]::SetEnvironmentVariable('Path',$restored,$Scope) }
}

function Get-GnMigrationPathMapping($Plan) {
    $native = if ($Plan.platform -eq 'windows-arm64') { 'clangarm64' } else { 'mingw64' }
    return @{ 'cmd'='bin';'bin'='runtime/bin';'usr/bin'='runtime/usr/bin';'mingw64/bin'=('runtime/'+$native+'/bin');'clangarm64/bin'=('runtime/'+$native+'/bin') }
}

function Set-GnMigratedPath($Plan,[string]$Scope,[string]$Original) {
    $current = [Environment]::GetEnvironmentVariable('Path',$Scope)
    $parts = [Collections.Generic.List[string]]::new()
    foreach ($part in @($current -split ';')) { if ($part) { $parts.Add($part) } }
    $mapping = Get-GnMigrationPathMapping $Plan
    foreach ($part in @($Original -split ';')) {
        if (-not $part) { continue }
        $expanded = [Environment]::ExpandEnvironmentVariables($part).Replace('/','\').TrimEnd('\')
        if (-not $expanded.StartsWith($Plan.original_root+'\',[StringComparison]::OrdinalIgnoreCase)) { continue }
        $relative = $expanded.Substring($Plan.original_root.Length+1).Replace('\','/')
        if (-not $mapping.ContainsKey($relative)) { continue }
        $target = Join-Path $Plan.install_root $mapping[$relative]
        if (-not [IO.Directory]::Exists($target)) { throw 'A migrated command directory is missing from the owned runtime.' }
        for ($i=$parts.Count-1;$i -ge 0;$i--) { if ($parts[$i].TrimEnd('\','/') -ieq $expanded -or $parts[$i].TrimEnd('\','/') -ieq $target.TrimEnd('\','/')) { $parts.RemoveAt($i) } }
        $parts.Add($target)
    }
    $primaryScope = if ($Plan.install_scope -eq 'machine') { 'Machine' } else { 'User' }
    if ($Scope -eq $primaryScope) {
        $bin = Join-Path $Plan.install_root 'bin'
        for ($i=$parts.Count-1;$i -ge 0;$i--) { if ($parts[$i].TrimEnd('\','/') -ieq $bin.TrimEnd('\','/')) { $parts.RemoveAt($i) } }
        $parts.Insert(0,$bin)
    }
    $updated = $parts.ToArray() -join ';'
    if ($updated -cne $current) { [Environment]::SetEnvironmentVariable('Path',$updated,$Scope) }
}

function Start-GnTakeover($Plan,[string]$StateRoot,[string]$TlsArchive) {
    if (-not $Plan.complete_git_parity -or -not $Plan.release_eligible) { throw 'Takeover requires the accepted complete release; the candidate remains separate.' }
    if ($Plan.hive -eq 'LocalMachine' -and $Plan.install_scope -ne 'machine') { throw 'Replacing machine-wide Git requires an accepted machine-wide MSI; a user MSI cannot remove it.' }
    if (@($Plan.unmapped).Count) { throw 'Unmapped legacy executables prevent removal. Preserve the old installation and finish the explicit mappings.' }
    if (($Plan.hive -eq 'LocalMachine' -or $Plan.install_scope -eq 'machine') -and -not (Test-GnAdministrator)) { throw 'Replacing the selected machine installation requires one elevated migration run.' }
    if ($Plan.same_directory -and (-not $Plan.PSObject.Properties['original_snapshot'] -or -not $Plan.original_snapshot)) { throw 'Same-directory takeover requires its verified pre-MSI original snapshot; preserve the current installation.' }
    if ($Plan.same_directory) {
        $originalSnapshot = Read-GnOriginalSnapshot $Plan.original_snapshot.manifest $Plan.original_snapshot.sha256 $Plan.original_root
        if ($originalSnapshot.candidate_package_sha256 -cne $Plan.package_sha256) { throw 'The original snapshot belongs to another candidate.' }
        $snapshotRoot = [IO.Path]::GetDirectoryName($Plan.original_snapshot.manifest)
        foreach ($file in $originalSnapshot.files) {
            $source = Assert-GnOwnedPath $snapshotRoot (Join-Path $snapshotRoot ('original/'+$file.relative))
            if ((Get-FileHash -LiteralPath $source -Algorithm SHA256).Hash.ToLowerInvariant() -cne $file.sha256) { throw 'The protected pre-MSI original bytes changed; takeover was not started.' }
        }
    }
    if ((Test-GnBusy $Plan.install_root) -or (Test-GnBusy $Plan.original_root)) { throw 'An owned Git/runtime process is active; migration was not started.' }
    $packagePath = Join-Path $Plan.install_root 'package.json'
    if ((Get-FileHash -LiteralPath $packagePath -Algorithm SHA256).Hash.ToLowerInvariant() -cne $Plan.package_sha256) { throw 'The selected candidate changed after planning.' }
    $package = Get-Content -LiteralPath $packagePath -Raw -Encoding UTF8 | ConvertFrom-Json
    Assert-GnInstalledFiles $Plan.install_root $package
    $native = Assert-GnOwnedPath $Plan.install_root (Join-Path $Plan.install_root 'bin/git.exe')
    $identity = (& $native version --json) | ConvertFrom-Json
    if ($LASTEXITCODE -ne 0 -or $identity.product -cne 'git-next' -or $identity.version -cne $Plan.version -or $identity.complete_git_parity -ne $true -or $identity.external_git_fallback -ne $false -or $identity.external_ssh_fallback -ne $false) { throw 'The embedded native identity does not permit takeover; the old Git is preserved.' }
    $compatibility = (& $native compatibility --json) | ConvertFrom-Json
    if ($LASTEXITCODE -ne 0 -or $compatibility.complete_git_parity -ne $true) { throw 'The embedded native identity does not permit takeover; the old Git is preserved.' }
    $language = (& $native language get --json) | ConvertFrom-Json
    if ($LASTEXITCODE -ne 0 -or $language.schema_version -ne 1 -or $language.language -notin @('chinese','english')) { throw 'The native user configuration could not be loaded; the old Git is preserved.' }
    $providerScript = Join-Path $Plan.install_root 'components/install-tls-provider.ps1'
    $arguments = @('-NoLogo','-NoProfile','-NonInteractive','-ExecutionPolicy','Bypass','-WindowStyle','Hidden','-File',$providerScript,'-Platform',$Plan.platform,'-Quiet')
    if ($Plan.install_scope -eq 'machine') { $arguments += '-Machine' }
    if ($TlsArchive) { $arguments += @('-Archive',[IO.Path]::GetFullPath($TlsArchive)) }
    & (Join-Path $env:SystemRoot 'System32/WindowsPowerShell/v1.0/powershell.exe') @arguments
    if ($LASTEXITCODE) { throw 'The pinned legacy TLS provider could not be prepared; the old Git is preserved.' }
    $StateRoot = [IO.Path]::GetFullPath($StateRoot)
    $machine = $Plan.install_scope -eq 'machine'
    if ($machine) {
        $scopeContext = Get-GnScopeContext $Plan.install_root (Get-Content -LiteralPath $packagePath -Raw -Encoding UTF8 | ConvertFrom-Json)
        if ($StateRoot.TrimEnd('\') -ine [IO.Path]::GetFullPath($scopeContext.migrations).TrimEnd('\')) { throw 'Machine rollback data must use the protected machine migration root.' }
    }
    foreach ($root in @($Plan.original_root,$Plan.install_root)) {
        if ($StateRoot.TrimEnd('\','/') -ieq $root -or $StateRoot.StartsWith($root+'\',[StringComparison]::OrdinalIgnoreCase)) { throw 'The protected backup must be outside both installations.' }
    }
    $ownership = Assert-GnOwnedPath $StateRoot (Join-Path $StateRoot 'git-next-migrations.json')
    $sid = [Security.Principal.WindowsIdentity]::GetCurrent().User.Value
    if ([IO.Directory]::Exists($StateRoot)) {
        if ($machine) { Assert-GnProtectedObject $StateRoot }
        if (-not [IO.File]::Exists($ownership)) { throw 'The existing backup directory has no git-next ownership marker.' }
        $owner = Get-Content -LiteralPath $ownership -Raw -Encoding UTF8 | ConvertFrom-Json
        if ($owner.schema_version -ne 1 -or $owner.product -cne 'git-next' -or $owner.user_sid -cne $sid) { throw 'The backup directory belongs to a different owner.' }
    } else {
        New-GnPrivateDirectory $StateRoot -Machine:$machine
        Write-GnJson $ownership @{schema_version=1;product='git-next';user_sid=$sid}
    }
    $transactionId = [Guid]::NewGuid().ToString('N')
    $transaction = Assert-GnOwnedPath $StateRoot (Join-Path $StateRoot $transactionId)
    New-GnPrivateDirectory $transaction -Machine:$machine
    $Plan | Add-Member -NotePropertyName transaction_id -NotePropertyValue $transactionId
    $Plan | Add-Member -NotePropertyName user_sid -NotePropertyValue $sid
    $Plan | Add-Member -NotePropertyName state_root -NotePropertyValue $StateRoot
    $Plan | Add-Member -NotePropertyName uninstaller_pid -NotePropertyValue $null
    $Plan | Add-Member -NotePropertyName uninstaller_start_identity -NotePropertyValue $null
    $Plan.user_path = [Environment]::GetEnvironmentVariable('Path','User')
    $Plan.machine_path = [Environment]::GetEnvironmentVariable('Path','Machine')
    $Plan.root_acl = (Get-Acl -LiteralPath $Plan.original_root).Sddl
    $registryPaths = @('Software\GitForWindows','Software\Microsoft\Windows\CurrentVersion\Uninstall\Git_is1',
        'Software\Classes\Applications\git.exe','Software\Classes\Applications\git-gui.exe','Software\Classes\Applications\gitk.exe',
        'Software\Classes\Directory\shell\git_gui','Software\Classes\Directory\shell\git_shell',
        'Software\Classes\Directory\Background\shell\git_gui','Software\Classes\Directory\Background\shell\git_shell',
        'Software\Classes\LibraryFolder\Background\shell\git_gui','Software\Classes\LibraryFolder\Background\shell\git_shell',
        'Software\Classes\.git','Software\Classes\.gitignore','Software\Classes\.gitattributes','Software\Classes\.gitmodules',
        'Software\Classes\.sh','Software\Classes\git_auto_file','Software\Classes\sh_auto_file')
    $Plan.registry = @($registryPaths | ForEach-Object { Get-GnRegistrySnapshot $Plan.hive $_ })
    foreach ($path in @('Console\Git Bash','Console\Git CMD')) { $Plan.registry += Get-GnRegistrySnapshot 'CurrentUser' $path }
    $Plan.old_update_tasks = @(Get-GnOldUpdateTasks $Plan.original_root)
    Write-GnTakeoverJournal $transaction $Plan
    $backup = Assert-GnOwnedPath $transaction (Join-Path $transaction 'original')
    New-GnPrivateDirectory $backup -Machine:$machine
    foreach ($file in $Plan.files) { Copy-GnMigrationFile $Plan.original_root $backup $file }
    $relatedBackup = Assert-GnOwnedPath $transaction (Join-Path $transaction 'integration')
    New-GnPrivateDirectory $relatedBackup -Machine:$machine
    Copy-GnRelatedIntegration $relatedBackup $Plan.related $false
    $Plan.status = 'backed-up'; Write-GnTakeoverJournal $transaction $Plan
    $process = $null
    try {
        if ((Test-GnBusy $Plan.install_root) -or (Test-GnBusy $Plan.original_root)) { throw 'An installation became busy; removal was deferred.' }
        if ($machine) { Initialize-GnMachineDirectory ([IO.Path]::GetDirectoryName($Plan.pointer)) }
        else { [void][IO.Directory]::CreateDirectory([IO.Path]::GetDirectoryName($Plan.pointer)) }
        $pointerData = @{schema_version=1;product='git-next';install_root=$Plan.install_root;install_scope=$Plan.install_scope;original_root=$Plan.original_root;transaction=$transaction;transaction_id=$transactionId} | ConvertTo-Json -Compress
        $stream = Open-GnNewFile $Plan.pointer
        try { $data = [Text.UTF8Encoding]::new($false).GetBytes($pointerData+"`n"); $stream.Write($data,0,$data.Length); $stream.Flush($true) } finally { $stream.Dispose() }
        $configurationChanges = @()
        foreach ($file in $Plan.files) {
            if ($file.pe -or $file.relative -notmatch '^(etc|usr/etc|mingw64/etc|clangarm64/etc)/' -or $file.relative -in @('etc/gitconfig','etc/package-versions.txt','etc/install-options.txt')) { continue }
            $relative = 'runtime/'+$file.relative
            Assert-GnRuntimeConfigPath $relative
            $target = Assert-GnOwnedPath $Plan.install_root (Join-Path $Plan.install_root $relative)
            $configurationChanges += [pscustomobject]@{target=$relative;source=(Assert-GnOwnedPath $backup (Join-Path $backup $file.relative));sha256=$file.sha256;attributes=$file.attributes;access=$file.access;expected=(Get-GnRuntimeConfigHash $target)}
        }
        $Plan | Add-Member -NotePropertyName runtime_configuration -NotePropertyValue (Join-Path $transaction 'runtime-configuration')
        Write-GnTakeoverJournal $transaction $Plan
        Start-GnRuntimeConfigTransaction $Plan.install_root (Get-Content -LiteralPath $packagePath -Raw -Encoding UTF8 | ConvertFrom-Json) $Plan.runtime_configuration $configurationChanges
        Set-GnOldUpdateTasks $Plan.old_update_tasks $false
        $uninstaller = Get-GnRegisteredUninstaller $Plan.original_root (Join-Path $Plan.original_root $Plan.uninstaller_relative)
        if ((Get-FileHash -LiteralPath $uninstaller -Algorithm SHA256).Hash.ToLowerInvariant() -cne $Plan.uninstaller_sha256) { throw 'The old uninstaller changed after planning.' }
        $Plan.status = 'removing-original'; Write-GnTakeoverJournal $transaction $Plan
        $process = Start-Process -FilePath $uninstaller -ArgumentList '/VERYSILENT /SUPPRESSMSGBOXES /NORESTART' -WindowStyle Hidden -PassThru
        $Plan.uninstaller_pid = $process.Id
        $Plan.uninstaller_start_identity = 'ticks:' + $process.StartTime.ToUniversalTime().Ticks.ToString([Globalization.CultureInfo]::InvariantCulture)
        Write-GnTakeoverJournal $transaction $Plan
        if (-not $process.WaitForExit(300000)) { throw 'The original uninstaller is still running; retain this transaction for recovery.' }
        if ($process.ExitCode -ne 0) { throw 'The original uninstaller failed; the protected backup is retained.' }
        Restore-GnTakeoverRoot $Plan
        if ($Plan.same_directory) {
            Restore-GnSameDirectoryPayload $Plan $package $backup
            # git-default-v2: the payload already owns the official directory, so the official
            # uninstaller's leftovers are removed outright instead of being replaced by bridges.
            # A registration left behind by an earlier layout would point cmd\git.exe and
            # bin\git.exe at themselves once InstallRoot moves to this directory, and a
            # forwarder that starts itself never stops: it is removed with the leftovers.
            $bridgeBase = [Microsoft.Win32.RegistryKey]::OpenBaseKey([Microsoft.Win32.RegistryHive]::$($Plan.hive),[Microsoft.Win32.RegistryView]::Registry64)
            try {
                foreach ($relative in @('cmd\git.exe','bin\git.exe')) {
                    $name = 'Software\git-next\Bridges\' + (Join-Path $Plan.original_root $relative)
                    $existing = $bridgeBase.OpenSubKey($name)
                    if ($existing) { try { $existing.Dispose() } finally { $bridgeBase.DeleteSubKeyTree($name,$false) } }
                }
            } finally { $bridgeBase.Dispose() }
            Remove-GnSameDirectoryStale $Plan
        } else {
            foreach ($file in @(Get-GnTree $Plan.original_root)) {
                if (-not $file.pe) { continue }
                $original = @($Plan.files | Where-Object { $_.relative -ceq $file.relative })
                if ($original.Count -ne 1 -or $original[0].sha256 -cne $file.sha256) { throw 'A new executable appeared in the old directory; removal was stopped.' }
                $target = Assert-GnOwnedPath $Plan.original_root (Join-Path $Plan.original_root $file.relative)
                Remove-Item -LiteralPath $target -Force
            }
            foreach ($file in $Plan.retained) { Copy-GnMigrationFile $backup $Plan.original_root $file -RestoreAccess }
            foreach ($record in $Plan.bridges) {
                $target = Assert-GnOwnedPath $Plan.original_root (Join-Path $Plan.original_root $record.relative)
                [void][IO.Directory]::CreateDirectory([IO.Path]::GetDirectoryName($target))
                $source = Assert-GnOwnedPath $Plan.install_root (Join-Path $Plan.install_root $record.bridge)
                if ((Get-FileHash -LiteralPath $source -Algorithm SHA256).Hash.ToLowerInvariant() -cne $record.sha256) { throw 'An owned bridge changed during migration.' }
                Set-GnBridgeRegistration $Plan $record $false
                [IO.File]::Copy($source,$target,$true)
            }
        }
        foreach ($record in $Plan.registry) {
            if ($record.path -ceq 'Software\Microsoft\Windows\CurrentVersion\Uninstall\Git_is1') { continue }
            if ($record.path -match '\\shell\\git_(gui|shell)$') {
                Assert-GnRegistryCanRestore $record
                $base = [Microsoft.Win32.RegistryKey]::OpenBaseKey([Microsoft.Win32.RegistryHive]::$($record.hive),[Microsoft.Win32.RegistryView]::Registry64)
                try { $base.DeleteSubKeyTree($record.path,$false) } finally { $base.Dispose() }
                continue
            }
            Assert-GnRegistryCanRestore $record
            Restore-GnRegistrySnapshot $record
        }
        $base = [Microsoft.Win32.RegistryKey]::OpenBaseKey([Microsoft.Win32.RegistryHive]::$($Plan.hive),[Microsoft.Win32.RegistryView]::Registry64)
        try {
            $key = $base.OpenSubKey('Software\GitForWindows',$true)
            if ($key) { try { $key.SetValue('CurrentVersion','2.54.0.git-next') } finally { $key.Dispose() } }
        } finally { $base.Dispose() }
        Copy-GnRelatedIntegration $relatedBackup $Plan.related $true -ExcludeRetiredGui
        $configRoot = [IO.Path]::GetDirectoryName($Plan.system_config)
        [void][IO.Directory]::CreateDirectory($configRoot)
        [void](Assert-GnOwnedPath $configRoot $Plan.system_config)
        if ([IO.File]::Exists($Plan.system_config)) { throw 'Another writer created the migration configuration.' }
        $escape = { param($value) $value.Replace('\','/').Replace('"','\"') }
        $baseConfig = & $escape (Join-Path $Plan.install_root 'runtime/etc/gitconfig')
        $legacyConfig = & $escape (Join-Path $Plan.original_root 'etc/gitconfig')
        $text = "# git-next migration $transactionId`n[include]`n"
        # Preserve ordered multivars such as credential.helper without prepending new helpers.
        # In git-default-v2 the legacy etc\ location disappears with the official layout, so the
        # include must always point at the payload's own runtime configuration.
        $selectedConfig = if ($Plan.same_directory -or -not [IO.File]::Exists((Join-Path $Plan.original_root 'etc/gitconfig'))) { $baseConfig } else { $legacyConfig }
        $text += "`tpath = `"$selectedConfig`"`n"
        # The product defaults are rendered from packaging/runtime-defaults/gitconfig by
        # tools/devtool/defaults.go and checked by the Go check-runtime-defaults command,
        # so a migrated system configuration cannot disagree with the payload's own.
        $text += "[core]`n`tautocrlf = input`n`teol = lf`n`tlongpaths = true`n`tfscache = false`n`tpreloadIndex = false`n`tcommitGraph = true`n`tquotePath = false`n`tfsmonitor = false`n[gc]`n`tauto = 256`n`tautoDetach = true`n`twriteCommitGraph = true`n[fetch]`n`tprune = true`n`tpruneTags = true`n`twriteCommitGraph = true`n[feature]`n`tmanyFiles = true`n[diff]`n`talgorithm = histogram`n[merge]`n`tconflictStyle = zdiff3`n[rebase]`n`tautoStash = true`n`tupdateRefs = true`n[push]`n`tautoSetupRemote = true`n[init]`n`tdefaultBranch = main`n[branch]`n`tsort = -committerdate`n[tag]`n`tsort = version:refname`n[index]`n`tthreads = 1`n[grep]`n`tthreads = 1`n"
        $stream = Open-GnNewFile $Plan.system_config
        try { $data = [Text.UTF8Encoding]::new($false).GetBytes($text); $stream.Write($data,0,$data.Length); $stream.Flush($true) } finally { $stream.Dispose() }
        $Plan.system_config_sha256 = (Get-FileHash -LiteralPath $Plan.system_config -Algorithm SHA256).Hash.ToLowerInvariant()
        if ($machine) { Set-GnMigratedPath $Plan 'Machine' $Plan.machine_path }
        Set-GnMigratedPath $Plan 'User' $Plan.user_path
        Assert-GnInstalledFiles $Plan.install_root $package
        if ((Get-FileHash -LiteralPath $packagePath -Algorithm SHA256).Hash.ToLowerInvariant() -cne $Plan.package_sha256) { throw 'The selected candidate changed during takeover.' }
        $Plan.status = 'active'; Write-GnTakeoverJournal $transaction $Plan
        return $transaction
    } catch {
        if ($Plan.status -eq 'removing-original' -and $process -and -not $process.HasExited) {
            $Plan.status = 'waiting-for-uninstaller'; Write-GnTakeoverJournal $transaction $Plan
        } else {
            $Plan.status = 'recovery-required'; Write-GnTakeoverJournal $transaction $Plan
            $restored = $false
            try { Restore-GnTakeover $transaction; $restored = $true } catch { }
            if ($restored) { throw 'Migration failed; the original Git and integration were automatically restored. The protected backup is retained.' }
        }
        throw 'Migration did not complete. The protected transaction and original backup are retained; run takeover Rollback after the original uninstaller exits.'
    }
}

function Get-GnTakeoverRecoveryContext([string]$Transaction) {
    $Transaction = [IO.Path]::GetFullPath($Transaction).TrimEnd('\','/')
    $journal = Assert-GnOwnedPath $Transaction (Join-Path $Transaction 'transaction.json')
    Assert-GnScopedFile $journal
    if (([IO.FileInfo]::new($journal)).Length -gt 64MB) { throw 'The protected migration journal exceeds its size limit.' }
    $plan = Get-Content -LiteralPath $journal -Raw -Encoding UTF8 | ConvertFrom-Json
    if ($plan.schema_version -ne 1 -or $plan.product -cne 'git-next' -or $plan.transaction_id -cnotmatch '^[a-f0-9]{32}$' -or $plan.transaction_id -cne [IO.Path]::GetFileName($Transaction) -or $plan.user_sid -cne [Security.Principal.WindowsIdentity]::GetCurrent().User.Value -or $plan.platform -notin @('windows-x64','windows-arm64') -or $plan.install_scope -notin @('user','machine') -or $plan.package_sha256 -cnotmatch '^[a-f0-9]{64}$') { throw 'The transaction does not belong to this user and directory.' }
    $machine = $plan.install_scope -eq 'machine'
    foreach ($path in @($plan.install_root,$plan.original_root,$plan.state_root,$plan.pointer,$plan.system_config)) { if (-not [IO.Path]::IsPathRooted($path)) { throw 'The protected transaction contains a relative installation/state path.' } }
    if ($plan.PSObject.Properties['same_directory'] -and $plan.same_directory -and [IO.Path]::GetFullPath($plan.install_root).TrimEnd('\','/') -ine [IO.Path]::GetFullPath($plan.original_root).TrimEnd('\','/')) { throw 'The protected same-directory transaction roots differ.' }
    if ($machine -and -not (Test-GnAdministrator)) { throw 'The protected machine rollback requires an administrator.' }
    [void](Assert-GnOwnedPath $plan.state_root $Transaction)
    if ([IO.Path]::GetDirectoryName($Transaction) -ine [IO.Path]::GetFullPath($plan.state_root).TrimEnd('\','/')) { throw 'The transaction is outside its selected state root.' }
    $ownership = Assert-GnOwnedPath $plan.state_root (Join-Path $plan.state_root 'git-next-migrations.json')
    Assert-GnScopedFile $ownership
    $owner = Get-Content -LiteralPath $ownership -Raw -Encoding UTF8 | ConvertFrom-Json
    if ($owner.schema_version -ne 1 -or $owner.product -cne 'git-next' -or $owner.user_sid -cne $plan.user_sid) { throw 'The protected transaction state has a different owner.' }
    Assert-GnTakeoverPrivateObject $plan.state_root $machine
    Assert-GnTakeoverPrivateObject $Transaction $machine
    Assert-GnTakeoverPrivateObject $journal $machine
    Assert-GnTakeoverPrivateObject $ownership $machine
    $root = if ($machine) { Get-GnMachineDataRoot } else { Join-Path ([Environment]::GetFolderPath('LocalApplicationData')) 'git-next' }
    if ($machine) {
        if ([IO.Path]::GetFullPath($plan.state_root).TrimEnd('\','/') -ine (Join-Path $root ('migrations/'+$plan.platform)).TrimEnd('\','/')) { throw 'The protected machine migration root is invalid.' }
        Assert-GnProtectedObject $plan.state_root; Assert-GnProtectedObject $Transaction; Assert-GnProtectedObject $journal
    }
    $configuration = if ($machine) { Join-Path $root 'config' } else { Get-GnConfigDirectory }
    $pointerPath = Assert-GnOwnedPath $configuration (Join-Path $configuration 'migration.json')
    if ([IO.Path]::GetFullPath($plan.pointer) -ine $pointerPath) { throw 'The protected migration pointer is outside its selected configuration root.' }
    if ([IO.Path]::GetFullPath($plan.system_config) -ine (Join-Path $configuration 'system.gitconfig')) { throw 'The protected migration system configuration is outside its selected root.' }
    if ([IO.File]::Exists($pointerPath)) {
        Assert-GnScopedFile $pointerPath
        $pointer = Get-Content -LiteralPath $pointerPath -Raw -Encoding UTF8 | ConvertFrom-Json
        if ($pointer.schema_version -ne 1 -or $pointer.product -cne 'git-next' -or $pointer.transaction_id -cne $plan.transaction_id -or $pointer.install_scope -cne $plan.install_scope -or [IO.Path]::GetFullPath($pointer.transaction).TrimEnd('\','/') -ine $Transaction -or [IO.Path]::GetFullPath($pointer.install_root).TrimEnd('\','/') -ine [IO.Path]::GetFullPath($plan.install_root).TrimEnd('\','/')) { throw 'A different migration owns the active handoff; rollback was deferred.' }
    }
    $backup = Assert-GnOwnedPath $Transaction (Join-Path $Transaction 'original')
    $packageRoot = if ($plan.PSObject.Properties['same_directory'] -and $plan.same_directory) { $backup } else { $plan.install_root }
    $packagePath = Assert-GnOwnedPath $packageRoot (Join-Path $packageRoot 'package.json')
    if ((Get-FileHash -LiteralPath $packagePath -Algorithm SHA256).Hash.ToLowerInvariant() -cne $plan.package_sha256) { throw 'The original candidate manifest in the protected transaction changed.' }
    $package = Get-Content -LiteralPath $packagePath -Raw -Encoding UTF8 | ConvertFrom-Json
    if ($package.schema_version -ne 1 -or $package.product -cne 'git-next' -or $package.platform -cne $plan.platform -or (Get-GnInstallScope $package) -cne $plan.install_scope -or $package.engine -cne 'source-built-git-next-native-core') { throw 'The protected candidate manifest identity is invalid.' }
    return [pscustomobject]@{plan=$plan;package=$package;transaction=$Transaction;backup=$backup;scope=$plan.install_scope;machine=$machine;root=$root;updates=(Join-Path $root ('updates/'+$plan.platform));migrations=$plan.state_root;config=$configuration}
}

function Test-GnTakeoverMsiInstalled([string]$ProductCode) {
    if ($ProductCode -cnotmatch '^\{[0-9A-Fa-f]{8}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{12}\}$') { throw 'The rollback MSI product identity is invalid.' }
    $installer = New-Object -ComObject WindowsInstaller.Installer
    try {
        $state = $installer.ProductState($ProductCode)
        if ($state -notin @(-1,5)) { throw 'The rollback MSI product is not in an installed or removed state.' }
        return $state -eq 5
    } finally { [void][Runtime.InteropServices.Marshal]::FinalReleaseComObject($installer) }
}

function Assert-GnTakeoverCurrentMsi([string]$InstallRoot,$Package) {
    if ($Package.schema_version -ne 1 -or $Package.product -cne 'git-next' -or $Package.install_kind -cne 'msi' -or $Package.engine -cne 'source-built-git-next-native-core' -or -not $Package.complete_git_parity -or -not $Package.release_eligible -or -not (Test-GnTakeoverMsiInstalled $Package.product_code)) { throw 'An exact installed native MSI is required for controlled rollback.' }
    [void](Get-GnScopeContext $InstallRoot $Package)
    $hive = if ((Get-GnInstallScope $Package) -eq 'machine') { 'LocalMachine' } else { 'CurrentUser' }
    $base = [Microsoft.Win32.RegistryKey]::OpenBaseKey([Microsoft.Win32.RegistryHive]::$hive,[Microsoft.Win32.RegistryView]::Registry64)
    try {
        $key = $base.OpenSubKey('Software\git-next\MSI\'+$Package.platform)
        if (-not $key) { throw 'The owned MSI registration is missing.' }
        try { $registered = $key.GetValue('InstallRoot') } finally { $key.Dispose() }
        if (-not $registered -or [IO.Path]::GetFullPath($registered).TrimEnd('\','/') -ine [IO.Path]::GetFullPath($InstallRoot).TrimEnd('\','/')) { throw 'The owned MSI registration belongs to a different root.' }
    } finally { $base.Dispose() }
}

function Get-GnTakeoverOriginalRecovery($Recovery) {
    $plan = $Recovery.plan
    if ($plan.PSObject.Properties['original_snapshot'] -and $plan.original_snapshot) {
        $binding = $plan.original_snapshot
        $snapshot = Read-GnOriginalSnapshot $binding.manifest $binding.sha256 $plan.original_root
    } else {
        $path = Assert-GnOwnedPath $Recovery.transaction (Join-Path $Recovery.transaction 'recovery-source.json')
        Assert-GnScopedFile $path
        $attachment = Get-Content -LiteralPath $path -Raw -Encoding UTF8 | ConvertFrom-Json
        if ($attachment.schema_version -ne 1 -or $attachment.product -cne 'git-next' -or $attachment.kind -cne 'operator-recovery-source' -or $attachment.transaction_id -cne $plan.transaction_id -or $attachment.user_sid -cne $plan.user_sid -or $attachment.original_candidate_package_sha256 -cne $plan.package_sha256 -or $attachment.preserved_journal_sha256 -cnotmatch '^[a-f0-9]{64}$') { throw 'The explicit operator recovery attachment is invalid.' }
        $preserved = Assert-GnOwnedPath $Recovery.transaction (Join-Path $Recovery.transaction 'transaction-before-operator-recovery.json')
        Assert-GnScopedFile $preserved
        if ((Get-FileHash -LiteralPath $preserved -Algorithm SHA256).Hash.ToLowerInvariant() -cne $attachment.preserved_journal_sha256) { throw 'The original operator recovery journal evidence changed.' }
        $old = Get-Content -LiteralPath $preserved -Raw -Encoding UTF8 | ConvertFrom-Json
        if ($old.transaction_id -cne $plan.transaction_id -or $old.package_sha256 -cne $plan.package_sha256 -or $old.user_sid -cne $plan.user_sid -or $old.original_root -ine $plan.original_root) { throw 'The preserved journal belongs to another migration.' }
        $snapshot = Read-GnOriginalSnapshot $attachment.snapshot_manifest $attachment.snapshot_sha256 $plan.original_root -AllowOperator
        if ($snapshot.kind -cne 'operator-recovery-original-git' -or $snapshot.original_git_sha256 -cne $attachment.original_git_expected_sha256 -or -not $snapshot.PSObject.Properties['metadata_provenance']) { throw 'Operator recovery must state its distinct original-byte and metadata provenance.' }
        foreach ($name in @('files','file_access','registry','paths','related','root_acl','old_update_tasks')) {
            if (-not $snapshot.metadata_provenance.PSObject.Properties[$name] -or -not $snapshot.metadata_provenance.$name) { throw 'Operator recovery metadata provenance is incomplete.' }
        }
        $binding = [pscustomobject]@{manifest=$attachment.snapshot_manifest;sha256=$attachment.snapshot_sha256}
    }
    if ($snapshot.candidate_package_sha256 -cne $plan.package_sha256 -or $snapshot.platform -cne $plan.platform -or $snapshot.install_scope -cne $plan.install_scope) { throw 'The original snapshot belongs to another migration candidate.' }
    $root = [IO.Path]::GetDirectoryName($binding.manifest)
    foreach ($file in @($snapshot.files)) {
        $source = Assert-GnOwnedPath $root (Join-Path $root ('original/'+$file.relative))
        if ((Get-FileHash -LiteralPath $source -Algorithm SHA256).Hash.ToLowerInvariant() -cne $file.sha256) { throw 'Protected original recovery bytes changed; MSI removal was not authorized.' }
    }
    return [pscustomobject]@{binding=$binding;snapshot=$snapshot;root=$root}
}

function Initialize-GnTakeoverRecoveryTools($Recovery,$Package,[string]$PackagePath) {
    $plan = $Recovery.plan
    $tools = Assert-GnOwnedPath $Recovery.transaction (Join-Path $Recovery.transaction 'recovery-tools')
    if (-not [IO.Directory]::Exists($tools)) { New-GnPrivateDirectory $tools -Machine:$Recovery.machine }
    if ($Recovery.machine) { Assert-GnProtectedObject $tools }
    $hashes = [ordered]@{}
    foreach ($name in @('migration/takeover.ps1','migration/Takeover.Core.psm1','updater/Update.Core.psm1','updater/Scope.Core.ps1','updater/Scope.Native.cs','updater/Runtime.Configuration.ps1','updater/messages.zh-CN.json')) {
        $property = $Package.files.PSObject.Properties[$name]
        if (-not $property -or $property.Value -cnotmatch '^[a-f0-9]{64}$') { throw 'The current MSI lacks an exact recovery tool.' }
        $source = Assert-GnOwnedPath $plan.install_root (Join-Path $plan.install_root $name)
        if ((Get-FileHash -LiteralPath $source -Algorithm SHA256).Hash.ToLowerInvariant() -cne $property.Value) { throw 'A recovery tool differs from its current MSI manifest.' }
        $item = Get-Item -LiteralPath $source -Force
        Copy-GnMigrationFile $plan.install_root $tools ([pscustomobject]@{relative=$name;sha256=$property.Value;attributes=[int]$item.Attributes;access=''})
        $hashes[$name] = $property.Value
    }
    $control = Assert-GnOwnedPath $Recovery.transaction (Join-Path $Recovery.transaction 'rollback-package.json')
    $controlHash = (Get-FileHash -LiteralPath $PackagePath -Algorithm SHA256).Hash.ToLowerInvariant()
    if ([IO.File]::Exists($control)) {
        if ((Get-FileHash -LiteralPath $control -Algorithm SHA256).Hash.ToLowerInvariant() -cne $controlHash) { throw 'A different current MSI already owns the rollback authorization.' }
    } else {
        $source = [IO.File]::OpenRead($PackagePath)
        $output = $null
        try { $output = Open-GnNewFile $control; $source.CopyTo($output); $output.Flush($true) } finally { if ($output) { $output.Dispose() }; $source.Dispose() }
    }
    $plan | Add-Member -NotePropertyName rollback_package_sha256 -NotePropertyValue $controlHash -Force
    $plan | Add-Member -NotePropertyName rollback_product_code -NotePropertyValue $Package.product_code -Force
    $plan | Add-Member -NotePropertyName rollback_tools -NotePropertyValue ([pscustomobject]$hashes) -Force
    if (-not ('GitNext.ScopeNative' -as [type])) { Add-Type -Path (Join-Path $tools 'updater/Scope.Native.cs') }
    Write-GnTakeoverJournal $Recovery.transaction $plan
}

function Assert-GnTakeoverRecoveryTools($Recovery) {
    $plan = $Recovery.plan
    if (-not $plan.PSObject.Properties['rollback_package_sha256'] -or $plan.rollback_package_sha256 -cnotmatch '^[a-f0-9]{64}$' -or -not $plan.PSObject.Properties['rollback_tools']) { throw 'The protected rollback authorization is incomplete.' }
    $control = Assert-GnOwnedPath $Recovery.transaction (Join-Path $Recovery.transaction 'rollback-package.json')
    Assert-GnScopedFile $control
    if ((Get-FileHash -LiteralPath $control -Algorithm SHA256).Hash.ToLowerInvariant() -cne $plan.rollback_package_sha256) { throw 'The authorized current MSI manifest changed.' }
    $package = Get-Content -LiteralPath $control -Raw -Encoding UTF8 | ConvertFrom-Json
    if ($package.product -cne 'git-next' -or $package.platform -cne $plan.platform -or (Get-GnInstallScope $package) -cne $plan.install_scope -or $package.product_code -cne $plan.rollback_product_code) { throw 'The authorized rollback MSI identity differs.' }
    $tools = Assert-GnOwnedPath $Recovery.transaction (Join-Path $Recovery.transaction 'recovery-tools')
    $names = @('migration/takeover.ps1','migration/Takeover.Core.psm1','updater/Update.Core.psm1','updater/Scope.Core.ps1','updater/Scope.Native.cs','updater/Runtime.Configuration.ps1','updater/messages.zh-CN.json')
    if (@($plan.rollback_tools.PSObject.Properties).Count -ne $names.Count) { throw 'The recovery tool allowlist differs.' }
    foreach ($name in $names) {
        $pin = $plan.rollback_tools.PSObject.Properties[$name]
        $expected = $package.files.PSObject.Properties[$name]
        if (-not $pin -or -not $expected -or $pin.Value -cne $expected.Value) { throw 'A protected recovery tool is not owned by the authorized current MSI.' }
        $path = Assert-GnOwnedPath $tools (Join-Path $tools $name)
        Assert-GnScopedFile $path
        if ((Get-FileHash -LiteralPath $path -Algorithm SHA256).Hash.ToLowerInvariant() -cne $pin.Value) { throw 'A protected recovery tool changed.' }
    }
    return $package
}

function Invoke-GnTakeoverMsiRemoval($Recovery) {
    $plan = $Recovery.plan
    $hostDirectory = if (-not [Environment]::Is64BitProcess -and [Environment]::Is64BitOperatingSystem) { 'Sysnative' } else { 'System32' }
    $msi = Join-Path $env:SystemRoot ($hostDirectory+'/msiexec.exe')
    $log = Assert-GnOwnedPath $Recovery.transaction (Join-Path $Recovery.transaction 'rollback-msi.log')
    $process = Start-Process -FilePath $msi -WindowStyle Hidden -ArgumentList ('/x '+$plan.rollback_product_code+' /qn /norestart REBOOT=ReallySuppress MSIRESTARTMANAGERCONTROL=Disable /l*v "'+$log+'"') -PassThru
    $plan | Add-Member -NotePropertyName rollback_msi_pid -NotePropertyValue $process.Id -Force
    $plan | Add-Member -NotePropertyName rollback_msi_start_identity -NotePropertyValue ('ticks:'+$process.StartTime.ToUniversalTime().Ticks.ToString([Globalization.CultureInfo]::InvariantCulture)) -Force
    Write-GnTakeoverJournal $Recovery.transaction $plan
    if (-not $process.WaitForExit(1800000)) { throw 'The authorized MSI removal is still running; resume this protected transaction after it exits.' }
    if ($process.ExitCode -ne 0) { throw ('The authorized MSI removal did not complete without restart; exit '+$process.ExitCode+'. The protected rollback transaction is retained.') }
}

function Assert-GnTakeoverRegistryRecovery($Original,$PostInstall) {
    $current = Get-GnRegistrySnapshot $Original.hive $Original.path
    $allowed = @{}
    foreach ($snapshot in @($Original,$PostInstall)) {
        if (-not $snapshot) { continue }
        if ($snapshot.hive -cne $Original.hive -or $snapshot.path -cne $Original.path) { throw 'Recovery registry baselines refer to different keys.' }
        foreach ($key in $snapshot.keys) {
            if (-not $allowed.ContainsKey($key.relative)) { $allowed[$key.relative] = @{} }
            foreach ($entry in $key.values) {
                if (-not $allowed[$key.relative].ContainsKey($entry.name)) { $allowed[$key.relative][$entry.name] = @() }
                $allowed[$key.relative][$entry.name] += ($entry | ConvertTo-Json -Depth 5 -Compress)
            }
        }
    }
    foreach ($key in $current.keys) {
        if (-not $allowed.ContainsKey($key.relative)) { throw 'A registry key changed outside this takeover; recovery preserved it.' }
        foreach ($entry in $key.values) {
            $ours = $Original.path -ceq 'Software\GitForWindows' -and $entry.name -ceq 'CurrentVersion' -and $entry.value -ceq '2.54.0.git-next'
            if (-not $ours -and (-not $allowed[$key.relative].ContainsKey($entry.name) -or ($entry | ConvertTo-Json -Depth 5 -Compress) -cnotin $allowed[$key.relative][$entry.name])) { throw 'A registry value changed outside this takeover; recovery preserved it.' }
        }
    }
}

function Restore-GnTakeoverOriginalIntegration($Original,$Plan) {
    $post = @{}
    foreach ($record in $Plan.related) { $post[$record.source] = $record }
    foreach ($record in $Original.snapshot.related) {
        if (-not [IO.File]::Exists($record.source)) { continue }
        [void](Assert-GnOwnedPath ([IO.Path]::GetDirectoryName($record.source)) $record.source)
        $current = (Get-FileHash -LiteralPath $record.source -Algorithm SHA256).Hash.ToLowerInvariant()
        $access = (Get-Acl -LiteralPath $record.source).GetSecurityDescriptorSddlForm([Security.AccessControl.AccessControlSections]::Access)
        $allowed = $post[$record.source]
        if ($access -cne $record.access -and (-not $allowed -or $access -cne $allowed.access)) { throw 'An integration file access rule changed; recovery preserved it.' }
        if ($current -ceq $record.sha256) { Restore-GnFileAccess $record.source $record.access; continue }
        if (-not $allowed -or $current -cne $allowed.sha256) { throw 'An integration file changed outside this takeover; recovery preserved it.' }
        if ((Get-FileHash -LiteralPath $record.source -Algorithm SHA256).Hash.ToLowerInvariant() -cne $current) { throw 'An integration file changed during recovery; it was preserved.' }
        Remove-Item -LiteralPath $record.source -Force
    }
    Copy-GnRelatedIntegration (Join-Path $Original.root 'integration') $Original.snapshot.related $true
}

function Restore-GnTakeover([string]$Transaction,[switch]$PrepareOnly) {
    $recovery = Get-GnTakeoverRecoveryContext $Transaction
    $plan = $recovery.plan
    $Transaction = $recovery.transaction
    if ($plan.status -eq 'rolled-back') {
        if ([IO.File]::Exists($plan.pointer)) { [IO.File]::Delete((Assert-GnOwnedPath $recovery.config $plan.pointer)) }
        return
    }
    if ($plan.uninstaller_pid) {
        $pending = Get-Process -Id $plan.uninstaller_pid -ErrorAction SilentlyContinue
        if ($pending -and ('ticks:' + $pending.StartTime.ToUniversalTime().Ticks.ToString([Globalization.CultureInfo]::InvariantCulture)) -ceq $plan.uninstaller_start_identity) { throw 'The original uninstaller is still running; rollback was deferred.' }
    }
    if ((Test-GnBusy $plan.install_root) -or (Test-GnBusy $plan.original_root)) { throw 'A Git/runtime process is active; rollback was deferred.' }
    $same = $plan.PSObject.Properties['same_directory'] -and $plan.same_directory
    $original = $null
    $package = $recovery.package
    if ($same) {
        $original = Get-GnTakeoverOriginalRecovery $recovery
        if ($plan.PSObject.Properties['rollback_msi_pid'] -and $plan.rollback_msi_pid) {
            $pending = Get-Process -Id $plan.rollback_msi_pid -ErrorAction SilentlyContinue
            if ($pending -and ('ticks:'+$pending.StartTime.ToUniversalTime().Ticks.ToString([Globalization.CultureInfo]::InvariantCulture)) -ceq $plan.rollback_msi_start_identity) { throw 'The authorized MSI removal is still running; rollback was deferred.' }
        }
        $live = Assert-GnOwnedPath $plan.install_root (Join-Path $plan.install_root 'package.json')
        if ($plan.status -ne 'rolling-back') {
            $package = Get-Content -LiteralPath $live -Raw -Encoding UTF8 | ConvertFrom-Json
            if ($package.platform -cne $plan.platform -or (Get-GnInstallScope $package) -cne $plan.install_scope) { throw 'The current MSI differs from the selected rollback scope.' }
            Assert-GnTakeoverCurrentMsi $plan.install_root $package
            Initialize-GnTakeoverRecoveryTools $recovery $package $live
        } else { $package = Assert-GnTakeoverRecoveryTools $recovery }
        if ($PrepareOnly) { return (Assert-GnOwnedPath $Transaction (Join-Path $Transaction 'recovery-tools/migration/takeover.ps1')) }
    }
    $runtimePending = $plan.PSObject.Properties['runtime_configuration'] -and $plan.runtime_configuration -and (-not $plan.PSObject.Properties['runtime_configuration_rolled_back'] -or -not $plan.runtime_configuration_rolled_back)
    if ($runtimePending) {
        if ([IO.Path]::GetFullPath($plan.runtime_configuration).TrimEnd('\','/') -ine (Join-Path $Transaction 'runtime-configuration')) { throw 'The runtime configuration journal is outside this takeover.' }
        Restore-GnRuntimeConfigTransaction $plan.install_root $package $plan.runtime_configuration
        if ($same) {
            Complete-GnRuntimeConfigTransaction $plan.install_root $package $plan.runtime_configuration
            $plan | Add-Member -NotePropertyName runtime_configuration_rolled_back -NotePropertyValue $true -Force
            Write-GnTakeoverJournal $Transaction $plan
        }
    }
    if ($same) {
        $msiPresent = Test-GnTakeoverMsiInstalled $plan.rollback_product_code
        if ($msiPresent) { Assert-GnInstalledFiles $plan.install_root $package }
        if (-not [IO.File]::Exists($plan.pointer)) {
            if ($plan.status -notin @('backed-up','recovery-required') -or -not $msiPresent) { throw 'The active migration pointer is missing; MSI removal was not authorized.' }
            Write-GnJson $plan.pointer @{schema_version=1;product='git-next';install_root=$plan.install_root;install_scope=$plan.install_scope;original_root=$plan.original_root;transaction=$Transaction;transaction_id=$plan.transaction_id}
        }
        $plan.status = 'rolling-back'; Write-GnTakeoverJournal $Transaction $plan
        if ($msiPresent) {
            if ((Test-GnBusy $plan.install_root) -or (Test-GnBusy $plan.original_root)) { throw 'A Git/runtime process became active; rollback was deferred.' }
            Invoke-GnTakeoverMsiRemoval $recovery
        }
        if ((Test-GnTakeoverMsiInstalled $plan.rollback_product_code) -or [IO.File]::Exists((Join-Path $plan.install_root 'package.json'))) { throw 'The MSI still owns the installation; original bytes were not restored.' }
    }
    $restoreRegistry = if ($same) { @($original.snapshot.registry) } else { @($plan.registry) }
    $postRegistry = @{}
    foreach ($record in $plan.registry) { $postRegistry[$record.hive+'/'+$record.path] = $record }
    foreach ($record in $restoreRegistry) {
        if ($same) { Assert-GnTakeoverRegistryRecovery $record $postRegistry[$record.hive+'/'+$record.path] }
        else { Assert-GnRegistryCanRestore $record }
    }
    foreach ($record in $plan.bridges) {
        $path = Assert-GnOwnedPath $plan.original_root (Join-Path $plan.original_root $record.relative)
        if ([IO.File]::Exists($path)) {
            $hash = (Get-FileHash -LiteralPath $path -Algorithm SHA256).Hash.ToLowerInvariant()
            $bridgeOriginal = @($plan.files | Where-Object { $_.relative -ceq $record.relative })[0]
            if ($hash -cne $record.sha256 -and $hash -cne $bridgeOriginal.sha256) { throw 'A legacy entry changed after migration; rollback preserves it for review.' }
        }
    }
    if ($plan.system_config_sha256 -and [IO.File]::Exists($plan.system_config)) {
        if ((Get-FileHash -LiteralPath $plan.system_config -Algorithm SHA256).Hash.ToLowerInvariant() -cne $plan.system_config_sha256) { throw 'The migrated system configuration changed; rollback preserves it for review.' }
        [IO.File]::Delete($plan.system_config)
    }
    $backup = $recovery.backup
    if ($same) {
        $backup = Join-Path $original.root 'original'
        if ([IO.Directory]::Exists($plan.original_root)) {
            $rootAccess = (Get-Acl -LiteralPath $plan.original_root).Sddl
            if ($rootAccess -cne $plan.root_acl -and $rootAccess -cne $original.snapshot.root_acl) { throw 'The installation root access changed outside takeover; recovery preserved it.' }
            if ($rootAccess -cne $original.snapshot.root_acl) {
                $security = [Security.AccessControl.DirectorySecurity]::new(); $security.SetSecurityDescriptorSddlForm($original.snapshot.root_acl)
                Set-Acl -LiteralPath $plan.original_root -AclObject $security
            }
        } else { Restore-GnTakeoverRoot ([pscustomobject]@{original_root=$plan.original_root;root_acl=$original.snapshot.root_acl}) }
    } else { Restore-GnTakeoverRoot $plan }
    $restoreFiles = if ($same) { @($original.snapshot.files) } else { @($plan.files) }
    $postFiles = Get-GnMigrationFileIndex $plan.files
    $bridgeFiles = Get-GnMigrationFileIndex $plan.bridges
    foreach ($file in $restoreFiles) {
        $path = Assert-GnOwnedPath $plan.original_root (Join-Path $plan.original_root $file.relative)
        $replacement = if ($bridgeFiles.ContainsKey($file.relative)) { $bridgeFiles[$file.relative] } else { $null }
        if ([IO.File]::Exists($path)) {
            $hash = (Get-FileHash -LiteralPath $path -Algorithm SHA256).Hash.ToLowerInvariant()
            if ($hash -cne $file.sha256 -and (-not $replacement -or $replacement.sha256 -cne $hash)) { throw 'A retained installation file changed; rollback will not overwrite it.' }
            if ($same -and $original.snapshot.kind -ceq 'pre-msi-original-git') {
                $access = (Get-Acl -LiteralPath $path).GetSecurityDescriptorSddlForm([Security.AccessControl.AccessControlSections]::Access)
                $postFile = if ($postFiles.ContainsKey($file.relative)) { $postFiles[$file.relative] } else { $null }
                if ($access -cne $file.access -and (-not $postFile -or $access -cne $postFile.access)) { throw 'An original file access rule changed; recovery preserved it.' }
            }
        }
        $exactAccess = -not $same -or $original.snapshot.kind -ceq 'pre-msi-original-git'
        Copy-GnMigrationFile $backup $plan.original_root $file $(if ($replacement) { $replacement.sha256 } else { '' }) -RestoreAccess:$exactAccess
        if ($same -and $exactAccess) { Restore-GnFileAccess $path $file.access }
    }
    foreach ($record in $plan.bridges) { Set-GnBridgeRegistration $plan $record $true }
    foreach ($record in $restoreRegistry) { Restore-GnRegistrySnapshot $record }
    if ($same) {
        Set-GnOldUpdateTasks $original.snapshot.old_update_tasks $true
        Restore-GnTakeoverOriginalIntegration $original $plan
        $pathPlan = [pscustomobject]@{user_path=$original.snapshot.user_path;machine_path=$original.snapshot.machine_path;original_root=$plan.original_root;install_root=$plan.install_root;install_scope=$plan.install_scope;platform=$plan.platform}
        Restore-GnMigrationPath $pathPlan 'Machine'
        Restore-GnMigrationPath $pathPlan 'User'
    } else {
        Set-GnOldUpdateTasks $plan.old_update_tasks $true
        Copy-GnRelatedIntegration (Assert-GnOwnedPath $Transaction (Join-Path $Transaction 'integration')) $plan.related $true
        Restore-GnMigrationPath $plan 'Machine'
        Restore-GnMigrationPath $plan 'User'
        if ($plan.PSObject.Properties['runtime_configuration'] -and $plan.runtime_configuration) { Complete-GnRuntimeConfigTransaction $plan.install_root $package $plan.runtime_configuration }
    }
    $plan.status = 'rolled-back'; Write-GnTakeoverJournal $Transaction $plan
    if ([IO.File]::Exists($plan.pointer)) {
        [void](Get-GnTakeoverRecoveryContext $Transaction)
        [IO.File]::Delete((Assert-GnOwnedPath $recovery.config $plan.pointer))
    }
}

function Assert-GnTakeoverRemoval([string]$InstallRoot) {
    $package = Get-Content -LiteralPath (Join-Path $InstallRoot 'package.json') -Raw -Encoding UTF8 | ConvertFrom-Json
    $context = Get-GnScopeContext $InstallRoot $package
    $directory = if ($context.machine) { $context.config } else { Get-GnConfigDirectory }
    $pointer = Assert-GnOwnedPath $directory (Join-Path $directory 'migration.json')
    if (-not [IO.File]::Exists($pointer)) { return }
    Assert-GnScopedFile $pointer
    $state = Get-Content -LiteralPath $pointer -Raw -Encoding UTF8 | ConvertFrom-Json
    if ($state.schema_version -ne 1 -or $state.product -cne 'git-next') { throw 'The migration state is invalid; preserve the current installation for recovery.' }
    if ([IO.Path]::GetFullPath($state.install_root).TrimEnd('\','/') -ine [IO.Path]::GetFullPath($InstallRoot).TrimEnd('\','/')) { return }
    $recovery = Get-GnTakeoverRecoveryContext $state.transaction
    $plan = $recovery.plan
    if (-not $plan.PSObject.Properties['same_directory'] -or -not $plan.same_directory -or $plan.status -cne 'rolling-back') { throw 'Rollback the active Git takeover before uninstalling its MSI. The original Git, login data and caller paths will be restored from the protected transaction.' }
    $authorized = Assert-GnTakeoverRecoveryTools $recovery
    $packagePath = Assert-GnOwnedPath $InstallRoot (Join-Path $InstallRoot 'package.json')
    if ((Get-FileHash -LiteralPath $packagePath -Algorithm SHA256).Hash.ToLowerInvariant() -cne $plan.rollback_package_sha256 -or $package.product_code -cne $authorized.product_code) { throw 'The MSI removal is not bound to this protected rollback.' }
    [void](Get-GnTakeoverOriginalRecovery $recovery)
    if ($plan.PSObject.Properties['runtime_configuration'] -and $plan.runtime_configuration -and (-not $plan.PSObject.Properties['runtime_configuration_rolled_back'] -or -not $plan.runtime_configuration_rolled_back)) { throw 'The runtime configuration must be restored before controlled MSI removal.' }
    $guardWorker = Get-GnTakeoverGuardWorker $InstallRoot $authorized
    if (Test-GnTakeoverRemovalBusy $InstallRoot $authorized $guardWorker) { throw 'An owned Git/runtime process is active; controlled MSI removal was deferred.' }
}

function Get-GnTakeoverGuardWorker([string]$InstallRoot,$Package) {
    $pin = $Package.files.PSObject.Properties['updater/msi-worker.exe']
    if (-not $pin -or $pin.Value -cnotmatch '^[a-f0-9]{64}$') { return $null }
    $expected = Assert-GnOwnedPath $InstallRoot (Join-Path $InstallRoot 'updater/msi-worker.exe')
    if ((Get-FileHash -LiteralPath $expected -Algorithm SHA256).Hash.ToLowerInvariant() -cne $pin.Value) { throw 'The MSI guard worker differs from its current manifest.' }
    try {
        $self = Get-CimInstance -ClassName Win32_Process -Filter ('ProcessId='+$PID) -ErrorAction Stop
        $parent = Get-CimInstance -ClassName Win32_Process -Filter ('ProcessId='+[uint32]$self.ParentProcessId) -ErrorAction Stop
        $worker = Get-Process -Id $parent.ProcessId -ErrorAction Stop
        $command = '^(?:"'+[regex]::Escape($expected)+'"|'+[regex]::Escape($expected)+')\s+-Mode\s+GuardRemoval\s*$'
        if ($worker.MainModule.FileName -ine $expected -or $parent.ExecutablePath -ine $expected -or $parent.CommandLine -cnotmatch $command) { return $null }
        return [pscustomobject]@{id=$worker.Id;start_ticks=$worker.StartTime.ToUniversalTime().Ticks;path=$expected}
    } catch { return $null }
}

function Test-GnTakeoverRemovalBusy([string]$InstallRoot,$Package,$GuardWorker) {
    if (-not $GuardWorker) { return Test-GnBusy $InstallRoot }
    $prefix = [IO.Path]::GetFullPath($InstallRoot).TrimEnd('\')+'\'
    $names = @('git','git-next','git-terminal','git-lfs','git-next-lfs-filter')
    $names += @($Package.files.PSObject.Properties | Where-Object { $_.Name.EndsWith('.exe',[StringComparison]::OrdinalIgnoreCase) } | ForEach-Object { [IO.Path]::GetFileNameWithoutExtension($_.Name) })
    $patterns = @($names | Sort-Object -Unique | ForEach-Object { [WildcardPattern]::Escape($_) })
    foreach ($process in @(Get-Process -Name $patterns -ErrorAction SilentlyContinue)) {
        try {
            $path = $process.MainModule.FileName
            if ($process.Id -eq $GuardWorker.id -and $process.StartTime.ToUniversalTime().Ticks -eq $GuardWorker.start_ticks -and $path -ieq $GuardWorker.path) { continue }
        } catch { return $true }
        if (-not $path -or $path.StartsWith($prefix,[StringComparison]::OrdinalIgnoreCase)) { return $true }
    }
    return $false
}

Export-ModuleMember -Function Get-GnTakeoverPlan,Start-GnTakeover,Restore-GnTakeover,Assert-GnTakeoverRemoval,New-GnPreinstallSnapshot,Get-GnTakeoverRecoveryContext
