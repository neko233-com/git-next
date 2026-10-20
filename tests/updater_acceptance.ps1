# Copyright 2026 git-next contributors. MIT license.
$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest
$root = Split-Path -Parent $PSScriptRoot
Import-Module (Join-Path $root 'packaging/windows/Update.Core.psm1') -Force
Add-Type -AssemblyName System.Net.Http
Add-Type -ReferencedAssemblies System.Net.Http -TypeDefinition @'
using System;
using System.Collections.Generic;
using System.Net;
using System.Net.Http;
using System.Threading;
using System.Threading.Tasks;
public sealed class GitNextUpdateFixture : HttpMessageHandler {
    public sealed class Rule {
        public byte[] Body; public int Status, Delay; public string Redirect;
    }
    private readonly Dictionary<string, Rule> rules = new Dictionary<string, Rule>();
    public readonly List<string> Requests = new List<string>();
    public void Add(string method, string url, byte[] body, int status, int delay, string redirect) {
        rules[method + " " + url] = new Rule { Body = body, Status = status, Delay = delay, Redirect = redirect };
    }
    protected override async Task<HttpResponseMessage> SendAsync(HttpRequestMessage request, CancellationToken token) {
        string key = request.Method + " " + request.RequestUri.AbsoluteUri;
        lock (Requests) {
            if (request.Headers.Authorization != null || request.Headers.Contains("Cookie")) throw new InvalidOperationException("Credentials reached public update transport");
            Requests.Add(key);
        }
        Rule rule;
        if (!rules.TryGetValue(key, out rule)) return new HttpResponseMessage(HttpStatusCode.NotFound) { Content = new ByteArrayContent(new byte[0]) };
        if (rule.Delay > 0) await Task.Delay(rule.Delay, token);
        var response = new HttpResponseMessage((HttpStatusCode)rule.Status) { Content = new ByteArrayContent(rule.Body) };
        if (!String.IsNullOrEmpty(rule.Redirect)) response.Headers.Location = new Uri(rule.Redirect);
        return response;
    }
}
'@
$temporary = Join-Path ([IO.Path]::GetTempPath()) ('git-next-updater-' + [Guid]::NewGuid().ToString('N'))
[void][IO.Directory]::CreateDirectory($temporary)
$checks = [Collections.Generic.List[string]]::new()
$clients = [Collections.Generic.List[Net.Http.HttpClient]]::new()
function Check([string]$Name, [bool]$Condition) { if (-not $Condition) { throw ('Acceptance failed: ' + $Name) }; $checks.Add($Name) }
function Reject([string]$Name, [scriptblock]$Action) {
    $rejected = $false
    try { & $Action | Out-Null } catch { $rejected = $true }
    Check $Name $rejected
}
function Hash([byte[]]$Bytes) {
    $algorithm = [Security.Cryptography.SHA256]::Create()
    try { return [BitConverter]::ToString($algorithm.ComputeHash($Bytes)).Replace('-','').ToLowerInvariant() }
    finally { $algorithm.Dispose() }
}
$oldLanguage = $env:GIT_NEXT_LANGUAGE
$oldConfig = $env:GIT_NEXT_CONFIG_HOME
try {
    $env:GIT_NEXT_LANGUAGE = 'english'
    $env:GIT_NEXT_CONFIG_HOME = Join-Path $temporary 'config'
    Check 'stable version ordering' (Test-GnNewer '1.2.0' '1.1.9')
    Check 'stable replaces same preview' (Test-GnNewer '1.2.0' '1.2.0-dev')
    Check 'equal version is unchanged' (-not (Test-GnNewer '1.2.0' '1.2.0'))
    Check 'downgrade refused by ordering' (-not (Test-GnNewer '1.1.9' '1.2.0'))
    foreach ($version in @('v1.0.0','1.02.0','1.0','1.0.0+build','1.0.0/../../')) { Reject ('invalid version ' + $version) { ConvertTo-GnVersion $version } }
    Reject 'automatic preview rejected' { Test-GnNewer '2.0.0-dev' '1.0.0' }
    Reject 'path traversal rejected' { Assert-GnOwnedPath $temporary (Join-Path $temporary '../outside.msi') }
    $destination = Join-Path $temporary 'download.msi'
    Check 'owned file path accepted' ((Assert-GnOwnedPath $temporary $destination) -eq [IO.Path]::GetFullPath($destination))
    $statePath = Join-Path $temporary 'state.json'
    Write-GnJson $statePath @{schema_version=1;enabled=$true}
    Write-GnJson $statePath @{schema_version=1;enabled=$false}
    $stateBytes = [IO.File]::ReadAllBytes($statePath)
    Check 'atomic state replacement and LF' ((-not (Get-Content -LiteralPath $statePath -Raw | ConvertFrom-Json).enabled) -and $stateBytes[-1] -eq 10 -and $stateBytes -notcontains 13 -and $stateBytes[0] -eq 123)
    $env:GIT_NEXT_LANGUAGE = 'chinese'
    Check 'UTF-8 Chinese updater message' ((Get-GnMessage 'git-next is current.') -ne 'git-next is current.')
    Check 'unknown diagnostic preserved' ((Get-GnMessage 'untranslated-detail-{count}') -ceq 'untranslated-detail-{count}')
    $env:GIT_NEXT_LANGUAGE = 'english'
    $installed = [pscustomobject]@{version='1.0.0';platform='windows-x64';upgrade_code='{981F8880-E13B-4D22-A36E-A1B166601663}'}
    $payload = [byte[]]::new(8192)
    for ($i=0; $i -lt $payload.Length; $i++) { $payload[$i] = $i % 251 }
    $assetUrl = 'https://github.com/neko233-com/git-next/releases/download/v1.1.0/git-next-1.1.0-windows-x64.msi'
    $manifestUrl = 'https://github.com/neko233-com/git-next/releases/download/v1.1.0/git-next-update.json'
    $api = 'https://api.github.com/repos/neko233-com/git-next/releases/latest'
    $entry = @{asset='git-next-1.1.0-windows-x64.msi';sha256=(Hash $payload);size=$payload.Length;upgrade_code=$installed.upgrade_code;product_code='{00000000-0000-4000-8000-000000000001}';msi_version='1.1.0'}
    $manifest = @{schema_version=1;product='git-next';version='1.1.0';source_commit=('a' * 40);complete_git_parity=$true;platforms=@{'windows-x64'=$entry}}
    $manifestBytes = [Text.Encoding]::UTF8.GetBytes(($manifest | ConvertTo-Json -Depth 20 -Compress))
    $release = @{id=233;tag_name='v1.1.0';draft=$false;prerelease=$false;assets=@(
        @{name='git-next-update.json';size=$manifestBytes.Length;digest=('sha256:' + (Hash $manifestBytes));browser_download_url=$manifestUrl},
        @{name=$entry.asset;size=$payload.Length;digest=('sha256:' + $entry.sha256);browser_download_url=$assetUrl})}
    function Fixture($Release, [byte[]]$ManifestBytes) {
        $handler = [GitNextUpdateFixture]::new()
        $handler.Add('GET',$api,[Text.Encoding]::UTF8.GetBytes(($Release | ConvertTo-Json -Depth 20 -Compress)),200,0,$null)
        $handler.Add('GET',$manifestUrl,$ManifestBytes,200,0,$null)
        $client = [Net.Http.HttpClient]::new($handler)
        $clients.Add($client)
        return @{client=$client;handler=$handler}
    }
    $fixture = Fixture $release $manifestBytes
    $plan = Get-GnOfficialPlan $fixture.client $installed
    Check 'official metadata identifies exact stable update' ($plan.Newer -and $plan.Version -ceq '1.1.0' -and $plan.Url -ceq $assetUrl)
    Check 'manifest downloaded without a mirror' ($fixture.handler.Requests.Count -eq 2 -and $fixture.handler.Requests[1] -ceq ('GET ' + $manifestUrl))
    foreach ($kind in @('draft','prerelease','metadata-digest','asset-digest','asset-size','asset-url','duplicate-asset')) {
        $bad = $release | ConvertTo-Json -Depth 20 | ConvertFrom-Json
        switch ($kind) {
            'draft' { $bad.draft=$true }
            'prerelease' { $bad.prerelease=$true }
            'metadata-digest' { $bad.assets[0].digest='sha256:' + ('0' * 64) }
            'asset-digest' { $bad.assets[1].digest='sha256:' + ('0' * 64) }
            'asset-size' { $bad.assets[1].size=1 }
            'asset-url' { $bad.assets[1].browser_download_url='https://example.invalid/package.msi' }
            'duplicate-asset' { $bad.assets += $bad.assets[1] }
        }
        $badFixture = Fixture $bad $manifestBytes
        Reject ('reject release ' + $kind) { Get-GnOfficialPlan $badFixture.client $installed }
    }
    $redirectFixture = Fixture $release $manifestBytes
    $redirectFixture.handler.Add('GET',$api,[byte[]]::new(0),302,0,'https://example.invalid/stolen-metadata')
    Reject 'official metadata refuses outside redirect' { Get-GnOfficialPlan $redirectFixture.client $installed }
    Check 'outside redirect was never requested' ($redirectFixture.handler.Requests.Count -eq 1)
    $fallbackFixture = Fixture $release $manifestBytes
    $fallbackFixture.handler.Add('GET',$api,[byte[]]::new(0),403,0,$null)
    $fallbackFixture.handler.Add('GET','https://github.com/neko233-com/git-next/releases/latest/download/git-next-update.json',[byte[]]::new(0),302,0,$manifestUrl)
    $fallbackPlan = Get-GnOfficialPlan $fallbackFixture.client $installed
    Check 'API rate limit uses official stable release metadata' ($fallbackPlan.MetadataTrust -ceq 'official-release-https' -and $fallbackPlan.Url -ceq $assetUrl)
    Check 'metadata fallback never requested a mirror' (@($fallbackFixture.handler.Requests | Where-Object { $_ -match 'ghfast|ghproxy' }).Count -eq 0)
    $mirror = 'https://ghfast.top/' + $assetUrl
    $other = 'https://ghproxy.net/' + $assetUrl
    foreach ($spec in @(@($assetUrl,150),@($mirror,10),@($other,80))) {
        $fixture.handler.Add('HEAD',$spec[0],[byte[]]::new(0),200,$spec[1],$null)
    }
    $fixture.handler.Add('GET',$mirror,[byte[]]::new(8192),200,0,$null)
    $fixture.handler.Add('GET',$other,$payload,200,0,$null)
    $fixture.handler.Add('GET',$assetUrl,$payload,200,0,$null)
    $transport = Save-GnVerifiedPackage $fixture.client $plan $destination
    Check 'corrupt fast mirror falls through to verified payload' ($transport.AbsoluteUri -ceq $other -and (Hash ([IO.File]::ReadAllBytes($destination))) -ceq $entry.sha256)
    $downloadBefore = Hash ([IO.File]::ReadAllBytes($destination))
    Reject 'existing download is never overwritten' { Save-GnVerifiedPackage $fixture.client $plan $destination }
    Check 'existing download preserved' ((Hash ([IO.File]::ReadAllBytes($destination))) -ceq $downloadBefore)
    [IO.File]::Delete($destination)
    foreach ($url in @($assetUrl,$mirror,$other)) { $fixture.handler.Add('GET',$url,[byte[]]::new(1),200,0,$null) }
    Reject 'all corrupt transports reject before installation' { Save-GnVerifiedPackage $fixture.client $plan $destination }
    Check 'all failed downloads removed' (-not [IO.File]::Exists($destination))
    $fixture.handler.Add('GET',$api,[byte[]]::new(0),403,0,$null)
    Reject 'GitHub API limit fails closed' { Get-GnOfficialPlan $fixture.client $installed }
    Reject 'invalid MSI cannot execute' { Assert-GnMsi $statePath $entry }
    $packageRoot = Join-Path $temporary 'installed'
    [void][IO.Directory]::CreateDirectory((Join-Path $packageRoot 'bin'))
    $file = Join-Path $packageRoot 'bin/git-next.exe'
    [IO.File]::WriteAllBytes($file,$payload)
    $package = [pscustomobject]@{ schema_version=1;product='git-next';install_scope='user';files=[pscustomobject]@{'bin/git-next.exe'=$entry.sha256} }
    Assert-GnInstalledFiles $packageRoot $package
    Check 'installed payload hashes accepted' $true
    [IO.File]::WriteAllBytes($file,[byte[]]::new(1))
    Reject 'modified installed files reject update' { Assert-GnInstalledFiles $packageRoot $package }
    $configurationSource = Join-Path $temporary 'runtime-default.conf'
    $configurationTarget = Join-Path $temporary 'runtime-active.conf'
    [IO.File]::WriteAllBytes($configurationSource,$payload)
    & (Get-Module Update.Core) { param($source,$target,$digest) Copy-GnRuntimeConfiguration $source $target $digest 0 '' $false $null } $configurationSource $configurationTarget $entry.sha256
    Check 'absent runtime configuration retains null destination state' ((Hash ([IO.File]::ReadAllBytes($configurationTarget))) -ceq $entry.sha256)
    [IO.File]::WriteAllBytes($configurationTarget,[byte[]]::new(1))
    Reject 'concurrently edited runtime configuration is preserved' { & (Get-Module Update.Core) { param($source,$target,$digest) Copy-GnRuntimeConfiguration $source $target $digest 0 '' $false $digest } $configurationSource $configurationTarget $entry.sha256 }
    Check 'concurrent runtime edit bytes retained' (([IO.File]::ReadAllBytes($configurationTarget)).Length -eq 1)
    & (Get-Module Update.Core) {
        param($root,$payload,$payloadHash)
        $install = Join-Path $root 'configuration-install'
        $defaults = Join-Path $install 'runtime/defaults/etc'
        [void][IO.Directory]::CreateDirectory($defaults)
        [IO.File]::WriteAllBytes((Join-Path $defaults 'fixture.conf'),$payload)
        $manifestPath = Join-Path $install 'runtime/defaults/manifest.json'
        $manifest = @{schema_version=1;product='git-next';platform='windows-x64';version='1.0.0';files=@(@{target='runtime/etc/fixture.conf';source='runtime/defaults/etc/fixture.conf';sha256=$payloadHash;attributes=0})}
        [IO.File]::WriteAllText($manifestPath,($manifest | ConvertTo-Json -Depth 6),[Text.UTF8Encoding]::new($false))
        $package = [pscustomobject]@{schema_version=1;product='git-next';platform='windows-x64';version='1.0.0';install_scope='user';files=[pscustomobject]@{'runtime/defaults/manifest.json'=(Get-GnRuntimeConfigHash $manifestPath);'runtime/defaults/etc/fixture.conf'=$payloadHash}}
        $scopeFunction = (Get-Command Get-GnScopeContext).ScriptBlock
        $script:runtimeFixtureContext = [pscustomobject]@{scope='user';machine=$false;root=(Join-Path $root 'configuration-state');config=(Join-Path $root 'configuration-state/config');updates=(Join-Path $root 'configuration-state/updates')}
        Set-Item -Path Function:script:Get-GnScopeContext -Value { param($InstallRoot,$Package) return $script:runtimeFixtureContext }
        try {
            Initialize-GnRuntimeConfiguration $install $package
            $active = Join-Path $install 'runtime/etc/fixture.conf'
            if ((Get-GnRuntimeConfigHash $active) -cne $payloadHash) { throw 'Runtime initialization did not install its default.' }
            $transaction = Join-Path $script:runtimeFixtureContext.updates 'runtime-configuration-msi'
            Restore-GnRuntimeConfigTransaction $install $package $transaction
            Complete-GnRuntimeConfigTransaction $install $package $transaction
            if ([IO.File]::Exists($active)) { throw 'Runtime rollback retained a newly created default.' }
        } finally {
            Set-Item -Path Function:script:Get-GnScopeContext -Value $scopeFunction
            Remove-Variable -Name runtimeFixtureContext -Scope Script
        }
    } $temporary $payload $entry.sha256
    Check 'complete runtime initialization and rollback use isolated state' $true
    [pscustomobject]@{status='pass';scope='offline deterministic update transport/version/path/checksum contracts; no MSI lifecycle or real network/mirror performance acceptance';checks=$checks} | ConvertTo-Json -Depth 5
} finally {
    foreach ($client in $clients) { $client.Dispose() }
    $env:GIT_NEXT_LANGUAGE=$oldLanguage
    $env:GIT_NEXT_CONFIG_HOME=$oldConfig
    $resolved = [IO.Path]::GetFullPath($temporary)
    $allowed = [IO.Path]::GetFullPath([IO.Path]::GetTempPath()).TrimEnd('\') + '\git-next-updater-'
    if (-not $resolved.StartsWith($allowed,[StringComparison]::OrdinalIgnoreCase)) { throw 'Unexpected updater fixture cleanup path.' }
    Remove-Item -LiteralPath $resolved -Recurse -Force
}
