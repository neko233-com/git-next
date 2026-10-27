# Copyright 2026 git-next contributors. MIT license.
$ErrorActionPreference = 'Stop'
Import-Module (Join-Path $PSScriptRoot '../packaging/windows/Update.Core.psm1') -Force -DisableNameChecking
$module = Import-Module (Join-Path $PSScriptRoot '../packaging/windows/Takeover.Core.psm1') -Force -DisableNameChecking -PassThru
$fixture = Join-Path ([IO.Path]::GetTempPath()) ('git-next-uninstall-' + [Guid]::NewGuid().ToString('N'))
[void][IO.Directory]::CreateDirectory($fixture)
$cases = 5
try {
    foreach ($name in @('unins000.exe','unins001.exe')) {
        $path = Join-Path $fixture $name
        [IO.File]::WriteAllText($path,'fixture')
        $actual = & $module { param($root,$command) Get-GnRegisteredUninstaller $root $command } $fixture ('"'+$path+'"')
        if ($actual -cne $path) { throw 'Registered filename was not preserved.' }
    }
    foreach ($bad in @((Join-Path $fixture '../unins000.exe'),(Join-Path $fixture 'missing.exe'),((Join-Path $fixture 'unins001.exe')+' /silent'))) {
        $rejected = $false
        try { & $module { param($root,$command) Get-GnRegisteredUninstaller $root $command } $fixture $bad | Out-Null } catch { $rejected = $true }
        if (-not $rejected) { throw 'Unsafe uninstaller registration was accepted.' }
    }
    $same = Join-Path $fixture 'same-directory'
    $backup = Join-Path $fixture 'payload-snapshot'
    [void][IO.Directory]::CreateDirectory((Join-Path $same 'bin'))
    [void][IO.Directory]::CreateDirectory((Join-Path $same 'runtime'))
    [void][IO.Directory]::CreateDirectory((Join-Path $same 'runtime/etc'))
    [IO.File]::WriteAllText((Join-Path $same 'runtime/etc/fstab'),"none /tmp usertemp binary,posix=0 0 0`n")
    $image = [byte[]]::new(256)
    $image[0]=77; $image[1]=90; $image[60]=64; $image[64]=80; $image[65]=69; $image[156]=3
    [IO.File]::WriteAllBytes((Join-Path $same 'bin/git.exe'),$image)
    [IO.File]::WriteAllText((Join-Path $same 'runtime/support.dat'),'owned support')
    foreach ($name in @('legacy-remove.dat','legacy-changed.dat','legacy-duplicate.dat')) { [IO.File]::WriteAllText((Join-Path $same $name),'original '+$name) }
    $package = [pscustomobject]@{schema_version=1;product='git-next';platform='windows-x64';install_scope='user';files=[pscustomobject]@{
        'bin/git.exe'=(Get-FileHash -LiteralPath (Join-Path $same 'bin/git.exe') -Algorithm SHA256).Hash.ToLowerInvariant()
        'runtime/support.dat'=(Get-FileHash -LiteralPath (Join-Path $same 'runtime/support.dat') -Algorithm SHA256).Hash.ToLowerInvariant()
    }}
    [IO.File]::WriteAllText((Join-Path $same 'package.json'),($package | ConvertTo-Json -Depth 5),[Text.UTF8Encoding]::new($false))
    $packageHash=(Get-FileHash -LiteralPath (Join-Path $same 'package.json') -Algorithm SHA256).Hash.ToLowerInvariant()
    $files=@(& $module {param($root) Get-GnTree $root} $same)
    $stale=@(& $module {param($package,$hash,$files) Get-GnSameDirectoryStale $package $hash $files} $package $packageHash $files)
    if (@($stale | Where-Object relative -ieq 'package.json').Count -or @($stale | Where-Object relative -ieq 'bin/git.exe').Count -or $stale.Count -ne 3) { throw 'The package self manifest or payload was considered stale.' }
    $cases++
    foreach ($file in $files) { & $module {param($source,$target,$record) Copy-GnMigrationFile $source $target $record} $same $backup $file }
    $plan=[pscustomobject]@{install_root=$same;original_root=$same;package_sha256=$packageHash;files=$files;stale=$stale}
    [IO.File]::Delete((Join-Path $same 'bin/git.exe'))
    [IO.File]::Delete((Join-Path $same 'package.json'))
    & $module {param($plan,$package,$backup) Restore-GnSameDirectoryPayload $plan $package $backup} $plan $package $backup
    if ((Get-FileHash -LiteralPath (Join-Path $same 'bin/git.exe') -Algorithm SHA256).Hash.ToLowerInvariant() -cne $package.files.'bin/git.exe' -or (Get-FileHash -LiteralPath (Join-Path $same 'package.json') -Algorithm SHA256).Hash.ToLowerInvariant() -cne $packageHash) { throw 'A simulated official uninstaller deletion was not restored from exact snapshot bytes.' }
    $cases++
    [IO.File]::WriteAllText((Join-Path $same 'legacy-changed.dat'),'changed outside takeover')
    [IO.File]::WriteAllText((Join-Path $same 'new-unowned.dat'),'new outside takeover')
    $duplicate=@($stale | Where-Object relative -ceq 'legacy-duplicate.dat')[0]
    $self=@($files | Where-Object relative -ceq 'package.json')[0]
    $active=@($files | Where-Object relative -ceq 'runtime/etc/fstab')[0]
    $plan.stale=@($stale)+@($duplicate,$self,$active)
    & $module {param($plan) Remove-GnSameDirectoryStale $plan} $plan
    if ([IO.File]::Exists((Join-Path $same 'legacy-remove.dat'))) { throw 'An unchanged unique stale file was not removed.' }
    foreach ($name in @('package.json','bin/git.exe','runtime/etc/fstab','legacy-changed.dat','legacy-duplicate.dat','new-unowned.dat')) {
        if (-not [IO.File]::Exists((Join-Path $same $name))) { throw 'Guarded stale removal deleted preserved data.' }
    }
    $cases++
    [IO.File]::WriteAllText((Join-Path $same 'bin/git.exe'),'unrelated changed payload')
    $rejected=$false
    try { & $module {param($plan,$package,$backup) Restore-GnSameDirectoryPayload $plan $package $backup} $plan $package $backup } catch { $rejected=$true }
    if (-not $rejected -or [IO.File]::ReadAllText((Join-Path $same 'bin/git.exe')) -cne 'unrelated changed payload') { throw 'Existing mismatched MSI bytes were overwritten.' }
    $cases++
    [IO.File]::WriteAllText((Join-Path $same 'package.json'),'changed manifest')
    $rejected=$false
    try { & $module {param($plan) Remove-GnSameDirectoryStale $plan} $plan } catch { $rejected=$true }
    if (-not $rejected) { throw 'Changed self manifest did not prevent stale removal.' }
    $cases++
    $indexed=& $module {
        $items=for($i=0;$i -lt 4000;$i++){[pscustomobject]@{relative=('legacy/'+$i+'.dat');sha256=('a'*64)}}
        $index=Get-GnMigrationFileIndex $items
        if ($index.Count -ne 4000 -or -not $index.ContainsKey('legacy/3999.dat') -or $index.ContainsKey('Legacy/3999.dat')) { throw 'Stale lookup lost its exact case-sensitive identity.' }
        return $true
    }
    if (-not $indexed) { throw 'Stale index fixture did not complete.' }
    $cases++
    $extra = & $module {
        param($fixture,$sourceRoot,$image)
        $saved=@{}
        $mockNames=@('Test-GnBusy','Get-GnOriginalGitRegistration','Get-GnRelatedIntegration','Get-GnTakeoverRegistry','Get-GnOldUpdateTasks','Get-GnConfigDirectory','Get-GnTakeoverRegisteredMsiRoot','Test-GnTakeoverMsiInstalled','Invoke-GnTakeoverMsiRemoval','Restore-GnRegistrySnapshot','Set-GnOldUpdateTasks','Restore-GnMigrationPath','New-GnOutputParent')
        foreach($name in $mockNames){$saved[$name]=(Get-Command $name -CommandType Function).ScriptBlock}
        $count=0
        function Expect-Rejection([scriptblock]$Action,[string]$Message){$rejected=$false;try{&$Action|Out-Null}catch{$rejected=$true};if(-not $rejected){throw $Message}}
        try {
            $script:FixtureBusy=$false
            $script:FixtureMsi=$true
            $script:FixtureRemovalCalls=0
            $script:FixtureConfig=Join-Path $fixture 'isolated-config'
            [void][IO.Directory]::CreateDirectory($script:FixtureConfig)
            function script:Test-GnBusy([string]$Root){return $script:FixtureBusy}
            function script:Get-GnOriginalGitRegistration([string]$Root){return [pscustomobject]@{hive='CurrentUser';uninstaller=(Join-Path $Root 'unins001.exe')}}
            function script:Get-GnRelatedIntegration([string]$Root){return @()}
            function script:Get-GnTakeoverRegistry([string]$Hive){return @()}
            function script:Get-GnOldUpdateTasks([string]$Root){return @()}
            function script:Get-GnConfigDirectory{return $script:FixtureConfig}
            function script:Get-GnTakeoverRegisteredMsiRoot($Package){return $script:FixtureCurrentRoot}
            function script:Test-GnTakeoverMsiInstalled([string]$Code){return $script:FixtureMsi}
            function script:Restore-GnRegistrySnapshot($Record){throw 'A fixture must not restore real registry entries.'}
            function script:Set-GnOldUpdateTasks($Tasks,[bool]$Restore){if(@($Tasks).Count){throw 'A fixture must not restore real tasks.'}}
            function script:Restore-GnMigrationPath($Plan,[string]$Scope){if($Plan.original_root -notlike ($fixture+'*')){throw 'Fixture path escaped.'}}
            $original=Join-Path $fixture 'snapshot-original'
            $script:FixtureCurrentRoot=$original
            [void][IO.Directory]::CreateDirectory((Join-Path $original 'cmd'))
            [void][IO.Directory]::CreateDirectory((Join-Path $original 'bin'))
            foreach($name in @('cmd/git.exe','bin/git.exe','unins001.exe')){[IO.File]::WriteAllBytes((Join-Path $original $name),$image)}
            [IO.File]::WriteAllText((Join-Path $original 'old-vendor.dat'),'true original bytes')
            $candidate=Join-Path $fixture 'candidate'
            [void][IO.Directory]::CreateDirectory($candidate)
            $owned=[ordered]@{}
            foreach($name in @('migration/takeover.ps1','migration/Takeover.Core.psm1','updater/Update.Core.psm1','updater/Scope.Core.ps1','updater/Scope.Native.cs','updater/Runtime.Configuration.ps1','updater/messages.zh-CN.json')){
                $sourceName=$name.Substring($name.IndexOf('/')+1)
                $target=Join-Path $candidate $name
                [void][IO.Directory]::CreateDirectory([IO.Path]::GetDirectoryName($target))
                [IO.File]::Copy((Join-Path $sourceRoot $sourceName),$target,$false)
                $owned[$name]=(Get-FileHash -LiteralPath $target -Algorithm SHA256).Hash.ToLowerInvariant()
            }
            $newImage=[byte[]]$image.Clone();$newImage[200]=8
            foreach($name in @('cmd/git.exe','bin/git.exe','migration/git-next-bridge.exe','migration/git-next-bridge-gui.exe','updater/msi-worker.exe')){
                $target=Join-Path $candidate $name;[void][IO.Directory]::CreateDirectory([IO.Path]::GetDirectoryName($target));[IO.File]::WriteAllBytes($target,$newImage)
                $owned[$name]=(Get-FileHash -LiteralPath $target -Algorithm SHA256).Hash.ToLowerInvariant()
            }
            $package=[pscustomobject]@{schema_version=1;product='git-next';platform='windows-x64';version='1.0.3';install_scope='user';install_kind='msi';product_code='{11111111-2222-3333-4444-555555555555}';engine='source-built-git-next-native-core';complete_git_parity=$true;release_eligible=$true;files=[pscustomobject]$owned}
            $candidateManifest=Join-Path $candidate 'package.json';Write-GnJson $candidateManifest $package
            $snapshot=New-GnPreinstallSnapshot $original (Join-Path $fixture 'pre-msi') $candidateManifest ('b'*64)
            $snapshotHash=(Get-FileHash -LiteralPath $snapshot -Algorithm SHA256).Hash.ToLowerInvariant()
            $pre=Read-GnOriginalSnapshot $snapshot $snapshotHash $original
            if($pre.original_git_sha256 -ceq $package.files.'cmd/git.exe' -or [IO.File]::ReadAllText((Join-Path ([IO.Path]::GetDirectoryName($snapshot)) 'original/old-vendor.dat')) -cne 'true original bytes'){throw 'The pre-MSI snapshot did not retain distinct original bytes.'}
            $count++
            Expect-Rejection {Read-GnOriginalSnapshot $snapshot ('a'*64) $original} 'A different pre-MSI snapshot digest was accepted.';$count++
            $snapshotBytes=[IO.File]::ReadAllBytes($snapshot)
            $pre.kind='operator-recovery-original-git';Write-GnJson $snapshot $pre
            $operatorHash=(Get-FileHash -LiteralPath $snapshot -Algorithm SHA256).Hash.ToLowerInvariant()
            Expect-Rejection {Read-GnOriginalSnapshot $snapshot $operatorHash $original} 'An operator recovery attachment was accepted as a normal pre-MSI snapshot.'
            [IO.File]::WriteAllBytes($snapshot,$snapshotBytes);$pre.kind='pre-msi-original-git';$count++
            $drift=Join-Path $fixture 'snapshot-source-drift'
            [void][IO.Directory]::CreateDirectory((Join-Path $drift 'cmd'))
            [IO.File]::WriteAllBytes((Join-Path $drift 'cmd/git.exe'),$image);[IO.File]::WriteAllBytes((Join-Path $drift 'unins001.exe'),$image)
            function script:Get-GnRelatedIntegration([string]$Root){[IO.File]::WriteAllText((Join-Path $Root 'added-during-snapshot.dat'),'new unrecorded file');return @()}
            Expect-Rejection {New-GnPreinstallSnapshot $drift (Join-Path $fixture 'rejected-drift-snapshot') $candidateManifest ('b'*64)} 'A source file added during snapshotting escaped the final inventory readback.'
            if([IO.File]::Exists((Join-Path $fixture 'rejected-drift-snapshot/snapshot.json'))){throw 'An inconsistent original snapshot was published.'};$count++
            function script:Get-GnRelatedIntegration([string]$Root){return @()}
            foreach($name in @($owned.Keys)+@('package.json')){
                $target=Join-Path $original $name;[void][IO.Directory]::CreateDirectory([IO.Path]::GetDirectoryName($target));[IO.File]::Copy((Join-Path $candidate $name),$target,$true)
            }
            Expect-Rejection {New-GnPreinstallSnapshot $original (Join-Path $fixture 'invalid-post-msi') $candidateManifest ('b'*64)} 'Post-MSI bytes were accepted as a new original snapshot.';$count++
            Expect-Rejection {Get-GnTakeoverPlan $original $original $snapshot ('a'*64)} 'Apply accepted a snapshot hash not pinned before MSI.';$count++
            $plan=Get-GnTakeoverPlan $original $original $snapshot $snapshotHash
            $state=Join-Path $fixture 'protected-migrations';New-GnPrivateDirectory $state
            $sid=[Security.Principal.WindowsIdentity]::GetCurrent().User.Value
            Write-GnJson (Join-Path $state 'git-next-migrations.json') @{schema_version=1;product='git-next';user_sid=$sid}
            $id=[Guid]::NewGuid().ToString('N');$transaction=Join-Path $state $id;New-GnPrivateDirectory $transaction
            foreach($pair in @{transaction_id=$id;user_sid=$sid;state_root=$state;uninstaller_pid=$null;uninstaller_start_identity=$null}.GetEnumerator()){$plan|Add-Member -NotePropertyName $pair.Key -NotePropertyValue $pair.Value}
            $plan.status='active';$plan.root_acl=(Get-Acl -LiteralPath $original).Sddl
            $backup=Join-Path $transaction 'original';New-GnPrivateDirectory $backup
            foreach($file in $plan.files){Copy-GnMigrationFile $original $backup $file}
            Write-GnTakeoverJournal $transaction $plan
            Write-GnJson $plan.pointer @{schema_version=1;product='git-next';install_root=$original;install_scope='user';original_root=$original;transaction=$transaction;transaction_id=$id}
            Expect-Rejection {Assert-GnTakeoverRemoval $original} 'Active takeover allowed an ordinary unprotected MSI removal.';$count++
            $oldSid=$plan.user_sid;$plan.user_sid='S-1-5-99';Write-GnTakeoverJournal $transaction $plan
            Expect-Rejection {Get-GnTakeoverRecoveryContext $transaction} 'A foreign owner was accepted.';$plan.user_sid=$oldSid;Write-GnTakeoverJournal $transaction $plan;$count++
            $oldSnapshot=$plan.original_snapshot.sha256;$plan.original_snapshot.sha256='a'*64;Write-GnTakeoverJournal $transaction $plan
            Expect-Rejection {Restore-GnTakeover $transaction} 'Rollback accepted changed original snapshot metadata.';$plan.original_snapshot.sha256=$oldSnapshot;Write-GnTakeoverJournal $transaction $plan;$count++
            $script:FixtureBusy=$true
            Expect-Rejection {Restore-GnTakeover $transaction} 'A busy original installation allowed rollback.';$script:FixtureBusy=$false;$count++
            $pointerBytes=[IO.File]::ReadAllBytes($plan.pointer)
            Write-GnJson $plan.pointer @{schema_version=1;product='git-next';install_root=$original;install_scope='user';transaction=$transaction;transaction_id=('a'*32)}
            Expect-Rejection {Restore-GnTakeover $transaction} 'A different active transaction pointer was accepted.';[IO.File]::WriteAllBytes($plan.pointer,$pointerBytes);$count++
            $oldOriginal=Join-Path ([IO.Path]::GetDirectoryName($snapshot)) 'original/old-vendor.dat'
            [IO.File]::WriteAllText($oldOriginal,'tampered original backup')
            Expect-Rejection {Restore-GnTakeover $transaction} 'Changed protected original bytes authorized MSI removal.';[IO.File]::WriteAllText($oldOriginal,'true original bytes');$count++
            Expect-Rejection {Restore-GnTakeoverRoot ([pscustomobject]@{original_root=$original;root_acl='changed outside root ACL'})} 'An unrelated existing root ACL was overwritten.';$count++
            # A repair may replace the controlling MSI while the original snapshot retains its first candidate binding.
            $package|Add-Member -NotePropertyName recovery_revision -NotePropertyValue 'new MSI control'
            $package.release_eligible=$false
            Write-GnJson (Join-Path $original 'package.json') $package
            if((Get-FileHash -LiteralPath (Join-Path $original 'package.json') -Algorithm SHA256).Hash.ToLowerInvariant() -ceq $plan.package_sha256){throw 'Fixture current and original candidate identities were not distinct.'}
            Assert-GnTakeoverCurrentMsi $original $package
            $candidatePlan=$plan|ConvertTo-Json -Depth 20|ConvertFrom-Json;$candidatePlan.release_eligible=$false
            Expect-Rejection {Start-GnTakeover $candidatePlan $state ''} 'An experimental recovery controller was allowed to activate a takeover.';$count++
            $prepared=Restore-GnTakeover $transaction -PrepareOnly
            if($prepared -ine (Join-Path $transaction 'recovery-tools/migration/takeover.ps1') -or $script:FixtureRemovalCalls -ne 0 -or -not [IO.File]::Exists((Join-Path $original 'package.json'))){throw 'Preparing the protected runner performed MSI removal or selected another transaction.'};$count++
            function script:Invoke-GnTakeoverMsiRemoval($Recovery){
                $script:FixtureRemovalCalls++
                Assert-GnTakeoverRemoval $Recovery.plan.install_root
                $current=Assert-GnTakeoverRecoveryTools $Recovery
                foreach($entry in $current.files.PSObject.Properties){[IO.File]::Delete((Join-Path $Recovery.plan.install_root $entry.Name))}
                [IO.File]::Delete((Join-Path $Recovery.plan.install_root 'package.json'))
                $script:FixtureMsi=$false
                throw 'Fixture interruption after normal MSI removal.'
            }
            Expect-Rejection {Restore-GnTakeover $transaction} 'The simulated post-MSI interruption was not retained.'
            if($script:FixtureRemovalCalls -ne 1 -or [IO.File]::Exists((Join-Path $original 'package.json'))){throw 'Controlled removal did not use the bound current MSI.'}
            $count++
            $recovery=Get-GnTakeoverRecoveryContext $transaction
            if($recovery.plan.status -cne 'rolling-back' -or $recovery.plan.rollback_package_sha256 -ceq $recovery.plan.package_sha256){throw 'Recovery lost its separate original and current MSI bindings.'}
            if((Restore-GnTakeover $transaction -PrepareOnly) -ine $prepared -or $script:FixtureRemovalCalls -ne 1){throw 'Protected-runner preparation could not resume without the live MSI manifest.'};$count++
            $tool=Join-Path $transaction 'recovery-tools/updater/messages.zh-CN.json';$toolBytes=[IO.File]::ReadAllBytes($tool);[IO.File]::WriteAllText($tool,'changed protected runner')
            Expect-Rejection {Restore-GnTakeover $transaction} 'A changed protected recovery tool was accepted.';[IO.File]::WriteAllBytes($tool,$toolBytes);$count++
            Restore-GnTakeover $transaction
            $done=Get-Content -LiteralPath (Join-Path $transaction 'transaction.json') -Raw | ConvertFrom-Json
            if($done.status -cne 'rolled-back' -or [IO.File]::Exists($plan.pointer) -or $script:FixtureRemovalCalls -ne 1){throw 'Rollback did not resume safely after MSI removed the live manifest.'}
            foreach($file in $pre.files){if((Get-FileHash -LiteralPath (Join-Path $original $file.relative) -Algorithm SHA256).Hash.ToLowerInvariant() -cne $file.sha256){throw 'Rollback did not restore exact pre-MSI original bytes.'}}
            $count++
            $raceSource=Join-Path $fixture 'race-source';$raceTarget=Join-Path $fixture 'race-target'
            [void][IO.Directory]::CreateDirectory($raceSource);[IO.File]::WriteAllText((Join-Path $raceSource 'collision.dat'),'snapshot bytes')
            $record=@(Get-GnTree $raceSource)[0]
            function script:New-GnOutputParent([string]$Target){[void][IO.Directory]::CreateDirectory([IO.Path]::GetDirectoryName($Target));[IO.File]::WriteAllText($Target,'concurrent third-party bytes')}
            Expect-Rejection {Copy-GnMigrationFile $raceSource $raceTarget $record} 'A concurrently created destination was overwritten.'
            if([IO.File]::ReadAllText((Join-Path $raceTarget 'collision.dat')) -cne 'concurrent third-party bytes'){throw 'Missing-only copy deleted a concurrent file.'};$count++
            [IO.File]::WriteAllText((Join-Path $raceTarget 'collision.dat'),'authorized bridge bytes')
            $bridgeHash=(Get-FileHash -LiteralPath (Join-Path $raceTarget 'collision.dat') -Algorithm SHA256).Hash.ToLowerInvariant()
            Expect-Rejection {Copy-GnMigrationFile $raceSource $raceTarget $record $bridgeHash} 'Changed authorized replacement bytes were overwritten.'
            if([IO.File]::ReadAllText((Join-Path $raceTarget 'collision.dat')) -cne 'concurrent third-party bytes'){throw 'Authorized replacement deleted a changed destination.'};$count++
        } finally {
            foreach($name in $mockNames){Set-Item -Path ('Function:script:'+$name) -Value $saved[$name]}
            foreach($name in @('FixtureBusy','FixtureMsi','FixtureRemovalCalls','FixtureConfig','FixtureCurrentRoot')){Remove-Variable -Name $name -Scope Script -ErrorAction SilentlyContinue}
        }
        return $count
    } $fixture (Join-Path $PSScriptRoot '../packaging/windows') $image
    $cases += $extra
    $cases += & $module {
        param($fixture)
        $time=[DateTime]::UtcNow
        $workerPath=Join-Path $fixture 'worker/updater/msi-worker.exe'
        $package=[pscustomobject]@{files=[pscustomobject]@{'updater/msi-worker.exe'=('a'*64);'bin/git.exe'=('b'*64)}}
        $guard=[pscustomobject]@{id=1001;start_ticks=$time.ToUniversalTime().Ticks;path=$workerPath}
        $script:FixtureProcesses=@([pscustomobject]@{Id=1001;StartTime=$time;MainModule=[pscustomobject]@{FileName=$workerPath}})
        function script:Get-Process {param($Name,$ErrorAction) return $script:FixtureProcesses}
        try {
            if(Test-GnTakeoverRemovalBusy (Join-Path $fixture 'worker') $package $guard){throw 'The exact current MSI guard worker was treated as another busy worker.'}
            $guard.start_ticks++
            if(-not (Test-GnTakeoverRemovalBusy (Join-Path $fixture 'worker') $package $guard)){throw 'A reused worker PID with different start identity was ignored.'}
            $guard.start_ticks--
            $script:FixtureProcesses+= [pscustomobject]@{Id=1002;StartTime=$time;MainModule=[pscustomobject]@{FileName=(Join-Path $fixture 'worker/bin/git.exe')}}
            if(-not (Test-GnTakeoverRemovalBusy (Join-Path $fixture 'worker') $package $guard)){throw 'A real additional owned Git process was ignored.'}
            return 3
        } finally {Remove-Item Function:script:Get-Process;Remove-Variable FixtureProcesses -Scope Script}
    } $fixture
    $cases += & $module {
        $original=[pscustomobject]@{hive='CurrentUser';path='Software\isolated-takeover-fixture';keys=@([pscustomobject]@{relative='';values=@([pscustomobject]@{name='entry';kind='String';value='old'})})}
        $post=[pscustomobject]@{hive=$original.hive;path=$original.path;keys=@([pscustomobject]@{relative='';values=@([pscustomobject]@{name='entry';kind='String';value='post MSI'})})}
        $saved=(Get-Command Get-GnRegistrySnapshot -CommandType Function).ScriptBlock
        function script:Get-GnRegistrySnapshot($Hive,$Path){return $script:FixtureRegistry}
        try {
            $script:FixtureRegistry=$post;Assert-GnTakeoverRegistryRecovery $original $post
            $script:FixtureRegistry=[pscustomobject]@{keys=@([pscustomobject]@{relative='';values=@([pscustomobject]@{name='entry';kind='String';value='third-party change'})})}
            $rejected=$false;try{Assert-GnTakeoverRegistryRecovery $original $post}catch{$rejected=$true}
            if(-not $rejected){throw 'An unrelated registry value was authorized for restoration.'}
            return 2
        } finally {Set-Item Function:script:Get-GnRegistrySnapshot $saved;Remove-Variable FixtureRegistry -Scope Script}
    }
    $cases += & $module {
        $sid=[Security.Principal.WindowsIdentity]::GetCurrent().User.Value
        $record=[pscustomobject]@{name='isolated-old-updater';xml='<Task>original fixture</Task>';sid=$sid}
        $folder=[pscustomobject]@{current=[pscustomobject]@{Xml=$record.xml;Definition=[pscustomobject]@{Principal=[pscustomobject]@{UserId=$sid;LogonType=3}}};registered=0;deleted=0;flags=0;race=$false}
        $folder|Add-Member -MemberType ScriptMethod -Name GetTask -Value {param($Name)return $this.current}
        $folder|Add-Member -MemberType ScriptMethod -Name RegisterTask -Value {param($Name,$Xml,$Flags,$User,$Password,$Logon,$Security)$this.registered++;$this.flags=$Flags;if($this.race){$this.current='concurrent task';throw 'A task appeared after lookup.'}}
        $folder|Add-Member -MemberType ScriptMethod -Name DeleteTask -Value {param($Name,$Flags)$this.deleted++}
        Set-GnOldUpdateTasksAtFolder $folder @($record) $true
        if($folder.registered -or $folder.deleted){throw 'An unchanged old updater task was unnecessarily replaced.'}
        $folder.current.Xml='<Task>unrelated new task</Task>'
        $rejected=$false;try{Set-GnOldUpdateTasksAtFolder $folder @($record) $true}catch{$rejected=$true}
        if(-not $rejected -or $folder.registered -or $folder.current.Xml -cne '<Task>unrelated new task</Task>'){throw 'A same-named third-party updater task was overwritten.'}
        $rejected=$false;try{Set-GnOldUpdateTasksAtFolder $folder @($record) $false}catch{$rejected=$true}
        if(-not $rejected -or $folder.deleted){throw 'Removing the old updater deleted a changed same-named task.'}
        $folder.current.Xml=$record.xml;$folder.current.Definition.Principal.UserId='S-1-5-99'
        $rejected=$false;try{Set-GnOldUpdateTasksAtFolder $folder @($record) $true}catch{$rejected=$true}
        if(-not $rejected -or $folder.registered){throw 'An updater task running as another account was overwritten.'}
        $folder.current=$null;Set-GnOldUpdateTasksAtFolder $folder @($record) $true
        if($folder.registered -ne 1 -or $folder.flags -ne 2){throw 'A missing updater task did not use create-only registration.'}
        $folder.race=$true
        $rejected=$false;try{Set-GnOldUpdateTasksAtFolder $folder @($record) $true}catch{$rejected=$true}
        if(-not $rejected -or $folder.current -cne 'concurrent task' -or $folder.flags -ne 2){throw 'A concurrent updater task was replaced.'}
        return 6
    }
    foreach($kind in @('worktree','bare','pointer')){
        $root=Join-Path $fixture ('repo-'+$kind);[void][IO.Directory]::CreateDirectory($root)
        if($kind -eq 'pointer'){[IO.File]::WriteAllText((Join-Path $root '.git'),'gitdir: ../outside-preserved')}
        else{
            $metadata=if($kind -eq 'bare'){$root}else{Join-Path $root '.git'}
            [void][IO.Directory]::CreateDirectory((Join-Path $metadata 'objects'));[void][IO.Directory]::CreateDirectory((Join-Path $metadata 'refs'))
            [IO.File]::WriteAllText((Join-Path $metadata 'HEAD'),"ref: refs/heads/main`n")
        }
        $rejected=$false;try{&$module {param($root)Get-GnTree $root} $root|Out-Null}catch{$rejected=$true}
        if(-not $rejected){throw 'A real repository inside the installation was accepted for destructive migration.'};$cases++
    }
    $ordinary=Join-Path $fixture 'ordinary-vendor';[void][IO.Directory]::CreateDirectory($ordinary)
    foreach($name in @('.gitignore','.gitmodules')){[IO.File]::WriteAllText((Join-Path $ordinary $name),'ordinary vendor text')}
    if(@(&$module {param($root)Get-GnTree $root} $ordinary).Count -ne 2){throw 'Ordinary vendor Git configuration names were treated as a repository.'};$cases++
    [pscustomobject]@{status='pass';cases=$cases;scope='isolated takeover snapshot, simulated MSI removal/resume, repository and payload/stale guards';git_or_terminal_executed=$false;installation_modified=$false} | ConvertTo-Json -Compress
} finally {
    $resolved = [IO.Path]::GetFullPath($fixture)
    if ([IO.Path]::GetDirectoryName($resolved).TrimEnd('\') -ine ([IO.Path]::GetTempPath()).TrimEnd('\') -or [IO.Path]::GetFileName($resolved) -notmatch '^git-next-uninstall-[a-f0-9]{32}$') { throw 'Unexpected fixture cleanup path.' }
    Remove-Item -LiteralPath $resolved -Recurse -Force
}
