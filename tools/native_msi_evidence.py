"""Reuse passed MSI transactions only when their bytes and validation sources match."""
import hashlib
import importlib.util
import json
from pathlib import Path

UNRELATED_HARNESS_CHANGES={
    'tools/native_product_batch.py','tools/freeze_native_batch.py','tools/native_msi_evidence.py',
    'tests/native_msi_evidence_test.py','../git-terminal/tests/windows_desktop_acceptance.py',
    '../git-terminal/src/shell_acceptance.rs',
}


def _product_defaults_block():
    """The rendered product defaults, loaded next to this file so cwd cannot matter."""
    spec = importlib.util.spec_from_file_location(
        "runtime_defaults", Path(__file__).resolve().parent / "runtime_defaults.py")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return (module.core_body() + module.tail()).encode("utf-8")


def validate_desktop_dependencies(previous, current):
    if any(previous.get(key)!=current.get(key) for key in ('product','version')):
        raise ValueError('Desktop package identity changed')
    def runtime(matrix):
        root=Path(matrix['platforms']['windows-x64']['directory']).resolve(strict=True)
        values={}
        for name,digest in matrix['files'].items():
            try:relative=Path(name).resolve().relative_to(root).as_posix()
            except ValueError:continue
            if relative.startswith(('docs/','migration/','updater/')) or relative in ('README.md','README.zh-CN.md','package.json','git-next-distribution.json'):
                continue
            values[relative]=digest
        if not {'bin/git-terminal.exe','bin/git.exe','runtime/usr/bin/bash.exe','runtime/etc/gitconfig'}<=values.keys():
            raise ValueError('Desktop runtime dependency set is incomplete')
        return root,values
    old_root,old=runtime(previous);new_root,new=runtime(current)
    old_config=old.pop('runtime/etc/gitconfig');new_config=new.pop('runtime/etc/gitconfig')
    if old!=new:
        raise ValueError('Desktop runtime bytes changed')
    if old_config!=new_config:
        original=(old_root/'runtime/etc/gitconfig').read_bytes()
        actual=(new_root/'runtime/etc/gitconfig').read_bytes()
        if hashlib.sha256(original).hexdigest()!=old_config or hashlib.sha256(actual).hexdigest()!=new_config:
            raise ValueError('Desktop configuration bytes differ from their frozen receipt')
        # A desktop configuration may only differ from the accepted one where the product
        # defaults changed, and the current defaults come from the shared fragment. A
        # change to them is a deliberate product change, so old evidence is rejected
        # instead of being patched into shape.
        block=_product_defaults_block()
        if not actual.endswith(block):
            raise ValueError('Desktop configuration lacks the current product defaults')
        if not original.endswith(block):
            raise ValueError('The product configuration defaults changed; desktop evidence cannot be reused')
        if original[:-len(block)]!=actual[:-len(block)]:
            raise ValueError('Desktop configuration differs outside the product defaults')


def validate_reuse(report, previous, current, reference_sha256, cases=('msi-user','msi-machine')):
    if report.get('phase')!='correctness' or report.get('reference_sha256')!=reference_sha256:
        raise ValueError('MSI evidence has a different phase or reference')
    if report.get('source_tree_sha256')!=current['source_tree_sha256'] or previous['source_tree_sha256']!=current['source_tree_sha256']:
        raise ValueError('MSI evidence has different native sources')
    if previous['files']!=current['files'] or previous['platforms']!=current['platforms']:
        if cases==('terminal-native-desktop',):
            validate_desktop_dependencies(previous,current)
        else:
            raise ValueError('MSI evidence identifies different immutable package inputs')
    changed={name for name in set(previous['harnesses'])|set(current['harnesses'])
             if previous['harnesses'].get(name)!=current['harnesses'].get(name)}
    allowed=UNRELATED_HARNESS_CHANGES
    if 'terminal-native-desktop' in cases:
        desktop_sources={name for name in set(previous['harnesses'])|set(current['harnesses'])
                         if (name.startswith('../git-terminal/') and name!='../git-terminal/src/shell_acceptance.rs')
                         or name=='tools/terminal_runtime_fixture.py'}
        allowed=changed-desktop_sources
        if not any(item['case']=='frozen-input-readback' and item['status']=='pass' for item in report['results']):
            raise ValueError('Desktop evidence requires passed frozen-input readback')
    if changed-allowed:
        raise ValueError('MSI validation sources changed')
    results={}
    for case in cases:
        matches=[item for item in report['results'] if item['case']==case]
        if len(matches)>1:
            raise ValueError('Duplicate MSI scope evidence is invalid')
        if matches and matches[0]['status']=='pass' and matches[0].get('exit_code')==0:
            results[case]=matches[0]
    return results


def load_reuse(report_path, matrix_path, current, reference_sha256, cases=('msi-user','msi-machine')):
    report_bytes=Path(report_path).read_bytes();matrix_bytes=Path(matrix_path).read_bytes()
    report=json.loads(report_bytes);previous=json.loads(matrix_bytes)
    if report['matrix_sha256']!=hashlib.sha256(matrix_bytes).hexdigest():
        raise ValueError('Previous MSI matrix differs from its batch receipt')
    results=validate_reuse(report,previous,current,reference_sha256,cases)
    digest=hashlib.sha256(report_bytes).hexdigest()
    return {case:dict(case=case,status='pass',exit_code=0,seconds=0,
                     dependency_scope='terminal runtime bytes and exact desktop test' if cases==('terminal-native-desktop',) else 'all immutable package bytes and installer tests',
                     original_seconds=item['seconds'],reused_evidence_batch_sha256=digest,
                     reused_evidence_matrix_sha256=report['matrix_sha256']) for case,item in results.items()}
