# Copyright 2026 git-next contributors. MIT license.
[CmdletBinding()]
param(
    [ValidateSet('auto','user','machine')][string]$Scope = 'auto',
    [string]$Version,
    [string]$PackagePath,
    [string]$ExpectedSha256,
    [string]$TlsArchive,
    [switch]$Plan
)
Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'
$env:PSModulePath = (Join-Path $PSHOME 'Modules') + ';' + $env:PSModulePath
$script:GnBootstrapScriptPath = $PSCommandPath

function Initialize-GnBootstrapNative {
    if ('GitNext.BootstrapNative' -as [type]) { return }
    Add-Type -TypeDefinition @'
using System;
using System.Collections.Generic;
using System.ComponentModel;
using System.IO;
using System.Runtime.InteropServices;
using System.Text;
namespace GitNext {
 public static class BootstrapNative {
  [StructLayout(LayoutKind.Sequential)] struct SecurityAttributes { public int length; public IntPtr descriptor; public int inherit; }
  [DllImport("advapi32.dll", CharSet=CharSet.Unicode, SetLastError=true)] static extern bool ConvertStringSecurityDescriptorToSecurityDescriptor(string text, uint revision, out IntPtr descriptor, out uint size);
  [DllImport("kernel32.dll", CharSet=CharSet.Unicode, SetLastError=true)] static extern bool CreateDirectory(string path, ref SecurityAttributes attributes);
  [DllImport("kernel32.dll")] static extern IntPtr LocalFree(IntPtr memory);
  [DllImport("msi.dll", CharSet=CharSet.Unicode)] static extern uint MsiOpenDatabase(string path, IntPtr persist, out uint database);
  [DllImport("msi.dll", CharSet=CharSet.Unicode)] static extern uint MsiDatabaseOpenView(uint database, string sql, out uint view);
  [DllImport("msi.dll")] static extern uint MsiViewExecute(uint view, uint record);
  [DllImport("msi.dll")] static extern uint MsiViewFetch(uint view, out uint record);
  [DllImport("msi.dll")] static extern uint MsiRecordGetFieldCount(uint record);
  [DllImport("msi.dll", CharSet=CharSet.Unicode)] static extern uint MsiRecordGetString(uint record, uint field, StringBuilder text, ref uint length);
  [DllImport("msi.dll")] static extern uint MsiRecordReadStream(uint record, uint field, byte[] data, ref uint count);
  [DllImport("msi.dll")] static extern uint MsiCloseHandle(uint handle);
  static void Check(uint code) { if (code != 0) throw new Win32Exception((int)code); }
  static uint Open(string path) { uint database; Check(MsiOpenDatabase(path, IntPtr.Zero, out database)); return database; }
  public static void CreatePrivateDirectory(string path, string sddl) {
   IntPtr descriptor; uint size;
   if (!ConvertStringSecurityDescriptorToSecurityDescriptor(sddl, 1, out descriptor, out size)) throw new Win32Exception(Marshal.GetLastWin32Error());
   try { var attributes = new SecurityAttributes { length=Marshal.SizeOf(typeof(SecurityAttributes)), descriptor=descriptor };
    if (!CreateDirectory(path, ref attributes)) throw new Win32Exception(Marshal.GetLastWin32Error());
   } finally { LocalFree(descriptor); }
  }
  public static string[][] Query(string path, string sql) {
   uint database=Open(path), view=0;
   try { Check(MsiDatabaseOpenView(database, sql, out view)); Check(MsiViewExecute(view, 0)); var rows=new List<string[]>();
    while (true) { uint record; uint code=MsiViewFetch(view, out record); if (code==259) break; Check(code);
     try { uint fields=MsiRecordGetFieldCount(record) & 0xffff; if (fields==0 || fields>32 || rows.Count>=75000) throw new InvalidDataException("MSI table exceeds bootstrap limits.");
      var row=new string[fields]; for (uint field=1;field<=fields;field++) { uint length=4096; var value=new StringBuilder(4097); Check(MsiRecordGetString(record,field,value,ref length)); row[field-1]=value.ToString(); } rows.Add(row);
     } finally { MsiCloseHandle(record); }
    } return rows.ToArray();
   } finally { if(view!=0) MsiCloseHandle(view); MsiCloseHandle(database); }
  }
  public static void ExportCabinet(string path, string name, string destination) {
   if (name.Length>120 || !System.Text.RegularExpressions.Regex.IsMatch(name,@"^[A-Za-z0-9_.-]+$")) throw new InvalidDataException("Unsafe embedded cabinet name.");
   uint database=Open(path),view=0,record=0;
   try { Check(MsiDatabaseOpenView(database,"SELECT `Data` FROM `_Streams` WHERE `Name`='"+name+"'",out view)); Check(MsiViewExecute(view,0)); Check(MsiViewFetch(view,out record));
    using(var target=new FileStream(destination,FileMode.CreateNew,FileAccess.Write,FileShare.None)) { byte[] buffer=new byte[1048576]; long total=0;
     while(true) { uint count=(uint)buffer.Length; Check(MsiRecordReadStream(record,1,buffer,ref count)); if(count==0) break; total+=count; if(total>1073741824) throw new InvalidDataException("Embedded cabinet exceeds bootstrap limit."); target.Write(buffer,0,(int)count); }
     if(total==0) throw new InvalidDataException("Embedded cabinet is empty.");
    }
   } finally { if(record!=0) MsiCloseHandle(record); if(view!=0) MsiCloseHandle(view); MsiCloseHandle(database); }
  }
 }
}
'@
}
function New-GnBootstrapPrivateDirectory([string]$Path,[string]$InstallScope) {
    Initialize-GnBootstrapNative
    $full=[IO.Path]::GetFullPath($Path)
    if($InstallScope -eq 'machine'){
        $common=[Environment]::GetFolderPath([Environment+SpecialFolder]::CommonApplicationData).TrimEnd('\')
        if(-not $common -or -not $full.StartsWith($common+'\',[StringComparison]::OrdinalIgnoreCase)){throw 'A machine bootstrap cache must be beneath the native common application-data directory.'}
    }
    $ancestor=[IO.Path]::GetDirectoryName($full)
    while ($ancestor) {
        if (-not [IO.Directory]::Exists($ancestor) -or ([IO.File]::GetAttributes($ancestor) -band [IO.FileAttributes]::ReparsePoint)) { throw 'Bootstrap cache ancestors must be existing regular directories.' }
        if($InstallScope -eq 'machine'){
            $acl=Get-Acl -LiteralPath $ancestor
            $trusted=@('S-1-5-18','S-1-5-32-544','S-1-5-80-956008885-3418522649-1831038044-1853292631-2271478464')
            if($acl.GetOwner([Security.Principal.SecurityIdentifier]).Value -notin $trusted){throw 'A machine bootstrap cache ancestor has an untrusted owner.'}
            $unsafeRights=[Security.AccessControl.FileSystemRights]::Delete -bor [Security.AccessControl.FileSystemRights]::DeleteSubdirectoriesAndFiles -bor [Security.AccessControl.FileSystemRights]::ChangePermissions -bor [Security.AccessControl.FileSystemRights]::TakeOwnership
            foreach($rule in $acl.GetAccessRules($true,$true,[Security.Principal.SecurityIdentifier])){
                if($rule.AccessControlType -eq [Security.AccessControl.AccessControlType]::Allow -and -not ($rule.PropagationFlags -band [Security.AccessControl.PropagationFlags]::InheritOnly) -and $rule.IdentityReference.Value -notin $trusted -and ($rule.FileSystemRights -band $unsafeRights)){throw 'A machine bootstrap cache ancestor grants an untrusted replacement permission.'}
            }
        }
        $ancestor=[IO.Path]::GetDirectoryName($ancestor)
    }
    $sid=[Security.Principal.WindowsIdentity]::GetCurrent().User.Value
    $sddl=if($InstallScope -eq 'machine'){'O:BAG:BAD:P(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)'}else{'O:'+ $sid +'G:'+ $sid +'D:P(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)(A;OICI;FA;;;'+$sid+')'}
    [GitNext.BootstrapNative]::CreatePrivateDirectory($full,$sddl)
    return $full
}
function Get-GnBootstrapForwardArguments {
    $arguments=@{Scope=$Scope}
    foreach($name in @('Version','PackagePath','ExpectedSha256','TlsArchive')) { $value=Get-Variable -Name $name -ValueOnly; if($value){$arguments[$name]=[string]$value} }
    if ($Plan) { $arguments.Plan=$true }
    return $arguments
}
function Get-GnBootstrapRelayCode([string]$ScriptPath,[string]$ScriptSha256,[hashtable]$Arguments,[string]$ResultPath='') {
    $payload=@{path=[IO.Path]::GetFullPath($ScriptPath);sha256=$ScriptSha256;arguments=$Arguments;result=$ResultPath} | ConvertTo-Json -Depth 5 -Compress
    $encoded=[Convert]::ToBase64String([Text.Encoding]::UTF8.GetBytes($payload))
    # A read lease prevents script replacement between the digest check and invocation.
    return '$ErrorActionPreference="Stop"; if(-not [Environment]::Is64BitProcess){throw "Native 64-bit PowerShell is required."}; $v=[Text.Encoding]::UTF8.GetString([Convert]::FromBase64String("'+$encoded+'"))|ConvertFrom-Json; $lease=[IO.File]::Open($v.path,[IO.FileMode]::Open,[IO.FileAccess]::Read,[IO.FileShare]::Read); try { if((Get-FileHash -LiteralPath $v.path -Algorithm SHA256).Hash.ToLowerInvariant() -cne $v.sha256.ToLowerInvariant()){throw "Installer changed before native forwarding."}; $a=@{}; $v.arguments.PSObject.Properties|ForEach-Object{$a[$_.Name]=$_.Value}; $exitCode=0; try{$value=(& $v.path @a|Out-String)}catch{$value=[pscustomobject]@{status="failed";message=$_.Exception.Message}|ConvertTo-Json; $exitCode=1}; if($v.result){$bytes=[Text.UTF8Encoding]::new($false).GetBytes($value); $file=[IO.File]::Open($v.result,[IO.FileMode]::CreateNew,[IO.FileAccess]::Write,[IO.FileShare]::None); try{$file.Write($bytes,0,$bytes.Length)}finally{$file.Dispose()}}else{Write-Output $value}; if($exitCode){exit $exitCode} } finally {$lease.Dispose()}'
}
function Invoke-GnBootstrapNativeRelay([string]$ScriptPath,[string]$ScriptSha256,[hashtable]$Arguments) {
    $code=Get-GnBootstrapRelayCode $ScriptPath $ScriptSha256 $Arguments
    $start=[Diagnostics.ProcessStartInfo]::new()
    $start.FileName=Join-Path $env:SystemRoot 'Sysnative\WindowsPowerShell\v1.0\powershell.exe'
    $start.Arguments='-NoProfile -NonInteractive -ExecutionPolicy Bypass -EncodedCommand '+[Convert]::ToBase64String([Text.Encoding]::Unicode.GetBytes($code))
    $start.UseShellExecute=$false;$start.CreateNoWindow=$true;$start.RedirectStandardOutput=$true;$start.RedirectStandardError=$true
    $process=[Diagnostics.Process]::new();$process.StartInfo=$start
    try {
        if(-not $process.Start()){throw 'Native PowerShell forwarding did not start.'}
        $output=$process.StandardOutput.ReadToEndAsync();$errors=$process.StandardError.ReadToEndAsync()
        if(-not $process.WaitForExit(3600000)){$process.Kill();throw 'Native PowerShell forwarding timed out.'}
        $value=$output.GetAwaiter().GetResult();$errorText=$errors.GetAwaiter().GetResult()
        if($process.ExitCode){throw ('Native PowerShell forwarding failed. '+$value+' '+$errorText)}
        if(-not $value.Trim()){throw 'Native PowerShell forwarding returned no status.'}
        Write-Output $value
    } finally {$process.Dispose()}
}

function Get-GnBootstrapArchitecture {
    $architecture = [Environment]::GetEnvironmentVariable('PROCESSOR_ARCHITEW6432')
    if (-not $architecture) { $architecture = [Environment]::GetEnvironmentVariable('PROCESSOR_ARCHITECTURE') }
    switch ($architecture.ToUpperInvariant()) {
        'ARM64' { 'windows-arm64' }
        'AMD64' { 'windows-x64' }
        default { throw 'git-next supports Windows x64 and ARM64.' }
    }
}
function Get-GnBootstrapRegistrations {
    foreach ($hive in @([Microsoft.Win32.RegistryHive]::LocalMachine,[Microsoft.Win32.RegistryHive]::CurrentUser)) {
        foreach ($view in @([Microsoft.Win32.RegistryView]::Registry64,[Microsoft.Win32.RegistryView]::Registry32)) {
            $base = [Microsoft.Win32.RegistryKey]::OpenBaseKey($hive,$view)
            try {
                $key = $base.OpenSubKey('Software\Microsoft\Windows\CurrentVersion\Uninstall\Git_is1')
                if ($key) {
                    try {
                        $location = [string]$key.GetValue('InstallLocation')
                        if ($location -and [IO.File]::Exists((Join-Path $location 'cmd\git.exe'))) {
                            [pscustomobject]@{ product='Git'; scope=$(if($hive -eq [Microsoft.Win32.RegistryHive]::LocalMachine){'machine'}else{'user'}); root=[IO.Path]::GetFullPath($location).TrimEnd('\') }
                        }
                    } finally { $key.Dispose() }
                }
            } finally { $base.Dispose() }
        }
    }
}
function Get-GnBootstrapProgramFiles {
    # The 64-bit Program Files path, resolved independently of the bitness of this process.
    # `$env:ProgramW6432` is empty in a 32-bit process, which would silently collapse the
    # path to the root drive and defeat every prefix check that relies on it.
    $base=[Microsoft.Win32.RegistryKey]::OpenBaseKey([Microsoft.Win32.RegistryHive]::LocalMachine,[Microsoft.Win32.RegistryView]::Registry64)
    try {
        $key=$base.OpenSubKey('SOFTWARE\Microsoft\Windows\CurrentVersion')
        if ($key) { try { $path=$key.GetValue('ProgramFilesDir',$null,[Microsoft.Win32.RegistryValueOptions]::DoNotExpandEnvironmentNames) } finally { $key.Dispose() } }
    } finally { $base.Dispose() }
    if ($path -and [IO.Path]::IsPathRooted($path)) { return [IO.Path]::GetFullPath($path) }
    if ($env:ProgramW6432) { return [IO.Path]::GetFullPath($env:ProgramW6432) }
    if ($env:ProgramFiles) { return [IO.Path]::GetFullPath($env:ProgramFiles) }
    return [IO.Path]::GetFullPath((Join-Path $env:SystemDrive 'Program Files'))
}
function Get-GnBootstrapSelection([string]$RequestedScope) {
    $platform = Get-GnBootstrapArchitecture
    $programFiles=Get-GnBootstrapProgramFiles
    $registrations = @(Get-GnBootstrapRegistrations | Sort-Object scope,root -Unique)
    $selectedScope = $RequestedScope
    if ($selectedScope -eq 'auto') {
        $machineInstalled = [IO.File]::Exists((Join-Path $programFiles 'Git\package.json')) -or [IO.File]::Exists((Join-Path $programFiles ('git-next\'+$platform+'\package.json')))
        $selectedScope = if ($machineInstalled -or @($registrations | Where-Object scope -eq 'machine').Count) { 'machine' } else { 'user' }
    }
    $original = @($registrations | Where-Object scope -eq $selectedScope)
    if ($original.Count -gt 1) { throw 'More than one registered Git installation matches the selected scope.' }
    if (-not $original.Count -and $registrations.Count) { throw 'The requested scope differs from the registered Git installation. Use -Scope auto.' }
    $prefix = if ($selectedScope -eq 'machine') { $programFiles } else { Join-Path $env:LOCALAPPDATA 'Programs' }
    # git-default-v2 always installs into the official Git default directory so the primary
    # IDE entry point (cmd\git.exe) is the real git-next payload instead of a bridge forwarder.
    $root = Join-Path $prefix 'Git'
    $legacy = Join-Path $prefix ($(if($selectedScope -eq 'machine'){'git-next'}else{'git-next-msi'})+'\'+$platform)
    [pscustomobject]@{
        platform=$platform
        scope=$selectedScope
        root=$root
        legacy_root=$(if([IO.File]::Exists((Join-Path $legacy 'package.json'))){$legacy}else{$null})
        original_root=$(if($original.Count){$original[0].root}else{$null})
    }
}
function Get-GnBootstrapRelease([string]$Platform,[string]$InstallScope,[string]$RequestedVersion) {
    if ($RequestedVersion -and $RequestedVersion -cnotmatch '^[0-9]+\.[0-9]+\.[0-9]+$') { throw 'Pass a stable version such as 0.3.0.' }
    [Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12
    $endpoint = if ($RequestedVersion) { 'tags/v'+$RequestedVersion } else { 'latest' }
    $release = Invoke-RestMethod -Uri ('https://api.github.com/repos/neko233-com/git-next/releases/'+$endpoint) -Headers @{'User-Agent'='git-next-bootstrap';Accept='application/vnd.github+json'} -TimeoutSec 30
    if ($release.draft -or $release.prerelease -or $release.tag_name -cnotmatch '^v([0-9]+\.[0-9]+\.[0-9]+)$') { throw 'The selected official release is not stable.' }
    $releaseVersion = $Matches[1]
    $suffix = if ($InstallScope -eq 'user') { '-user' } else { '' }
    $name = 'git-next-'+$releaseVersion+'-'+$Platform+$suffix+'.msi'
    $assets = @($release.assets | Where-Object name -CEQ $name)
    $url = 'https://github.com/neko233-com/git-next/releases/download/v'+$releaseVersion+'/'+$name
    if ($assets.Count -ne 1 -or $assets[0].browser_download_url -cne $url -or $assets[0].digest -cnotmatch '^sha256:[a-f0-9]{64}$' -or $assets[0].size -lt 4096 -or $assets[0].size -gt 1073741824) { throw 'Official GitHub package identity is missing or invalid.' }
    [pscustomobject]@{version=$releaseVersion;name=$name;url=$url;sha256=$assets[0].digest.Substring(7);size=[long]$assets[0].size}
}
function Save-GnBootstrapPackage($Release,[string]$Destination) {
    # Only official API metadata selects the version and expected bytes; mirrors transport bytes.
    Add-Type -AssemblyName System.Net.Http
    $handler = New-Object Net.Http.HttpClientHandler
    $client = New-Object Net.Http.HttpClient($handler)
    $client.Timeout = [TimeSpan]::FromSeconds(180)
    $client.DefaultRequestHeaders.UserAgent.ParseAdd('git-next-bootstrap/1')
    $probes = @()
    foreach ($url in @($Release.url,('https://ghfast.top/'+$Release.url),('https://ghproxy.net/'+$Release.url))) {
        $request = New-Object Net.Http.HttpRequestMessage([Net.Http.HttpMethod]::Head,$url)
        $cancel = New-Object Threading.CancellationTokenSource([TimeSpan]::FromSeconds(5))
        $probes += [pscustomobject]@{url=$url;request=$request;cancel=$cancel;task=$client.SendAsync($request,[Net.Http.HttpCompletionOption]::ResponseHeadersRead,$cancel.Token);rank=[double]::PositiveInfinity;watch=[Diagnostics.Stopwatch]::StartNew();done=$false}
    }
    try {
        while (@($probes | Where-Object { -not $_.done }).Count) {
            foreach ($probe in @($probes | Where-Object { -not $_.done -and $_.task.IsCompleted })) {
                $probe.done = $true
                try { $response = $probe.task.GetAwaiter().GetResult(); try { if ($response.IsSuccessStatusCode) { $probe.rank=$probe.watch.Elapsed.TotalMilliseconds } } finally { $response.Dispose() } } catch { }
            }
            if (@($probes | Where-Object { -not $_.done }).Count) { Start-Sleep -Milliseconds 20 }
        }
        foreach ($probe in @($probes | Sort-Object rank,url)) {
            $response=$null; $source=$null; $target=$null
            try {
                $response=$client.GetAsync($probe.url,[Net.Http.HttpCompletionOption]::ResponseHeadersRead).GetAwaiter().GetResult()
                [void]$response.EnsureSuccessStatusCode()
                $source=$response.Content.ReadAsStreamAsync().GetAwaiter().GetResult()
                $target=[IO.File]::Open($Destination,[IO.FileMode]::CreateNew,[IO.FileAccess]::Write,[IO.FileShare]::None)
                $buffer=New-Object byte[] 1048576; $total=0L
                while ($true) {
                    $task=$source.ReadAsync($buffer,0,$buffer.Length)
                    if (-not $task.Wait(30000)) { throw 'Package download timed out.' }
                    $read=$task.GetAwaiter().GetResult(); if (-not $read) { break }
                    $total+=$read; if ($total -gt $Release.size) { throw 'Package exceeds official size.' }
                    $target.Write($buffer,0,$read)
                }
                $target.Dispose(); $target=$null
                if ($total -ne $Release.size -or (Get-FileHash -LiteralPath $Destination -Algorithm SHA256).Hash.ToLowerInvariant() -cne $Release.sha256) { throw 'Package checksum mismatch.' }
                return
            } catch {
                if ($target) { $target.Dispose(); $target=$null }
                if ([IO.File]::Exists($Destination)) { [IO.File]::Delete($Destination) }
            } finally { if($source){$source.Dispose()};if($response){$response.Dispose()} }
        }
        throw 'No verified package could be downloaded. Existing Git was not changed.'
    } finally { foreach($probe in $probes){$probe.cancel.Dispose();$probe.request.Dispose()};$client.Dispose();$handler.Dispose() }
}
function Get-GnBootstrapTlsPin([string]$Platform) {
    $pins = '{"windows-x64":{"url":"https://github.com/git-for-windows/git/releases/download/v2.54.0.windows.1/Git-2.54.0-64-bit.tar.bz2","sha256":"e1819cee60d09793dde322cdb1170e03663c41cd9265cf45246219fc5e6aeecd","size":116824890,"name":"windows-x64-openssl.tar.bz2"},"windows-arm64":{"url":"https://github.com/git-for-windows/git/releases/download/v2.54.0.windows.1/Git-2.54.0-arm64.tar.bz2","sha256":"ce10b24c74ac9c724ab81e2ee30d06e7ee693977a552b8da4e434e909a641847","size":157085415,"name":"windows-arm64-openssl.tar.bz2"}}' | ConvertFrom-Json
    return $pins.PSObject.Properties[$Platform].Value
}
function Invoke-GnBootstrapMsi([string]$Package,[string]$Log,[string]$InstallRoot) {
    $process=Start-Process -FilePath (Join-Path $env:SystemRoot 'System32\msiexec.exe') -ArgumentList ('/i "'+$Package+'" INSTALLFOLDER="'+$InstallRoot+'" REPLACEGIT=1 /qn /norestart /L*v "'+$Log+'"') -WindowStyle Hidden -Wait -PassThru
    if ($process.ExitCode -notin @(0,3010)) { throw ('MSI installation failed ('+$process.ExitCode+'). Log: '+$Log) }
    # The launch condition that guards an existing official Git installation only passes when
    # REPLACEGIT really reached the installer. A property that the installer ignored would
    # either block the install outright or, worse, let it proceed without the authorization
    # the takeover depends on. The verbose log is the only trustworthy record of the value
    # the installer actually saw, so it is checked instead of assumed.
    #
    # Windows Installer writes the client-side property table to the log as
    # "Property(S): <name> = <value>", so the match is anchored on that prefix and on a
    # complete value token; a bare substring search would also accept "REPLACEGIT = 10".
    $replacementSeen=$false
    foreach ($line in [IO.File]::ReadLines($Log)) {
        if ($line -match 'Property\(S\):\s*REPLACEGIT\s*=\s*1\s*$') { $replacementSeen=$true; break }
    }
    if (-not $replacementSeen) { throw ('The MSI did not record the replacement authorization (REPLACEGIT). The official Git directory was left untouched. Log: '+$Log) }
    return $process.ExitCode
}
function Get-GnBootstrapMsiVersion([string]$Package) {
    $installer=New-Object -ComObject WindowsInstaller.Installer
    $database=$null;$view=$null;$record=$null
    try {
        $database=$installer.OpenDatabase($Package,0)
        $view=$database.OpenView('SELECT `Value` FROM `Property` WHERE `Property`=''ProductVersion''')
        [void]$view.Execute();$record=$view.Fetch()
        if (-not $record) { throw 'Verified MSI has no product version.' }
        $version=[string]$record.StringData(1)
        if ($version -cnotmatch '^[0-9]+\.[0-9]+\.[0-9]+$') { throw 'Verified MSI version is invalid.' }
        return $version
    } finally { foreach($item in @($record,$view,$database,$installer)){if($item){[void][Runtime.InteropServices.Marshal]::FinalReleaseComObject($item)}} }
}
function Get-GnBootstrapMsiRows([string]$Package,[string]$Sql) {
    Initialize-GnBootstrapNative
    foreach($row in [GitNext.BootstrapNative]::Query($Package,$Sql)){[pscustomobject]@{values=$row}}
}
function Get-GnBootstrapSupportPaths {
    @('package.json','migration/takeover.ps1','migration/Takeover.Core.psm1','updater/Update.Core.psm1','updater/Scope.Core.ps1','updater/Scope.Native.cs','updater/Runtime.Configuration.ps1','updater/messages.zh-CN.json')
}
function Get-GnBootstrapMsiName([string]$Name) {
    $target=($Name -split ':',2)[0]
    $target=($target -split '\|')[-1]
    if($target -eq '.'){return ''}
    if(-not $target -or $target -in @('..',' ') -or $target -match '[\\/:\x00-\x1f]' -or $target.EndsWith('.') -or $target.EndsWith(' ')){throw 'Unsafe MSI target name.'}
    return $target
}
function Resolve-GnBootstrapMsiDirectory([string]$Id,[hashtable]$Directories,[hashtable]$Seen,[int]$Depth=0) {
    if($Id -ceq 'INSTALLFOLDER'){return ''}
    if($Depth -ge 64 -or $Seen.ContainsKey($Id) -or -not $Directories.ContainsKey($Id)){throw 'MSI payload directory is outside the bounded installation tree.'}
    $Seen[$Id]=$true
    try {
        $row=$Directories[$Id]
        $parent=Resolve-GnBootstrapMsiDirectory $row[1] $Directories $Seen ($Depth+1)
        $name=Get-GnBootstrapMsiName $row[2]
        return (@($parent,$name)|Where-Object{$_}) -join '/'
    } finally {$Seen.Remove($Id)}
}
function Get-GnBootstrapSupportMap([string]$Package) {
    $directories=@{};$components=@{};$wanted=@{};$selected=@{}
    foreach($path in Get-GnBootstrapSupportPaths){$wanted[$path]=$path}
    foreach($item in Get-GnBootstrapMsiRows $Package 'SELECT `Directory`,`Directory_Parent`,`DefaultDir` FROM `Directory`'){
        $row=$item.values;if($directories.ContainsKey($row[0])){throw 'Ambiguous MSI directory.'};$directories[$row[0]]=$row
    }
    foreach($item in Get-GnBootstrapMsiRows $Package 'SELECT `Component`,`Directory_` FROM `Component`'){
        $row=$item.values;if($components.ContainsKey($row[0])){throw 'Ambiguous MSI component.'};$components[$row[0]]=$row[1]
    }
    $media=@();$last=0L
    foreach($item in Get-GnBootstrapMsiRows $Package 'SELECT `DiskId`,`LastSequence`,`Cabinet` FROM `Media` ORDER BY `DiskId`'){
        $row=$item.values;$bound=0L
        if(-not [long]::TryParse($row[1],[ref]$bound) -or $bound -le $last -or $row[2] -cnotmatch '^#[A-Za-z0-9_.-]{1,120}$'){throw 'Only bounded embedded MSI cabinets are supported.'}
        $media+=,[pscustomobject]@{first=$last+1;last=$bound;cabinet=$row[2].Substring(1)};$last=$bound
    }
    foreach($item in Get-GnBootstrapMsiRows $Package 'SELECT `File`,`Component_`,`FileName`,`FileSize`,`Sequence` FROM `File`'){
        $row=$item.values
        if(-not $components.ContainsKey($row[1])){throw 'MSI file references an unknown component.'}
        $directory=Resolve-GnBootstrapMsiDirectory $components[$row[1]] $directories @{}
        $name=Get-GnBootstrapMsiName $row[2]
        $path=(@($directory,$name)|Where-Object{$_}) -join '/'
        if(-not $wanted.ContainsKey($path)){continue}
        $canonical=$wanted[$path]
        $size=0L;$sequence=0L
        if($selected.ContainsKey($canonical) -or $row[0] -cnotmatch '^[A-Za-z_][A-Za-z0-9_.]{0,119}$' -or $row[0].EndsWith('.') -or -not [long]::TryParse($row[3],[ref]$size) -or $size -le 0 -or $size -gt 33554432 -or -not [long]::TryParse($row[4],[ref]$sequence)){throw 'Invalid or ambiguous MSI bootstrap member.'}
        $cabinet=@($media|Where-Object{$sequence -ge $_.first -and $sequence -le $_.last})
        if($cabinet.Count -ne 1){throw 'MSI bootstrap member has no unique embedded cabinet.'}
        $selected[$canonical]=[pscustomobject]@{path=$canonical;id=$row[0];bytes=$size;cabinet=$cabinet[0].cabinet}
    }
    if($selected.Count -ne $wanted.Count){throw 'Verified MSI is missing a required preinstall support member.'}
    return $selected
}
function Export-GnBootstrapCabinet([string]$Package,[string]$Name,[string]$Destination) {
    Initialize-GnBootstrapNative
    [GitNext.BootstrapNative]::ExportCabinet($Package,$Name,$Destination)
}
function Expand-GnBootstrapCabinetMember([string]$Cabinet,[string]$Id,[string]$Directory) {
    if($Id -cnotmatch '^[A-Za-z_][A-Za-z0-9_.]{0,119}$' -or $Id.EndsWith('.')){throw 'Unsafe cabinet member selector.'}
    $start=[Diagnostics.ProcessStartInfo]::new()
    $start.FileName=Join-Path $env:SystemRoot 'System32\expand.exe'
    $start.Arguments='"'+$Cabinet+'" -F:'+ $Id +' "'+$Directory+'"'
    $start.UseShellExecute=$false;$start.CreateNoWindow=$true;$start.RedirectStandardOutput=$true;$start.RedirectStandardError=$true
    $process=[Diagnostics.Process]::new();$process.StartInfo=$start
    try {
        if(-not $process.Start()){throw 'Embedded cabinet extraction did not start.'}
        $output=$process.StandardOutput.ReadToEndAsync();$errors=$process.StandardError.ReadToEndAsync()
        if(-not $process.WaitForExit(30000)){$process.Kill();throw 'Embedded cabinet extraction timed out.'}
        [void]$output.GetAwaiter().GetResult();$failure=$errors.GetAwaiter().GetResult()
        if($process.ExitCode){throw ('Embedded cabinet extraction failed. '+$failure)}
    } finally {$process.Dispose()}
    $members=@([IO.Directory]::GetFileSystemEntries($Directory))
    if($members.Count -ne 1 -or [IO.Path]::GetFileName($members[0]) -cne $Id -or -not [IO.File]::Exists($members[0]) -or ([IO.File]::GetAttributes($members[0]) -band [IO.FileAttributes]::ReparsePoint)){throw 'Cabinet extraction did not produce exactly the requested regular member.'}
    return $members[0]
}
function Get-GnBootstrapFileSha256([string]$Path) { return (Get-FileHash -LiteralPath $Path -Algorithm SHA256).Hash.ToLowerInvariant() }
function Assert-GnBootstrapCandidate($Selection,$Manifest,[string]$TargetVersion) {
    if($Manifest.schema_version -ne 1 -or $Manifest.product -cne 'git-next' -or $Manifest.install_kind -cne 'msi' -or $Manifest.version -cne $TargetVersion -or $Manifest.platform -cne $Selection.platform -or $Manifest.install_scope -cne $Selection.scope -or $Manifest.installation_layout -cne 'git-default-v2' -or $Manifest.release_eligible -ne $true -or $Manifest.complete_git_parity -ne $true){throw 'The verified MSI must contain the selected complete native package identity.'}
}
function Expand-GnBootstrapSupport([string]$Package,[string]$ExpectedMsiSha256,[string]$Destination,$Selection,[string]$TargetVersion) {
    if($ExpectedMsiSha256 -cnotmatch '^[a-f0-9]{64}$'){throw 'A verified MSI SHA256 is required before support extraction.'}
    $lease=[IO.File]::Open($Package,[IO.FileMode]::Open,[IO.FileAccess]::Read,[IO.FileShare]::Read)
    try {
        if((Get-GnBootstrapFileSha256 $Package) -cne $ExpectedMsiSha256){throw 'MSI changed before preinstall support extraction.'}
        $map=Get-GnBootstrapSupportMap $Package
        $total=0L;foreach($entry in $map.Values){$total+=$entry.bytes};if($total -gt 67108864){throw 'Preinstall support exceeds the extraction limit.'}
        [void](New-GnBootstrapPrivateDirectory $Destination $Selection.scope)
        $support=Join-Path $Destination 'support';[void][IO.Directory]::CreateDirectory($support)
        $cabinets=@{}
        foreach($entry in @($map.Values|Sort-Object path)){
            if(-not $cabinets.ContainsKey($entry.cabinet)){
                $cab=Join-Path $Destination ('cabinet-'+$cabinets.Count+'.cab')
                Export-GnBootstrapCabinet $Package $entry.cabinet $cab;$cabinets[$entry.cabinet]=$cab
            }
            $scratch=Join-Path $Destination ('member-'+[Guid]::NewGuid().ToString('N'));[void][IO.Directory]::CreateDirectory($scratch)
            $member=Expand-GnBootstrapCabinetMember $cabinets[$entry.cabinet] $entry.id $scratch
            if((Get-Item -LiteralPath $member).Length -ne $entry.bytes){throw 'Embedded support member size differs from the MSI File table.'}
            $target=Join-Path $support $entry.path;[void][IO.Directory]::CreateDirectory([IO.Path]::GetDirectoryName($target));[IO.File]::Move($member,$target);[IO.Directory]::Delete($scratch)
        }
        $packageManifest=Join-Path $support 'package.json';$manifest=[IO.File]::ReadAllText($packageManifest)|ConvertFrom-Json
        Assert-GnBootstrapCandidate $Selection $manifest $TargetVersion
        foreach($path in Get-GnBootstrapSupportPaths){
            if($path -ceq 'package.json'){continue}
            $property=@($manifest.files.PSObject.Properties|Where-Object{$_.Name -ceq $path})
            if($property.Count -ne 1){throw ('Package manifest does not bind bootstrap member: '+$path)}
            $hash=[string]$property[0].Value;$file=Join-Path $support $path
            if($hash -cnotmatch '^[a-f0-9]{64}$' -or (Get-GnBootstrapFileSha256 $file) -cne $hash){throw ('Bootstrap module checksum mismatch: '+$path)}
        }
        foreach($cab in $cabinets.Values){[IO.File]::Delete($cab)}
        return [pscustomobject]@{root=$support;manifest_path=$packageManifest;manifest=$manifest;package_sha256=(Get-GnBootstrapFileSha256 $packageManifest);msi_sha256=$ExpectedMsiSha256}
    } finally {$lease.Dispose()}
}
function New-GnBootstrapOriginalSnapshot($Selection,$Support,[string]$SnapshotRoot,[string]$BindingPath) {
    Import-Module (Join-Path $Support.root 'updater\Update.Core.psm1') -Force -DisableNameChecking
    Import-Module (Join-Path $Support.root 'migration\Takeover.Core.psm1') -Force -DisableNameChecking
    $returned=@(New-GnPreinstallSnapshot -OriginalGitRoot $Selection.original_root -SnapshotRoot $SnapshotRoot -CandidatePackagePath $Support.manifest_path -CandidateMsiSha256 $Support.msi_sha256)
    $expected=Join-Path ([IO.Path]::GetFullPath($SnapshotRoot)) 'snapshot.json'
    if($returned.Count -ne 1 -or $returned[0] -isnot [string] -or [IO.Path]::GetFullPath($returned[0]) -cne $expected -or -not [IO.File]::Exists($expected) -or ([IO.File]::GetAttributes($expected) -band [IO.FileAttributes]::ReparsePoint)){throw 'Preinstall snapshot did not return its expected protected manifest.'}
    $binding=[ordered]@{schema=1;candidate_msi_sha256=$Support.msi_sha256;candidate_package_sha256=$Support.package_sha256;original_root=[IO.Path]::GetFullPath($Selection.original_root);install_root=[IO.Path]::GetFullPath($Selection.root);owner_sid=[Security.Principal.WindowsIdentity]::GetCurrent().User.Value;snapshot_path=$expected;snapshot_sha256=(Get-GnBootstrapFileSha256 $expected)}
    $bytes=[Text.UTF8Encoding]::new($false).GetBytes(($binding|ConvertTo-Json -Depth 5)+[Environment]::NewLine)
    $file=[IO.File]::Open($BindingPath,[IO.FileMode]::CreateNew,[IO.FileAccess]::Write,[IO.FileShare]::None)
    try{$file.Write($bytes,0,$bytes.Length)}finally{$file.Dispose()}
    return [pscustomobject]$binding
}
function Invoke-GnBootstrapApply($Selection,[string]$PreparedTls,$Binding) {
    if((Get-GnBootstrapFileSha256 $Binding.snapshot_path) -cne $Binding.snapshot_sha256){throw 'The original-Git snapshot changed after the pre-MSI checkpoint.'}
    if((Get-GnBootstrapFileSha256 (Join-Path $Selection.root 'package.json')) -cne $Binding.candidate_package_sha256){throw 'The installed package does not match the pre-MSI snapshot candidate.'}
    return (& (Join-Path $Selection.root 'migration\takeover.ps1') -Mode Apply -OriginalGitRoot $Selection.original_root -TlsArchive $PreparedTls -OriginalSnapshot $Binding.snapshot_path -OriginalSnapshotSha256 $Binding.snapshot_sha256 | Out-String) | ConvertFrom-Json
}
function Assert-GnBootstrapPayload($Selection,$Manifest,[string]$TargetVersion) {
    if ($Manifest.version -cne $TargetVersion) { throw 'The installed manifest does not match the selected MSI version.' }
    if (-not $Manifest.PSObject.Properties['installation_layout'] -or $Manifest.installation_layout -cne 'git-default-v2') { throw 'The git-default-v2 layout is required so the official Git entry points resolve to the git-next payload.' }
    Import-Module (Join-Path $Selection.root 'updater\Update.Core.psm1') -Force -DisableNameChecking
    Assert-GnInstalledFiles $Selection.root $Manifest
}
function Remove-GnBootstrapLegacyInstall($Selection) {
    # The legacy layout is superseded in place: its payload is redundant once the official
    # directory carries git-next. Only a fully verified install may remove it.
    $legacy=$Selection.legacy_root
    if (-not $legacy) { return }
    if ([IO.Path]::GetFullPath($legacy).TrimEnd('\') -ieq [IO.Path]::GetFullPath($Selection.root).TrimEnd('\')) { throw 'Refusing to remove the active installation root.' }
    # The prefix has to be the real 64-bit Program Files path. A 32-bit process has no
    # `ProgramW6432`, which would collapse the expected path to `\git-next` and make the
    # prefix check accept almost any directory.
    $prefix=if($Selection.scope -eq 'machine'){Get-GnBootstrapProgramFiles}else{$env:LOCALAPPDATA}
    $expected=if($Selection.scope -eq 'machine'){Join-Path $prefix 'git-next'}else{Join-Path $prefix 'Programs\git-next-msi'}
    if (-not ([IO.Path]::GetFullPath($legacy).TrimEnd('\')).StartsWith(([IO.Path]::GetFullPath($expected)).TrimEnd('\')+'\',[StringComparison]::OrdinalIgnoreCase)) { throw 'Refusing to remove a directory outside the git-next installation prefix.' }
    Remove-Item -LiteralPath $legacy -Recurse -Force
}
function Get-GnBootstrapTerminalVersion([string]$Terminal) {
    $start=[Diagnostics.ProcessStartInfo]::new()
    $start.FileName=$Terminal;$start.Arguments='--version'
    $start.UseShellExecute=$false;$start.CreateNoWindow=$true
    $start.RedirectStandardOutput=$true;$start.RedirectStandardError=$true
    $process=[Diagnostics.Process]::new();$process.StartInfo=$start
    try {
        if (-not $process.Start()) { throw 'Native Git-Terminal version probe did not start.' }
        $output=$process.StandardOutput.ReadToEndAsync();$errorOutput=$process.StandardError.ReadToEndAsync()
        if (-not $process.WaitForExit(10000)) { $process.Kill();throw 'Native Git-Terminal version probe timed out.' }
        $identity=$output.GetAwaiter().GetResult().Trim()
        [void]$errorOutput.GetAwaiter().GetResult()
        if ($process.ExitCode -ne 0 -or $identity -notmatch '^git-terminal [0-9]+\.[0-9]+\.[0-9]+$') { throw 'Native Git-Terminal verification failed.' }
        return $identity
    } finally { $process.Dispose() }
}
function Test-GnBootstrapInstallation($Selection) {
    $machine=[Environment]::GetEnvironmentVariable('Path','Machine'); $user=[Environment]::GetEnvironmentVariable('Path','User')
    $env:Path=$machine+';'+$user
    $command=Get-Command git.exe -CommandType Application -ErrorAction Stop | Select-Object -First 1
    $manifest=Get-Content -LiteralPath (Join-Path $Selection.root 'package.json') -Raw | ConvertFrom-Json
    Assert-GnBootstrapPayload $Selection $manifest $manifest.version
    $identity=(& $command.Source version --json | Out-String) | ConvertFrom-Json
    if ($LASTEXITCODE -ne 0 -or $identity.product -cne 'git-next' -or $identity.version -cne $manifest.version -or -not $identity.complete_git_parity -or $identity.external_git_fallback -ne $false -or $identity.external_ssh_fallback -ne $false) { throw 'The default Git command does not identify the accepted installed version.' }
    $lfs=(& $command.Source lfs version) -join "`n"
    if ($LASTEXITCODE -ne 0 -or $lfs -notmatch '^git-lfs/') { throw 'Built-in Git LFS verification failed.' }
    $terminal=Join-Path $Selection.root 'bin\git-terminal.exe'
    [void](Get-GnBootstrapTerminalVersion $terminal)
    return $command
}
function Undo-GnBootstrapTakeover($Selection,[string]$Transaction) {
    & (Join-Path $Selection.root 'migration\takeover.ps1') -Mode Rollback -Transaction $Transaction | Out-Null
}
function Complete-GnBootstrap($Selection,[string]$NewTransaction) {
    try { return Test-GnBootstrapInstallation $Selection }
    catch {
        $failure=$_.Exception.Message
        if ($NewTransaction) {
            try { Undo-GnBootstrapTakeover $Selection $NewTransaction }
            catch { throw ('Final verification failed and rollback needs recovery. Transaction: '+$NewTransaction+'. Run migration\takeover.ps1 -Mode Rollback -Transaction with this exact path. '+$_.Exception.Message) }
            throw ('Final verification failed; this invocation restored the original Git using its protected transaction. '+$failure)
        }
        throw ('Final verification failed; no original-Git transaction from this invocation was rolled back. '+$failure)
    }
}
function Invoke-GnBootstrapMain {
    if(-not [Environment]::Is64BitProcess -and [Environment]::Is64BitOperatingSystem){
        Invoke-GnBootstrapNativeRelay $script:GnBootstrapScriptPath (Get-GnBootstrapFileSha256 $script:GnBootstrapScriptPath) (Get-GnBootstrapForwardArguments)
        return
    }
    $selection=Get-GnBootstrapSelection $Scope
    if ($Plan) { $selection | Add-Member NoteProperty changes_applied $false -PassThru | ConvertTo-Json; return }
    $principal=New-Object Security.Principal.WindowsPrincipal([Security.Principal.WindowsIdentity]::GetCurrent())
    $elevated=$principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
    if ($selection.scope -eq 'machine' -and -not $elevated) {
        $resultDirectory=New-GnBootstrapPrivateDirectory (Join-Path $env:LOCALAPPDATA ('git-next-elevation-'+[Guid]::NewGuid().ToString('N'))) 'user'
        $resultPath=Join-Path $resultDirectory 'result.json'
        $code=Get-GnBootstrapRelayCode $script:GnBootstrapScriptPath (Get-GnBootstrapFileSha256 $script:GnBootstrapScriptPath) (Get-GnBootstrapForwardArguments) $resultPath
        $command=[Convert]::ToBase64String([Text.Encoding]::Unicode.GetBytes($code))
        $nativeShell=Join-Path $env:SystemRoot 'System32\WindowsPowerShell\v1.0\powershell.exe'
        try {
            $child=Start-Process -FilePath $nativeShell -Verb RunAs -WindowStyle Hidden -ArgumentList ('-NoProfile -NonInteractive -ExecutionPolicy Bypass -EncodedCommand '+$command) -Wait -PassThru
            $result=Get-Content -LiteralPath $resultPath -Raw
            if ($child.ExitCode) { $failure=$result | ConvertFrom-Json; throw ('Elevated installation failed. '+$failure.message) }
            if (-not $result) { throw 'Elevated installation returned no verified status.' }
            Write-Output $result
        } finally { if([IO.File]::Exists($resultPath)){[IO.File]::Delete($resultPath)};[IO.Directory]::Delete($resultDirectory) }
        return
    }
    $cacheBase=if($selection.scope -eq 'machine'){[Environment]::GetFolderPath([Environment+SpecialFolder]::CommonApplicationData)}else{[Environment]::GetFolderPath([Environment+SpecialFolder]::LocalApplicationData)}
    $cache=New-GnBootstrapPrivateDirectory (Join-Path $cacheBase ('git-next-bootstrap-'+[Guid]::NewGuid().ToString('N'))) $selection.scope
    $package=Join-Path $cache 'git-next.msi'
    $preparedTls=$null
    $newTransaction=$null
    $originalBinding=$null
    try {
        if ($PackagePath) {
            if ($ExpectedSha256 -cnotmatch '^[a-f0-9]{64}$') { throw 'Local MSI installation requires -ExpectedSha256 from trusted release metadata.' }
            Copy-Item -LiteralPath ([IO.Path]::GetFullPath($PackagePath)) -Destination $package
            if ((Get-FileHash -LiteralPath $package -Algorithm SHA256).Hash.ToLowerInvariant() -cne $ExpectedSha256) { throw 'Local MSI checksum mismatch.' }
            $verifiedMsiSha256=$ExpectedSha256
        } else {
            $release=Get-GnBootstrapRelease $selection.platform $selection.scope $Version
            Save-GnBootstrapPackage $release $package
            $verifiedMsiSha256=$release.sha256
        }
        $preparedTls=$null
        if ($selection.original_root) {
            $pin=Get-GnBootstrapTlsPin $selection.platform
            $preparedTls=Join-Path $cache $pin.name
            if ($TlsArchive) {
                Copy-Item -LiteralPath ([IO.Path]::GetFullPath($TlsArchive)) -Destination $preparedTls
                if ((Get-Item -LiteralPath $preparedTls).Length -ne $pin.size -or (Get-FileHash -LiteralPath $preparedTls -Algorithm SHA256).Hash.ToLowerInvariant() -cne $pin.sha256) { throw 'Offline TLS component checksum mismatch.' }
            } elseif ($PackagePath) {
                throw 'Offline takeover requires -TlsArchive for the pinned independent OpenSSL compatibility component. No installation has started.'
            } else {
                Write-Host 'Preparing the pinned independent OpenSSL compatibility component before changing the installation.'
                Save-GnBootstrapPackage $pin $preparedTls
            }
        }
        $targetVersion=Get-GnBootstrapMsiVersion $package
        if($Version -and $targetVersion -cne $Version){throw 'The verified MSI version differs from the requested version.'}
        if($selection.original_root){
            $support=Expand-GnBootstrapSupport $package $verifiedMsiSha256 (Join-Path $cache 'preinstall') $selection $targetVersion
            $originalBinding=New-GnBootstrapOriginalSnapshot $selection $support (Join-Path $cache 'original-snapshot') (Join-Path $cache 'preinstall-binding.json')
        }
        if((Get-GnBootstrapFileSha256 $package) -cne $verifiedMsiSha256){throw 'The verified MSI changed before installation.'}
        $msiExit=Invoke-GnBootstrapMsi $package (Join-Path $cache 'install.log') $selection.root
        $manifest=Get-Content -LiteralPath (Join-Path $selection.root 'package.json') -Raw | ConvertFrom-Json
        if($originalBinding -and (Get-GnBootstrapFileSha256 (Join-Path $selection.root 'package.json')) -cne $originalBinding.candidate_package_sha256){throw 'The installed package differs from the protected pre-MSI candidate.'}
        if ($manifest.product -cne 'git-next' -or $manifest.platform -cne $selection.platform -or $manifest.install_scope -cne $selection.scope -or -not $manifest.release_eligible -or -not $manifest.complete_git_parity) { throw 'An accepted complete native MSI is required before Git takeover.' }
        try { Assert-GnBootstrapPayload $selection $manifest $targetVersion }
        catch { if ($msiExit -eq 3010) { throw 'Windows Installer requires a restart and new payload activation is not verified. Restart Windows and rerun; original Git was not removed by this invocation.' };throw }
        # The verified payload now occupies the official Git directory, so the legacy layout and
        # the superseded bridge forwarders must not survive it.
        Remove-GnBootstrapLegacyInstall $selection
        if ($selection.original_root) {
            [void](Get-GnBootstrapTerminalVersion (Join-Path $selection.root 'bin/git-terminal.exe'))
            if(-not $originalBinding){throw 'The original Git must be snapshotted before MSI installation.'}
            $handoff=Invoke-GnBootstrapApply $selection $preparedTls $originalBinding
            if ($handoff.status -cne 'active' -or -not $handoff.transaction) { throw 'Takeover did not return a recoverable transaction.' }
            $newTransaction=[string]$handoff.transaction
        }
        $command=Complete-GnBootstrap $selection $newTransaction
        [pscustomobject]@{product='git-next';status='installed';version=$manifest.version;platform=$selection.platform;scope=$selection.scope;git=$command.Source;terminal=Join-Path $selection.root 'bin\git-terminal.exe';native_lfs=$true;repositories_modified=$false;credential_vault_exported=$false;log_directory=$cache} | ConvertTo-Json
    } catch {
        if($originalBinding){throw ('Installation did not complete. The protected pre-MSI original-Git snapshot is retained at '+$originalBinding.snapshot_path+' (SHA256 '+$originalBinding.snapshot_sha256+'). '+$_.Exception.Message)}
        throw
    } finally {
        if ([IO.File]::Exists($package)) { [IO.File]::Delete($package) }
        if ($preparedTls -and [IO.File]::Exists($preparedTls)) { [IO.File]::Delete($preparedTls) }
    }
}
# Dot sourcing exposes pure planning functions for the isolated acceptance harness.
if ($MyInvocation.InvocationName -ne '.') { Invoke-GnBootstrapMain }
