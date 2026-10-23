# Copyright 2026 git-next contributors. MIT license.
param([string]$Output,[string]$PackagePath,[string]$TerminalPath)
$ErrorActionPreference='Stop'
$bootstrapFixturePackage=$PackagePath
$bootstrapFixtureTerminal=$TerminalPath
$root=[IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))
. (Join-Path $root 'install.ps1')
$results=@()
function Assert([bool]$Condition,[string]$Message){if(-not $Condition){throw $Message}}
function Invoke-BootstrapTestProcess([string]$Executable,[string]$Code) {
    $start=[Diagnostics.ProcessStartInfo]::new();$start.FileName=$Executable
    $start.Arguments='-NoProfile -NonInteractive -ExecutionPolicy Bypass -EncodedCommand '+[Convert]::ToBase64String([Text.Encoding]::Unicode.GetBytes($Code))
    $start.UseShellExecute=$false;$start.CreateNoWindow=$true;$start.RedirectStandardOutput=$true;$start.RedirectStandardError=$true
    $process=[Diagnostics.Process]::new();$process.StartInfo=$start
    try{
        Assert $process.Start() 'Isolated bootstrap PowerShell did not start'
        $stdout=$process.StandardOutput.ReadToEndAsync();$stderr=$process.StandardError.ReadToEndAsync()
        if(-not $process.WaitForExit(30000)){$process.Kill();throw 'Isolated bootstrap PowerShell timed out'}
        return [pscustomobject]@{exit_code=$process.ExitCode;stdout=$stdout.GetAwaiter().GetResult();stderr=$stderr.GetAwaiter().GetResult()}
    }finally{$process.Dispose()}
}
function Invoke-BootstrapRelayContracts {
    $fixture=New-GnBootstrapPrivateDirectory (Join-Path ([Environment]::GetFolderPath('LocalApplicationData')) ('git-next-relay-test-'+[Guid]::NewGuid().ToString('N'))) 'user'
    try{
        $script=Join-Path $fixture 'literal-arguments.ps1'
        [IO.File]::WriteAllText($script,'param([string]$Scope,[string]$Version,[string]$PackagePath,[string]$ExpectedSha256,[string]$TlsArchive,[switch]$Plan) @{Scope=$Scope;Version=$Version;PackagePath=$PackagePath;ExpectedSha256=$ExpectedSha256;TlsArchive=$TlsArchive;Plan=[bool]$Plan} | ConvertTo-Json -Compress',[Text.UTF8Encoding]::new($false))
        $unicode=[string][char]0x4E2D+[char]0x6587
        $arguments=@{Scope='user';Version=($unicode+' " '' ; $(throw literal)');PackagePath=('C:\space '+$unicode+'\quoted''file.msi');ExpectedSha256=('a'*64);TlsArchive='C:\a & b\tls.tar.bz2';Plan=$true}
        $shell=Join-Path $env:SystemRoot ($(if([Environment]::Is64BitProcess){'System32'}else{'Sysnative'})+'\WindowsPowerShell\v1.0\powershell.exe')
        $actual=Invoke-BootstrapTestProcess $shell (Get-GnBootstrapRelayCode $script (Get-GnBootstrapFileSha256 $script) $arguments)
        Assert ($actual.exit_code -eq 0) ('Native relay failed: '+$actual.stderr)
        $parsed=$actual.stdout|ConvertFrom-Json
        foreach($key in $arguments.Keys){Assert ($parsed.PSObject.Properties[$key].Value -ceq $arguments[$key]) ('Relay changed literal argument '+$key)}
        $bad=Invoke-BootstrapTestProcess $shell (Get-GnBootstrapRelayCode $script ('0'*64) $arguments)
        Assert ($bad.exit_code -ne 0 -and -not $bad.stdout.Trim() -and $bad.stderr.Contains('Installer changed')) 'Relay accepted a script with the wrong pre-forward digest'
        [pscustomobject]@{case='native-relay-preserves-literal-arguments-and-rejects-script-drift';status='pass'}
        $wow=Join-Path $env:SystemRoot 'SysWOW64\WindowsPowerShell\v1.0\powershell.exe'
        Assert ([IO.File]::Exists($wow)) 'The actual 32-bit PowerShell forwarding fixture is unavailable'
        $installer=(Join-Path $root 'install.ps1').Replace("'","''")
        $code='$ErrorActionPreference="Stop"; if([Environment]::Is64BitProcess){throw "Expected actual 32-bit fixture."}; . '''+$installer+'''; function Get-GnBootstrapSelection {throw "32-bit selection ran before native relay."}; $Scope="auto"; $Plan=$true; Invoke-GnBootstrapMain'
        $actual=Invoke-BootstrapTestProcess $wow $code
        Assert ($actual.exit_code -eq 0) ('Actual 32-bit Plan forwarding failed: '+$actual.stderr)
        $planResult=$actual.stdout|ConvertFrom-Json
        Assert ($planResult.changes_applied -eq $false -and $planResult.platform -in @('windows-x64','windows-arm64')) 'Actual 32-bit Plan forwarding did not preserve its non-mutating result'
        [pscustomobject]@{case='actual-32-bit-plan-relays-before-selection-without-elevation';status='pass'}
    }finally{
        Assert ([IO.Path]::GetFullPath($fixture).StartsWith([Environment]::GetFolderPath('LocalApplicationData')+'\',[StringComparison]::OrdinalIgnoreCase)) 'Unsafe relay fixture cleanup path'
        Remove-Item -LiteralPath $fixture -Recurse -Force
    }
}
function Invoke-BootstrapSupportContracts {
    $fixture=New-GnBootstrapPrivateDirectory (Join-Path ([Environment]::GetFolderPath('LocalApplicationData')) ('git-next-support-test-'+[Guid]::NewGuid().ToString('N'))) 'user'
    try{
        $paths=@(Get-GnBootstrapSupportPaths);$files=@{};$fixturePaths=@{};$fixtureBytes=@{};$index=0
        foreach($path in $paths){
            if($path -ceq 'package.json'){continue}
            $bytes=[Text.Encoding]::UTF8.GetBytes('fixture: '+$path);$file=Join-Path $fixture ('source-'+$index)
            [IO.File]::WriteAllBytes($file,$bytes);$files[$path]=Get-GnBootstrapFileSha256 $file;$fixturePaths[$path]=$file;$fixtureBytes[$path]=$bytes.Length;$index++
        }
        $manifest=[ordered]@{schema_version=1;product='git-next';install_kind='msi';version='1.0.3';platform='windows-x64';install_scope='user';installation_layout='git-default-v2';release_eligible=$true;complete_git_parity=$true;files=$files}
        $manifestPath=Join-Path $fixture 'fixture-package.json';[IO.File]::WriteAllText($manifestPath,($manifest|ConvertTo-Json -Depth 5),[Text.UTF8Encoding]::new($false))
        $fixturePaths['package.json']=$manifestPath;$fixtureBytes['package.json']=(Get-Item -LiteralPath $manifestPath).Length
        $msi=Join-Path $fixture 'fixture.msi';[IO.File]::WriteAllText($msi,'only a fixture');$msiHash=Get-GnBootstrapFileSha256 $msi
        $selection=[pscustomobject]@{scope='user';platform='windows-x64'}
        $fixtureDuplicate=$false;$fixtureExternal=$false
        function Get-GnBootstrapMsiRows([string]$Package,[string]$Sql){
            if($Sql.Contains('FROM `Directory`')){foreach($row in @(@('INSTALLFOLDER','TARGETDIR','Git'),@('MIG','INSTALLFOLDER','migration'),@('UPD','INSTALLFOLDER','updater'))){[pscustomobject]@{values=$row}}}
            elseif($Sql.Contains('FROM `Component`')){foreach($path in $paths){[pscustomobject]@{values=@(('C'+[array]::IndexOf($paths,$path)),$(if($path.StartsWith('migration/')){'MIG'}elseif($path.StartsWith('updater/')){'UPD'}else{'INSTALLFOLDER'}))}}}
            elseif($Sql.Contains('FROM `Media`')){[pscustomobject]@{values=@('1','100',$(if($fixtureExternal){'outside.cab'}else{'#inside.cab'}))}}
            elseif($Sql.Contains('FROM `File`')){foreach($path in $paths){$i=[array]::IndexOf($paths,$path);[pscustomobject]@{values=@(('F'+$i),('C'+$i),($path -split '/')[-1],[string]$fixtureBytes[$path],[string]($i+1))}};if($fixtureDuplicate){[pscustomobject]@{values=@('duplicate','C0','PACKAGE.JSON',[string]$fixtureBytes['package.json'],'99')}}}
            else{throw 'Unexpected bootstrap MSI query'}
        }
        function Export-GnBootstrapCabinet([string]$Package,[string]$Name,[string]$Destination){[IO.File]::WriteAllText($Destination,'bounded fixture cabinet')}
        function Expand-GnBootstrapCabinetMember([string]$Cabinet,[string]$Id,[string]$Directory){$path=$paths[[int]$Id.Substring(1)];$script:bootstrapExtractedPaths+=,$path;$target=Join-Path $Directory $Id;[IO.File]::Copy($fixturePaths[$path],$target);return $target}
        $script:bootstrapExtractedPaths=@()
        $support=Expand-GnBootstrapSupport $msi $msiHash (Join-Path $fixture 'valid') $selection '1.0.3'
        Assert (@([IO.Directory]::GetFiles($support.root,'*',[IO.SearchOption]::AllDirectories)).Count -eq 8 -and $script:bootstrapExtractedPaths.Count -eq 8) 'Bootstrap extracted more than the exact package plus seven-module allowlist'
        Assert ($support.package_sha256 -ceq (Get-GnBootstrapFileSha256 $manifestPath)) 'Preinstall support lost its exact candidate manifest digest'
        [pscustomobject]@{case='embedded-cabinet-extracts-only-eight-allowlisted-members-with-module-pins';status='pass'}
        $fixtureDuplicate=$true;$rejected=$false;try{Get-GnBootstrapSupportMap $msi|Out-Null}catch{$rejected=$true};Assert $rejected 'Case-ambiguous bootstrap member was accepted';$fixtureDuplicate=$false
        $fixtureExternal=$true;$rejected=$false;try{Get-GnBootstrapSupportMap $msi|Out-Null}catch{$rejected=$true};Assert $rejected 'External MSI cabinet was accepted';$fixtureExternal=$false
        $rejected=$false;try{Expand-GnBootstrapSupport $msi ('0'*64) (Join-Path $fixture 'bad-msi') $selection '1.0.3'|Out-Null}catch{$rejected=$true};Assert ($rejected -and -not [IO.Directory]::Exists((Join-Path $fixture 'bad-msi'))) 'Unpinned MSI started support extraction'
        [IO.File]::AppendAllText($fixturePaths['migration/takeover.ps1'],'tamper');$fixtureBytes['migration/takeover.ps1']=(Get-Item -LiteralPath $fixturePaths['migration/takeover.ps1']).Length
        $rejected=$false;try{Expand-GnBootstrapSupport $msi $msiHash (Join-Path $fixture 'bad-module') $selection '1.0.3'|Out-Null}catch{$rejected=$_.Exception.Message.Contains('checksum mismatch')};Assert $rejected 'A module differing from package.files was accepted'
        [pscustomobject]@{case='preinstall-extraction-rejects-ambiguous-path-external-cabinet-and-digest-drift';status='pass'}
        $rejected=$false;try{New-GnBootstrapPrivateDirectory (Join-Path $fixture 'wrong-machine-root') 'machine'|Out-Null}catch{$rejected=$true};Assert $rejected 'A writable user path was accepted as a machine private-cache parent'
        function Test-BootstrapUnsafeMachineParent {
            $target=Join-Path ([Environment]::GetFolderPath('CommonApplicationData')) ('git-next-bootstrap-forbidden-test-'+[Guid]::NewGuid().ToString('N'))
            function Get-Acl {
                param([string]$LiteralPath)
                $acl=New-Object Security.AccessControl.DirectorySecurity
                $acl.SetOwner([Security.Principal.SecurityIdentifier]::new('S-1-5-32-544'))
                $acl.AddAccessRule([Security.AccessControl.FileSystemAccessRule]::new([Security.Principal.SecurityIdentifier]::new('S-1-1-0'),[Security.AccessControl.FileSystemRights]::DeleteSubdirectoriesAndFiles,[Security.AccessControl.AccessControlType]::Allow))
                return $acl
            }
            $rejected=$false;try{New-GnBootstrapPrivateDirectory $target 'machine'|Out-Null}catch{$rejected=$_.Exception.Message.Contains('replacement permission')}
            Assert ($rejected -and -not [IO.Directory]::Exists($target)) 'A native common-data parent granting Everyone DELETE_CHILD was accepted'
        }
        Test-BootstrapUnsafeMachineParent
        [pscustomobject]@{case='machine-cache-requires-native-common-data-and-protected-ancestors';status='pass'}
    }finally{
        Assert ([IO.Path]::GetFullPath($fixture).StartsWith([Environment]::GetFolderPath('LocalApplicationData')+'\',[StringComparison]::OrdinalIgnoreCase)) 'Unsafe support fixture cleanup path'
        Remove-Item -LiteralPath $fixture -Recurse -Force
    }
}
function Invoke-BootstrapPreinstallOrderingContracts {
    Assert ([Environment]::Is64BitProcess) 'Run the bootstrap acceptance controller in native 64-bit PowerShell; its separate fixture covers actual 32-bit Plan forwarding'
    $script:bootstrapOrderCache=$null
    $fixture=New-GnBootstrapPrivateDirectory (Join-Path ([Environment]::GetFolderPath('LocalApplicationData')) ('git-next-order-test-'+[Guid]::NewGuid().ToString('N'))) 'user'
    try{
        $owned=Join-Path $fixture 'original';[void][IO.Directory]::CreateDirectory((Join-Path $owned 'cmd'));[void][IO.Directory]::CreateDirectory((Join-Path $owned 'bin'));[void][IO.Directory]::CreateDirectory((Join-Path $owned 'migration'))
        [IO.File]::WriteAllText((Join-Path $owned 'cmd/git.exe'),'original command bytes');[IO.File]::WriteAllText((Join-Path $owned 'bin/git.exe'),'original bin bytes')
        $candidate=Join-Path $fixture 'candidate.json';[IO.File]::WriteAllText($candidate,'{"product":"git-next","platform":"windows-x64","install_scope":"user","release_eligible":true,"complete_git_parity":true,"version":"1.0.3"}')
        $localMsi=Join-Path $fixture 'candidate.msi';[IO.File]::WriteAllText($localMsi,'fixture MSI only')
        $tls=Join-Path $fixture 'candidate.tls';[IO.File]::WriteAllText($tls,'fixture TLS only')
        $selection=[pscustomobject]@{scope='user';platform='windows-x64';root=$owned;original_root=$owned;legacy_root=$null}
        $support=[pscustomobject]@{root=$fixture;manifest_path=$candidate;package_sha256=(Get-GnBootstrapFileSha256 $candidate);msi_sha256=(Get-GnBootstrapFileSha256 $localMsi)}
        function Get-GnBootstrapSelection([string]$RequestedScope){return $selection}
        function Get-GnBootstrapTlsPin([string]$Platform){[pscustomobject]@{name='tls.bin';size=(Get-Item -LiteralPath $tls).Length;sha256=(Get-GnBootstrapFileSha256 $tls)}}
        function Get-GnBootstrapMsiVersion([string]$Package){'1.0.3'}
        function Expand-GnBootstrapSupport([string]$Package,[string]$ExpectedMsiSha256,[string]$Destination,$Selection,[string]$TargetVersion){$script:bootstrapOrderCache=[IO.Path]::GetDirectoryName($Destination);return $support}
        function Import-Module {param($Name,[switch]$Force,[switch]$DisableNameChecking)}
        function New-GnPreinstallSnapshot([string]$OriginalGitRoot,[string]$SnapshotRoot,[string]$CandidatePackagePath,[string]$CandidateMsiSha256){
            Assert (-not $script:bootstrapOrderMsiCalled) 'The snapshot was created after MSI execution'
            Assert ([IO.File]::ReadAllText((Join-Path $OriginalGitRoot 'cmd/git.exe')) -ceq 'original command bytes') 'Pre-MSI command bytes were already overwritten'
            Assert ([IO.File]::ReadAllText((Join-Path $OriginalGitRoot 'bin/git.exe')) -ceq 'original bin bytes') 'Pre-MSI bin bytes were already overwritten'
            [void][IO.Directory]::CreateDirectory($SnapshotRoot);$path=Join-Path $SnapshotRoot 'snapshot.json';[IO.File]::WriteAllText($path,'{"kind":"pre-msi-fixture"}')
            $script:bootstrapOrderSnapshotHash=Get-GnBootstrapFileSha256 $path;$script:bootstrapOrderSnapshotPath=$path;return $path
        }
        function Invoke-GnBootstrapMsi([string]$Package,[string]$Log,[string]$InstallRoot){
            Assert ([IO.File]::Exists($script:bootstrapOrderSnapshotPath)) 'MSI started without an original snapshot'
            $binding=[IO.File]::ReadAllText((Join-Path $script:bootstrapOrderCache 'preinstall-binding.json'))|ConvertFrom-Json
            Assert ($binding.snapshot_sha256 -ceq $script:bootstrapOrderSnapshotHash -and $binding.candidate_msi_sha256 -ceq $support.msi_sha256 -and $binding.candidate_package_sha256 -ceq $support.package_sha256) 'Pre-MSI reference did not pin the three digests'
            $script:bootstrapOrderMsiCalled=$true
            [IO.File]::WriteAllText((Join-Path $InstallRoot 'cmd/git.exe'),'new command bytes');[IO.File]::WriteAllText((Join-Path $InstallRoot 'bin/git.exe'),'new bin bytes');[IO.File]::Copy($candidate,(Join-Path $InstallRoot 'package.json'))
            return 0
        }
        function Assert-GnBootstrapPayload($Selection,$Manifest,[string]$TargetVersion){}
        function Remove-GnBootstrapLegacyInstall($Selection){}
        function Get-GnBootstrapTerminalVersion([string]$Terminal){'fixture terminal probe; no product was executed'}
        function Complete-GnBootstrap($Selection,[string]$NewTransaction){Assert ($NewTransaction -ceq 'fixture-transaction') 'The current snapshot transaction was lost';[pscustomobject]@{Source=(Join-Path $Selection.root 'cmd/git.exe')}}
        $takeover='param($Mode,$OriginalGitRoot,$TlsArchive,$OriginalSnapshot,$OriginalSnapshotSha256) @{mode=$Mode;snapshot=$OriginalSnapshot;sha256=$OriginalSnapshotSha256} | ConvertTo-Json | Set-Content -LiteralPath (Join-Path $PSScriptRoot "called.json"); @{status="active";transaction="fixture-transaction"}|ConvertTo-Json'
        [IO.File]::WriteAllText((Join-Path $owned 'migration/takeover.ps1'),$takeover)
        $Scope='user';$Plan=$false;$Version='1.0.3';$PackagePath=$localMsi;$ExpectedSha256=$support.msi_sha256;$TlsArchive=$tls
        $script:bootstrapOrderMsiCalled=$false;$actual=Invoke-GnBootstrapMain|ConvertFrom-Json
        Assert ($actual.status -ceq 'installed' -and $script:bootstrapOrderMsiCalled) 'The mocked MSI workflow did not complete'
        $called=[IO.File]::ReadAllText((Join-Path $owned 'migration/called.json'))|ConvertFrom-Json
        Assert ($called.mode -ceq 'Apply' -and $called.snapshot -ceq $script:bootstrapOrderSnapshotPath -and $called.sha256 -ceq $script:bootstrapOrderSnapshotHash) 'Post-MSI Apply did not receive the pre-MSI pinned snapshot digest'
        [pscustomobject]@{case='main-snapshots-original-cmd-and-bin-before-msi-and-pins-apply-reference';status='pass'}
        $binding=[IO.File]::ReadAllText((Join-Path $script:bootstrapOrderCache 'preinstall-binding.json'))|ConvertFrom-Json
        [IO.File]::AppendAllText($binding.snapshot_path,'drift')
        $rejected=$false;try{Invoke-GnBootstrapApply $selection $tls $binding|Out-Null}catch{$rejected=$_.Exception.Message.Contains('snapshot changed')};Assert $rejected 'Apply accepted a snapshot modified after MSI'
        [pscustomobject]@{case='post-msi-snapshot-drift-is-rejected-before-takeover';status='pass'}
    }finally{
        if($script:bootstrapOrderCache){Assert ([IO.Path]::GetFullPath($script:bootstrapOrderCache).StartsWith([Environment]::GetFolderPath('LocalApplicationData')+'\git-next-bootstrap-',[StringComparison]::OrdinalIgnoreCase)) 'Unsafe ordering cache cleanup';Remove-Item -LiteralPath $script:bootstrapOrderCache -Recurse -Force}
        Assert ([IO.Path]::GetFullPath($fixture).StartsWith([Environment]::GetFolderPath('LocalApplicationData')+'\',[StringComparison]::OrdinalIgnoreCase)) 'Unsafe ordering fixture cleanup'
        Remove-Item -LiteralPath $fixture -Recurse -Force
    }
}
$results+=@(Invoke-BootstrapRelayContracts)
$results+=@(Invoke-BootstrapSupportContracts)
$results+=@(Invoke-BootstrapPreinstallOrderingContracts)
if ($bootstrapFixtureTerminal) {
    $actual=@(Get-GnBootstrapTerminalVersion ([IO.Path]::GetFullPath($bootstrapFixtureTerminal)))
    Assert ($actual.Count -eq 1 -and $actual[0] -cmatch '^git-terminal [0-9]+\.[0-9]+\.[0-9]+$') 'Real GUI-subsystem EXE identity was not awaited and captured'
    $results+=@{case='real-gui-terminal-version-is-awaited-and-captured';status='pass';version=$actual[0]}
}
if ($bootstrapFixturePackage) {
    $actual=@(Get-GnBootstrapMsiVersion ([IO.Path]::GetFullPath($bootstrapFixturePackage)))
    Assert ($actual.Count -eq 1 -and $actual[0] -is [string] -and $actual[0] -cmatch '^[0-9]+\.[0-9]+\.[0-9]+$') 'Real MSI metadata emitted extra COM pipeline values'
    $results+=@{case='real-msi-version-is-exactly-one-scalar-string';status='pass';version=$actual[0]}
}
$originalArch=$env:PROCESSOR_ARCHITECTURE; $originalWow=$env:PROCESSOR_ARCHITEW6432
try {
    $env:PROCESSOR_ARCHITECTURE='x86';$env:PROCESSOR_ARCHITEW6432='ARM64'
    Assert ((Get-GnBootstrapArchitecture) -ceq 'windows-arm64') 'Native ARM64 selection failed'
    $env:PROCESSOR_ARCHITEW6432='AMD64'
    Assert ((Get-GnBootstrapArchitecture) -ceq 'windows-x64') 'Native x64 selection failed'
    $results+=@{case='native-architecture-from-32-bit-process';status='pass'}
} finally {$env:PROCESSOR_ARCHITECTURE=$originalArch;$env:PROCESSOR_ARCHITEW6432=$originalWow}
function Get-GnBootstrapRegistrations { [pscustomobject]@{product='Git';scope='machine';root='C:\Program Files\Git'} }
$selection=Get-GnBootstrapSelection 'auto'
Assert ($selection.scope -ceq 'machine' -and $selection.original_root -ceq 'C:\Program Files\Git') 'Registered machine Git was not selected'
Assert ($selection.root -ceq 'C:\Program Files\Git') 'Machine bootstrap did not target the official Git default directory'
Assert ($selection.root -ceq $selection.original_root) 'Registered official Git was not adopted in place'
$rejected=$false;try{Get-GnBootstrapSelection 'user' | Out-Null}catch{$rejected=$true}
Assert $rejected 'Scope mismatch was accepted'
$results+=@{case='registered-git-default-auto-replacement-and-scope-refusal';status='pass'}
function Get-GnBootstrapRegistrations { [pscustomobject]@{product='Git';scope='user';root=(Join-Path $env:LOCALAPPDATA 'Programs\Git')} }
$userSelection=Get-GnBootstrapSelection 'user'
Assert ($userSelection.root -ceq (Join-Path $env:LOCALAPPDATA 'Programs\Git')) 'Bootstrap user root differs from the git-default-v2 user layout'
$results+=@{case='bootstrap-user-prefix-matches-msi-layout';status='pass'}
$script:assetDigest='sha256:'+('a'*64)
function Invoke-RestMethod {
    param($Uri,$Headers,$TimeoutSec)
    Assert ($Uri -ceq 'https://api.github.com/repos/neko233-com/git-next/releases/latest') 'Untrusted release metadata origin'
    [pscustomobject]@{draft=$false;prerelease=$false;tag_name='v0.3.0';assets=@([pscustomobject]@{name='git-next-0.3.0-windows-x64.msi';browser_download_url='https://github.com/neko233-com/git-next/releases/download/v0.3.0/git-next-0.3.0-windows-x64.msi';digest=$script:assetDigest;size=123456})}
}
$release=Get-GnBootstrapRelease 'windows-x64' 'machine' ''
Assert ($release.sha256 -ceq ('a'*64) -and $release.version -ceq '0.3.0') 'Official asset identity not bound'
$script:assetDigest='sha256:broken'
$rejected=$false;try{Get-GnBootstrapRelease 'windows-x64' 'machine' '' | Out-Null}catch{$rejected=$true}
Assert $rejected 'Unverified package metadata was accepted'
$results+=@{case='official-stable-package-digest-required-before-mirror-download';status='pass'}
$catalog=Get-Content (Join-Path $root 'packaging/tls-providers.lock.json') -Raw | ConvertFrom-Json
foreach($platform in @('windows-x64','windows-arm64')){
    $pin=Get-GnBootstrapTlsPin $platform;$expected=$catalog.platforms.PSObject.Properties[$platform].Value
    Assert ($pin.sha256 -ceq $expected.archive_sha256 -and $pin.size -eq $expected.archive_size -and $pin.url -ceq $expected.archive_url) 'Offline TLS pin drift'
}
$results+=@{case='independent-tls-material-pinned-and-prepared-before-install';status='pass'}
$script:payloadVerified=$false
function Import-Module { param($Name,[switch]$Force,[switch]$DisableNameChecking) }
function Assert-GnInstalledFiles($Root,$Manifest) { $script:payloadVerified=$true }
$fakeManifest=[pscustomobject]@{version='0.3.0';installation_layout='git-default-v2'}
$rejected=$false;try { Assert-GnBootstrapPayload $selection $fakeManifest '0.4.0' } catch { $rejected=$true }
Assert ($rejected -and -not $script:payloadVerified) 'Old installed version was accepted after a pending upgrade'
Assert-GnBootstrapPayload $selection $fakeManifest '0.3.0'
Assert $script:payloadVerified 'Activated payload byte verification was not invoked'
$results+=@{case='target-version-and-immutable-payload-activation-required-before-takeover';status='pass'}
$legacyLayoutManifest=[pscustomobject]@{version='0.3.0';installation_layout='git-next-msi'}
$script:payloadVerified=$false
$rejected=$false;try { Assert-GnBootstrapPayload $selection $legacyLayoutManifest '0.3.0' } catch { $rejected=$true }
Assert ($rejected -and -not $script:payloadVerified) 'A payload without the git-default-v2 layout was activated'
$results+=@{case='git-default-v2-layout-is-mandatory-for-payload-activation';status='pass'}
$script:rollbackTransactions=@()
function Test-GnBootstrapInstallation($Selection) { throw 'injected final identity failure' }
function Undo-GnBootstrapTakeover($Selection,[string]$Transaction) { $script:rollbackTransactions+=$Transaction }
$rejected=$false;try { Complete-GnBootstrap $selection 'C:\protected\owned-transaction' | Out-Null } catch { $rejected=$_.Exception.Message.Contains('restored the original Git') }
Assert ($rejected -and $script:rollbackTransactions.Count -eq 1 -and $script:rollbackTransactions[0] -ceq 'C:\protected\owned-transaction') 'Final-check failure did not restore its own transaction'
try { Complete-GnBootstrap $selection $null | Out-Null } catch { }
Assert ($script:rollbackTransactions.Count -eq 1) 'Upgrade failure rolled back an unrelated prior transaction'
$results+=@{case='post-takeover-failure-restores-only-current-invocation';status='pass'}
function Undo-GnBootstrapTakeover($Selection,[string]$Transaction) { throw 'injected recovery conflict' }
$rejected=$false;try { Complete-GnBootstrap $selection 'C:\protected\owned-transaction' | Out-Null } catch { $rejected=$_.Exception.Message.Contains('rollback needs recovery') -and $_.Exception.Message.Contains('C:\protected\owned-transaction') }
Assert $rejected 'Recovery conflict was hidden'
$results+=@{case='rollback-conflict-reports-exact-protected-recovery-path';status='pass'}
$value=[ordered]@{status='pass';scope='bootstrap-planning-and-trust-contracts; actual MSI lifecycle and protected takeover are separate acceptance lanes';repositories_modified=$false;personal_credentials_accessed=$false;results=$results}
$json=$value | ConvertTo-Json -Depth 6
if($Output){[IO.File]::WriteAllText([IO.Path]::GetFullPath($Output),$json+[Environment]::NewLine,[Text.UTF8Encoding]::new($false))}
$json
