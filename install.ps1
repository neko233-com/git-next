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
function Get-GnBootstrapSelection([string]$RequestedScope) {
    $platform = Get-GnBootstrapArchitecture
    $programFiles=if($env:ProgramW6432){$env:ProgramW6432}else{$env:ProgramFiles}
    $registrations = @(Get-GnBootstrapRegistrations | Sort-Object scope,root -Unique)
    $selectedScope = $RequestedScope
    if ($selectedScope -eq 'auto') {
        $machineInstalled = [IO.File]::Exists((Join-Path $programFiles ('git-next\'+$platform+'\package.json')))
        $selectedScope = if ($machineInstalled -or @($registrations | Where-Object scope -eq 'machine').Count) { 'machine' } else { 'user' }
    }
    $original = @($registrations | Where-Object scope -eq $selectedScope)
    if ($original.Count -gt 1) { throw 'More than one registered Git installation matches the selected scope.' }
    if (-not $original.Count -and $registrations.Count) { throw 'The requested scope differs from the registered Git installation. Use -Scope auto.' }
    $prefix = if ($selectedScope -eq 'machine') { $programFiles } else { Join-Path $env:LOCALAPPDATA 'Programs' }
    [pscustomobject]@{platform=$platform;scope=$selectedScope;root=Join-Path $prefix ($(if($selectedScope -eq 'machine'){'git-next'}else{'git-next-msi'})+'\'+$platform);original_root=$(if($original.Count){$original[0].root}else{$null})}
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
function Invoke-GnBootstrapMsi([string]$Package,[string]$Log) {
    $process=Start-Process -FilePath (Join-Path $env:SystemRoot 'System32\msiexec.exe') -ArgumentList ('/i "'+$Package+'" /qn /norestart /L*v "'+$Log+'"') -WindowStyle Hidden -Wait -PassThru
    if ($process.ExitCode -notin @(0,3010)) { throw ('MSI installation failed ('+$process.ExitCode+'). Log: '+$Log) }
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
function Assert-GnBootstrapPayload($Selection,$Manifest,[string]$TargetVersion) {
    if ($Manifest.version -cne $TargetVersion) { throw 'The installed manifest does not match the selected MSI version.' }
    Import-Module (Join-Path $Selection.root 'updater\Update.Core.psm1') -Force -DisableNameChecking
    Assert-GnInstalledFiles $Selection.root $Manifest
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
    $terminalIdentity=(& $terminal --version) -join "`n"
    if ($LASTEXITCODE -ne 0 -or $terminalIdentity -notmatch '^git-terminal ') { throw 'Native Git-Terminal verification failed.' }
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
    $selection=Get-GnBootstrapSelection $Scope
    if ($Plan) { $selection | Add-Member NoteProperty changes_applied $false -PassThru | ConvertTo-Json; return }
    $principal=New-Object Security.Principal.WindowsPrincipal([Security.Principal.WindowsIdentity]::GetCurrent())
    $elevated=$principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
    if ($selection.scope -eq 'machine' -and -not $elevated) {
        $arguments=@{Scope=$Scope}
        foreach($name in @('Version','PackagePath','ExpectedSha256','TlsArchive')) { $value=Get-Variable -Name $name -ValueOnly; if($value){$arguments[$name]=[string]$value} }
        $json=$arguments | ConvertTo-Json -Compress
        $encoded=[Convert]::ToBase64String([Text.Encoding]::UTF8.GetBytes($json))
        $resultPath=[IO.Path]::GetTempFileName()
        $resultLiteral=$resultPath.Replace("'","''")
        $scriptPath=$PSCommandPath.Replace("'","''")
        $scriptSha=(Get-FileHash -LiteralPath $PSCommandPath -Algorithm SHA256).Hash
        $code='$ErrorActionPreference="Stop"; $p=' + "'"+$scriptPath+"'" + '; if((Get-FileHash -LiteralPath $p -Algorithm SHA256).Hash -cne "'+$scriptSha+'") { throw "Installer changed before elevation." }; $a=@{}; ([Text.Encoding]::UTF8.GetString([Convert]::FromBase64String("'+$encoded+'")) | ConvertFrom-Json).PSObject.Properties | ForEach-Object { $a[$_.Name]=$_.Value }; try { $value=(& $p @a | Out-String); [IO.File]::WriteAllText(' + "'"+$resultLiteral+"'" + ',$value,[Text.UTF8Encoding]::new($false)) } catch { $value=[pscustomobject]@{status="failed";message=$_.Exception.Message}|ConvertTo-Json; [IO.File]::WriteAllText(' + "'"+$resultLiteral+"'" + ',$value,[Text.UTF8Encoding]::new($false)); exit 1 }'
        $command=[Convert]::ToBase64String([Text.Encoding]::Unicode.GetBytes($code))
        $nativeShell=Join-Path $env:SystemRoot 'System32\WindowsPowerShell\v1.0\powershell.exe'
        if (-not [Environment]::Is64BitProcess -and [Environment]::Is64BitOperatingSystem) { $nativeShell=Join-Path $env:SystemRoot 'Sysnative\WindowsPowerShell\v1.0\powershell.exe' }
        try {
            $child=Start-Process -FilePath $nativeShell -Verb RunAs -WindowStyle Hidden -ArgumentList ('-NoProfile -NonInteractive -ExecutionPolicy Bypass -EncodedCommand '+$command) -Wait -PassThru
            $result=Get-Content -LiteralPath $resultPath -Raw
            if ($child.ExitCode) { $failure=$result | ConvertFrom-Json; throw ('Elevated installation failed. '+$failure.message) }
            if (-not $result) { throw 'Elevated installation returned no verified status.' }
            Write-Output $result
        } finally { if([IO.File]::Exists($resultPath)){[IO.File]::Delete($resultPath)} }
        return
    }
    $cacheBase=if($selection.scope -eq 'machine'){$env:ProgramData}else{$env:LOCALAPPDATA}
    $cache=Join-Path $cacheBase ('git-next-bootstrap-'+[Guid]::NewGuid().ToString('N'))
    [void][IO.Directory]::CreateDirectory($cache)
    if ($selection.scope -eq 'machine') {
        $acl=New-Object Security.AccessControl.DirectorySecurity
        $acl.SetSecurityDescriptorSddlForm('O:BAG:BAD:P(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)')
        Set-Acl -LiteralPath $cache -AclObject $acl
    }
    $package=Join-Path $cache 'git-next.msi'
    $preparedTls=$null
    $newTransaction=$null
    try {
        if ($PackagePath) {
            if ($ExpectedSha256 -cnotmatch '^[a-f0-9]{64}$') { throw 'Local MSI installation requires -ExpectedSha256 from trusted release metadata.' }
            Copy-Item -LiteralPath ([IO.Path]::GetFullPath($PackagePath)) -Destination $package
            if ((Get-FileHash -LiteralPath $package -Algorithm SHA256).Hash.ToLowerInvariant() -cne $ExpectedSha256) { throw 'Local MSI checksum mismatch.' }
        } else {
            $release=Get-GnBootstrapRelease $selection.platform $selection.scope $Version
            Save-GnBootstrapPackage $release $package
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
        $msiExit=Invoke-GnBootstrapMsi $package (Join-Path $cache 'install.log')
        $manifest=Get-Content -LiteralPath (Join-Path $selection.root 'package.json') -Raw | ConvertFrom-Json
        if ($manifest.product -cne 'git-next' -or $manifest.platform -cne $selection.platform -or $manifest.install_scope -cne $selection.scope -or -not $manifest.release_eligible -or -not $manifest.complete_git_parity) { throw 'An accepted complete native MSI is required before Git takeover.' }
        try { Assert-GnBootstrapPayload $selection $manifest $targetVersion }
        catch { if ($msiExit -eq 3010) { throw 'Windows Installer requires a restart and new payload activation is not verified. Restart Windows and rerun; original Git was not removed by this invocation.' };throw }
        if ($selection.original_root) {
            $handoff=(& (Join-Path $selection.root 'migration\takeover.ps1') -Mode Apply -OriginalGitRoot $selection.original_root -TlsArchive $preparedTls | Out-String) | ConvertFrom-Json
            if ($handoff.status -cne 'active' -or -not $handoff.transaction) { throw 'Takeover did not return a recoverable transaction.' }
            $newTransaction=[string]$handoff.transaction
        }
        $command=Complete-GnBootstrap $selection $newTransaction
        [pscustomobject]@{product='git-next';status='installed';version=$manifest.version;platform=$selection.platform;scope=$selection.scope;git=$command.Source;terminal=Join-Path $selection.root 'bin\git-terminal.exe';native_lfs=$true;repositories_modified=$false;credential_vault_exported=$false;log_directory=$cache} | ConvertTo-Json
    } finally {
        if ([IO.File]::Exists($package)) { [IO.File]::Delete($package) }
        if ($preparedTls -and [IO.File]::Exists($preparedTls)) { [IO.File]::Delete($preparedTls) }
    }
}
# Dot sourcing exposes pure planning functions for the isolated acceptance harness.
if ($MyInvocation.InvocationName -ne '.') { Invoke-GnBootstrapMain }
