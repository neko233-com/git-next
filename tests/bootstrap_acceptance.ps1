# Copyright 2026 git-next contributors. MIT license.
param([string]$Output,[string]$PackagePath,[string]$TerminalPath)
$ErrorActionPreference='Stop'
$bootstrapFixturePackage=$PackagePath
$bootstrapFixtureTerminal=$TerminalPath
$root=[IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))
. (Join-Path $root 'install.ps1')
$results=@()
function Assert([bool]$Condition,[string]$Message){if(-not $Condition){throw $Message}}
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
$rejected=$false;try{Get-GnBootstrapSelection 'user' | Out-Null}catch{$rejected=$true}
Assert $rejected 'Scope mismatch was accepted'
$results+=@{case='registered-git-default-auto-replacement-and-scope-refusal';status='pass'}
function Get-GnBootstrapRegistrations { [pscustomobject]@{product='Git';scope='user';root=(Join-Path $env:LOCALAPPDATA 'Programs\Git')} }
$userSelection=Get-GnBootstrapSelection 'user'
Assert ($userSelection.root -ceq (Join-Path $env:LOCALAPPDATA ('Programs\git-next-msi\'+$userSelection.platform))) 'Bootstrap user prefix differs from the native user MSI'
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
$fakeManifest=[pscustomobject]@{version='0.3.0'}
$rejected=$false;try { Assert-GnBootstrapPayload $selection $fakeManifest '0.4.0' } catch { $rejected=$true }
Assert ($rejected -and -not $script:payloadVerified) 'Old installed version was accepted after a pending upgrade'
Assert-GnBootstrapPayload $selection $fakeManifest '0.3.0'
Assert $script:payloadVerified 'Activated payload byte verification was not invoked'
$results+=@{case='target-version-and-immutable-payload-activation-required-before-takeover';status='pass'}
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
