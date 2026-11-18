#!/usr/bin/env python3
"""Exercise the real shell installer against isolated release fixtures."""
import hashlib
import io
import pathlib
import platform
import subprocess
import tarfile
import tempfile
import unittest

SCRIPT = pathlib.Path(__file__).resolve().parent / 'install.sh'

class InstallerTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = pathlib.Path(self.temp.name)
        self.dest = self.root / 'bin'
        self.dest.mkdir()
        self.old = self.dest / 'bgmcat'
        self.old.write_text('old version')
        self.data = self.dest / 'database.sqlite'
        self.data.write_text('existing data')
        osname = 'macos' if platform.system() == 'Darwin' else 'linux'
        arch = 'arm64' if platform.machine() in ('arm64', 'aarch64') else 'amd64'
        name = f'bgmcat-{osname}-{arch}'
        self.name = name
        self.archive = self.root / (name + '.tar.gz')
        with tarfile.open(self.archive, 'w:gz') as archive:
            body = b'#!/bin/sh\necho 0.2.0\n'
            entry = tarfile.TarInfo(name + '/bgmcat')
            entry.size = len(body)
            entry.mode = 0o755
            archive.addfile(entry, io.BytesIO(body))
        self.checksum = self.root / 'SHA256SUMS'
        self.checksum.write_text(hashlib.sha256(self.archive.read_bytes()).hexdigest() + '  ' + self.archive.name + '\n')

    def install(self):
        return subprocess.run(['sh', str(SCRIPT), '--local-dir', str(self.root), '--dir', str(self.dest)], capture_output=True, text=True)

    def test_install_preserves_data_and_previous_binary(self):
        result = self.install()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(subprocess.check_output([str(self.old), '--version'], text=True).strip(), '0.2.0')
        self.assertEqual((self.dest / 'bgmcat.previous').read_text(), 'old version')
        self.assertEqual(self.data.read_text(), 'existing data')

    def test_stdin_install_without_checkout_and_start(self):
        result = subprocess.run(['sh', '-s', '--', '--local-dir', str(self.root), '--dir', str(self.dest), '--start'],
                                input=SCRIPT.read_text(), cwd=self.root, capture_output=True, text=True)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn('Starting bgmcat', result.stdout)
        self.assertEqual(result.stdout.count('0.2.0'), 2)

    def test_corruption_leaves_installation_unchanged(self):
        with self.archive.open('ab') as stream:
            stream.write(b'corrupt')
        self.assertNotEqual(self.install().returncode, 0)
        self.assertEqual(self.old.read_text(), 'old version')

    def test_missing_checksum_rejected(self):
        self.checksum.write_text('')
        self.assertNotEqual(self.install().returncode, 0)
        self.assertEqual(self.old.read_text(), 'old version')

    def test_duplicate_checksum_rejected(self):
        self.checksum.write_text(self.checksum.read_text() * 2)
        self.assertNotEqual(self.install().returncode, 0)
        self.assertEqual(self.old.read_text(), 'old version')

if __name__ == '__main__':
    unittest.main()
