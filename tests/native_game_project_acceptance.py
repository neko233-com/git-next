"""Game-asset defaults, automatic LFS and lock enforcement with generated data only."""
import argparse
import base64
import hashlib
import json
import os
from pathlib import Path
import subprocess
import tempfile
import time

from lfs_transfer_acceptance import Fixture


def main():
    parser=argparse.ArgumentParser(__doc__)
    parser.add_argument('binary',type=Path)
    parser.add_argument('--output',type=Path,required=True)
    args=parser.parse_args();binary=args.binary.resolve(strict=True);package=binary.parent.parent
    results=[];started=time.monotonic()
    with tempfile.TemporaryDirectory(prefix='gn-game-') as temporary:
        root=Path(temporary);home=root/'home';home.mkdir();repo=root/'game';bare=root/'remote.git';clone=root/'clone'
        env={k:v for k,v in os.environ.items() if not k.startswith(('GIT_','GCM_','SSH_','GNUPG'))}
        env.update(HOME=str(home),USERPROFILE=str(home),XDG_CONFIG_HOME=str(home/'.config'),SSH_ROOT_PATH=str(home/'.ssh'),
                   GIT_CONFIG_GLOBAL=str(home/'.gitconfig'),GIT_CONFIG_SYSTEM=str(package/'runtime/etc/gitconfig'),
                   GIT_NEXT_CONFIG_HOME=str(home/'git-next'),GIT_TERMINAL_PROMPT='0',GCM_INTERACTIVE='never',GIT_NEXT_LANGUAGE='english',
                   GIT_AUTHOR_NAME='Fixture',GIT_COMMITTER_NAME='Fixture',GIT_AUTHOR_EMAIL='fixture@example.invalid',GIT_COMMITTER_EMAIL='fixture@example.invalid')
        fixture=Fixture(root);fixture.locks_supported=True
        auth=base64.b64encode(('fixture:'+fixture.password).encode()).decode()
        overrides=[('http.extraheader','Authorization: Basic '+auth),('http.sslCAInfo',str(fixture.ca)),
                   ('http.schannelUseSSLCAInfo','true'),('http.schannelCheckRevoke','false'),('credential.helper','')]
        env['GIT_CONFIG_COUNT']=str(len(overrides))
        for i,(key,value) in enumerate(overrides):env['GIT_CONFIG_KEY_'+str(i)]=key;env['GIT_CONFIG_VALUE_'+str(i)]=value
        def git(*arguments,cwd=None,ok=True):
            p=subprocess.run([str(binary),*map(str,arguments)],cwd=cwd or (repo if repo.exists() else root),env=env,capture_output=True,timeout=90)
            assert (p.returncode==0)==ok, 'Game fixture command expectation failed: '+str(arguments[:2])
            return p.stdout
        def record(name,action):
            try:action();results.append({'case':name,'status':'pass'})
            except (AssertionError,OSError,ValueError,subprocess.SubprocessError):results.append({'case':name,'status':'fail'})
        try:
            git('init','-q','-b','main',repo);git('init','-q','--bare','-b','main',bare,cwd=root)
            git('remote','add','origin',bare)
            def defaults():
                assert b'git-lfs/' in git('lfs','version')
                assert git('config','--get','filter.lfs.process').strip()==b'git-lfs filter-process'
                for name in ('pre-push','post-checkout','post-commit','post-merge'):
                    assert ('git lfs '+name).encode() in (repo/'.git/hooks'/name).read_bytes()
                assert git('status','--porcelain')==b''
            record('builtin-lfs-without-separate-install-and-empty-init-worktree',defaults)
            attributes=(package/'docs/game-profiles/unity.gitattributes').read_bytes()+b'*.uasset filter=lfs diff=lfs merge=lfs -text lockable\n'
            (repo/'.gitattributes').write_bytes(attributes)
            (repo/'.gitignore').write_bytes((package/'docs/game-profiles/unity.gitignore').read_bytes())
            (repo/'.lfsconfig').write_text('[lfs]\n\turl = '+fixture.url+'\n',encoding='utf-8',newline='\n')
            for name in ('Assets/Scenes','Assets/Art','Assets/Binary','Content','Library'):(repo/name).mkdir(parents=True,exist_ok=True)
            scene=repo/'Assets/Scenes/中文场景.unity';scene.write_bytes(b'%YAML 1.1\nScene: 1\n')
            (repo/'Assets/Scenes/中文场景.unity.meta').write_bytes(b'fileFormatVersion: 2\nguid: 00000000000000000000000000000001\n')
            binary_asset=b'\0\r\nraw asset\n'*1024;(repo/'Assets/Binary/object.asset').write_bytes(binary_asset)
            media=(b'\0\r\nFBX-LFS-fixture\n'*250000)[:4*1024*1024]
            fbx=repo/'Assets/Art/model.fbx';fbx.write_bytes(media)
            unreal=repo/'Content/Hero.uasset';unreal.write_bytes(b'\0UE-ASSET\r\n'*1024)
            (repo/'Library/generated-cache').write_bytes(b'ignored')
            git('add','-A');git('commit','-qm','game fixture')
            def content():
                assert git('show','HEAD:Assets/Binary/object.asset')==binary_asset
                assert b'version https://git-lfs.github.com/spec/v1' in git('show','HEAD:Assets/Art/model.fbx')
                assert b'Library/' not in git('ls-files')
                scene.write_bytes(scene.read_bytes().replace(b'\n',b'\r\n'))
                assert git('status','--porcelain','--','Assets/Scenes')==b''
                scene.write_bytes(b'%YAML 1.1\nScene: 2\n')
                assert git('status','--porcelain','--','Assets/Scenes')
                git('add',scene);git('commit','-qm','real scene edit')
            record('unity-yaml-meta-binary-auto-detection-lfs-pointer-and-real-vs-newline-edits',content)
            def push_clone():
                git('push','-u','origin','main')
                assert hashlib.sha256(media).hexdigest() in fixture.objects
                git('clone','-q',bare,clone,cwd=root)
                assert (clone/'Assets/Art/model.fbx').read_bytes()==media
                assert (clone/'Assets/Binary/object.asset').read_bytes()==binary_asset
                assert (clone/'Assets/Scenes/中文场景.unity').read_bytes()==scene.read_bytes()
            record('automatic-lfs-prepush-upload-and-clone-smudge-without-lfs-install',push_clone)
            def locks():
                git('lfs','lock','Content/Hero.uasset');assert fixture.asset_locks
                git('lfs','lock','Content/Hero.uasset',ok=False)
                unreal.write_bytes(b'\0UE-EDIT\r\n'*1024);git('add',unreal);git('commit','-qm','locked game edit')
                current=git('rev-parse','refs/heads/main',cwd=bare)
                for item in fixture.asset_locks.values():item['owner']['name']='other-fixture'
                git('config','lfs.locksverify','true');git('push','origin','main',ok=False)
                assert git('rev-parse','refs/heads/main',cwd=bare)==current
                for item in fixture.asset_locks.values():item['owner']['name']='fixture'
                git('push','origin','main');git('lfs','unlock','Content/Hero.uasset');assert not fixture.asset_locks
            record('lfs-lock-unlock-duplicate-and-foreign-lock-blocks-ref-publication',locks)
            def transfer_failure():
                fbx.write_bytes(media+b'changed');git('add',fbx);git('commit','-qm','upload failure fixture')
                current=git('rev-parse','refs/heads/main',cwd=bare);fixture.failure='reject'
                git('push','origin','main',ok=False);assert git('rev-parse','refs/heads/main',cwd=bare)==current
                fixture.failure=None;git('push','origin','main')
            record('failed-game-asset-transfer-retains-remote-refs-and-recovers',transfer_failure)
            def paths_hooks():
                directory=repo/'Assets'
                for _ in range(12):directory=directory/'deep-asset-folder';directory.mkdir(exist_ok=True)
                file=directory/'角色.txt';file.write_bytes(b'long-path\n');git('add',file)
                hook=repo/'.git/hooks/pre-commit';hook.write_bytes(b'#!/bin/sh\nexit 42\n');hook.chmod(0o755)
                git('commit','-qm','must reject custom hook',ok=False);assert hook.read_bytes()==b'#!/bin/sh\nexit 42\n'
                hook.unlink();git('commit','-qm','deep unicode path')
            record('deep-unicode-game-paths-and-existing-custom-hook-preservation',paths_hooks)
        finally:fixture.close()
    value=dict(status='pass' if all(v['status']=='pass' for v in results) else 'fail',product='git-next',
               seconds=round(time.monotonic()-started,3),separate_lfs_installer_used=False,personal_repository_accessed=False,results=results)
    args.output.parent.mkdir(parents=True,exist_ok=True);args.output.write_text(json.dumps(value,indent=2)+'\n',encoding='utf-8');print(json.dumps(value))
    raise SystemExit(value['status']!='pass')


if __name__=='__main__':main()
