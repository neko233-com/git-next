"""Run one complete-package Docker acceptance item and remove its exact owned resources."""
import argparse
import hashlib
import json
from pathlib import Path
import shutil
import subprocess
import tarfile
import uuid

ROOT=Path(__file__).resolve().parents[1]
OWNER='git-next-native-product-batch'


def main():
    parser=argparse.ArgumentParser(__doc__)
    parser.add_argument('--platform',choices=['linux-x64','linux-arm64','macos-arm64'],required=True)
    parser.add_argument('--archive',type=Path,required=True)
    parser.add_argument('--sha256',required=True)
    parser.add_argument('--base-image',required=True)
    parser.add_argument('--output',type=Path,required=True)
    args=parser.parse_args()
    archive=args.archive.resolve(strict=True)
    with archive.open('rb') as stream: assert hashlib.file_digest(stream,'sha256').hexdigest()==args.sha256
    output=args.output.resolve();output.mkdir(parents=True,exist_ok=False)
    identity=uuid.uuid4().hex;image='git-next-native-batch-'+identity;container='git-next-native-check-'+identity
    labels=['--label','git-next.owner='+OWNER,'--label','git-next.scope='+identity]
    mac=args.platform=='macos-arm64'
    dockerfile=ROOT/'tests/containers'/('Dockerfile_mac' if mac else 'native.Dockerfile')
    arch='arm64' if args.platform=='linux-arm64' else 'amd64'
    status='failed';cleanup={};created=False
    try:
        assert subprocess.run(['docker','image','inspect',image],capture_output=True).returncode!=0
        created=True
        built=subprocess.run(['docker','build','--platform','linux/'+arch,*labels,'--build-arg','BASE='+args.base_image,
                             '-f',str(dockerfile),'-t',image,str(dockerfile.parent)],capture_output=True,timeout=900)
        (output/'image-build.log').write_bytes(built.stdout+b'\n'+built.stderr)
        built.check_returncode()
        command=['docker','run','--rm','--name',container,*labels,'--platform','linux/'+arch,'--network','none',
                 '--read-only','--cap-drop=ALL','--security-opt=no-new-privileges',
                 '--tmpfs','/tmp:rw,exec,size=1536m',
                 '--mount','type=bind,source='+str(output)+',target=/results',
                 '--mount','type=bind,source='+str(ROOT/'tests')+',target=/tests,readonly',
                 '--mount','type=bind,source='+str(ROOT/'tests/containers')+',target=/harness,readonly']
        command+=['--mount','type=bind,source='+str(archive)+',target=/inputs/package.tar.gz,readonly']
        command+=[image]
        if mac: command+=['--archive','/inputs/package.tar.gz','--output','/results/macos-package-static.json']
        process=subprocess.run(command,capture_output=True,timeout=3600 if arch=='arm64' else 1800)
        (output/'container.log').write_bytes(process.stdout+b'\n'+process.stderr)
        process.check_returncode();status='pass'
    finally:
        for kind,name,remove in [('container',container,['rm','-f',container]),('image',image,['image','rm',image])]:
            probe=subprocess.run(['docker',kind,'inspect',name],capture_output=True,text=True)
            if probe.returncode==0:
                item=json.loads(probe.stdout)[0]
                labels_read=item['Config']['Labels']
                if labels_read.get('git-next.owner')!=OWNER or labels_read.get('git-next.scope')!=identity:
                    raise ValueError('Unrelated Docker resource prevents cleanup')
                subprocess.run(['docker',*remove],check=True,stdout=subprocess.DEVNULL)
            cleanup[kind+'_absent']=subprocess.run(['docker',kind,'inspect',name],capture_output=True).returncode!=0
        # Mac expansion is a task-owned review fixture, not an installed package.
        package=output/'package'
        if package.exists():
            if not package.resolve().is_relative_to(output) or package.is_symlink(): raise ValueError('Unsafe fixture cleanup path')
            shutil.rmtree(package)
        report=dict(schema_version=1,status=status,platform=args.platform,archive_sha256=args.sha256,
                    cleanup=cleanup,macos_product_executed=False if mac else None,personal_profiles_mounted=False)
        (output/'docker.json').write_text(json.dumps(report,indent=2)+'\n',newline='\n')
    print(json.dumps(report))


if __name__=='__main__': main()
