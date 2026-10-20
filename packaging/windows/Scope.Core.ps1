# Copyright 2026 git-next contributors. MIT license.
# Dot-sourced by Update.Core so installation and migration share the same scope.
function Get-GnInstallScope($Package) {
    $scope = if ($Package.PSObject.Properties['install_scope']) { $Package.install_scope } else { 'user' }
    if ($scope -notin @('user','machine')) { throw 'The MSI installation scope is invalid.' }
    return $scope
}

function Test-GnAdministrator {
    return ([Security.Principal.WindowsPrincipal]::new([Security.Principal.WindowsIdentity]::GetCurrent())).IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
}

function Get-GnMachineDataRoot { return Join-Path ([Environment]::GetFolderPath('CommonApplicationData')) 'git-next' }

function Get-GnNativeProgramFiles {
    $base = [Microsoft.Win32.RegistryKey]::OpenBaseKey([Microsoft.Win32.RegistryHive]::LocalMachine,[Microsoft.Win32.RegistryView]::Registry64)
    try {
        $key = $base.OpenSubKey('SOFTWARE\Microsoft\Windows\CurrentVersion')
        if (-not $key) { throw 'The native Program Files registry entry is unavailable.' }
        try { $path = $key.GetValue('ProgramFilesDir',$null,[Microsoft.Win32.RegistryValueOptions]::DoNotExpandEnvironmentNames) } finally { $key.Dispose() }
    } finally { $base.Dispose() }
    if (-not $path -or -not [IO.Path]::IsPathRooted($path)) { throw 'The native Program Files path is invalid.' }
    return [IO.Path]::GetFullPath($path)
}

function Assert-GnProtectedObject([string]$Path) {
    if (-not (Test-Path -LiteralPath $Path)) { return }
    $item = Get-Item -LiteralPath $Path -Force
    if ($item.Attributes -band [IO.FileAttributes]::ReparsePoint) { throw 'A machine installation/state path is a reparse point.' }
    $acl = Get-Acl -LiteralPath $Path
    $trusted = @('S-1-5-18','S-1-5-32-544')
    if ($acl.GetOwner([Security.Principal.SecurityIdentifier]).Value -notin $trusted) { throw 'Machine files must be owned by SYSTEM or Administrators.' }
    $write = [Security.AccessControl.FileSystemRights]::WriteData -bor [Security.AccessControl.FileSystemRights]::AppendData -bor [Security.AccessControl.FileSystemRights]::WriteExtendedAttributes -bor [Security.AccessControl.FileSystemRights]::WriteAttributes -bor [Security.AccessControl.FileSystemRights]::Delete -bor [Security.AccessControl.FileSystemRights]::ChangePermissions -bor [Security.AccessControl.FileSystemRights]::TakeOwnership
    foreach ($rule in $acl.GetAccessRules($true,$true,[Security.Principal.SecurityIdentifier])) {
        if ($rule.PropagationFlags -band [Security.AccessControl.PropagationFlags]::InheritOnly) { continue }
        if ($rule.AccessControlType -eq [Security.AccessControl.AccessControlType]::Allow -and ($rule.FileSystemRights -band $write) -and $rule.IdentityReference.Value -notin $trusted) { throw 'A machine installation/state path permits writes by a non-administrator.' }
    }
}

function Initialize-GnMachineDirectory([string]$Path,[switch]$Private) {
    $root = [IO.Path]::GetFullPath((Get-GnMachineDataRoot)).TrimEnd('\')
    $full = [IO.Path]::GetFullPath($Path).TrimEnd('\')
    if ($full -ine $root -and -not $full.StartsWith($root+'\',[StringComparison]::OrdinalIgnoreCase)) { throw 'Machine state must stay in the protected git-next data root.' }
    Assert-GnProtectedObject $root
    if (-not [IO.Directory]::Exists($root)) { throw 'The machine MSI has not provisioned its protected state root.' }
    $pending = [Collections.Generic.Stack[string]]::new()
    $cursor = $full
    while (-not [IO.Directory]::Exists($cursor)) { $pending.Push($cursor); $cursor = [IO.Path]::GetDirectoryName($cursor) }
    Assert-GnProtectedObject $cursor
    while ($pending.Count) {
        $directory = $pending.Pop()
        if (-not ('GitNext.ScopeNative' -as [type])) { Add-Type -Path (Join-Path $PSScriptRoot 'Scope.Native.cs') }
        $sddl = 'O:BAG:BAD:P(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)'
        if (-not $Private) { $sddl += '(A;OICI;0x1200a9;;;BU)' }
        [GitNext.ScopeNative]::CreateProtectedDirectory($directory,$sddl)
        Assert-GnProtectedObject $directory
    }
    Assert-GnProtectedObject $full
    [void](Assert-GnOwnedPath $root (Join-Path $full '.scope-check'))
}

function Assert-GnMachineInstall([string]$InstallRoot,$Package) {
    $programFiles = Get-GnNativeProgramFiles
    $root = Assert-GnOwnedPath $programFiles $InstallRoot
    Assert-GnProtectedObject $root
    $base = [Microsoft.Win32.RegistryKey]::OpenBaseKey([Microsoft.Win32.RegistryHive]::LocalMachine,[Microsoft.Win32.RegistryView]::Registry64)
    try {
        $key = $base.OpenSubKey('Software\git-next\MSI\'+$Package.platform)
        if (-not $key) { throw 'The machine MSI registration is missing.' }
        try { $registered = $key.GetValue('InstallRoot') } finally { $key.Dispose() }
        if (-not $registered -or [IO.Path]::GetFullPath($registered).TrimEnd('\') -ine $root.TrimEnd('\')) { throw 'The protected machine MSI registration does not match its runtime.' }
    } finally { $base.Dispose() }
}

function Get-GnScopeContext([string]$InstallRoot,$Package) {
    $scope = Get-GnInstallScope $Package
    $machine = $scope -eq 'machine'
    if ($machine) { Assert-GnMachineInstall $InstallRoot $Package }
    $root = if ($machine) { Get-GnMachineDataRoot } else { Join-Path ([Environment]::GetFolderPath('LocalApplicationData')) 'git-next' }
    return [pscustomobject]@{scope=$scope;machine=$machine;root=$root;updates=(Join-Path $root ('updates/'+$Package.platform));migrations=(Join-Path $root ('migrations/'+$Package.platform));config=(Join-Path $root 'config');components=(Join-Path $root 'components');task_name=$(if($machine){'git-next-update-'+$Package.platform+'-machine'}else{'git-next-update-'+$Package.platform+'-'+[Security.Principal.WindowsIdentity]::GetCurrent().User.Value})}
}

function Assert-GnScopedFile([string]$Path) {
    $root = [IO.Path]::GetFullPath((Get-GnMachineDataRoot)).TrimEnd('\')+'\'
    if ([IO.Path]::GetFullPath($Path).StartsWith($root,[StringComparison]::OrdinalIgnoreCase)) { Assert-GnProtectedObject $Path }
}

function Open-GnNewFile([string]$Path) {
    $root = [IO.Path]::GetFullPath((Get-GnMachineDataRoot)).TrimEnd('\')+'\'
    $full = [IO.Path]::GetFullPath($Path)
    if ($full.StartsWith($root,[StringComparison]::OrdinalIgnoreCase)) {
        Assert-GnProtectedObject ([IO.Path]::GetDirectoryName($full))
        if (-not ('GitNext.ScopeNative' -as [type])) { Add-Type -Path (Join-Path $PSScriptRoot 'Scope.Native.cs') }
        $sddl = 'O:BAG:BAD:P(A;;FA;;;SY)(A;;FA;;;BA)'
        if (-not $full.StartsWith($root+'migrations\',[StringComparison]::OrdinalIgnoreCase)) { $sddl += '(A;;FR;;;BU)' }
        return [GitNext.ScopeNative]::CreateProtectedFile($full,$sddl)
    }
    return [IO.File]::Open($Path,[IO.FileMode]::CreateNew,[IO.FileAccess]::Write,[IO.FileShare]::None)
}

function Open-GnExclusiveLock([string]$Path) {
    Assert-GnScopedFile $Path
    if ([IO.File]::Exists($Path)) { return [IO.File]::Open($Path,[IO.FileMode]::Open,[IO.FileAccess]::ReadWrite,[IO.FileShare]::None) }
    return Open-GnNewFile $Path
}

function New-GnOutputParent([string]$Path) {
    $root = [IO.Path]::GetFullPath((Get-GnMachineDataRoot)).TrimEnd('\')+'\'
    $parent = [IO.Path]::GetDirectoryName([IO.Path]::GetFullPath($Path))
    if (($parent+'\').StartsWith($root,[StringComparison]::OrdinalIgnoreCase)) {
        Initialize-GnMachineDirectory $parent -Private:($parent.StartsWith($root+'migrations\',[StringComparison]::OrdinalIgnoreCase))
    } else { [void][IO.Directory]::CreateDirectory($parent) }
}

function Set-GnMachinePublicObject([string]$Path) {
    $root = [IO.Path]::GetFullPath((Get-GnMachineDataRoot)).TrimEnd('\')
    [void](Assert-GnOwnedPath $root $Path)
    $item = Get-Item -LiteralPath $Path -Force
    if ($item.Attributes -band [IO.FileAttributes]::ReparsePoint) { throw 'A machine installation/state path is a reparse point.' }
    $directory = $item.Attributes -band [IO.FileAttributes]::Directory
    $security = if ($directory) { [Security.AccessControl.DirectorySecurity]::new() } else { [Security.AccessControl.FileSecurity]::new() }
    $sddl = if ($directory) { 'O:BAG:BAD:P(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)(A;OICI;0x1200a9;;;BU)' } else { 'O:BAG:BAD:P(A;;FA;;;SY)(A;;FA;;;BA)(A;;FR;;;BU)' }
    $security.SetSecurityDescriptorSddlForm($sddl)
    Set-Acl -LiteralPath $Path -AclObject $security
    Assert-GnProtectedObject $Path
}
