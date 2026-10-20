"""The consolidated native-product correctness batch and its separate paired timing phase."""
import argparse
from concurrent.futures import ThreadPoolExecutor
import hashlib
import json
import os
from pathlib import Path
import subprocess
import sys
import time
import uuid

from product_batch import require_implementation_complete

ROOT=Path(__file__).resolve().parents[1]


def sha(path):
    with Path(path).open('rb') as stream: return hashlib.file_digest(stream,'sha256').hexdigest()


def main():
    parser=argparse.ArgumentParser(__doc__)
    parser.add_argument('--native-packages',type=Path,required=True)
    parser.add_argument('--reference',type=Path,required=True)
    parser.add_argument('--output',type=Path,required=True)
    parser.add_argument('--workers',type=int,default=8,choices=range(1,17))
    parser.add_argument('--wall-clock-target',type=float,default=60,help='report the actual batch time against this target; never skip checks')
    parser.add_argument('--performance',action='store_true')
    parser.add_argument('--correctness-receipt',type=Path)
    args=parser.parse_args()
    started_batch=time.monotonic()
    if args.wall_clock_target<=0: parser.error('A positive wall-clock target is required')
    require_implementation_complete(parser)
    matrix=json.loads(args.native_packages.read_text())
    expected={'windows-x64','windows-arm64','linux-x64','linux-arm64','macos-arm64'}
    if matrix['schema_version']!=1 or set(matrix['platforms'])!=expected: parser.error('Five complete native package inputs required')
    for name,value in matrix['files'].items():
        if sha(name)!=value: parser.error('Frozen batch input changed: '+name)
    for name,value in matrix['harnesses'].items():
        if sha(ROOT/name)!=value: parser.error('Frozen batch harness changed: '+name)
    reference=args.reference.resolve(strict=True);reference_sha=sha(reference)
    output=args.output.resolve();output.mkdir(parents=True,exist_ok=False)
    identity=sha(args.native_packages)
    win=matrix['platforms']['windows-x64'];binary=Path(win['directory'])/'bin/git.exe'
    env={**os.environ,'GIT_TERMINAL_PROMPT':'0','LANG':'C','LC_ALL':'C',
         'PATH':str(reference.parent)+os.pathsep+os.environ.get('PATH','')}
    results=[]
    def run(case):
        name,command,timeout=case;started=time.monotonic()
        try:
            process=subprocess.run(command,cwd=ROOT,env=env,capture_output=True,timeout=timeout)
            (output/(name+'.log')).write_bytes(process.stdout+b'\n'+process.stderr)
            result=dict(case=name,status='pass' if process.returncode==0 else 'fail',exit_code=process.returncode)
        except subprocess.TimeoutExpired as error:
            (output/(name+'.log')).write_bytes((error.stdout or b'')+b'\n'+(error.stderr or b''))
            result=dict(case=name,status='fail',reason='aggregate item timeout')
        except OSError as error:
            result=dict(case=name,status='fail',reason=str(error)[:200])
        result['seconds']=round(time.monotonic()-started,3)
        result['started_seconds_from_batch']=round(started-started_batch,3)
        result['finished_seconds_from_batch']=round(time.monotonic()-started_batch,3)
        print(name+' => '+result['status'],flush=True)
        return result
    python=sys.executable
    shell=str(Path(os.environ['SystemRoot'])/'System32/WindowsPowerShell/v1.0/powershell.exe')
    if args.performance:
        if not args.correctness_receipt: parser.error('The passed correctness batch receipt is required before timings')
        prior=json.loads(args.correctness_receipt.read_text())
        if prior['status']!='pass' or prior['matrix_sha256']!=identity or prior['reference_sha256']!=reference_sha:
            parser.error('Correctness receipt does not identify these final bytes and reference')
        checkpoint=output/'native-identity.json'
        checkpoint.write_text(json.dumps(dict(binary_sha256=sha(binary),source_tree_sha256=matrix['source_tree_sha256'])))
        for files,dirs in [(1000,16),(10000,1000),(100000,1000)]:
            results.append(run(('performance-'+str(files),[python,'tests/benchmark.py',str(binary),'--reference',str(reference),
                '--runs','40','--files',str(files),'--directories',str(dirs),'--read-commands','--system-config-enabled','--native-distribution',
                '--fixture-layout','packed','--source-checkpoint',str(checkpoint),'--output',str(output/('performance-'+str(files)+'.json'))],1800)))
        results.append(run(('performance-network',[python,'tests/network_benchmark.py',str(binary),'--reference',str(reference),
                            '--runs','20','--native-distribution','--source-checkpoint',str(checkpoint),'--output',str(output/'performance-network.json')],1800)))
    else:
        cases=[('windows-core-contracts',[python,'tests/native_contract_acceptance.py',str(binary),'--reference',str(reference),'--output',str(output/'windows-core.json')],1200),
               ('windows-owned-integrations',[python,'tests/native_integrations_acceptance.py',str(binary),'--output',str(output/'windows-integrations.json')],900),
               ('windows-dpapi-migration',[python,'tests/gcm_acceptance.py',str(binary),'--helper',win['gcm'],'--native-distribution'],900),
               ('windows-update-contracts',[shell,'-NoProfile','-ExecutionPolicy','Bypass','-File','tests/updater_acceptance.ps1'],900)]
        for platform in ['linux-x64','linux-arm64','macos-arm64']:
            item=matrix['platforms'][platform]
            cases.append((platform,[python,'tools/native_docker_batch.py','--platform',platform,'--archive',item['archive'],
                '--sha256',matrix['files'][item['archive']],'--base-image',item['base_image'],'--output',str(output/platform)],4500))
        cases.append(('docker-login-migration',[python,'tests/containers/migration_host.py','--native-package',matrix['platforms']['linux-x64']['archive'],
                            '--output',str(output/'docker-login-migration.json')],2400))
        msi_lane=[]
        for scope in ['user','machine']:
            command=[shell,'-NoProfile','-ExecutionPolicy','Bypass','-File',
                     'tests/elevated_msi_acceptance.ps1' if scope=='machine' else 'tests/msi_acceptance.ps1',
                     '-Package',win['base_msi_'+scope],'-UpgradePackage',win['msi_'+scope]]
            if scope=='machine': command+=['-Output',str(output/'msi-machine-elevated.log')]
            msi_lane.append(('msi-'+scope,command,1800))
        # One race-enabled POSIX updater test item; the source mount is read-only.
        container='git-next-updater-race-'+uuid.uuid4().hex
        command=['docker','run','--rm','--name',container,'--label','git-next.owner=git-next-native-product-batch',
                 '--network','none','--read-only','--tmpfs','/tmp:rw,exec,size=1024m',
                 '--mount','type=bind,source='+matrix['race_binary']+',target=/updater-tests,readonly',
                 '-e','HOME=/tmp/home','-e','GOCACHE=/tmp/go-cache','-e','GOTOOLCHAIN=local','-e','GOPATH=/tmp/go',
                 matrix['go_race_image'],'/updater-tests','-test.v','-test.count=1']
        cases.append(('posix-updater-race-contracts',command,900))
        # Repository/profile/container fixtures are independent. Windows Installer owns
        # one system-wide transaction, so both MSI scopes share one serial worker lane.
        lanes=[[case] for case in cases]+[msi_lane]
        def run_lane(lane):
            return [run(case) for case in lane]
        try:
            with ThreadPoolExecutor(max_workers=args.workers) as pool:
                for lane_results in pool.map(run_lane,lanes): results.extend(lane_results)
        finally:
            probe=subprocess.run(['docker','inspect',container],capture_output=True,text=True)
            if probe.returncode==0:
                value=json.loads(probe.stdout)[0]
                if value['Config']['Labels'].get('git-next.owner')!='git-next-native-product-batch': raise ValueError('Unrelated race fixture')
                subprocess.run(['docker','rm','-f',container],check=True)
    unchanged=sha(args.native_packages)==identity and sha(reference)==reference_sha
    unchanged=unchanged and all(sha(path)==value for path,value in matrix['files'].items())
    unchanged=unchanged and all(sha(ROOT/name)==value for name,value in matrix['harnesses'].items())
    results.append(dict(case='frozen-input-readback',status='pass' if unchanged else 'fail'))
    elapsed=round(time.monotonic()-started_batch,3)
    report=dict(schema_version=1,status='pass' if all(item['status']=='pass' for item in results) else 'fail',
                phase='performance' if args.performance else 'correctness',matrix_sha256=identity,reference_sha256=reference_sha,
                source_tree_sha256=matrix['source_tree_sha256'],results=results,release_eligible=False,
                native_mac_product_executed=False,personal_account_accessed=False,
                elapsed_seconds=elapsed,wall_clock_target_seconds=args.wall_clock_target,
                wall_clock_target_met=elapsed<args.wall_clock_target,
                workers=1 if args.performance else args.workers,msi_transactions_serialized=True,
                checks_skipped_to_meet_time_target=False)
    if not args.performance:
        functional=[item for item in results if item['case'] not in ('msi-user','msi-machine','frozen-input-readback')]
        report['parallel_functional_elapsed_seconds']=round(max(item['finished_seconds_from_batch'] for item in functional)-min(item['started_seconds_from_batch'] for item in functional),3)
        report['parallel_functional_target_met']=report['parallel_functional_elapsed_seconds']<args.wall_clock_target
        report['serialized_msi_lifecycle_seconds']=round(sum(item['seconds'] for item in results if item['case'] in ('msi-user','msi-machine')),3)
    (output/'batch.json').write_text(json.dumps(report,indent=2)+'\n',newline='\n')
    print(json.dumps(dict(status=report['status'],phase=report['phase'],items=len(results),
                          elapsed_seconds=elapsed,wall_clock_target_met=report['wall_clock_target_met'])))
    return report['status']!='pass'


if __name__=='__main__': raise SystemExit(main())
