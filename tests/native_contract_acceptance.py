"""One collected native-core contract matrix using disposable repositories only."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile


def main():
    parser = argparse.ArgumentParser(__doc__)
    parser.add_argument('binary',type=Path)
    parser.add_argument('--reference',required=True,type=Path)
    parser.add_argument('--output',required=True,type=Path)
    args = parser.parse_args()
    binary = args.binary.resolve(strict=True)
    reference = args.reference.resolve(strict=True)
    if args.output.exists(): parser.error('Use a new batch result')
    results = []
    with tempfile.TemporaryDirectory(prefix='git-next-native-contract-') as temporary:
        root = Path(temporary)
        home = root/'home'; home.mkdir()
        environment = {key:value for key,value in os.environ.items()
                       if not key.startswith(('GIT_','GCM_','SSH_'))}
        environment.update(HOME=str(home),USERPROFILE=str(home),SSH_ROOT_PATH=str(home/'.ssh'),
            XDG_CONFIG_HOME=str(home/'.config'),GIT_NEXT_CONFIG_HOME=str(home/'git-next'),
            GIT_CONFIG_NOSYSTEM='1',GIT_CONFIG_GLOBAL=str(home/'.gitconfig'),GIT_TERMINAL_PROMPT='0',
            GIT_AUTHOR_NAME='Fixture',GIT_AUTHOR_EMAIL='fixture@example.invalid',
            GIT_COMMITTER_NAME='Fixture',GIT_COMMITTER_EMAIL='fixture@example.invalid',
            GIT_AUTHOR_DATE='2026-10-20T00:00:00Z',GIT_COMMITTER_DATE='2026-10-20T00:00:00Z',
            GIT_NEXT_LANGUAGE='english',LANG='C',LC_ALL='C')
        def run(exe,*arguments,cwd=root,ok=True,input=None,extra=None):
            process = subprocess.run([str(exe),*map(str,arguments)],cwd=cwd,env={**environment,**(extra or {})},
                                     input=input,capture_output=True,timeout=60)
            if (process.returncode==0) != ok:
                raise AssertionError('command expectation failed: '+str(arguments[:2])+', exit='+str(process.returncode))
            return process.stdout
        def git(*arguments,**kwargs): return run(binary,*arguments,**kwargs)
        def ref(*arguments,**kwargs): return run(reference,*arguments,**kwargs)
        def seed(name,format='sha1'):
            repo = root/name
            ref('init','-q','-b','main','--object-format='+format,repo)
            ref('config','core.autocrlf','input',cwd=repo)
            ref('config','core.eol','lf',cwd=repo)
            (repo/'.gitattributes').write_bytes(b'* text=auto eol=lf\n*.bin -text\n')
            (repo/'scene.unity').write_bytes(b'%YAML 1.1\nvalue: original\n')
            (repo/'data.bin').write_bytes(b'\x00\r\n\xff\n')
            ref('add','-A',cwd=repo); ref('commit','-qm','seed',cwd=repo)
            return repo
        def compare(repo,*arguments):
            assert git(*arguments,cwd=repo)==ref(*arguments,cwd=repo), 'reference output differs: '+str(arguments[:2])
        def record(name,action):
            try:
                action()
                results.append(dict(case=name,status='pass'))
            except (AssertionError,OSError,subprocess.SubprocessError,ValueError) as error:
                results.append(dict(case=name,status='fail',reason=str(error)[:240]))

        def identity():
            assert b'git-next' in git('--version')
            assert b'product: git-next' in git('version','--build-options')
            value = json.loads(git('version','--json'))
            assert value['product']=='git-next' and value['external_git_fallback'] is False
            assert value['external_ssh_fallback'] is False
            assert git('version','--json',extra={'LANG':'zh_CN.UTF-8'})==git('version','--json')
        record('D04-native-identity-and-stable-json',identity)

        def language():
            env = dict(environment); env.pop('GIT_NEXT_LANGUAGE',None)
            def invoke(*args):
                value = subprocess.run([str(binary),*args],cwd=root,env=env,capture_output=True,timeout=15)
                assert value.returncode==0
                return value.stdout
            invoke('language','set','chinese')
            assert json.loads(invoke('language','get','--json'))['language']=='chinese'
            invoke('language','set','english')
            assert json.loads(invoke('language','get','--json'))['language']=='english'
            invoke('language','set','auto')
            assert json.loads(git('--language=chinese','language','get','--json'))['language']=='chinese'
        record('D04-language-set-get-auto-and-cli-priority',language)

        for format in ('sha1','sha256'):
            def objects(format=format):
                repo=seed('objects-'+format,format)
                for command in [('status','--porcelain=v1','-z'),('status','--porcelain=v2','-z'),
                    ('rev-parse','HEAD'),('show','--format=fuller','HEAD'),('ls-files','-s','-z'),
                    ('ls-tree','-rz','HEAD'),('log','--format=%H %s'),('diff','--raw'),('cat-file','-p','HEAD')]:
                    compare(repo,*command)
                git('gc','--prune=now',cwd=repo); git('commit-graph','write','--reachable',cwd=repo)
                git('fsck','--full','--strict',cwd=repo)
                compare(repo,'cat-file','-p','HEAD:scene.unity')
                assert git('cat-file','--batch-check',cwd=repo,input=b'HEAD\n')==ref('cat-file','--batch-check',cwd=repo,input=b'HEAD\n')
            record('D01-objects-index-pack-gc-'+format,objects)

        def line_endings():
            repo=seed('line-endings')
            scene=repo/'scene.unity'
            scene.write_bytes(scene.read_bytes().replace(b'\n',b'\r\n'))
            assert git('status','--porcelain=v1','-z',cwd=repo)==b''
            assert git('diff','--exit-code',cwd=repo)==b''
            assert (repo/'data.bin').read_bytes()==b'\x00\r\n\xff\n'
            scene.write_bytes(b'%YAML 1.1\r\nvalue: changed\r\n')
            assert b'scene.unity' in git('status','--porcelain=v2','-z',cwd=repo)
            git('add','scene.unity',cwd=repo)
            assert b'\r' not in git('show',':scene.unity',cwd=repo)
            before=git('write-tree',cwd=repo)
            report=json.loads(git('eol','--apply','--json',cwd=repo))
            assert report['target_policy']=='LF' and report['index_renormalized'] is False
            assert git('write-tree',cwd=repo)==before
        record('D03-unity-pure-newlines-real-edits-binary-and-explicit-policy',line_endings)

        def changes():
            repo=seed('changes')
            git('switch','-c','topic',cwd=repo)
            git('mv','scene.unity','renamed.unity',cwd=repo)
            git('commit','-qm','rename',cwd=repo)
            git('tag','-a','v-fixture','-m','tag fixture',cwd=repo)
            compare(repo,'show-ref','--head')
            (repo/'renamed.unity').write_bytes(b'change\n')
            git('stash','push','-m','saved',cwd=repo)
            assert git('status','--porcelain',cwd=repo)==b''
            git('stash','pop',cwd=repo)
            assert (repo/'renamed.unity').read_bytes()==b'change\n'
            git('restore','--source=HEAD','--worktree','renamed.unity',cwd=repo)
            git('switch','main',cwd=repo); git('merge','--ff-only','topic',cwd=repo)
            git('reset','--hard','HEAD^',cwd=repo)
            git('cherry-pick','topic',cwd=repo)
            git('revert','--no-edit','HEAD',cwd=repo)
            git('fsck','--full',cwd=repo)
        record('D01-rename-switch-tag-stash-restore-merge-reset-pick-revert',changes)

        def conflicts():
            repo=seed('conflicts')
            git('switch','-c','topic',cwd=repo)
            (repo/'scene.unity').write_bytes(b'topic\n');git('commit','-qam','topic',cwd=repo)
            git('switch','main',cwd=repo)
            (repo/'scene.unity').write_bytes(b'main\n');git('commit','-qam','main',cwd=repo)
            original=git('rev-parse','HEAD',cwd=repo)
            git('merge','topic',cwd=repo,ok=False)
            compare(repo,'ls-files','--unmerged','-z')
            git('merge','--abort',cwd=repo)
            assert git('rev-parse','HEAD',cwd=repo)==original
            git('rebase','topic',cwd=repo,ok=False)
            git('rebase','--abort',cwd=repo)
            assert git('rev-parse','HEAD',cwd=repo)==original
        record('D01-conflict-stages-and-merge-rebase-abort',conflicts)

        def transport():
            repo=seed('local-transport')
            bare=root/'server.git';clone=root/'clone'
            git('clone','--bare',repo,bare)
            git('clone','--no-local',bare,clone)
            (clone/'new.txt').write_bytes(b'push\n')
            git('add','-A',cwd=clone);git('commit','-qm','push',cwd=clone)
            git('push','origin','HEAD:main',cwd=clone)
            git('remote','add','origin',bare,cwd=repo)
            git('fetch','origin',cwd=repo);git('pull','--ff-only','origin','main',cwd=repo)
            assert git('rev-parse','HEAD',cwd=repo)==git('rev-parse','HEAD',cwd=clone)
            compare(repo,'ls-remote',bare)
            git('fsck','--full',cwd=bare)
        record('D01-owned-local-upload-receive-clone-fetch-pull-push',transport)

        def worktrees():
            repo=seed('worktrees');linked=root/'linked'
            git('worktree','add','-b','linked',linked,cwd=repo)
            assert git('rev-parse','--git-common-dir',cwd=linked)
            (linked/'linked.txt').write_bytes(b'linked\n')
            git('add','-A',cwd=linked);git('commit','-qm','linked',cwd=linked)
            compare(repo,'worktree','list','--porcelain')
            git('worktree','remove',linked,cwd=repo)
            git('worktree','prune',cwd=repo)
        record('D01-linked-worktree-index-and-removal',worktrees)

        def submodules():
            repo=seed('super');sub=seed('sub')
            git('-c','protocol.file.allow=always','submodule','add',sub,'module',cwd=repo)
            git('commit','-qam','submodule',cwd=repo)
            compare(repo,'submodule','status')
            clone=root/'recursive'
            git('-c','protocol.file.allow=always','clone','--recurse-submodules',repo,clone)
            assert (clone/'module/scene.unity').is_file()
            git('submodule','deinit','-f','--all',cwd=clone)
            git('-c','protocol.file.allow=always','submodule','update','--init','--recursive',cwd=clone)
        record('D01-recursive-submodule-clone-deinit-update',submodules)

        def hooks():
            repo=seed('hooks');hook=repo/'.git/hooks/pre-commit'
            hook.write_bytes(b'#!/bin/sh\nprintf "hook-ran\\n" > hook.marker\nexit 1\n');hook.chmod(0o755)
            (repo/'new.txt').write_bytes(b'rejected\n');git('add','-A',cwd=repo)
            old=git('rev-parse','HEAD',cwd=repo)
            git('commit','-qm','must reject',cwd=repo,ok=False)
            assert (repo/'hook.marker').read_bytes()==b'hook-ran\n'
            assert git('rev-parse','HEAD',cwd=repo)==old
        record('D02-owned-shell-hooks-and-rejected-commit',hooks)

        def sparse():
            repo=seed('sparse')
            (repo/'folder').mkdir();(repo/'folder/file.txt').write_bytes(b'folder\n')
            git('add','-A',cwd=repo);git('commit','-qm','folder',cwd=repo)
            git('sparse-checkout','init','--cone',cwd=repo)
            git('sparse-checkout','set','folder',cwd=repo)
            compare(repo,'ls-files','-t')
            git('sparse-checkout','disable',cwd=repo)
            git('update-index','--split-index',cwd=repo)
            compare(repo,'status','--porcelain=v2','-z')
            git('fsck','--full',cwd=repo)
        record('D01-sparse-checkout-and-split-index',sparse)

        def failure():
            repo=seed('failure');before=git('rev-parse','HEAD',cwd=repo)
            git('this-command-does-not-exist',cwd=repo,ok=False)
            git('reset','--invalid-option',cwd=repo,ok=False)
            assert git('rev-parse','HEAD',cwd=repo)==before
            assert not list((repo/'.git').rglob('*.lock'))
        record('D01-unknown-command-option-and-lock-preservation',failure)
    report = dict(schema_version=1,status='pass' if all(item['status']=='pass' for item in results) else 'fail',
                  binary_sha256=hashlib.sha256(binary.read_bytes()).hexdigest(),reference_sha256=hashlib.sha256(reference.read_bytes()).hexdigest(),
                  cases=results,personal_profiles_accessed=False,repositories_disposable=True)
    args.output.write_text(json.dumps(report,indent=2)+'\n',encoding='utf-8',newline='\n')
    print(json.dumps(report,indent=2))
    return report['status']!='pass'


if __name__=='__main__': raise SystemExit(main())
