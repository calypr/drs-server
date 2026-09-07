import hashlib
import io
import os
from pathlib import Path
import subprocess
import tarfile
import tempfile
import unittest


INSTALLER = Path(__file__).resolve().parents[2] / "install.sh"
VERSION = "v1.2.3"


class InstallerTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix="syfon-installer-test-")
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.bin = self.root / "tools"
        self.bin.mkdir()
        self.downloads = self.root / "downloads"
        self.downloads.mkdir()
        self.dest = self.root / "install with spaces"
        self.env = dict(os.environ, PATH=f"{self.bin}:{os.environ['PATH']}",
                        TMPDIR=str(self.root), FIXTURES=str(self.downloads),
                        GITHUB_TOKEN="")
        self.write_tool("uname", '#!/bin/sh\ncase "$1" in -s) echo Linux;; -m) echo x86_64;; esac\n')
        self.write_tool("curl", '''#!/usr/bin/env python3
import os
from pathlib import Path
import shutil
import sys
args = sys.argv[1:]
url = next(arg for arg in args if arg.startswith("https://"))
if "api.github.com" in url:
    if os.environ.get("EMPTY_RELEASE"):
        print("{}")
    elif "per_page" in url:
        print('[\\n{"tag_name": "v1.2.3"},\\n{"tag_name": "v1.2.2"}\\n]')
    else:
        print('{"tag_name": "v1.2.3"}')
    sys.exit(0)
source = Path(os.environ["FIXTURES"]) / url.rsplit("/", 1)[1]
if not source.exists():
    sys.exit(22)
shutil.copyfile(source, args[args.index("-o") + 1])
''')
        self.archive = self.downloads / f"syfon-linux-amd64-{VERSION}.tar.gz"
        payload = b'#!/bin/sh\necho "syfon fixture version"\n'
        with tarfile.open(self.archive, "w:gz") as archive:
            info = tarfile.TarInfo("syfon")
            info.size = len(payload)
            info.mode = 0o755
            archive.addfile(info, io.BytesIO(payload))
        self.checksums = self.downloads / f"syfon-{VERSION}-checksums.txt"
        digest = hashlib.sha256(self.archive.read_bytes()).hexdigest()
        self.checksums.write_text(f"{digest}  {self.archive.name}\n")

    def write_tool(self, name, source):
        path = self.bin / name
        path.write_text(source)
        path.chmod(0o755)

    def run_installer(self, *args):
        result = subprocess.run(["bash", str(INSTALLER), *args], cwd=self.root,
                                env=self.env, text=True, capture_output=True)
        self.assertEqual(list(self.root.glob("syfon-install.*")), [], result.stderr)
        return result

    def test_latest_installs_into_directory_with_spaces(self):
        result = self.run_installer("--dest", str(self.dest))
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertTrue(os.access(self.dest / "syfon", os.X_OK))
        self.assertIn("syfon fixture version", result.stdout)
        self.assertFalse((self.root / self.archive.name).exists())

    def test_positional_version_and_destination(self):
        result = self.run_installer(VERSION, str(self.dest))
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertTrue((self.dest / "syfon").is_file())

    def test_bad_checksum_preserves_existing_installation(self):
        self.dest.mkdir()
        binary = self.dest / "syfon"
        binary.write_text("existing installation")
        self.checksums.write_text(f"{'0' * 64}  {self.archive.name}\n")
        result = self.run_installer(VERSION, str(self.dest))
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("checksum verification failed", result.stderr)
        self.assertEqual(binary.read_text(), "existing installation")

    def test_missing_download_fails_without_installing(self):
        self.archive.unlink()
        result = self.run_installer(VERSION, str(self.dest))
        self.assertNotEqual(result.returncode, 0)
        self.assertFalse(self.dest.exists())

    def test_missing_checksum_entry_fails_without_installing(self):
        self.checksums.write_text(f"{'0' * 64}  another-archive.tar.gz\n")
        result = self.run_installer(VERSION, str(self.dest))
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("missing or invalid checksum", result.stderr)
        self.assertFalse(self.dest.exists())

    def test_missing_option_values_have_clear_errors(self):
        for option in ["--version", "--dest"]:
            with self.subTest(option=option):
                result = self.run_installer(option)
                self.assertNotEqual(result.returncode, 0)
                self.assertIn("requires", result.stderr)

    def test_unsupported_platform_fails_before_download(self):
        self.write_tool("uname", "#!/bin/sh\necho FreeBSD\n")
        result = self.run_installer(VERSION, str(self.dest))
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("supported operating systems", result.stderr)
        self.assertFalse(self.dest.exists())

    def test_empty_latest_release_fails(self):
        self.env["EMPTY_RELEASE"] = "1"
        result = self.run_installer("--dest", str(self.dest))
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("invalid release version", result.stderr)

    def test_help_and_release_listing(self):
        self.assertEqual(self.run_installer("--help").returncode, 0)
        result = self.run_installer("--list")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stdout.splitlines(), ["v1.2.3", "v1.2.2"])

    def test_unrunnable_release_preserves_existing_installation(self):
        self.dest.mkdir()
        binary = self.dest / "syfon"
        binary.write_text("existing installation")
        payload = b"#!/bin/sh\nexit 1\n"
        with tarfile.open(self.archive, "w:gz") as archive:
            info = tarfile.TarInfo("syfon")
            info.size = len(payload)
            info.mode = 0o755
            archive.addfile(info, io.BytesIO(payload))
        digest = hashlib.sha256(self.archive.read_bytes()).hexdigest()
        self.checksums.write_text(f"{digest}  {self.archive.name}\n")
        result = self.run_installer(VERSION, str(self.dest))
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(binary.read_text(), "existing installation")
        self.assertEqual(list(self.dest.glob(".syfon-install.*")), [])
