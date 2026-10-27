# Copyright 2026 git-next contributors. MIT license.
Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'
if ($PSVersionTable.PSEdition -eq 'Desktop') {
    $nativeModules = [IO.Path]::Combine($PSHOME,'Modules')
    $env:PSModulePath = $nativeModules + ';' + $env:PSModulePath
}
$script:Repository = 'neko233-com/git-next'
$script:MirrorPrefixes = @('https://ghfast.top/', 'https://ghproxy.net/')
. (Join-Path $PSScriptRoot 'Scope.Core.ps1')
. (Join-Path $PSScriptRoot 'Runtime.Configuration.ps1')

function Get-GnMessage([string]$English) {
    $selection = $env:GIT_NEXT_LANGUAGE
    if (-not $selection) {
        $configRoot = if ($env:GIT_NEXT_CONFIG_HOME) { $env:GIT_NEXT_CONFIG_HOME }
            elseif ($env:XDG_CONFIG_HOME) { Join-Path $env:XDG_CONFIG_HOME 'git-next' }
            else { Join-Path $(if ($env:HOME) { $env:HOME } else { $env:USERPROFILE }) '.config\git-next' }
        $settings = Join-Path $configRoot 'settings.json'
        if ([IO.File]::Exists($settings) -and ([IO.FileInfo]::new($settings)).Length -le 65536) {
            try {
                $saved = [IO.File]::ReadAllText($settings, [Text.Encoding]::UTF8) | ConvertFrom-Json
                if ($saved.schema_version -eq 1) { $selection = $saved.language }
            } catch { }
        }
    }
    if (-not $selection -or $selection -in @('auto','system')) {
        $selection = @($env:LC_ALL,$env:LC_MESSAGES,$env:LANGUAGE,$env:LANG | Where-Object { $_ } | Select-Object -First 1)
        if (-not $selection.Count) { $selection = [Globalization.CultureInfo]::CurrentUICulture.Name }
        else { $selection = [string]$selection[0] }
    }
    if ($selection -notmatch '^(chinese|zh(?:[-_: .]|$)|中文)') { return $English }
    if (-not (Get-Variable -Name ChineseMessages -Scope Script -ErrorAction SilentlyContinue)) {
        $script:ChineseMessages = [IO.File]::ReadAllText((Join-Path $PSScriptRoot 'messages.zh-CN.json'), [Text.Encoding]::UTF8) | ConvertFrom-Json
    }
    $translated = $script:ChineseMessages.PSObject.Properties[$English]
    if ($translated) { return $translated.Value }
    return $English
}

function Assert-GnOwnedPath([string]$Root, [string]$Path) {
    $rootPath = [IO.Path]::GetFullPath($Root).TrimEnd('\') + '\'
    $fullPath = [IO.Path]::GetFullPath($Path)
    if (-not $fullPath.StartsWith($rootPath, [StringComparison]::OrdinalIgnoreCase)) { throw 'Path escapes the git-next installation state.' }
    $cursor = [IO.DirectoryInfo]::new([IO.Path]::GetDirectoryName($fullPath))
    while ($cursor -and ($cursor.FullName.TrimEnd('\') + '\').StartsWith($rootPath, [StringComparison]::OrdinalIgnoreCase)) {
        if ($cursor.Exists -and ($cursor.Attributes -band [IO.FileAttributes]::ReparsePoint)) { throw 'Reparse points are not allowed in updater state.' }
        $cursor = $cursor.Parent
    }
    if ((Test-Path -LiteralPath $Root) -and ((Get-Item -LiteralPath $Root).Attributes -band [IO.FileAttributes]::ReparsePoint)) { throw 'Updater root is a reparse point.' }
    if ((Test-Path -LiteralPath $fullPath) -and ((Get-Item -LiteralPath $fullPath).Attributes -band [IO.FileAttributes]::ReparsePoint)) { throw 'Updater file is a reparse point.' }
    return $fullPath
}

function Write-GnJson([string]$Path, $Value) {
    Assert-GnScopedFile $Path
    $temporary = $Path + '.' + [Guid]::NewGuid().ToString('N') + '.new'
    $stream = Open-GnNewFile $temporary
    try { $bytes = [Text.UTF8Encoding]::new($false).GetBytes(($Value | ConvertTo-Json -Depth 20 -Compress) + "`n"); $stream.Write($bytes,0,$bytes.Length); $stream.Flush($true) }
    finally { $stream.Dispose() }
    try {
        if ([IO.File]::Exists($Path)) { [IO.File]::Replace($temporary, $Path, [NullString]::Value) }
        else { [IO.File]::Move($temporary, $Path) }
    } finally { if ([IO.File]::Exists($temporary)) { [IO.File]::Delete($temporary) } }
}

function ConvertTo-GnVersion([string]$Version) {
    if ($Version -notmatch '^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-([0-9A-Za-z.-]+))?$') { throw 'Invalid git-next release version.' }
    $parts = @([uint32]$Matches[1], [uint32]$Matches[2], [uint32]$Matches[3])
    $preview = if ($Matches.ContainsKey(4)) { $Matches[4] } else { '' }
    [pscustomobject]@{ Parts=$parts; Preview=$preview; Text=$Version }
}

function Test-GnNewer([string]$Candidate, [string]$Current) {
    $next = ConvertTo-GnVersion $Candidate
    $old = ConvertTo-GnVersion $Current
    if ($next.Preview) { throw 'Automatic updates require a stable release.' }
    for ($i = 0; $i -lt 3; $i++) {
        if ($next.Parts[$i] -ne $old.Parts[$i]) { return $next.Parts[$i] -gt $old.Parts[$i] }
    }
    return [bool]$old.Preview
}

function New-GnHttpClient {
    Add-Type -AssemblyName System.Net.Http
    $handler = [Net.Http.HttpClientHandler]::new()
    $handler.AllowAutoRedirect = $false
    $handler.UseCookies = $false
    $handler.UseDefaultCredentials = $false
    $client = [Net.Http.HttpClient]::new($handler)
    $client.Timeout = [TimeSpan]::FromSeconds(180)
    $client.DefaultRequestHeaders.UserAgent.ParseAdd('git-next-updater/1')
    return $client
}

function Invoke-GnRequest($Client, [Uri]$Uri, [bool]$Official, [string]$Method = 'GET', [int]$TimeoutSeconds = 180) {
    for ($redirect = 0; $redirect -le 8; $redirect++) {
        if ($Uri.Scheme -ne 'https' -or $Uri.UserInfo -or -not $Uri.IsDefaultPort) { throw 'Update transport requires public HTTPS without credentials.' }
        if ($Official -and $Uri.Host -notin @('api.github.com','github.com','release-assets.githubusercontent.com','objects.githubusercontent.com')) { throw 'Official update metadata redirected outside GitHub.' }
        $request = [Net.Http.HttpRequestMessage]::new([Net.Http.HttpMethod]::new($Method), $Uri)
        $cancel = [Threading.CancellationTokenSource]::new([TimeSpan]::FromSeconds($TimeoutSeconds))
        try { $response = $Client.SendAsync($request, [Net.Http.HttpCompletionOption]::ResponseHeadersRead, $cancel.Token).GetAwaiter().GetResult() }
        finally { $cancel.Dispose(); $request.Dispose() }
        if ([int]$response.StatusCode -in @(301,302,303,307,308)) {
            try {
                if (-not $response.Headers.Location) { throw 'Update redirect has no location.' }
                $Uri = [Uri]::new($Uri, $response.Headers.Location)
            } finally { $response.Dispose() }
            continue
        }
        if (-not $response.IsSuccessStatusCode) {
            $status = [int]$response.StatusCode
            $response.Dispose()
            $failure = [Net.Http.HttpRequestException]::new("Update endpoint returned HTTP $status.")
            $failure.Data['git-next-http-status'] = $status
            throw $failure
        }
        $response | Add-Member -NotePropertyName GitNextFinalUri -NotePropertyValue $Uri -Force
        return $response
    }
    throw 'Too many update transport redirects.'
}

function Read-GnBytes($Response, [long]$Limit) {
    $stream = $null
    $memory = [IO.MemoryStream]::new()
    try {
        if ($Response.Content.Headers.ContentLength -gt $Limit) { throw 'Update metadata exceeds its size limit.' }
        $stream = $Response.Content.ReadAsStreamAsync().GetAwaiter().GetResult()
        $buffer = [byte[]]::new(65536)
        $deadline = [DateTime]::UtcNow.AddSeconds(30)
        while ($true) {
            $readTask = $stream.ReadAsync($buffer, 0, $buffer.Length)
            if (-not $readTask.Wait(30000)) { throw 'Update metadata read timed out.' }
            $read = $readTask.GetAwaiter().GetResult()
            if (-not $read) { break }
            if ($memory.Length + $read -gt $Limit -or [DateTime]::UtcNow -gt $deadline) { throw 'Update metadata exceeds its size or time limit.' }
            $memory.Write($buffer, 0, $read)
        }
        return ,$memory.ToArray()
    } finally { if ($stream) { $stream.Dispose() }; $memory.Dispose(); $Response.Dispose() }
}

function Get-GnOfficialPlan($Client, $Installed) {
    $uri = 'https://api.github.com/repos/' + $script:Repository + '/releases/latest'
    try { $response = Invoke-GnRequest $Client $uri $true }
    catch {
        $failure = $_.Exception
        while ($failure.InnerException) { $failure = $failure.InnerException }
        if ($failure.Data['git-next-http-status'] -in @(403,429) -or $failure -is [Net.Http.HttpRequestException] -or $failure -is [Threading.Tasks.TaskCanceledException]) {
            return Get-GnOfficialDownloadPlan $Client $Installed
        }
        throw
    }
    $release = [Text.Encoding]::UTF8.GetString((Read-GnBytes $response 2097152)) | ConvertFrom-Json
    if ($release.draft -or $release.prerelease -or $release.tag_name -notmatch '^v([0-9]+\.[0-9]+\.[0-9]+)$') { throw 'Latest GitHub release is not a stable version.' }
    $version = $Matches[1]
    $metadata = @($release.assets | Where-Object name -CEQ 'git-next-update.json')
    if ($metadata.Count -ne 1 -or $metadata[0].size -gt 1048576) { throw 'Official update manifest is missing or ambiguous.' }
    $manifestAsset = $metadata[0]
    $expectedUrl = 'https://github.com/' + $script:Repository + '/releases/download/' + $release.tag_name + '/git-next-update.json'
    if ($manifestAsset.browser_download_url -cne $expectedUrl -or $manifestAsset.digest -notmatch '^sha256:[a-f0-9]{64}$') { throw 'GitHub did not supply a SHA-256 identity for the official manifest.' }
    $bytes = Read-GnBytes (Invoke-GnRequest $Client $expectedUrl $true) 1048576
    $algorithm = [Security.Cryptography.SHA256]::Create()
    try { $digest = [BitConverter]::ToString($algorithm.ComputeHash($bytes)).Replace('-','').ToLowerInvariant() }
    finally { $algorithm.Dispose() }
    if ('sha256:' + $digest -cne $manifestAsset.digest) { throw 'Official update manifest digest mismatch.' }
    $manifest = [Text.Encoding]::UTF8.GetString($bytes) | ConvertFrom-Json
    if ($manifest.schema_version -ne 1 -or $manifest.product -cne 'git-next' -or $manifest.version -cne $version -or $manifest.complete_git_parity -ne $true -or $manifest.source_commit -notmatch '^[a-f0-9]{40}$') { throw 'Release manifest does not identify an accepted git-next release.' }
    $entry = Get-GnUpdateEntry $manifest $Installed
    $name = $entry.asset
    $asset = @($release.assets | Where-Object name -CEQ $name)
    $url = 'https://github.com/' + $script:Repository + '/releases/download/v' + $version + '/' + $name
    if ($asset.Count -ne 1 -or $asset[0].browser_download_url -cne $url -or $asset[0].size -ne $entry.size -or $asset[0].digest -cne ('sha256:' + $entry.sha256)) { throw 'Package does not match official GitHub asset metadata.' }
    [pscustomobject]@{ Version=$version; Url=$url; Entry=$entry; ManifestSha256=$digest; ReleaseId=$release.id; MetadataTrust='official-api-digest'; Newer=(Test-GnNewer $version $Installed.version) }
}

function Get-GnUpdateEntry($Manifest, $Installed) {
    $entries = @($Manifest.platforms.PSObject.Properties | Where-Object Name -CEQ $Installed.platform)
    if ($entries.Count -ne 1) { throw 'The release has no unambiguous package for this platform.' }
    $entry = $entries[0].Value
    $scope = Get-GnInstallScope $Installed
    $entryScope = if ($entry.PSObject.Properties['install_scope']) { $entry.install_scope } else { 'user' }
    if ($entryScope -cne $scope) {
        $variants = $entry.PSObject.Properties['variants']
        $variant = if ($variants) { $variants.Value.PSObject.Properties[$scope] } else { $null }
        if (-not $variant) { throw 'The release has no package for the installed MSI scope.' }
        $entry = $variant.Value
        if (-not $entry.PSObject.Properties['install_scope'] -or $entry.install_scope -cne $scope) { throw 'The update package would change the installed MSI scope.' }
    }
    $suffix = if ($scope -eq 'user' -and $entry.PSObject.Properties['install_scope']) { '-user' } else { '' }
    $name = 'git-next-' + $Manifest.version + '-' + $Installed.platform + $suffix + '.msi'
    if ($entry.asset -cne $name -or $entry.sha256 -cnotmatch '^[a-f0-9]{64}$' -or $entry.size -lt 4096 -or $entry.size -gt 1073741824 -or $entry.upgrade_code -cne $Installed.upgrade_code) { throw 'Invalid platform update identity.' }
    return $entry
}

function Get-GnOfficialDownloadPlan($Client, $Installed) {
    $uri = 'https://github.com/' + $script:Repository + '/releases/latest/download/git-next-update.json'
    $response = Invoke-GnRequest $Client $uri $true
    $finalUri = $response.GitNextFinalUri
    $bytes = Read-GnBytes $response 1048576
    $manifest = [Text.Encoding]::UTF8.GetString($bytes) | ConvertFrom-Json
    if ($manifest.schema_version -ne 1 -or $manifest.product -cne 'git-next' -or $manifest.version -notmatch '^[0-9]+\.[0-9]+\.[0-9]+$' -or $manifest.complete_git_parity -ne $true -or $manifest.source_commit -notmatch '^[a-f0-9]{40}$') { throw 'Release manifest does not identify an accepted git-next release.' }
    $expectedPath = '/' + $script:Repository + '/releases/download/v' + $manifest.version + '/git-next-update.json'
    # The final CDN is official too; the stable-only GitHub /latest endpoint selected the release.
    if ($finalUri.Host -eq 'github.com' -and $finalUri.AbsolutePath -cne $expectedPath) { throw 'Official stable manifest version disagrees with its release URL.' }
    $entry = Get-GnUpdateEntry $manifest $Installed
    $name = $entry.asset
    if ($entry.asset -cne $name -or $entry.sha256 -notmatch '^[a-f0-9]{64}$' -or $entry.size -lt 4096 -or $entry.size -gt 1073741824 -or $entry.upgrade_code -cne $Installed.upgrade_code -or $entry.msi_version -cne $manifest.version) { throw 'Invalid platform update identity.' }
    $algorithm = [Security.Cryptography.SHA256]::Create()
    try { $digest = [BitConverter]::ToString($algorithm.ComputeHash($bytes)).Replace('-','').ToLowerInvariant() }
    finally { $algorithm.Dispose() }
    $url = 'https://github.com/' + $script:Repository + '/releases/download/v' + $manifest.version + '/' + $name
    [pscustomobject]@{ Version=$manifest.version; Url=$url; Entry=$entry; ManifestSha256=$digest; ReleaseId=$null; MetadataTrust='official-release-https'; Newer=(Test-GnNewer $manifest.version $Installed.version) }
}

function Get-GnDownloadOrder($Client, [string]$OfficialUrl) {
    # HEAD probes run together; only verified payloads may use mirrors.
    $probes = @()
    foreach ($url in @($OfficialUrl) + @($script:MirrorPrefixes | ForEach-Object { $_ + $OfficialUrl })) {
        $request = [Net.Http.HttpRequestMessage]::new([Net.Http.HttpMethod]::Head, $url)
        $cancel = [Threading.CancellationTokenSource]::new([TimeSpan]::FromSeconds(5))
        $watch = [Diagnostics.Stopwatch]::StartNew()
        $probes += [pscustomobject]@{ Url=$url; Request=$request; Cancel=$cancel; Watch=$watch; Task=$Client.SendAsync($request, [Net.Http.HttpCompletionOption]::ResponseHeadersRead, $cancel.Token); Rank=[double]::PositiveInfinity; Done=$false }
    }
    try {
        $pending = @($probes)
        while ($pending.Count) {
            foreach ($probe in @($pending | Where-Object { $_.Task.IsCompleted })) {
                $probe.Done = $true
                $probe.Watch.Stop()
                try {
                    $response = $probe.Task.GetAwaiter().GetResult()
                    try { if ([int]$response.StatusCode -in @(200,206,301,302,303,307,308)) { $probe.Rank = $probe.Watch.Elapsed.TotalMilliseconds } }
                    finally { $response.Dispose() }
                } catch { }
            }
            $pending = @($pending | Where-Object { -not $_.Done })
            if ($pending.Count) { Start-Sleep -Milliseconds 20 }
        }
        return @($probes | Sort-Object Rank,Url | ForEach-Object Url)
    } finally { foreach ($probe in $probes) { $probe.Cancel.Dispose(); $probe.Request.Dispose() } }
}

function Save-GnVerifiedPackage($Client, $Plan, [string]$Destination) {
    if ([IO.File]::Exists($Destination) -or [IO.Directory]::Exists($Destination)) { throw 'Refusing to overwrite an existing update download.' }
    foreach ($url in (Get-GnDownloadOrder $Client $Plan.Url)) {
        $response = $null
        $inputStream = $null
        $outputStream = $null
        $created = $false
        try {
            $response = Invoke-GnRequest $Client $url ($url -ceq $Plan.Url)
            if ($response.Content.Headers.ContentLength -and $response.Content.Headers.ContentLength -ne $Plan.Entry.size) { throw 'Update download size mismatch.' }
            $inputStream = $response.Content.ReadAsStreamAsync().GetAwaiter().GetResult()
            $outputStream = Open-GnNewFile $Destination
            $created = $true
            $buffer = [byte[]]::new(131072)
            $deadline = [DateTime]::UtcNow.AddMinutes(10)
            while ($true) {
                $readTask = $inputStream.ReadAsync($buffer, 0, $buffer.Length)
                if (-not $readTask.Wait(30000)) { throw 'Update download stalled.' }
                $read = $readTask.GetAwaiter().GetResult()
                if (-not $read) { break }
                if ($outputStream.Length + $read -gt $Plan.Entry.size -or [DateTime]::UtcNow -gt $deadline) { throw 'Update download exceeds size or time limit.' }
                $outputStream.Write($buffer, 0, $read)
            }
            $outputStream.Flush($true)
            $length = $outputStream.Length
            $outputStream.Dispose(); $outputStream = $null
            if ($length -ne $Plan.Entry.size -or (Get-FileHash -LiteralPath $Destination -Algorithm SHA256).Hash.ToLowerInvariant() -cne $Plan.Entry.sha256) { throw 'Update download SHA-256 mismatch.' }
            return [Uri]$url
        } catch {
            if ($outputStream) { $outputStream.Dispose(); $outputStream = $null }
            if ($created -and [IO.File]::Exists($Destination)) { [IO.File]::Delete($Destination) }
        } finally {
            if ($outputStream) { $outputStream.Dispose() }
            if ($inputStream) { $inputStream.Dispose() }
            if ($response) { $response.Dispose() }
        }
    }
    throw 'All package transports failed; installed files were not changed.'
}

function Read-GnMsiProperty($Database, [string]$Name, [bool]$Optional = $false) {
    if ($Name -notmatch '^[A-Za-z][A-Za-z0-9]*$') { throw 'Invalid MSI property name.' }
    $view = $Database.OpenView("SELECT ``Value`` FROM ``Property`` WHERE ``Property``='$Name'")
    try { [void]$view.Execute(); $record = $view.Fetch(); if (-not $record) { if ($Optional) { return '' }; throw 'MSI identity property is absent.' }; return [string]$record.StringData(1) }
    finally { [void]$view.Close(); [void][Runtime.InteropServices.Marshal]::FinalReleaseComObject($view) }
}

function Test-GnMissingTaskError($ErrorRecord) {
    $exception = $ErrorRecord.Exception
    while ($exception) {
        if ($exception.HResult -eq -2147024894) { return $true }
        $exception = $exception.InnerException
    }
    return $false
}

function Assert-GnMsi([string]$Path, $Entry) {
    $installer = New-Object -ComObject WindowsInstaller.Installer
    $database = $null
    try {
        $database = $installer.OpenDatabase($Path, 0)
        foreach ($property in @(@('ProductName','git-next'), @('Manufacturer','neko233-com'), @('ProductVersion',$Entry.msi_version), @('ProductCode',$Entry.product_code), @('UpgradeCode',$Entry.upgrade_code))) {
            if ((Read-GnMsiProperty $database $property[0]) -cne $property[1]) { throw 'MSI product identity mismatch.' }
        }
        $machine = $Entry.PSObject.Properties['install_scope'] -and $Entry.install_scope -eq 'machine'
        if ((Read-GnMsiProperty $database 'ALLUSERS' $true) -cne $(if($machine){'1'}else{''})) { throw 'MSI installation scope mismatch.' }
    } finally {
        if ($database) { [void][Runtime.InteropServices.Marshal]::FinalReleaseComObject($database) }
        [void][Runtime.InteropServices.Marshal]::FinalReleaseComObject($installer)
    }
}

function Test-GnBusy([string]$InstallRoot) {
    $prefix = [IO.Path]::GetFullPath($InstallRoot).TrimEnd('\') + '\'
    $workerPath = Join-Path $InstallRoot 'updater\msi-worker.exe'
    $workerId = 0
    try {
        $self = Get-CimInstance -ClassName Win32_Process -Filter ("ProcessId = {0}" -f $PID) -ErrorAction Stop
        $parent = Get-CimInstance -ClassName Win32_Process -Filter ("ProcessId = {0}" -f $self.ParentProcessId) -ErrorAction Stop
        if ($parent -and $parent.ExecutablePath -and $parent.ExecutablePath -ieq $workerPath) { $workerId = [int]$parent.ProcessId }
    } catch { }
    $names = @('git','git-next','git-terminal','git-lfs','git-next-lfs-filter')
    $packagePath = Assert-GnOwnedPath $InstallRoot (Join-Path $InstallRoot 'package.json')
    if ([IO.File]::Exists($packagePath)) {
        $package = Get-Content -LiteralPath $packagePath -Raw -Encoding UTF8 | ConvertFrom-Json
        $names += @($package.files.PSObject.Properties | Where-Object { $_.Name.EndsWith('.exe', [StringComparison]::OrdinalIgnoreCase) } | ForEach-Object { [IO.Path]::GetFileNameWithoutExtension($_.Name) })
    } elseif ([IO.Directory]::Exists($InstallRoot)) {
        $names += @(Get-ChildItem -LiteralPath $InstallRoot -Recurse -File -Filter '*.exe' | ForEach-Object BaseName)
    }
    $patterns = @($names | Sort-Object -Unique | ForEach-Object { [WildcardPattern]::Escape($_) })
    foreach ($process in @(Get-Process -Name $patterns -ErrorAction SilentlyContinue)) {
        if ($workerId -and $process.Id -eq $workerId) { continue }
        $path = $null
        try { $path = $process.MainModule.FileName } catch { }
        if (-not $path) {
            try {
                $detail = Get-CimInstance -ClassName Win32_Process -Filter ("ProcessId = {0}" -f $process.Id) -ErrorAction Stop
                if (-not $detail) { continue }
                $path = $detail.ExecutablePath
            } catch { return $true }
        }
        if (-not $path -or $path.StartsWith($prefix, [StringComparison]::OrdinalIgnoreCase)) { return $true }
    }
    return $false
}

function Set-GnUpdateTask([string]$InstallRoot, [string]$StateRoot, [string]$Platform, [string]$Mode, $Context = $null) {
    $machine = $null -ne $Context -and $Context.machine
    $sid = if ($machine) { 'S-1-5-18' } else { [Security.Principal.WindowsIdentity]::GetCurrent().User.Value }
    $logonType = if ($machine) { 5 } else { 3 }
    $taskName = if ($machine) { $Context.task_name } else { 'git-next-update-' + $Platform + '-' + $sid }
    $taskSecurity = if ($machine) { 'O:BAG:BAD:P(A;;GA;;;SY)(A;;GA;;;BA)(A;;GRGX;;;BU)' } else { $null }
    $service = New-Object -ComObject Schedule.Service
    [void]$service.Connect()
    $folder = $service.GetFolder('\')
    $task = $null
    try { $task = $folder.GetTask($taskName) } catch { if (-not (Test-GnMissingTaskError $_)) { throw } }
    $scriptFile = Join-Path $InstallRoot 'updater\update.ps1'
    $description = 'git-next automatic update: ' + [IO.Path]::GetFullPath($InstallRoot)
    if ($task -and $task.Definition.RegistrationInfo.Description -cne $description) { throw 'An unrelated task owns the git-next updater name.' }
    $backup = Assert-GnOwnedPath $StateRoot (Join-Path $StateRoot 'task-transaction.json')
    Assert-GnScopedFile $backup
    if ($Mode -eq 'RollbackRegistration') {
        if (Test-Path -LiteralPath $backup) {
            $saved = Get-Content -LiteralPath $backup -Raw | ConvertFrom-Json
            if ($saved.task_name -cne $taskName) { throw 'Unexpected scheduled-task rollback identity.' }
            if ($saved.xml) { [void]$folder.RegisterTask($taskName, $saved.xml, 6, $sid, $null, $logonType, $taskSecurity) }
            elseif ($task) { [void]$folder.DeleteTask($taskName, 0) }
            [IO.File]::Delete($backup)
        }
        return
    }
    Write-GnJson $backup @{ task_name=$taskName; xml=$(if ($task) { $task.Xml } else { $null }) }
    if ($Mode -eq 'Unregister') { if ($task) { [void]$folder.DeleteTask($taskName, 0) }; return }
    $definition = $service.NewTask(0)
    $definition.RegistrationInfo.Description = $description
    $definition.Principal.UserId = $sid
    $definition.Principal.LogonType = $logonType
    $definition.Principal.RunLevel = if ($machine) { 1 } else { 0 }
    $definition.Settings.Enabled = $true
    $definition.Settings.Hidden = $true
    $definition.Settings.StartWhenAvailable = $true
    $definition.Settings.DisallowStartIfOnBatteries = $false
    $definition.Settings.StopIfGoingOnBatteries = $false
    $definition.Settings.MultipleInstances = 2
    # An MSI transaction must not be killed by the scheduled-task deadline.
    $definition.Settings.ExecutionTimeLimit = 'PT0S'
    $daily = $definition.Triggers.Create(2)
    $daily.StartBoundary = [DateTime]::Now.AddMinutes(5).ToString('yyyy-MM-ddTHH:mm:ss')
    $daily.DaysInterval = 1
    $daily.RandomDelay = 'PT30M'
    $daily.Repetition.Interval = 'PT1H'
    $daily.Repetition.Duration = 'P1D'
    if ($machine) {
        $boot = $definition.Triggers.Create(8)
        $boot.Delay = 'PT5M'
    } else {
        $logon = $definition.Triggers.Create(9)
        $logon.UserId = $sid
        $logon.Delay = 'PT5M'
    }
    $action = $definition.Actions.Create(0)
    $action.Path = Join-Path $InstallRoot 'updater\msi-worker.exe'
    $action.Arguments = '-Mode Scheduled'
    $action.Arguments += ' -Request "$(Arg0)"'
    $action.WorkingDirectory = Join-Path $InstallRoot 'updater'
    [void]$folder.RegisterTaskDefinition($taskName, $definition, 6, $sid, $null, $logonType, $taskSecurity)
}

function Assert-GnInstalledFiles([string]$InstallRoot, $Package) {
    if ($Package.schema_version -ne 1 -or $Package.product -cne 'git-next' -or -not $Package.files) { throw 'Invalid installed package manifest.' }
    $entries = @($Package.files.PSObject.Properties)
    if (-not $entries.Count -or $entries.Count -gt 60000) { throw 'Unexpected installed package file count.' }
    $names = [Collections.Generic.HashSet[string]]::new([StringComparer]::OrdinalIgnoreCase)
    foreach ($entry in $entries) {
        if ($entry.Value -isnot [string] -or $entry.Value -cnotmatch '^[a-f0-9]{64}$' -or -not $names.Add($entry.Name)) { throw 'Unexpected installed package path or checksum.' }
        $parts = @($entry.Name -split '/')
        foreach ($part in $parts) {
            if (-not $part -or $part -in @('.','..') -or $part.IndexOfAny([IO.Path]::GetInvalidFileNameChars()) -ge 0 -or $part -match '[. ]$' -or $part -match '^(?i:CON|PRN|AUX|NUL|COM[1-9]|LPT[1-9])(?:\.|$)') { throw 'Unexpected installed package path or checksum.' }
        }
        if ($parts.Count -gt 1 -and $parts[0] -notin @('bin','cmd','preview','runtime','docs','native-core','LICENSES','updater','components','migration')) { throw 'Unexpected installed package root.' }
        $file = Assert-GnOwnedPath $InstallRoot (Join-Path $InstallRoot $entry.Name)
        if ((Get-GnInstallScope $Package) -eq 'machine') { Assert-GnProtectedObject $file }
        if ((Get-FileHash -LiteralPath $file -Algorithm SHA256).Hash.ToLowerInvariant() -cne $entry.Value) { throw 'Installed files were modified; use MSI repair before automatic updates.' }
    }
}

Export-ModuleMember -Function Test-GnMissingTaskError,Get-GnMessage,Assert-GnOwnedPath,Write-GnJson,ConvertTo-GnVersion,Test-GnNewer,New-GnHttpClient,Get-GnOfficialPlan,Get-GnDownloadOrder,Save-GnVerifiedPackage,Assert-GnMsi,Assert-GnInstalledFiles,Test-GnBusy,Set-GnUpdateTask,Get-GnInstallScope,Test-GnAdministrator,Get-GnMachineDataRoot,Assert-GnProtectedObject,Initialize-GnMachineDirectory,Assert-GnMachineInstall,Get-GnScopeContext,Assert-GnScopedFile,Open-GnNewFile,Open-GnExclusiveLock,New-GnOutputParent,Set-GnMachinePublicObject,Initialize-GnRuntimeConfiguration,Start-GnRuntimeConfigTransaction,Restore-GnRuntimeConfigTransaction,Complete-GnRuntimeConfigTransaction,Get-GnRuntimeConfigHash,Assert-GnRuntimeConfigPath
