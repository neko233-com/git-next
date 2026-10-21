"""One local Rust formatting/lint/unit lane for the peer terminal."""
import argparse
import json
import os
from pathlib import Path
import subprocess
import tempfile
import time


def main():
    parser=argparse.ArgumentParser(__doc__)
    parser.add_argument('--source',type=Path,required=True)
    parser.add_argument('--output',type=Path,required=True)
    parser.add_argument('--distribution',type=Path,required=True)
    args=parser.parse_args();source=args.source.resolve(strict=True)
    cargo=Path.home()/'.cargo/bin/cargo.exe'
    env={**os.environ,'PATH':str(cargo.parent)+os.pathsep+os.environ['PATH']}
    commands=[('format',['fmt','--all','--check']),
              ('clippy',['clippy','--locked','--release','--package','git-terminal','--all-targets','--target','x86_64-pc-windows-msvc','--no-deps','--','-D','warnings']),
              ('unit',['test','--locked','--release','--workspace','--lib','--bins','--target','x86_64-pc-windows-msvc']),
              ('shell-integration',['test','--locked','--release','--bin','git-terminal','--target','x86_64-pc-windows-msvc','shell_acceptance::shell_features_real_pty','--','--ignored','--exact'])]
    args.output.parent.mkdir(parents=True,exist_ok=True);results=[]
    userhome=Path.home()
    with tempfile.TemporaryDirectory(prefix='git-terminal-rust-home-') as temporary:
        home=Path(temporary)
        env={key:value for key,value in env.items() if not key.startswith(('GIT_','GCM_','SSH_','GNUPG'))}
        env.update(HOME=str(home),USERPROFILE=str(home),APPDATA=str(home/'appdata'),LOCALAPPDATA=str(home/'local'),XDG_CONFIG_HOME=str(home/'.config'),SSH_ROOT_PATH=str(home/'.ssh'),GIT_CONFIG_GLOBAL=str(home/'.gitconfig'),GIT_CONFIG_NOSYSTEM='1',GIT_NEXT_CONFIG_HOME=str(home/'git-next'),GIT_NEXT_COMPONENT_HOME=str(home/'components'),GIT_TERMINAL_TEST_GIT=str(args.distribution.resolve(strict=True)/'bin/git.exe'),CARGO_HOME=str(userhome/'.cargo'),RUSTUP_HOME=str(userhome/'.rustup'),LANG='C.UTF-8',LC_ALL='C.UTF-8')
        for name,arguments in commands:
            start=time.monotonic();p=subprocess.run([str(cargo),*arguments],cwd=source,env=env,capture_output=True,timeout=1800)
            args.output.with_name(args.output.stem+'-'+name+'.log').write_bytes(p.stdout+p.stderr)
            results.append(dict(case=name,status='pass' if p.returncode==0 else 'fail',seconds=round(time.monotonic()-start,3)))
    value=dict(status='pass' if all(item['status']=='pass' for item in results) else 'fail',product='git-terminal',results=results)
    args.output.write_text(json.dumps(value,indent=2)+'\n',encoding='utf-8');print(json.dumps(value));raise SystemExit(value['status']!='pass')


if __name__=='__main__':main()
