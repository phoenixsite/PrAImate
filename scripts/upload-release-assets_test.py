#!/usr/bin/env python3
import importlib.util
from pathlib import Path
import sys
import tempfile
import unittest
from unittest.mock import patch

sys.dont_write_bytecode = True
spec = importlib.util.spec_from_file_location("upload_assets", Path(__file__).with_name("upload-release-assets.py"))
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)


class UploadAssetsTest(unittest.TestCase):
    def fixture(self, names):
        directory = tempfile.TemporaryDirectory()
        self.addCleanup(directory.cleanup)
        for name in names:
            (Path(directory.name) / name).write_text("synthetic release file")
        if "SHA256SUMS" in names:
            checksums = [
                module.hashlib.sha256((Path(directory.name) / name).read_bytes()).hexdigest() + '  ' + name + '\n'
                for name in names if name != 'SHA256SUMS'
            ]
            (Path(directory.name) / "SHA256SUMS").write_text(''.join(checksums))
        return Path(directory.name)

    def test_desktop_completes_before_any_server_archive_upload_starts(self):
        names = ["praimate-server-linux-amd64.tar.gz", "praimate-linux-amd64.tar.gz", "praimate-server-windows-arm64.zip", "praimate-windows-arm64.zip", "praimate-cli-linux-amd64", "SHA256SUMS"]
        directory = self.fixture(names)
        with patch.object(module.subprocess, "run") as run:
            module.upload("v1.3.4", directory)
        groups = [[Path(arg).name for arg in call.args[0][4:-1]] for call in run.call_args_list]
        self.assertEqual(groups[0], ["praimate-desktop-windows-arm64.zip", "praimate-linux-amd64.tar.gz", "praimate-windows-arm64.zip"])
        self.assertEqual(set(groups[0] + groups[1]), set(names) | {'praimate-desktop-windows-arm64.zip'})
        self.assertEqual(len(groups[0] + groups[1]), len(names) + 1)

    def test_github_name_sorting_still_updates_old_windows_clients_to_desktop(self):
        for arch in ('amd64', 'arm64'):
            canonical = f'praimate-windows-{arch}.zip'
            server = f'praimate-server-windows-{arch}.zip'
            directory = self.fixture([canonical, server, 'SHA256SUMS'])

            def legacy_choice():
                return next(file for file in sorted(directory.iterdir()) if 'windows-' + arch in file.name and file.suffix == '.zip')

            self.assertEqual(legacy_choice().name, server)
            module.prepare_windows_aliases(directory)
            selected = legacy_choice()
            self.assertEqual(selected.name, f'praimate-desktop-windows-{arch}.zip')
            self.assertEqual(selected.read_bytes(), (directory / canonical).read_bytes())
            expected = module.hashlib.sha256(selected.read_bytes()).hexdigest()
            self.assertIn(expected + '  ' + selected.name, (directory / 'SHA256SUMS').read_text())
            module.prepare_windows_aliases(directory)
            self.assertEqual((directory / 'SHA256SUMS').read_text().count('  ' + selected.name + '\n'), 1)

    def test_rejects_changed_desktop_bundle_before_uploading(self):
        directory = self.fixture(['praimate-windows-amd64.zip', 'SHA256SUMS'])
        manifest = (directory / 'SHA256SUMS').read_bytes()
        (directory / 'praimate-windows-amd64.zip').write_text('unexpected contents')
        with patch.object(module.subprocess, 'run') as run:
            with self.assertRaisesRegex(ValueError, 'checksum'):
                module.upload('v1.3.4', directory)
            run.assert_not_called()
        self.assertFalse((directory / 'praimate-desktop-windows-amd64.zip').exists())
        self.assertEqual((directory / 'SHA256SUMS').read_bytes(), manifest)

    def test_server_only_releases_remain_separate_and_incomplete_combined_releases_are_rejected(self):
        directory = self.fixture(["praimate-server-linux-amd64.tar.gz"])
        with patch.object(module.subprocess, "run") as run:
            with self.assertRaises(ValueError):
                module.upload("v1.3.4", directory)
            run.assert_not_called()
            module.upload("server-v1.3.4", directory)
            self.assertEqual(run.call_count, 1)


if __name__ == "__main__":
    unittest.main()
