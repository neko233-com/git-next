# Copyright 2026 git-next contributors. MIT license.
# Shared by the updater and migration; ordinary Git commands do not load it.
function Assert-GnRuntimeConfigPath([string]$Relative) {
    if ($Relative -cnotmatch '^runtime/(?:etc|usr/etc|mingw64/etc|clangarm64/etc)/.+' -or $Relative -eq 'runtime/etc/gitconfig') { throw 'Invalid runtime configuration path.' }
    foreach ($part in @($Relative -split '/')) {
        if (-not $part -or $part -in @('.','..') -or $part.IndexOfAny([IO.Path]::GetInvalidFileNameChars()) -ge 0 -or $part -match '[. ]$' -or $part -match '^(?i:CON|PRN|AUX|NUL|COM[1-9]|LPT[1-9])(?:\.|$)') { throw 'Invalid runtime configuration path.' }
    }
}

function Get-GnRuntimeConfigHash([string]$Path) {
    if (-not [IO.File]::Exists($Path)) { return $null }
    Assert-GnScopedFile $Path
    return (Get-FileHash -LiteralPath $Path -Algorithm SHA256).Hash.ToLowerInvariant()
}

function Get-GnRuntimeConfigSecurity([string]$Access,[bool]$Machine) {
    $security = [Security.AccessControl.FileSecurity]::new()
    if ($Access) { $security.SetSecurityDescriptorSddlForm($Access,[Security.AccessControl.AccessControlSections]::Access) }
    elseif ($Machine) { $security.SetSecurityDescriptorSddlForm('D:P(A;;FA;;;SY)(A;;FA;;;BA)(A;;FR;;;BU)') }
    else { return $null }
    if ($Machine) {
        $security.SetAccessRuleProtection($true,$true)
        $security.SetOwner([Security.Principal.SecurityIdentifier]::new('S-1-5-32-544'))
        $unsafe = [int64]([Security.AccessControl.FileSystemRights]::WriteData -bor [Security.AccessControl.FileSystemRights]::AppendData -bor [Security.AccessControl.FileSystemRights]::WriteExtendedAttributes -bor [Security.AccessControl.FileSystemRights]::WriteAttributes -bor [Security.AccessControl.FileSystemRights]::Delete -bor [Security.AccessControl.FileSystemRights]::DeleteSubdirectoriesAndFiles -bor [Security.AccessControl.FileSystemRights]::ChangePermissions -bor [Security.AccessControl.FileSystemRights]::TakeOwnership)
        foreach ($rule in @($security.GetAccessRules($true,$true,[Security.Principal.SecurityIdentifier]))) {
            if ($rule.AccessControlType -ne [Security.AccessControl.AccessControlType]::Allow -or $rule.IdentityReference.Value -in @('S-1-5-18','S-1-5-32-544')) { continue }
            [void]$security.RemoveAccessRuleSpecific($rule)
            $remaining = [int64]$rule.FileSystemRights -band (-bnot $unsafe)
            if ($remaining) { $security.AddAccessRule([Security.AccessControl.FileSystemAccessRule]::new($rule.IdentityReference,[Security.AccessControl.FileSystemRights]$remaining,$rule.AccessControlType)) }
        }
        foreach ($sid in @('S-1-5-18','S-1-5-32-544')) { $security.AddAccessRule([Security.AccessControl.FileSystemAccessRule]::new([Security.Principal.SecurityIdentifier]::new($sid),[Security.AccessControl.FileSystemRights]::FullControl,[Security.AccessControl.AccessControlType]::Allow)) }
    }
    return $security.GetSecurityDescriptorSddlForm([Security.AccessControl.AccessControlSections]::All)
}

function Copy-GnRuntimeConfiguration([string]$Source,[string]$Target,[string]$Sha256,[int]$Attributes,[string]$Access,[bool]$Machine,$ExpectedTarget) {
    New-GnOutputParent $Target
    $temporary = $Target + '.' + [Guid]::NewGuid().ToString('N') + '.tmp'
    $inputStream = [IO.File]::Open($Source,[IO.FileMode]::Open,[IO.FileAccess]::Read,[IO.FileShare]::Read)
    $outputStream = $null
    try {
        $hash = [Security.Cryptography.SHA256]::Create()
        try { $actual = ([BitConverter]::ToString($hash.ComputeHash($inputStream))).Replace('-','').ToLowerInvariant() } finally { $hash.Dispose() }
        if ($actual -cne $Sha256) { throw 'Runtime configuration source changed.' }
        $inputStream.Position = 0
        $sddl = Get-GnRuntimeConfigSecurity $Access $Machine
        if ($Machine) {
            if (-not ('GitNext.ScopeNative' -as [type])) { Add-Type -Path (Join-Path $PSScriptRoot 'Scope.Native.cs') }
            $outputStream = [GitNext.ScopeNative]::CreateProtectedFile($temporary,$sddl)
        } else { $outputStream = Open-GnNewFile $temporary }
        $inputStream.CopyTo($outputStream); $outputStream.Flush($true); $outputStream.Dispose(); $outputStream = $null
        if (-not $Machine -and $sddl) { $security = [Security.AccessControl.FileSecurity]::new(); $security.SetSecurityDescriptorSddlForm($sddl); Set-Acl -LiteralPath $temporary -AclObject $security }
        if ((Get-GnRuntimeConfigHash $Target) -cne $ExpectedTarget) { throw 'Runtime configuration destination changed; it was preserved.' }
        if ([IO.File]::Exists($Target)) {
            if ($sddl) { $security = [Security.AccessControl.FileSecurity]::new(); $security.SetSecurityDescriptorSddlForm($sddl); Set-Acl -LiteralPath $Target -AclObject $security }
            [IO.File]::SetAttributes($Target,[IO.FileAttributes]::Normal); [IO.File]::Replace($temporary,$Target,[NullString]::Value)
        }
        else { [IO.File]::Move($temporary,$Target) }
        [IO.File]::SetAttributes($Target,[IO.FileAttributes]$Attributes)
        if ($Machine) { Assert-GnProtectedObject $Target }
    } finally {
        if ($outputStream) { $outputStream.Dispose() }
        $inputStream.Dispose()
        if ([IO.File]::Exists($temporary)) { [IO.File]::Delete($temporary) }
    }
}

function Get-GnRuntimeDefaults([string]$InstallRoot,$Package) {
    $path = Assert-GnOwnedPath $InstallRoot (Join-Path $InstallRoot 'runtime/defaults/manifest.json')
    $entry = $Package.files.PSObject.Properties['runtime/defaults/manifest.json']
    if (-not $entry -or (Get-GnRuntimeConfigHash $path) -cne $entry.Value -or ([IO.FileInfo]::new($path)).Length -gt 4MB) { throw 'Runtime default configuration manifest is invalid.' }
    $manifest = Get-Content -LiteralPath $path -Raw -Encoding UTF8 | ConvertFrom-Json
    if ($manifest.schema_version -ne 1 -or $manifest.product -cne 'git-next' -or $manifest.platform -cne $Package.platform -or $manifest.version -cne $Package.version -or @($manifest.files).Count -gt 10000) { throw 'Runtime default configuration manifest is invalid.' }
    $names = [Collections.Generic.HashSet[string]]::new([StringComparer]::OrdinalIgnoreCase)
    foreach ($file in @($manifest.files)) {
        Assert-GnRuntimeConfigPath $file.target
        if (-not $names.Add($file.target) -or $file.source -cne ('runtime/defaults/'+$file.target.Substring(8)) -or $file.sha256 -cnotmatch '^[a-f0-9]{64}$' -or $file.sha256 -cne $Package.files.PSObject.Properties[$file.source].Value) { throw 'Runtime default configuration manifest is invalid.' }
        [void](Assert-GnOwnedPath $InstallRoot (Join-Path $InstallRoot $file.source))
    }
    return $manifest
}

function Initialize-GnRuntimeConfiguration([string]$InstallRoot,$Package) {
    # Historical subset MSIs have no runtime tree.
    if (-not $Package.files.PSObject.Properties['runtime/defaults/manifest.json']) { return }
    $context = Get-GnScopeContext $InstallRoot $Package
    if ($context.machine) { Initialize-GnMachineDirectory $context.updates }
    else { [void][IO.Directory]::CreateDirectory($context.updates) }
    $manifest = Get-GnRuntimeDefaults $InstallRoot $Package
    $statePath = Assert-GnOwnedPath $context.config (Join-Path $context.config ('runtime-defaults-'+$Package.platform+'.json'))
    if ($context.machine) { Initialize-GnMachineDirectory $context.config }
    else { [void][IO.Directory]::CreateDirectory($context.config) }
    $previous = @{}
    if ([IO.File]::Exists($statePath)) {
        Assert-GnScopedFile $statePath
        $saved = Get-Content -LiteralPath $statePath -Raw -Encoding UTF8 | ConvertFrom-Json
        if ($saved.schema_version -ne 1 -or $saved.product -cne 'git-next' -or $saved.platform -cne $Package.platform -or $saved.install_scope -cne $context.scope -or [IO.Path]::GetFullPath($saved.install_root).TrimEnd('\') -ine [IO.Path]::GetFullPath($InstallRoot).TrimEnd('\')) { throw 'Runtime configuration state belongs to another installation.' }
        foreach ($property in $saved.files.PSObject.Properties) { Assert-GnRuntimeConfigPath $property.Name; if ($property.Value -cnotmatch '^[a-f0-9]{64}$') { throw 'Runtime configuration state is invalid.' }; $previous[$property.Name] = $property.Value }
    }
    $next = @{}; $changes = @()
    foreach ($file in @($manifest.files)) {
        $target = Assert-GnOwnedPath $InstallRoot (Join-Path $InstallRoot $file.target)
        $current = Get-GnRuntimeConfigHash $target
        if (-not $current -or ($previous.ContainsKey($file.target) -and $current -ceq $previous[$file.target])) {
            if ($current -cne $file.sha256) { $changes += [pscustomobject]@{target=$file.target;source=(Join-Path $InstallRoot $file.source);sha256=$file.sha256;attributes=$file.attributes;access='';expected=$current} }
        }
        $next[$file.target] = $file.sha256
    }
    foreach ($name in @($previous.Keys)) {
        if ($next.ContainsKey($name)) { continue }
        $target = Assert-GnOwnedPath $InstallRoot (Join-Path $InstallRoot $name)
        if ((Get-GnRuntimeConfigHash $target) -ceq $previous[$name]) { $changes += [pscustomobject]@{target=$name;source=$null;sha256=$null;attributes=0;access='';expected=$previous[$name]} }
    }
    $state = @{schema_version=1;product='git-next';platform=$Package.platform;install_scope=$context.scope;install_root=$InstallRoot;files=$next}
    $transaction = Join-Path $context.updates 'runtime-configuration-msi'
    Start-GnRuntimeConfigTransaction $InstallRoot $Package $transaction $changes $statePath $state
}

function New-GnRuntimeConfigPrivateDirectory([string]$Path,[bool]$Machine) {
    if ([IO.Directory]::Exists($Path) -or [IO.File]::Exists($Path)) { throw 'A configuration transaction already exists; preserve it for recovery.' }
    if ($Machine) { Initialize-GnMachineDirectory $Path -Private; return }
    [void][IO.Directory]::CreateDirectory($Path)
    $sid = [Security.Principal.WindowsIdentity]::GetCurrent().User.Value
    $security = [Security.AccessControl.DirectorySecurity]::new()
    $security.SetSecurityDescriptorSddlForm(('O:'+ $sid +'D:P(A;OICI;FA;;;'+$sid+')(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)'))
    Set-Acl -LiteralPath $Path -AclObject $security
}

function Start-GnRuntimeConfigTransaction([string]$InstallRoot,$Package,[string]$Transaction,$Changes,[string]$StatePath='',$State=$null) {
    $context = Get-GnScopeContext $InstallRoot $Package
    if (@($Changes).Count -gt 10000) { throw 'Runtime configuration transaction exceeds its file limit.' }
    $privateAccess = 'D:P(A;;FA;;;SY)(A;;FA;;;BA)'
    if (-not $context.machine) { $privateAccess += '(A;;FA;;;'+[Security.Principal.WindowsIdentity]::GetCurrent().User.Value+')' }
    $entries = @(); $snapshots = @(); $index = 0
    foreach ($change in @($Changes)) {
        Assert-GnRuntimeConfigPath $change.target
        $target = Assert-GnOwnedPath $InstallRoot (Join-Path $InstallRoot $change.target)
        $current = Get-GnRuntimeConfigHash $target
        if ($current -cne $change.expected) { throw 'Runtime configuration destination changed; it was preserved.' }
        $snapshot = $null; $attributes = 0; $access = ''
        if ($current) {
            $snapshot = ($index++).ToString() + '.before'
            $attributes = [int][IO.File]::GetAttributes($target)
            $access = (Get-Acl -LiteralPath $target).GetSecurityDescriptorSddlForm([Security.AccessControl.AccessControlSections]::Access)
            $snapshots += [pscustomobject]@{source=$target;target=(Join-Path $Transaction $snapshot);sha256=$current}
        }
        $entries += [pscustomobject]@{target=$change.target;before=$current;after=$change.sha256;snapshot=$snapshot;attributes=$attributes;access=$access}
    }
    $stateBefore = $null; $stateAfter = $null
    if ($StatePath) {
        $stateBefore = Get-GnRuntimeConfigHash $StatePath
        if ($stateBefore) { $snapshots += [pscustomobject]@{source=$StatePath;target=(Join-Path $Transaction 'state.before');sha256=$stateBefore} }
        $hash = [Security.Cryptography.SHA256]::Create()
        try { $stateAfter = ([BitConverter]::ToString($hash.ComputeHash([Text.UTF8Encoding]::new($false).GetBytes(($State | ConvertTo-Json -Depth 20 -Compress)+"`n")))).Replace('-','').ToLowerInvariant() } finally { $hash.Dispose() }
    }
    $journal = @{schema_version=1;product='git-next';install_root=$InstallRoot;install_scope=$context.scope;platform=$Package.platform;entries=$entries;state_path=$StatePath;state_before=$stateBefore;state_after=$stateAfter}
    New-GnRuntimeConfigPrivateDirectory $Transaction $context.machine
    Write-GnJson (Join-Path $Transaction 'journal.json') $journal
    try {
        foreach ($snapshot in $snapshots) { Copy-GnRuntimeConfiguration $snapshot.source $snapshot.target $snapshot.sha256 0 $privateAccess $context.machine $null }
        foreach ($change in @($Changes)) {
            $target = Assert-GnOwnedPath $InstallRoot (Join-Path $InstallRoot $change.target)
            if ($change.sha256) { Copy-GnRuntimeConfiguration $change.source $target $change.sha256 $change.attributes $change.access $context.machine $change.expected }
            else {
                if ((Get-GnRuntimeConfigHash $target) -cne $change.expected) { throw 'Runtime configuration destination changed; it was preserved.' }
                [IO.File]::SetAttributes($target,[IO.FileAttributes]::Normal); [IO.File]::Delete($target)
            }
        }
        if ($StatePath) { Write-GnJson $StatePath $State }
    } catch {
        try { Restore-GnRuntimeConfigTransaction $InstallRoot $Package $Transaction; Complete-GnRuntimeConfigTransaction $InstallRoot $Package $Transaction } catch { }
        throw
    }
}

function Get-GnRuntimeConfigJournal([string]$InstallRoot,$Package,[string]$Transaction) {
    $context = Get-GnScopeContext $InstallRoot $Package
    if ($context.machine) { [void](Assert-GnOwnedPath $context.root $Transaction) }
    else {
        $full = [IO.Path]::GetFullPath($Transaction)
        if (-not $full.StartsWith([IO.Path]::GetFullPath($context.root).TrimEnd('\')+'\',[StringComparison]::OrdinalIgnoreCase)) {
            $parent = [IO.Path]::GetDirectoryName($full)
            $migration = Get-Content -LiteralPath (Assert-GnOwnedPath $parent (Join-Path $parent 'transaction.json')) -Raw -Encoding UTF8 | ConvertFrom-Json
            if ($migration.schema_version -ne 1 -or $migration.product -cne 'git-next' -or $migration.user_sid -cne [Security.Principal.WindowsIdentity]::GetCurrent().User.Value -or $migration.transaction_id -cne [IO.Path]::GetFileName($parent) -or [IO.Path]::GetFileName($full) -cne 'runtime-configuration') { throw 'Runtime configuration transaction is invalid.' }
        }
    }
    $path = Assert-GnOwnedPath $Transaction (Join-Path $Transaction 'journal.json')
    if (-not [IO.File]::Exists($path)) { return $null }
    Assert-GnScopedFile $path
    if (([IO.FileInfo]::new($path)).Length -gt 16MB) { throw 'Runtime configuration transaction exceeds its size limit.' }
    $journal = Get-Content -LiteralPath $path -Raw -Encoding UTF8 | ConvertFrom-Json
    if ($journal.schema_version -ne 1 -or $journal.product -cne 'git-next' -or $journal.platform -cne $Package.platform -or $journal.install_scope -cne $context.scope -or [IO.Path]::GetFullPath($journal.install_root).TrimEnd('\') -ine [IO.Path]::GetFullPath($InstallRoot).TrimEnd('\') -or @($journal.entries).Count -gt 10000) { throw 'Runtime configuration transaction is invalid.' }
    $names = [Collections.Generic.HashSet[string]]::new([StringComparer]::OrdinalIgnoreCase)
    foreach ($entry in @($journal.entries)) {
        Assert-GnRuntimeConfigPath $entry.target
        if (-not $names.Add($entry.target) -or ($entry.before -and $entry.before -cnotmatch '^[a-f0-9]{64}$') -or ($entry.after -and $entry.after -cnotmatch '^[a-f0-9]{64}$') -or ($entry.snapshot -and $entry.snapshot -cnotmatch '^[0-9]+\.before$')) { throw 'Runtime configuration transaction is invalid.' }
    }
    if ($journal.state_path) {
        $expected = Join-Path $context.config ('runtime-defaults-'+$Package.platform+'.json')
        if ([IO.Path]::GetFullPath($journal.state_path) -ine [IO.Path]::GetFullPath($expected)) { throw 'Runtime configuration state path is invalid.' }
    }
    return $journal
}

function Restore-GnRuntimeConfigTransaction([string]$InstallRoot,$Package,[string]$Transaction) {
    $journal = Get-GnRuntimeConfigJournal $InstallRoot $Package $Transaction
    if (-not $journal) { return }
    $machine = (Get-GnInstallScope $Package) -eq 'machine'
    foreach ($entry in @($journal.entries)) {
        $target = Assert-GnOwnedPath $InstallRoot (Join-Path $InstallRoot $entry.target)
        if ((Get-GnRuntimeConfigHash $target) -notin @($entry.before,$entry.after)) { throw 'Runtime configuration changed after the transaction; recovery preserves it.' }
    }
    if ($journal.state_path -and (Get-GnRuntimeConfigHash $journal.state_path) -notin @($journal.state_before,$journal.state_after)) { throw 'Runtime configuration state changed; recovery preserves it.' }
    foreach ($entry in @($journal.entries)) {
        $target = Assert-GnOwnedPath $InstallRoot (Join-Path $InstallRoot $entry.target)
        $current = Get-GnRuntimeConfigHash $target
        if ($entry.before -and $current -cne $entry.before) { Copy-GnRuntimeConfiguration (Join-Path $Transaction $entry.snapshot) $target $entry.before $entry.attributes $entry.access $machine $current }
        elseif ($entry.before) {
            $security = [Security.AccessControl.FileSecurity]::new(); $security.SetSecurityDescriptorSddlForm((Get-GnRuntimeConfigSecurity $entry.access $machine)); Set-Acl -LiteralPath $target -AclObject $security
            [IO.File]::SetAttributes($target,[IO.FileAttributes]$entry.attributes)
        }
        elseif ($current) { [IO.File]::SetAttributes($target,[IO.FileAttributes]::Normal); [IO.File]::Delete($target) }
    }
    if ($journal.state_path) {
        $current = Get-GnRuntimeConfigHash $journal.state_path
        if ($journal.state_before -and $current -cne $journal.state_before) { Copy-GnRuntimeConfiguration (Join-Path $Transaction 'state.before') $journal.state_path $journal.state_before 0 '' $machine $current }
        elseif (-not $journal.state_before -and $current) { [IO.File]::Delete($journal.state_path) }
    }
}

function Complete-GnRuntimeConfigTransaction([string]$InstallRoot,$Package,[string]$Transaction) {
    $journal = Get-GnRuntimeConfigJournal $InstallRoot $Package $Transaction
    if (-not $journal) { return }
    foreach ($entry in @($journal.entries)) {
        if ($entry.snapshot) { [IO.File]::Delete((Assert-GnOwnedPath $Transaction (Join-Path $Transaction $entry.snapshot))) }
    }
    if ($journal.state_before) { [IO.File]::Delete((Join-Path $Transaction 'state.before')) }
    [IO.File]::Delete((Join-Path $Transaction 'journal.json'))
    [IO.Directory]::Delete([IO.Path]::GetFullPath($Transaction),$false)
}
