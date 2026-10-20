# Copyright 2026 git-next contributors. MIT license.
Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

function Get-GnTree([string]$Root) {
    $Root = [IO.Path]::GetFullPath($Root).TrimEnd('\','/')
    [void](Assert-GnOwnedPath $Root (Join-Path $Root '.ownership-check'))
    $pending = [Collections.Generic.Stack[IO.DirectoryInfo]]::new()
    $pending.Push([IO.DirectoryInfo]::new($Root))
    $result = [Collections.Generic.List[object]]::new()
    [long]$bytes = 0
    while ($pending.Count) {
        foreach ($item in $pending.Pop().GetFileSystemInfos()) {
            if ($item.Attributes -band [IO.FileAttributes]::ReparsePoint) { throw 'Migration refuses reparse points in the selected installation.' }
            if ($item.Attributes -band [IO.FileAttributes]::Directory) { $pending.Push($item); continue }
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
    $service = New-Object -ComObject Schedule.Service; [void]$service.Connect(); $folder = $service.GetFolder('\')
    foreach ($record in $Tasks) {
        if ($Restore) { [void]$folder.RegisterTask($record.name,$record.xml,6,$record.sid,$null,3,$null) }
        else { [void]$folder.DeleteTask($record.name,0) }
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
        if ($file.Extension -ieq '.lnk') {
            $target = [Environment]::ExpandEnvironmentVariables($shell.CreateShortcut($file.FullName).TargetPath)
            if (-not $target.StartsWith($OriginalRoot+'\',[StringComparison]::OrdinalIgnoreCase)) { continue }
        } else {
            $text = [IO.File]::ReadAllText($file.FullName).Replace('\\','\').Replace('/','\')
            if ($text.IndexOf($OriginalRoot,[StringComparison]::OrdinalIgnoreCase) -lt 0) { continue }
        }
        $relative = ([BitConverter]::ToString([Security.Cryptography.SHA256]::Create().ComputeHash([Text.Encoding]::UTF8.GetBytes($file.FullName)))).Replace('-','').ToLowerInvariant()
        $access = (Get-Acl -LiteralPath $file.FullName).GetSecurityDescriptorSddlForm([Security.AccessControl.AccessControlSections]::Access)
        $result += [pscustomobject]@{source=$file.FullName;relative=$relative;sha256=(Get-FileHash -LiteralPath $file.FullName -Algorithm SHA256).Hash.ToLowerInvariant();attributes=[int]$file.Attributes;access=$access}
        if ($result.Count -gt 128) { throw 'Unexpected number of legacy integration files.' }
    }
    return $result
}

function Restore-GnFileAccess([string]$Path,[string]$Access) {
    $security = [Security.AccessControl.FileSecurity]::new()
    $security.SetSecurityDescriptorSddlForm($Access,[Security.AccessControl.AccessControlSections]::Access)
    Set-Acl -LiteralPath $Path -AclObject $security
}

function Copy-GnRelatedIntegration([string]$Backup,$Records,[bool]$Restore) {
    foreach ($record in $Records) {
        $stored = Assert-GnOwnedPath $Backup (Join-Path $Backup $record.relative)
        $source = if ($Restore) { $stored } else { $record.source }
        $target = if ($Restore) { $record.source } else { $stored }
        [void](Assert-GnOwnedPath ([IO.Path]::GetDirectoryName($record.source)) $record.source)
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

function Get-GnTakeoverPlan([string]$InstallRoot,[string]$OriginalGitRoot) {
    $InstallRoot = [IO.Path]::GetFullPath($InstallRoot).TrimEnd('\','/')
    $OriginalGitRoot = [IO.Path]::GetFullPath($OriginalGitRoot).TrimEnd('\','/')
    if ($InstallRoot.StartsWith($OriginalGitRoot+'\',[StringComparison]::OrdinalIgnoreCase) -or $OriginalGitRoot.StartsWith($InstallRoot+'\',[StringComparison]::OrdinalIgnoreCase) -or $InstallRoot -ieq $OriginalGitRoot) { throw 'The new and old installations must be separate.' }
    $packagePath = Assert-GnOwnedPath $InstallRoot (Join-Path $InstallRoot 'package.json')
    $package = Get-Content -LiteralPath $packagePath -Raw -Encoding UTF8 | ConvertFrom-Json
    Assert-GnInstalledFiles $InstallRoot $package
    if ($package.platform -notin @('windows-x64','windows-arm64') -or $package.engine -cne 'source-built-git-next-native-core') { throw 'The complete native MSI installation is required.' }
    foreach ($name in @('migration/git-next-bridge.exe','migration/git-next-bridge-gui.exe')) {
        if (-not $package.files.PSObject.Properties[$name]) { throw 'The MSI lacks its owned migration bridge.' }
    }
    $uninstaller = Assert-GnOwnedPath $OriginalGitRoot (Join-Path $OriginalGitRoot 'unins000.exe')
    if (-not [IO.File]::Exists((Join-Path $OriginalGitRoot 'cmd/git.exe')) -or -not [IO.File]::Exists($uninstaller)) { throw 'The selected old installation is not the supported Git for Windows installer layout.' }
    $hive = $null
    foreach ($candidate in @('CurrentUser','LocalMachine')) {
        $base = [Microsoft.Win32.RegistryKey]::OpenBaseKey([Microsoft.Win32.RegistryHive]::$candidate,[Microsoft.Win32.RegistryView]::Registry64)
        try {
            $key = $base.OpenSubKey('Software\Microsoft\Windows\CurrentVersion\Uninstall\Git_is1')
            if ($key) {
                try { $location = $key.GetValue('InstallLocation') } finally { $key.Dispose() }
                if ($location -and [IO.Path]::GetFullPath($location).TrimEnd('\','/') -ieq $OriginalGitRoot) { $hive = $candidate; break }
            }
        } finally { $base.Dispose() }
    }
    if (-not $hive) { throw 'The old uninstaller registration does not match the selected directory.' }
    $files = @(Get-GnTree $OriginalGitRoot)
    $nativePrefix = if ($package.platform -eq 'windows-arm64') { 'clangarm64' } else { 'mingw64' }
    $coreHashes = @($files | Where-Object { $_.relative -in @('cmd/git.exe','bin/git.exe',($nativePrefix+'/bin/git.exe'),($nativePrefix+'/libexec/git-core/git.exe')) } | ForEach-Object sha256)
    $builtins = if ($package.PSObject.Properties['builtins']) { @($package.builtins) } else { @() }
    $bridges = @(); $retained = @(); $unmapped = @()
    foreach ($file in $files) {
        if ($file.relative -match '(^|/)unins[0-9]+\.' -or $file.relative -match 'git-update-git-for-windows|git-for-windows-updater' -or $file.relative -in @('mingw64/bin/wintoast.exe','clangarm64/bin/wintoast.exe')) { continue }
        if (-not $file.pe) { $retained += $file; continue }
        if (-not $file.relative.EndsWith('.exe',[StringComparison]::OrdinalIgnoreCase)) { continue }
        $target = 'runtime/' + $file.relative
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
    $scopeContext = Get-GnScopeContext $InstallRoot $package
    $configDirectory = if ($scopeContext.machine) { $scopeContext.config } else { Get-GnConfigDirectory }
    $installScope = $scopeContext.scope
    $systemConfig = Assert-GnOwnedPath $configDirectory (Join-Path $configDirectory 'system.gitconfig')
    $pointer = Assert-GnOwnedPath $configDirectory (Join-Path $configDirectory 'migration.json')
    if ([IO.File]::Exists($pointer)) { throw 'A prior migration transaction exists; resume or roll it back first.' }
    if ([IO.File]::Exists($systemConfig)) { throw 'A prior system-config migration exists; resume or roll it back first.' }
    return [pscustomobject]@{schema_version=1;product='git-next';status='planned';install_root=$InstallRoot;install_scope=$installScope;original_root=$OriginalGitRoot;hive=$hive;platform=$package.platform;version=$package.version;package_sha256=(Get-FileHash -LiteralPath $packagePath -Algorithm SHA256).Hash.ToLowerInvariant();release_eligible=$package.release_eligible;complete_git_parity=$package.complete_git_parity;uninstaller_sha256=(Get-FileHash -LiteralPath $uninstaller -Algorithm SHA256).Hash.ToLowerInvariant();files=$files;bridges=$bridges;retained=$retained;unmapped=$unmapped;system_config=$systemConfig;pointer=$pointer;registry=@();related=@(Get-GnRelatedIntegration $OriginalGitRoot);old_update_tasks=@();user_path=$null;machine_path=$null;root_acl=$null;system_config_sha256=$null}
}

function Copy-GnMigrationFile([string]$SourceRoot,[string]$TargetRoot,$Record,[string]$AllowedTargetSha256 = '',[switch]$RestoreAccess) {
    $source = Assert-GnOwnedPath $SourceRoot (Join-Path $SourceRoot $Record.relative)
    $target = Assert-GnOwnedPath $TargetRoot (Join-Path $TargetRoot $Record.relative)
    if ((Get-FileHash -LiteralPath $source -Algorithm SHA256).Hash.ToLowerInvariant() -cne $Record.sha256) { throw 'Migration source bytes changed; no replacement was made.' }
    if ([IO.File]::Exists($target)) {
        Assert-GnScopedFile $target
        $current = (Get-FileHash -LiteralPath $target -Algorithm SHA256).Hash.ToLowerInvariant()
        if ($current -ceq $Record.sha256) { return }
        if (-not $AllowedTargetSha256 -or $current -cne $AllowedTargetSha256) { throw 'A migration destination changed; it was preserved without replacement.' }
        [IO.File]::SetAttributes($target,[IO.FileAttributes]::Normal)
    }
    New-GnOutputParent $target
    if ([IO.File]::Exists($target)) { [IO.File]::Delete($target) }
    $inputStream = [IO.File]::OpenRead($source)
    $outputStream = $null
    try { $outputStream = Open-GnNewFile $target; $inputStream.CopyTo($outputStream); $outputStream.Flush($true) }
    finally { if ($outputStream) { $outputStream.Dispose() }; $inputStream.Dispose() }
    [IO.File]::SetAttributes($target,[IO.FileAttributes]$Record.attributes)
    if ($RestoreAccess) { Restore-GnFileAccess $target $Record.access }
    if ((Get-FileHash -LiteralPath $target -Algorithm SHA256).Hash.ToLowerInvariant() -cne $Record.sha256) { throw 'Migration file copy verification failed.' }
}

function Set-GnBridgeRegistration($Plan,$Record,[bool]$Remove) {
    $path = Assert-GnOwnedPath $Plan.original_root (Join-Path $Plan.original_root $Record.relative)
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
    if ((Test-GnBusy $Plan.install_root) -or (Test-GnBusy $Plan.original_root)) { throw 'An owned Git/runtime process is active; migration was not started.' }
    $packagePath = Join-Path $Plan.install_root 'package.json'
    if ((Get-FileHash -LiteralPath $packagePath -Algorithm SHA256).Hash.ToLowerInvariant() -cne $Plan.package_sha256) { throw 'The selected candidate changed after planning.' }
    Assert-GnInstalledFiles $Plan.install_root (Get-Content -LiteralPath $packagePath -Raw -Encoding UTF8 | ConvertFrom-Json)
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
        $uninstaller = Assert-GnOwnedPath $Plan.original_root (Join-Path $Plan.original_root 'unins000.exe')
        if ((Get-FileHash -LiteralPath $uninstaller -Algorithm SHA256).Hash.ToLowerInvariant() -cne $Plan.uninstaller_sha256) { throw 'The old uninstaller changed after planning.' }
        $Plan.status = 'removing-original'; Write-GnTakeoverJournal $transaction $Plan
        $process = Start-Process -FilePath $uninstaller -ArgumentList '/VERYSILENT /SUPPRESSMSGBOXES /NORESTART' -WindowStyle Hidden -PassThru
        $Plan.uninstaller_pid = $process.Id
        $Plan.uninstaller_start_identity = 'ticks:' + $process.StartTime.ToUniversalTime().Ticks.ToString([Globalization.CultureInfo]::InvariantCulture)
        Write-GnTakeoverJournal $transaction $Plan
        if (-not $process.WaitForExit(300000)) { throw 'The original uninstaller is still running; retain this transaction for recovery.' }
        if ($process.ExitCode -ne 0) { throw 'The original uninstaller failed; the protected backup is retained.' }
        [void][IO.Directory]::CreateDirectory($Plan.original_root)
        $acl = [Security.AccessControl.DirectorySecurity]::new(); $acl.SetSecurityDescriptorSddlForm($Plan.root_acl); Set-Acl -LiteralPath $Plan.original_root -AclObject $acl
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
        foreach ($record in $Plan.registry) {
            if ($record.path -ceq 'Software\Microsoft\Windows\CurrentVersion\Uninstall\Git_is1') { continue }
            Assert-GnRegistryCanRestore $record
            Restore-GnRegistrySnapshot $record
        }
        $base = [Microsoft.Win32.RegistryKey]::OpenBaseKey([Microsoft.Win32.RegistryHive]::$($Plan.hive),[Microsoft.Win32.RegistryView]::Registry64)
        try {
            $key = $base.OpenSubKey('Software\GitForWindows',$true)
            if ($key) { try { $key.SetValue('CurrentVersion','2.54.0.git-next') } finally { $key.Dispose() } }
        } finally { $base.Dispose() }
        Copy-GnRelatedIntegration $relatedBackup $Plan.related $true
        $configRoot = [IO.Path]::GetDirectoryName($Plan.system_config)
        [void][IO.Directory]::CreateDirectory($configRoot)
        [void](Assert-GnOwnedPath $configRoot $Plan.system_config)
        if ([IO.File]::Exists($Plan.system_config)) { throw 'Another writer created the migration configuration.' }
        $escape = { param($value) $value.Replace('\','/').Replace('"','\"') }
        $baseConfig = & $escape (Join-Path $Plan.install_root 'runtime/etc/gitconfig')
        $legacyConfig = & $escape (Join-Path $Plan.original_root 'etc/gitconfig')
        $text = "# git-next migration $transactionId`n[include]`n"
        # Preserve ordered multivars such as credential.helper without prepending new helpers.
        $selectedConfig = if ([IO.File]::Exists((Join-Path $Plan.original_root 'etc/gitconfig'))) { $legacyConfig } else { $baseConfig }
        $text += "`tpath = `"$selectedConfig`"`n"
        $text += "[core]`n`tautocrlf = input`n`teol = lf`n"
        $stream = Open-GnNewFile $Plan.system_config
        try { $data = [Text.UTF8Encoding]::new($false).GetBytes($text); $stream.Write($data,0,$data.Length); $stream.Flush($true) } finally { $stream.Dispose() }
        $Plan.system_config_sha256 = (Get-FileHash -LiteralPath $Plan.system_config -Algorithm SHA256).Hash.ToLowerInvariant()
        if ($machine) { Set-GnMigratedPath $Plan 'Machine' $Plan.machine_path }
        Set-GnMigratedPath $Plan 'User' $Plan.user_path
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

function Restore-GnTakeover([string]$Transaction) {
    $Transaction = [IO.Path]::GetFullPath($Transaction).TrimEnd('\','/')
    Assert-GnScopedFile (Join-Path $Transaction 'transaction.json')
    $plan = Get-Content -LiteralPath (Assert-GnOwnedPath $Transaction (Join-Path $Transaction 'transaction.json')) -Raw -Encoding UTF8 | ConvertFrom-Json
    if ($plan.schema_version -ne 1 -or $plan.product -cne 'git-next' -or $plan.transaction_id -cne [IO.Path]::GetFileName($Transaction) -or $plan.user_sid -cne [Security.Principal.WindowsIdentity]::GetCurrent().User.Value) { throw 'The transaction does not belong to this user and directory.' }
    [void](Assert-GnOwnedPath $plan.state_root $Transaction)
    if ($plan.status -eq 'rolled-back') { return }
    if ([IO.File]::Exists($plan.pointer)) {
        $pointer = Get-Content -LiteralPath $plan.pointer -Raw -Encoding UTF8 | ConvertFrom-Json
        if ($pointer.transaction_id -cne $plan.transaction_id) { throw 'A different migration owns the active handoff; rollback was deferred.' }
    }
    if ($plan.uninstaller_pid) {
        $pending = Get-Process -Id $plan.uninstaller_pid -ErrorAction SilentlyContinue
        if ($pending -and ('ticks:' + $pending.StartTime.ToUniversalTime().Ticks.ToString([Globalization.CultureInfo]::InvariantCulture)) -ceq $plan.uninstaller_start_identity) { throw 'The original uninstaller is still running; rollback was deferred.' }
    }
    if ((Test-GnBusy $plan.install_root) -or (Test-GnBusy $plan.original_root)) { throw 'A Git/runtime process is active; rollback was deferred.' }
    $backup = Assert-GnOwnedPath $Transaction (Join-Path $Transaction 'original')
    if ($plan.PSObject.Properties['runtime_configuration'] -and $plan.runtime_configuration) {
        $package = Get-Content -LiteralPath (Join-Path $plan.install_root 'package.json') -Raw -Encoding UTF8 | ConvertFrom-Json
        Restore-GnRuntimeConfigTransaction $plan.install_root $package $plan.runtime_configuration
    }
    foreach ($record in $plan.registry) { Assert-GnRegistryCanRestore $record }
    foreach ($record in $plan.bridges) {
        $path = Assert-GnOwnedPath $plan.original_root (Join-Path $plan.original_root $record.relative)
        if ([IO.File]::Exists($path)) {
            $hash = (Get-FileHash -LiteralPath $path -Algorithm SHA256).Hash.ToLowerInvariant()
            $original = @($plan.files | Where-Object { $_.relative -ceq $record.relative })[0]
            if ($hash -cne $record.sha256 -and $hash -cne $original.sha256) { throw 'A legacy entry changed after migration; rollback preserves it for review.' }
        }
    }
    if ($plan.system_config_sha256 -and [IO.File]::Exists($plan.system_config)) {
        if ((Get-FileHash -LiteralPath $plan.system_config -Algorithm SHA256).Hash.ToLowerInvariant() -cne $plan.system_config_sha256) { throw 'The migrated system configuration changed; rollback preserves it for review.' }
        [IO.File]::Delete($plan.system_config)
    }
    [void][IO.Directory]::CreateDirectory($plan.original_root)
    foreach ($file in $plan.files) {
        $path = Assert-GnOwnedPath $plan.original_root (Join-Path $plan.original_root $file.relative)
        if ([IO.File]::Exists($path)) {
            $hash = (Get-FileHash -LiteralPath $path -Algorithm SHA256).Hash.ToLowerInvariant()
            $bridged = @($plan.bridges | Where-Object { $_.relative -ceq $file.relative -and $_.sha256 -ceq $hash })
            if ($hash -cne $file.sha256 -and -not $bridged.Count) { throw 'A retained installation file changed; rollback will not overwrite it.' }
        }
        $replacement = @($plan.bridges | Where-Object { $_.relative -ceq $file.relative })
        Copy-GnMigrationFile $backup $plan.original_root $file $(if ($replacement.Count) { $replacement[0].sha256 } else { '' }) -RestoreAccess
    }
    foreach ($record in $plan.bridges) { Set-GnBridgeRegistration $plan $record $true }
    foreach ($record in $plan.registry) { Restore-GnRegistrySnapshot $record }
    Set-GnOldUpdateTasks $plan.old_update_tasks $true
    Copy-GnRelatedIntegration (Assert-GnOwnedPath $Transaction (Join-Path $Transaction 'integration')) $plan.related $true
    Restore-GnMigrationPath $plan 'Machine'
    Restore-GnMigrationPath $plan 'User'
    if ([IO.File]::Exists($plan.pointer)) { [IO.File]::Delete($plan.pointer) }
    if ($plan.PSObject.Properties['runtime_configuration'] -and $plan.runtime_configuration) { Complete-GnRuntimeConfigTransaction $plan.install_root $package $plan.runtime_configuration }
    $plan.status = 'rolled-back'; Write-GnTakeoverJournal $Transaction $plan
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
    if ([IO.Path]::GetFullPath($state.install_root).TrimEnd('\','/') -ieq [IO.Path]::GetFullPath($InstallRoot).TrimEnd('\','/')) { throw 'Rollback the active Git takeover before uninstalling its MSI. The original Git, login data and caller paths will be restored from the protected transaction.' }
}

Export-ModuleMember -Function Get-GnTakeoverPlan,Start-GnTakeover,Restore-GnTakeover,Assert-GnTakeoverRemoval
