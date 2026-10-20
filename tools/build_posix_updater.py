"""Compile the owned POSIX updater for all targets; never invoke the produced programs."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import subprocess

from build_native_posix import image_platform

ROOT = Path(__file__).resolve().parents[1]
MODULE = ROOT/'packaging/posix/updater'
TARGETS = {'linux-x64':('linux','amd64'),'linux-arm64':('linux','arm64'),'macos-arm64':('darwin','arm64')}


def digest(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def main():
    parser = argparse.ArgumentParser(__doc__)
    parser.add_argument('--destination',required=True,type=Path,help='new output directory')
    parser.add_argument('--go',default='go')
    args = parser.parse_args()
    destination = args.destination.resolve()
    if destination.exists(): parser.error('Output directory must be new')
    sources = {path.name:digest(path) for path in sorted(MODULE.iterdir()) if path.is_file()}
    selected = Path(subprocess.check_output([args.go,'env','GOROOT'],cwd=MODULE,text=True).strip())/'bin'/('go.exe' if os.name == 'nt' else 'go')
    compiler = subprocess.check_output([str(selected),'version'],cwd=MODULE,text=True).strip()
    destination.mkdir(parents=True)
    binaries = {}
    for target,(system,arch) in TARGETS.items():
        output = destination/target/'updater'; output.parent.mkdir()
        environment = dict(os.environ,GOOS=system,GOARCH=arch,CGO_ENABLED='0',GOTOOLCHAIN='local')
        subprocess.run([str(selected),'build','-trimpath','-buildvcs=false','-ldflags=-s -w','-o',str(output),'.'],
                       cwd=MODULE,env=environment,check=True)
        if image_platform(output) != target: raise ValueError('Updater image differs from its target')
        binaries[target] = dict(sha256=digest(output),bytes=output.stat().st_size)
    if sources != {path.name:digest(path) for path in sorted(MODULE.iterdir()) if path.is_file()}: raise ValueError('Updater source changed during compilation')
    receipt = dict(schema_version=1,status='posix-updaters-compiled-not-runtime-accepted',compiler=compiler,
                   sources=sources,binaries=binaries,product_executed=False,tests_executed=False,
                   services_installed=False,installed=False,release_eligible=False)
    (destination/'receipt.json').write_text(json.dumps(receipt,indent=2)+'\n',encoding='utf-8',newline='\n')
    print(json.dumps({'status':receipt['status'],'platforms':list(binaries),'product_executed':False}))


if __name__ == '__main__': main()
