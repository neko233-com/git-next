"""Exercise native CPU*2 reading, overrides, bounded workers and inherited affinity."""
import argparse
import ctypes
import hashlib
import json
import os
from pathlib import Path
import subprocess
import tempfile
import time


def available_affinity():
    if os.name=='nt':
        kernel=ctypes.WinDLL('kernel32',use_last_error=True)
        kernel.GetCurrentProcess.restype=ctypes.c_void_p
        kernel.GetProcessAffinityMask.argtypes=[ctypes.c_void_p,ctypes.POINTER(ctypes.c_size_t),ctypes.POINTER(ctypes.c_size_t)]
        kernel.SetProcessAffinityMask.argtypes=[ctypes.c_void_p,ctypes.c_size_t]
        process=kernel.GetCurrentProcess();mask=ctypes.c_size_t();system=ctypes.c_size_t()
        assert kernel.GetProcessAffinityMask(process,ctypes.byref(mask),ctypes.byref(system))
        return mask.value.bit_count(),lambda: bool(kernel.SetProcessAffinityMask(process,mask.value)),lambda: bool(kernel.SetProcessAffinityMask(process,mask.value & -mask.value))
    original=os.sched_getaffinity(0)
    def restore():os.sched_setaffinity(0,original);return True
    def restrict():os.sched_setaffinity(0,{min(original)});return True
    return len(original),restore,restrict


def main():
    parser=argparse.ArgumentParser(__doc__);parser.add_argument('binary',type=Path);parser.add_argument('--output',type=Path,required=True)
    args=parser.parse_args();binary=args.binary.resolve(strict=True);results=[];started=time.monotonic()
    with tempfile.TemporaryDirectory(prefix='git-next-read-threads-') as directory:
        root=Path(directory);home=root/'home';home.mkdir();repo=root/'repo'
        env={key:value for key,value in os.environ.items() if not key.startswith(('GIT_','GCM_','SSH_','GNUPG'))}
        env.update(HOME=str(home),USERPROFILE=str(home),APPDATA=str(home/'appdata'),LOCALAPPDATA=str(home/'local'),XDG_CONFIG_HOME=str(home/'.config'),GIT_NEXT_CONFIG_HOME=str(home/'git-next'),GIT_NEXT_COMPONENT_HOME=str(home/'components'),GIT_CONFIG_GLOBAL=str(home/'config'),GIT_CONFIG_NOSYSTEM='1',GIT_OPTIONAL_LOCKS='0',GIT_TERMINAL_PROMPT='0',GIT_TRACE2_EVENT_NESTING='10',SSH_ROOT_PATH=str(home/'.ssh'))
        flags=subprocess.CREATE_NO_WINDOW if os.name=='nt' else 0
        def run(command,data=None,trace=None):
            variables={**env}
            if trace:variables['GIT_TRACE2_EVENT']=str(trace)
            p=subprocess.run([str(binary),*command],cwd=repo if repo.exists() else root,env=variables,input=data,capture_output=True,timeout=45,creationflags=flags)
            assert p.returncode==0,(command,p.returncode,p.stderr.decode(errors='replace')[:200])
            return p.stdout
        def values(name,command):
            path=root/(name+'.jsonl');value=run(command,trace=path)
            events=[json.loads(line) for line in path.read_text().splitlines()]
            readings={e['key']:int(e['value']) for e in events if e.get('event')=='data' and e.get('category')=='git-next'}
            return value,readings
        run(['init','-b','main',str(repo)])
        data=b'read-fixture\n';stream=bytearray(b'blob\nmark :1\ndata '+str(len(data)).encode()+b'\n'+data+b'\ncommit refs/heads/main\ncommitter Fixture <fixture@example.invalid> 981173106 +0000\ndata 7\nfixture\n')
        count=32000
        for i in range(count):stream.extend(('M 100644 :1 d%02d/f%06d.txt\n'%(i%32,i)).encode())
        stream.extend(b'\ndone\n');run(['fast-import','--quiet','--done'],bytes(stream));run(['read-tree','HEAD'])
        blob_id=run(['rev-parse','HEAD:d00/f000000.txt']).decode().strip()
        cpus,restore,restrict=available_affinity();budget=cpus*2
        default,events=values('default',['ls-files','--stage','-z'])
        assert events['read-thread-budget']==budget
        serial,one=values('serial',['-c','index.threads=1','ls-files','--stage','-z'])
        assert serial==default and one['read-thread-budget']==1 and one['index-entry-workers']==1
        assert events['index-entry-workers']>=2 if budget>=3 else events['index-entry-workers']>=1
        results.append(dict(case='native-index-default-cpu-times-two-and-explicit-single-thread',status='pass',available_cpus=cpus,default_budget=budget,actual_entry_workers=events['index-entry-workers']))
        # Keep the large index decode fixture; smaller stat/name/grep work still exercises multiple workers.
        count=4000
        stream=bytearray(b'commit refs/heads/read-small\ncommitter Fixture <fixture@example.invalid> 981173106 +0000\ndata 7\nfixture\nfrom refs/heads/main\ndeleteall\n')
        for i in range(count):stream.extend(('M 100644 %s d%02d/f%06d.txt\n'%(blob_id,i%32,i)).encode())
        stream.extend(b'\ndone\n');run(['fast-import','--quiet','--done'],bytes(stream));run(['symbolic-ref','HEAD','refs/heads/read-small']);run(['read-tree','HEAD'])
        status,preload=values('preload',['status','--porcelain=v1','-uno'])
        assert preload['preload-thread-budget']==budget and 2<=preload['preload-workers']<=min(budget,count//500)
        overridden,three=values('three',['-c','index.threads=3','status','--porcelain=v1','-uno'])
        assert overridden==status and three['preload-thread-budget']==3 and three['preload-workers']==3
        run(['config','index.threads','2'])
        _,override=values('command-priority',['-c','index.threads=3','status','--porcelain=v1','-uno'])
        assert override['preload-thread-budget']==3
        run(['config','--unset','index.threads'])
        results.append(dict(case='preload-default-and-repository-command-line-precedence',status='pass',actual_workers=preload['preload-workers']))
        try:
            assert restrict();_,limited=values('affinity',['ls-files','--stage','-z']);assert limited['read-thread-budget']==2
        finally:assert restore()
        results.append(dict(case='current-process-affinity-is-inherited-and-honored',status='pass'))
        first=(repo/'d00');first.mkdir();(first/'f000000.txt').write_bytes(data);(repo/'new-read-file.txt').write_bytes(data)
        index_before=hashlib.sha256((repo/'.git/index').read_bytes()).hexdigest()
        _,huge=values('large-budget',['-c','core.ignorecase=true','-c','index.threads=2147483647','status','--porcelain=v1','--untracked-files=all'])
        assert huge['name-hash-thread-budget']==2147483647 and 1<huge['name-hash-workers']<=count//2000
        assert hashlib.sha256((repo/'.git/index').read_bytes()).hexdigest()==index_before
        results.append(dict(case='large-explicit-index-budget-bounded-without-integer-overflow',status='pass',actual_name_workers=huge['name-hash-workers']))
        grep,ge=values('grep',['grep','--cached','-l','-F','read-fixture'])
        single,_=values('grep-single',['grep','--threads=1','--cached','-l','-F','read-fixture'])
        explicit,go=values('grep-override',['-c','grep.threads=3','grep','--threads=2','--cached','-l','-F','read-fixture'])
        assert grep==single==explicit and ge['grep-workers']==budget and go['grep-workers']==2
        results.append(dict(case='readonly-grep-default-budget-and-explicit-override-consistency',status='pass',default_workers=ge['grep-workers']))
        if os.name == 'nt':
            stable = {'core.fscache':'false','core.fsmonitor':'false','core.preloadindex':'false','index.threads':'1','grep.threads':'1'}
            shipped = {**env, 'GIT_CONFIG_NOSYSTEM':'0'}
            def shipped_run(command):
                p=subprocess.run([str(binary),*command],cwd=repo,env=shipped,capture_output=True,timeout=45,creationflags=flags)
                assert p.returncode==0,(command,p.returncode,p.stderr.decode(errors='replace')[:200])
                return p.stdout
            for key,value in stable.items():
                assert shipped_run(['config','--get',key]).decode().strip()==value,key
            (repo/'new-read-file.txt').unlink()
            shipped_run(['reset','--hard','HEAD'])
            expected=shipped_run(['ls-files','-z'])
            for _ in range(30):
                assert shipped_run(['grep','-z','-l','-F','read-fixture'])==expected
                assert shipped_run(['status','--porcelain=v1'])==b''
            results.append(dict(case='windows-shipped-read-safety-and-working-tree-completeness',status='pass',defaults=stable,files=count,rounds=30))
    value=dict(status='pass',scope='native index decode/preload/name-hash and read-only grep; no compression or checkout worker changes',seconds=round(time.monotonic()-started,3),personal_profile_accessed=False,results=results)
    args.output.write_text(json.dumps(value,indent=2)+'\n',encoding='utf-8');print(json.dumps(value))


if __name__=='__main__':main()
