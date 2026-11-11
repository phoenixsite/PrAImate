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
        return Path(directory.name)

    def test_desktop_completes_before_any_server_archive_upload_starts(self):
        names = ["praimate-server-linux-amd64.tar.gz", "praimate-linux-amd64.tar.gz", "praimate-server-windows-arm64.zip", "praimate-windows-arm64.zip", "praimate-cli-linux-amd64", "SHA256SUMS"]
        directory = self.fixture(names)
        with patch.object(module.subprocess, "run") as run:
            module.upload("v1.3.4", directory)
        groups = [[Path(arg).name for arg in call.args[0][4:-1]] for call in run.call_args_list]
        self.assertEqual(groups[0], ["praimate-linux-amd64.tar.gz", "praimate-windows-arm64.zip"])
        self.assertEqual(set(groups[0] + groups[1]), set(names))
        self.assertEqual(len(groups[0] + groups[1]), len(names))

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
