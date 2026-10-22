param(
    [string]$Archive,
    [string]$CommandDirectory = (Join-Path $HOME '.local\bin'),
    [string]$Prefix = (Join-Path $env:LOCALAPPDATA 'Programs\git-next'),
    [switch]$Uninstall
)
$ErrorActionPreference = 'Stop'
$Prefix = [IO.Path]::GetFullPath($Prefix)
$CommandDirectory = [IO.Path]::GetFullPath($CommandDirectory)
$manifestPath = Join-Path $Prefix 'credential-support.json'
$supportParent = Join-Path $Prefix 'support'
$destination = Join-Path $supportParent 'gcm'
$expectedArchiveHash = '529EA8AA1379B3A11BD8AAFE4705A839CF253A41E14908FCA113ABEEFA30657F'
$license = Join-Path $PSScriptRoot 'third_party\gcm\LICENSE'

function Write-LfJson([string]$Path, $Value) {
    $json = ($Value | ConvertTo-Json -Depth 8) -replace "`r`n", "`n"
    [IO.File]::WriteAllText($Path, $json + "`n", [Text.UTF8Encoding]::new($false))
}
function Assert-OwnedPath([string]$Path) {
    $absolute = [IO.Path]::GetFullPath($Path)
    if (-not $absolute.StartsWith($supportParent + [IO.Path]::DirectorySeparatorChar, [StringComparison]::OrdinalIgnoreCase)) {
        throw 'Credential support path is outside its installation directory.'
    }
    if ((Test-Path -LiteralPath $absolute) -and ((Get-Item -LiteralPath $absolute -Force).Attributes -band [IO.FileAttributes]::ReparsePoint)) {
        throw 'Credential support paths must not be reparse points.'
    }
}

$saved = if (Test-Path -LiteralPath $manifestPath) { Get-Content -LiteralPath $manifestPath -Raw | ConvertFrom-Json } else { $null }
if ($saved -and ($saved.product -ne 'git-next' -or $saved.directory -ne $destination)) { throw 'Unexpected credential support manifest.' }
if ($Uninstall) {
    if (-not $saved) { throw 'No managed credential support manifest found.' }
    $ownedFiles = @()
    foreach ($entry in $saved.files) {
        if ($entry.name -ne [IO.Path]::GetFileName($entry.name)) { throw 'Unexpected credential support filename.' }
        $path = [IO.Path]::GetFullPath((Join-Path $destination $entry.name))
        Assert-OwnedPath $path
        $ownedFiles += @{path=$path; sha256=$entry.sha256}
    }
    foreach ($entry in $saved.locators) {
        $allowed = @((Join-Path $CommandDirectory 'git-next-support.json'), (Join-Path $Prefix 'preview\bin\git-next-support.json'))
        if ($entry.path -notin $allowed) { throw 'Unexpected credential support locator.' }
        $ownedFiles += $entry
    }
    $removeLockPath = Join-Path $supportParent 'gcm.install.lock'
    $removeLock = [IO.File]::Open($removeLockPath, [IO.FileMode]::CreateNew, [IO.FileAccess]::Write, [IO.FileShare]::None)
    try {
        foreach ($entry in $ownedFiles) {
            if ((Test-Path -LiteralPath $entry.path) -and (Get-FileHash -LiteralPath $entry.path -Algorithm SHA256).Hash -eq $entry.sha256) {
                Remove-Item -LiteralPath $entry.path
            }
        }
    } finally {
        $removeLock.Dispose()
        Remove-Item -LiteralPath $removeLockPath
    }
    Write-Host 'Matching managed credential-support files removed. Modified files, backups and saved logins are preserved.'
    return
}
if ([Runtime.InteropServices.RuntimeInformation]::OSArchitecture -ne 'X64') { throw 'This credential-support package currently accepts Windows x64 only.' }
if (-not $Archive -or -not (Test-Path -LiteralPath $Archive -PathType Leaf)) { throw 'Pass the official gcm-win-x64-2.7.3.zip with -Archive.' }
if ((Get-FileHash -LiteralPath $Archive -Algorithm SHA256).Hash -ne $expectedArchiveHash) { throw 'Credential Manager archive checksum mismatch.' }
if (-not (Test-Path -LiteralPath $license -PathType Leaf)) { throw 'The preserved GCM license is missing.' }
$installation = Get-Content -LiteralPath (Join-Path $Prefix 'installation.json') -Raw | ConvertFrom-Json
$callbackGit = [IO.Path]::GetFullPath((Join-Path $Prefix 'preview\bin\git.exe'))
if ($installation.product -ne 'git-next' -or $installation.command -ne (Join-Path $CommandDirectory 'git-next.exe') -or $installation.preview_git -ne $callbackGit) {
    throw 'Install the matching git-next preview before credential support.'
}
if ((Get-FileHash -LiteralPath $callbackGit -Algorithm SHA256).Hash -ne $installation.sha256) { throw 'Native callback checksum mismatch.' }
$identity = (& $callbackGit version --json) | ConvertFrom-Json
if ($LASTEXITCODE -ne 0 -or $identity.product -ne 'git-next' -or $identity.external_git_fallback -ne $false) { throw 'Credential callbacks require native git-next.' }
$locatorPaths = @((Join-Path $CommandDirectory 'git-next-support.json'), (Join-Path (Split-Path -Parent $callbackGit) 'git-next-support.json')) | Select-Object -Unique
foreach ($path in $locatorPaths) {
    if (Test-Path -LiteralPath $path) {
        $existing = Get-Content -LiteralPath $path -Raw | ConvertFrom-Json
        if ($existing.product -ne 'git-next' -or $existing.credential_helper -ne (Join-Path $destination 'git-credential-manager.exe')) { throw 'An unrelated support locator already exists.' }
    }
}
if ((Test-Path -LiteralPath $destination) -and -not $saved) { throw 'Refusing to replace an unmanaged credential-support directory.' }
New-Item -ItemType Directory -Path $supportParent -Force | Out-Null
if ((Get-Item -LiteralPath $supportParent -Force).Attributes -band [IO.FileAttributes]::ReparsePoint) { throw 'Credential support parent must not be a reparse point.' }
Assert-OwnedPath $destination
$pending = Join-Path $supportParent ('.gcm-pending-' + [Guid]::NewGuid().ToString('N'))
Assert-OwnedPath $pending
$lockPath = Join-Path $supportParent 'gcm.install.lock'
$lock = [IO.File]::Open($lockPath, [IO.FileMode]::CreateNew, [IO.FileAccess]::Write, [IO.FileShare]::None)
$backupId = [DateTime]::UtcNow.ToString('yyyyMMddHHmmssfff')
$backup = $null
$published = $false
$writtenLocators = @()
$originalLocators = @{}
foreach ($path in $locatorPaths) {
    $originalLocators[$path] = if (Test-Path -LiteralPath $path) { [IO.File]::ReadAllBytes($path) } else { $null }
}
try {
    New-Item -ItemType Directory -Path $pending | Out-Null
    $zip = [IO.Compression.ZipFile]::OpenRead((Resolve-Path -LiteralPath $Archive).Path)
    try {
        foreach ($entry in $zip.Entries) {
            if ($entry.FullName -ne [IO.Path]::GetFileName($entry.FullName) -or $entry.Name -in @('git.exe','ssh.exe')) { throw 'Unexpected credential package layout.' }
            [IO.Compression.ZipFileExtensions]::ExtractToFile($entry, (Join-Path $pending $entry.Name))
        }
    } finally { $zip.Dispose() }
    Copy-Item -LiteralPath $license -Destination (Join-Path $pending 'LICENSE-GCM')
    $helper = Join-Path $pending 'git-credential-manager.exe'
    $signature = Get-AuthenticodeSignature -LiteralPath $helper
    if ($signature.Status -ne 'Valid' -or $signature.SignerCertificate.Subject -notlike 'CN=Microsoft Corporation,*') { throw 'Credential Manager signature verification failed.' }
    $version = (& $helper --version) -join "`n"
    if ($LASTEXITCODE -ne 0 -or $version -ne '2.7.3+5fa7116896c82164996a609accd1c5ad90fe730a') { throw 'Unexpected Credential Manager version.' }
    $files = @(Get-ChildItem -LiteralPath $pending -File | ForEach-Object { @{name=$_.Name; sha256=(Get-FileHash -LiteralPath $_.FullName -Algorithm SHA256).Hash} })
    if (Test-Path -LiteralPath $destination) {
        $backup = $destination + '.previous.' + $backupId
        Assert-OwnedPath $backup
        Move-Item -LiteralPath $destination -Destination $backup
    }
    Move-Item -LiteralPath $pending -Destination $destination
    $published = $true
    $helper = Join-Path $destination 'git-credential-manager.exe'
    $locator = @{schema_version=1; product='git-next'; credential_helper=$helper; native_callback_directory=(Split-Path -Parent $callbackGit)}
    $locators = @()
    foreach ($path in $locatorPaths) {
        if (Test-Path -LiteralPath $path) { Copy-Item -LiteralPath $path -Destination ($path + '.previous.' + $backupId) }
        Write-LfJson ($path + '.new') $locator
        Move-Item -LiteralPath ($path + '.new') -Destination $path -Force
        $writtenLocators += $path
        $locators += @{path=$path; sha256=(Get-FileHash -LiteralPath $path -Algorithm SHA256).Hash}
    }
    if ($saved) { Copy-Item -LiteralPath $manifestPath -Destination ($manifestPath + '.previous.' + $backupId) }
    Write-LfJson ($manifestPath + '.new') @{schema_version=1; product='git-next'; version=$version; directory=$destination; archive_sha256=$expectedArchiveHash; signature_verified=$true; files=$files; locators=$locators; config_changed=$false; login_reset=$false}
    Move-Item -LiteralPath ($manifestPath + '.new') -Destination $manifestPath -Force
    Write-Host 'Installed managed GCM 2.7.3 with native git-next callbacks. Saved logins and Git configuration are preserved.'
} catch {
    foreach ($path in $writtenLocators) {
        if ($null -eq $originalLocators[$path]) { Remove-Item -LiteralPath $path }
        else { [IO.File]::WriteAllBytes($path, $originalLocators[$path]) }
    }
    if ($published) {
        Assert-OwnedPath $destination
        foreach ($entry in $files) {
            $path = Join-Path $destination $entry.name
            if ((Test-Path -LiteralPath $path) -and (Get-FileHash -LiteralPath $path -Algorithm SHA256).Hash -eq $entry.sha256) {
                Remove-Item -LiteralPath $path
            }
        }
        if (-not (Get-ChildItem -LiteralPath $destination -Force | Select-Object -First 1)) { Remove-Item -LiteralPath $destination }
    }
    if ($backup -and (Test-Path -LiteralPath $backup) -and -not (Test-Path -LiteralPath $destination)) {
        Assert-OwnedPath $backup
        Move-Item -LiteralPath $backup -Destination $destination
    }
    throw
} finally {
    $lock.Dispose()
    Remove-Item -LiteralPath $lockPath
    if (Test-Path -LiteralPath $pending) {
        Assert-OwnedPath $pending
        Get-ChildItem -LiteralPath $pending -File | Remove-Item
        Remove-Item -LiteralPath $pending
    }
}
