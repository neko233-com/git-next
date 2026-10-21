"""Owned LFS, shell and signing integration using generated files and keys only."""
import argparse
import base64
import hashlib
import json
import os
from pathlib import Path
import subprocess
import tempfile

from lfs_transfer_acceptance import Fixture
from windows_msi_worker_acceptance import check_worker


def main():
    parser = argparse.ArgumentParser(__doc__)
    parser.add_argument('binary',type=Path)
    parser.add_argument('--output',type=Path,required=True)
    args = parser.parse_args()
    binary = args.binary.resolve(strict=True)
    results=[]
    with tempfile.TemporaryDirectory(prefix='gn-') as temporary:
        root=Path(temporary);home=root/'home';home.mkdir();repo=root/'repo';keys=root/'signing';keys.mkdir(mode=0o700)
        env={key:value for key,value in os.environ.items() if not key.startswith(('GIT_','GCM_','SSH_','GNUPG'))}
        env.update(HOME=str(home),USERPROFILE=str(home),SSH_ROOT_PATH=str(home/'.ssh'),
                   GNUPGHOME=str(keys),GIT_CONFIG_NOSYSTEM='1',GIT_CONFIG_GLOBAL=str(home/'.gitconfig'),
                   GIT_TERMINAL_PROMPT='0',GIT_AUTHOR_NAME='Fixture',GIT_COMMITTER_NAME='Fixture',
                   GIT_AUTHOR_EMAIL='fixture@example.invalid',GIT_COMMITTER_EMAIL='fixture@example.invalid',GIT_NEXT_LANGUAGE='english',
                   GIT_LFS_SKIP_SMUDGE='1')
        def git(*arguments,input=None,ok=True):
            p=subprocess.run([str(binary),*map(str,arguments)],cwd=repo if repo.exists() else root,
                             env=env,input=input,capture_output=True,timeout=90)
            assert (p.returncode==0)==ok, 'integration command failed: '+str(arguments[:2])+', exit='+str(p.returncode)
            return p.stdout
        git('init','-q','-b','main',repo)
        git('config','user.name','Fixture');git('config','user.email','fixture@example.invalid')
        def record(name,action):
            try: action();results.append(dict(case=name,status='pass'))
            except (AssertionError,OSError,ValueError,subprocess.SubprocessError) as e:
                results.append(dict(case=name,status='fail',reason=str(e)[:240]))
        def lfs():
            assert b'git-lfs/' in git('lfs','version')
            git('lfs','install','--local','--skip-smudge')
            git('lfs','track','*.asset')
            data=b'\x00\r\nfixture LFS content\n'*4096
            asset=repo/'scene.asset';asset.write_bytes(data)
            git('add','-A');git('commit','-qm','LFS fixture')
            pointer=git('show','HEAD:scene.asset')
            oid=hashlib.sha256(data).hexdigest()
            assert ('oid sha256:'+oid).encode() in pointer
            fixture=Fixture(root)
            try:
                git('remote','add','origin',root/'remote.git')
                git('config','lfs.url',fixture.url)
                git('config','http.sslCAInfo',fixture.ca)
                auth=base64.b64encode(('fixture:'+fixture.password).encode()).decode()
                git('config','http.extraHeader','Authorization: Basic '+auth)
                git('lfs','push','--all','origin')
                assert fixture.objects[oid]==data
                assert oid in fixture.verified
                cache=repo/'.git/lfs/objects'/oid[:2]/oid[2:4]/oid
                cache.unlink()
                git('lfs','fetch','origin','HEAD')
                assert cache.read_bytes()==data
                asset.write_bytes(pointer)
                git('lfs','checkout')
                assert asset.read_bytes()==data
                cache.unlink()
                fixture.failure='corrupt'
                git('lfs','fetch','origin','HEAD',ok=False)
                assert not cache.exists()
                fixture.failure=None
                git('lfs','fetch','origin','HEAD')
            finally: fixture.close()
        record('D02-primary-lfs-filter-https-upload-download-cache-integrity',lfs)
        def signing():
            package=binary.parent.parent
            choices=[package/'runtime/usr/bin/gpg',package/'runtime/usr/bin/gpg.exe',package/'runtime/mingw64/bin/gpg.exe']
            gpg=next((path for path in choices if path.is_file()),None)
            assert gpg is not None,'Owned signing entrypoint missing'
            if os.name == 'nt':
                cygpath=gpg.with_name('cygpath.exe')
                converted=subprocess.run([str(cygpath),'-u',str(keys)],env=env,capture_output=True,check=True,timeout=15)
                env['GNUPGHOME']=converted.stdout.decode().strip()
            process=subprocess.run([str(gpg),'--batch','--pinentry-mode','loopback','--passphrase','',
                                    '--quick-generate-key','Fixture <fixture@example.invalid>','ed25519','sign','0'],
                                   env=env,capture_output=True,timeout=90)
            assert process.returncode==0,'Generated fixture signing key refused: '+process.stderr.decode(errors='replace')[-180:]
            git('config','gpg.program',gpg.as_posix());git('config','user.signingkey','fixture@example.invalid')
            git('commit','--allow-empty','-S','-qm','signed fixture')
            git('verify-commit','HEAD')
            git('tag','-s','signed-fixture','-m','signed tag')
            git('verify-tag','signed-fixture')
            gpgconf=gpg.with_name('gpgconf'+gpg.suffix)
            if gpgconf.is_file(): subprocess.run([str(gpgconf),'--kill','all'],env=env,capture_output=True,timeout=15)
        record('D02-owned-gpg-generated-key-commit-and-tag-verification',signing)
        if os.name=='nt':
            record('D05-native-no-console-msi-worker-unicode-quoting-exit-and-argument-guards',lambda:check_worker(binary.parent.parent/'updater/msi-worker.exe',root,env))
    report=dict(schema_version=1,status='pass' if all(item['status']=='pass' for item in results) else 'fail',
                cases=results,personal_credentials_accessed=False,fixture_keys_deleted=True)
    if args.output.exists(): raise ValueError('Result output must be new')
    args.output.write_text(json.dumps(report,indent=2)+'\n',encoding='utf-8',newline='\n')
    print(json.dumps(report,indent=2))
    return report['status']!='pass'


if __name__=='__main__': raise SystemExit(main())
