import sys
from pathlib import Path
import unittest
sys.path.insert(0,str(Path(__file__).resolve().parents[1]/'tools'))
from complete_native_repair_batch import validate_windows_files,validate_harnesses


class RepairEvidence(unittest.TestCase):
    def setUp(self):
        self.old={'bin/git.exe':'core','bin/git-terminal.exe':'old','bin/git-terminal-shell.exe':'shell','updater/msi-worker.exe':'worker','docs/old.md':'manual'}
        self.new={**self.old,'bin/git-terminal.exe':'new','docs/old.md':'updated','docs/new.html':'new manual'}
        self.compiled={'git-terminal.exe':'new','git-terminal-shell.exe':'shell'}

    def test_terminal_only_update(self):
        self.assertEqual(len(validate_windows_files(self.old,self.new,self.compiled)),3)

    def test_changed_core_rejected(self):
        self.new['bin/git.exe']='changed'
        with self.assertRaises(ValueError):validate_windows_files(self.old,self.new,self.compiled)

    def test_unbound_terminal_provenance_rejected(self):
        self.new['LICENSES/git-terminal/git-terminal-package.json']='unbound'
        with self.assertRaises(ValueError):validate_windows_files(self.old,self.new,self.compiled)

    def test_changed_installer_worker_rejected(self):
        self.new['updater/msi-worker.exe']='changed'
        with self.assertRaises(ValueError):validate_windows_files(self.old,self.new,self.compiled)

    def test_uncompiled_terminal_rejected(self):
        self.new['bin/git-terminal.exe']='unrecognized'
        with self.assertRaises(ValueError):validate_windows_files(self.old,self.new,self.compiled)

    def test_removed_runtime_rejected(self):
        del self.new['bin/git.exe']
        with self.assertRaises(ValueError):validate_windows_files(self.old,self.new,self.compiled)

    def test_installer_test_change_rejected(self):
        with self.assertRaises(ValueError):validate_harnesses({'tests/msi_acceptance.ps1':'old'},{'tests/msi_acceptance.ps1':'new'})

    def test_terminal_session_change_rejected(self):
        with self.assertRaises(ValueError):validate_harnesses({'../git-terminal/src/session.rs':'old'},{'../git-terminal/src/session.rs':'new'})

    def test_only_actual_ui_fixture_and_report_sources_may_change(self):
        self.assertEqual(validate_harnesses({}, {'../git-terminal/assets/git-terminal.ico':'icon'}),['../git-terminal/assets/git-terminal.ico'])


if __name__ == '__main__':unittest.main()
