"""MSI evidence must fail closed after product or validation changes."""
import copy
from pathlib import Path
import sys
import unittest
sys.path.insert(0,str(Path(__file__).resolve().parents[1]/'tools'))
from native_msi_evidence import validate_reuse


class MsiEvidenceTests(unittest.TestCase):
    def setUp(self):
        self.matrix=dict(source_tree_sha256='source',files={'package.msi':'bytes'},platforms={'windows-x64':{'msi':'package.msi'}},harnesses={'tests/msi_acceptance.ps1':'original','../git-terminal/tests/windows_desktop_acceptance.py':'old'})
        self.current=copy.deepcopy(self.matrix)
        self.report=dict(phase='correctness',source_tree_sha256='source',reference_sha256='reference',results=[dict(case=case,status='pass',exit_code=0,seconds=10) for case in ('msi-user','msi-machine')])

    def test_unrelated_desktop_validation_change_preserves_msi_evidence(self):
        self.current['harnesses']['../git-terminal/tests/windows_desktop_acceptance.py']='new'
        self.assertEqual(set(validate_reuse(self.report,self.matrix,self.current,'reference')),{'msi-user','msi-machine'})

    def test_package_change_invalidates_evidence(self):
        self.current['files']['package.msi']='changed'
        with self.assertRaises(ValueError):validate_reuse(self.report,self.matrix,self.current,'reference')

    def test_test_only_shell_fixture_does_not_change_installer_validation(self):
        self.current['harnesses']['../git-terminal/src/shell_acceptance.rs']='no-console-test'
        self.assertEqual(set(validate_reuse(self.report,self.matrix,self.current,'reference')),{'msi-user','msi-machine'})

    def test_terminal_runtime_source_change_invalidates_evidence(self):
        self.current['harnesses']['../git-terminal/src/session.rs']='changed-runtime'
        with self.assertRaises(ValueError):validate_reuse(self.report,self.matrix,self.current,'reference')

    def desktop_report(self):
        self.report['results'] += [dict(case='terminal-native-desktop',status='pass',exit_code=0,seconds=23),dict(case='frozen-input-readback',status='pass')]

    def test_unchanged_desktop_evidence_is_reusable(self):
        self.desktop_report()
        self.assertEqual(set(validate_reuse(self.report,self.matrix,self.current,'reference',('terminal-native-desktop',))),{'terminal-native-desktop'})

    def test_changed_desktop_validation_is_never_reused(self):
        self.desktop_report();self.current['harnesses']['../git-terminal/tests/windows_desktop_acceptance.py']='changed'
        with self.assertRaises(ValueError):validate_reuse(self.report,self.matrix,self.current,'reference',('terminal-native-desktop',))

    def test_desktop_requires_successful_readback(self):
        self.desktop_report();self.report['results'][-1]['status']='fail'
        with self.assertRaises(ValueError):validate_reuse(self.report,self.matrix,self.current,'reference',('terminal-native-desktop',))

    def test_installer_validation_change_invalidates_evidence(self):
        self.current['harnesses']['tests/msi_acceptance.ps1']='changed'
        with self.assertRaises(ValueError):validate_reuse(self.report,self.matrix,self.current,'reference')

    def test_failed_or_missing_scope_is_never_reused(self):
        self.report['results'][1]['status']='fail'
        self.assertEqual(set(validate_reuse(self.report,self.matrix,self.current,'reference')),{'msi-user'})

    def test_duplicate_scope_is_rejected(self):
        self.report['results'].append(self.report['results'][0].copy())
        with self.assertRaises(ValueError):validate_reuse(self.report,self.matrix,self.current,'reference')

    def test_changed_reference_is_rejected(self):
        with self.assertRaises(ValueError):validate_reuse(self.report,self.matrix,self.current,'other')



class DesktopDependencyTests(unittest.TestCase):
    def setUp(self):
        import hashlib
        import tempfile
        self.temporary=tempfile.TemporaryDirectory(prefix='git-next-desktop-proof-')
        self.addCleanup(self.temporary.cleanup)
        self.old_root=Path(self.temporary.name)/'old';self.new_root=Path(self.temporary.name)/'new'
        self.config=b'[core]\n\tfscache = true\n[credential]\n\thelper = manager\n'
        self.stable=self.config.replace(b'\tfscache = true\n',b'\tfscache = false\n\tfsmonitor = false\n\tpreloadIndex = false\n')+b'\n[index]\n\tthreads = 1\n[grep]\n\tthreads = 1\n'
        def matrix(root,config):
            files={}
            for name,data in {'bin/git-terminal.exe':b'gui','bin/git.exe':b'core','runtime/usr/bin/bash.exe':b'shell','runtime/etc/gitconfig':config,'docs/TERMINAL.md':root.name.encode(),'migration/Takeover.Core.psm1':root.name.encode()}.items():
                p=root/name;p.parent.mkdir(parents=True,exist_ok=True);p.write_bytes(data)
                files[str(p)]=hashlib.sha256(data).hexdigest()
            return dict(product='git-next',version='0.3.0',source_tree_sha256='source',platforms={'windows-x64':{'directory':str(root)}},files=files,harnesses={})
        self.old=matrix(self.old_root,self.config);self.new=matrix(self.new_root,self.stable)

    def test_exact_stability_transition_preserves_desktop_runtime(self):
        from native_msi_evidence import validate_desktop_dependencies
        validate_desktop_dependencies(self.old,self.new)

    def test_changed_terminal_binary_refuses_reuse(self):
        from native_msi_evidence import validate_desktop_dependencies
        self.new['files'][str(self.new_root/'bin/git-terminal.exe')]='different'
        with self.assertRaises(ValueError):validate_desktop_dependencies(self.old,self.new)

    def test_unrelated_configuration_change_refuses_reuse(self):
        import hashlib
        from native_msi_evidence import validate_desktop_dependencies
        path=self.new_root/'runtime/etc/gitconfig';data=self.stable.replace(b'manager',b'other')
        path.write_bytes(data);self.new['files'][str(path)]=hashlib.sha256(data).hexdigest()
        with self.assertRaises(ValueError):validate_desktop_dependencies(self.old,self.new)

    def test_modified_original_configuration_refuses_reuse(self):
        from native_msi_evidence import validate_desktop_dependencies
        (self.old_root/'runtime/etc/gitconfig').write_bytes(b'changed')
        with self.assertRaises(ValueError):validate_desktop_dependencies(self.old,self.new)


class BatchRebindingTests(unittest.TestCase):
    def setUp(self):
        from complete_native_batch import EXPECTED
        self.previous=dict(product='git-next',version='0.3.0',source_tree_sha256='source',files={'runtime':'same'},platforms={'windows-x64':'same'},harnesses={'tests/native_read_threads_acceptance.py':'same'})
        self.current=copy.deepcopy(self.previous)
        self.current['harnesses']['tools/complete_native_batch.py']='new-report-tool'
        self.report=dict(phase='correctness',source_tree_sha256='source',results=[dict(case=name,status='fail' if name=='terminal-native-desktop' else 'pass',exit_code=1 if name=='terminal-native-desktop' else 0) for name in EXPECTED])

    def test_reporting_only_change_preserves_successful_case_evidence(self):
        from complete_native_batch import validate_rebinding
        validate_rebinding(self.previous,self.current,self.report)

    def test_changed_functional_validation_refuses_rebinding(self):
        from complete_native_batch import validate_rebinding
        self.current['harnesses']['tests/native_read_threads_acceptance.py']='changed'
        with self.assertRaises(ValueError):validate_rebinding(self.previous,self.current,self.report)

    def test_changed_product_bytes_refuse_rebinding(self):
        from complete_native_batch import validate_rebinding
        self.current['files']['runtime']='changed'
        with self.assertRaises(ValueError):validate_rebinding(self.previous,self.current,self.report)

    def test_another_failure_refuses_rebinding(self):
        from complete_native_batch import validate_rebinding
        next(r for r in self.report['results'] if r['case']=='msi-machine')['status']='fail'
        with self.assertRaises(ValueError):validate_rebinding(self.previous,self.current,self.report)

if __name__=='__main__':unittest.main()
