import os
from pathlib import Path
import subprocess
import tempfile
import unittest


SCRIPT = Path(__file__).resolve().parents[2] / "scripts" / "prepare-release.sh"


class ReleasePreparationTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix="syfon-release-test-")
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.remote = self.root / "origin.git"
        self.repo = self.root / "repo"
        self.repo.mkdir()
        self.git("init", "--bare", str(self.remote))
        self.git("init", "-b", "development")
        self.git("config", "user.name", "Release Test")
        self.git("config", "user.email", "release-test@example.invalid")
        self.git("remote", "add", "origin", str(self.remote))
        (self.repo / "client").mkdir()
        (self.repo / "apigen").mkdir()
        (self.repo / "go.mod").write_text(
            "module github.com/calypr/syfon\n\ngo 1.26.6\n\nrequire (\n"
            "github.com/calypr/syfon/client v0.1.0\n"
            "github.com/calypr/syfon/apigen v0.1.0\n)\n")
        (self.repo / "client/go.mod").write_text(
            "module github.com/calypr/syfon/client\n\ngo 1.26.6\n\n"
            "require github.com/calypr/syfon/apigen v0.1.0\n")
        (self.repo / "apigen/go.mod").write_text(
            "module github.com/calypr/syfon/apigen\n\ngo 1.26.6\n")
        (self.repo / "main.go").write_text("package main\nfunc main() {}\n")
        self.git("add", ".")
        self.git("commit", "-m", "baseline")
        for tag in ["v0.1.0", "client/v0.1.0", "apigen/v0.1.0"]:
            self.git("tag", tag)
        self.git("push", "origin", "development", "--tags")
        self.tools = self.root / "tools"
        self.tools.mkdir()
        gh = self.tools / "gh"
        gh.write_text("#!/bin/sh\necho 129\n")
        gh.chmod(0o755)
        self.output = self.root / "outputs"

    def git(self, *args, cwd=None):
        result = subprocess.run(["git", *args], cwd=cwd or self.repo, text=True,
                                capture_output=True, check=True)
        return result.stdout.strip()

    def change(self, path="client/new.go"):
        (self.repo / path).write_text("package fixture\n")
        self.git("add", ".")
        self.git("commit", "-m", "source change")
        self.source = self.git("rev-parse", "HEAD")
        self.git("push", "origin", "HEAD:development")
        self.git("checkout", "--detach", self.source)

    def prepare(self, repo=None, success=True):
        self.output.write_text("")
        env = dict(os.environ, EVENT_NAME="workflow_run", HEAD_SHA=self.source,
                   GITHUB_REPOSITORY="calypr/syfon", GITHUB_OUTPUT=str(self.output),
                   GOWORK="off", PATH=f"{self.tools}:{os.environ['PATH']}")
        result = subprocess.run(["bash", str(SCRIPT)], cwd=repo or self.repo,
                                env=env, text=True, capture_output=True)
        if success:
            self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
            return dict(line.split("=", 1) for line in self.output.read_text().splitlines())
        self.assertNotEqual(result.returncode, 0, result.stdout + result.stderr)

    def fresh_clone(self):
        clone = self.root / "retry"
        self.git("clone", "--branch", "development", str(self.remote), str(clone))
        self.git("checkout", "--detach", self.source, cwd=clone)
        return clone

    def test_client_release_pins_root_before_tagging_and_resumes(self):
        self.change()
        first = self.prepare()
        self.assertEqual(first["root_tag"], "v0.1.1")
        self.assertEqual(first["client_tag"], "client/v0.1.1")
        self.assertEqual(first["apigen_tag"], "")
        self.assertNotEqual(first["sha"], self.source)
        self.assertEqual(self.git("rev-parse", first["sha"] + "^"), self.source)
        root_mod = self.git("show", first["root_tag"] + ":go.mod")
        self.assertIn("github.com/calypr/syfon/client v0.1.1", root_mod)
        self.assertEqual(self.git("rev-parse", first["client_tag"]), first["sha"])
        second = self.prepare(repo=self.fresh_clone())
        self.assertEqual(first, second)

    def test_apigen_release_pins_both_consumers(self):
        self.change("apigen/new.go")
        result = self.prepare()
        self.assertEqual(result["apigen_tag"], "apigen/v0.1.1")
        self.assertEqual(result["client_tag"], "client/v0.1.1")
        for path in ["go.mod", "client/go.mod"]:
            content = self.git("show", result["sha"] + ":" + path)
            self.assertIn("github.com/calypr/syfon/apigen v0.1.1", content)

    def test_retry_recovers_after_only_sibling_tag_was_pushed(self):
        self.change("apigen/new.go")
        hook = self.remote / "hooks" / "update"
        hook.write_text('#!/bin/sh\n[ "$1" != refs/tags/client/v0.1.1 ]\n')
        hook.chmod(0o755)
        self.prepare(success=False)
        prepared = self.git("rev-parse", "refs/tags/apigen/v0.1.1", cwd=self.remote)
        hook.unlink()
        result = self.prepare(repo=self.fresh_clone())
        self.assertEqual(result["sha"], prepared)
        for tag in ["v0.1.1", "client/v0.1.1", "apigen/v0.1.1"]:
            self.assertEqual(self.git("rev-parse", "refs/tags/" + tag, cwd=self.remote), prepared)

    def test_docs_only_change_does_not_release(self):
        self.change("README.md")
        result = self.prepare()
        self.assertEqual(result["root_tag"], "")
        self.assertEqual(result["sha"], self.source)

    def test_breaking_change_bumps_root_minor_before_v1(self):
        self.change()
        self.git("commit", "--amend", "-m", "refactor: remove old public package",
                 "-m", "BREAKING CHANGE: migrate the old import path")
        self.source = self.git("rev-parse", "HEAD")
        result = self.prepare()
        self.assertEqual(result["root_tag"], "v0.2.0")
        self.assertEqual(result["client_tag"], "client/v0.1.1")

    def test_patch_release_does_not_roll_over_minor_or_major(self):
        for tag in ["v0.9.9", "client/v0.3.9", "apigen/v0.3.9"]:
            self.git("tag", tag)
        self.git("push", "origin", "--tags")
        self.change("apigen/new.go")
        result = self.prepare()
        self.assertEqual(result["root_tag"], "v0.9.10")
        self.assertEqual(result["client_tag"], "client/v0.3.10")
        self.assertEqual(result["apigen_tag"], "apigen/v0.3.10")

    def test_older_source_does_not_replace_a_newer_release(self):
        self.change()
        older_source = self.source
        self.change("client/newer.go")
        newer = self.prepare()
        self.source = older_source
        result = self.prepare(repo=self.fresh_clone())
        self.assertEqual(result["root_tag"], "")
        self.assertEqual(result["sha"], older_source)
        self.assertEqual(self.git("rev-parse", newer["root_tag"], cwd=self.remote), newer["sha"])

    def test_legacy_exact_source_tag_can_resume(self):
        self.change()
        for tag in ["v0.1.1", "client/v0.1.1"]:
            self.git("tag", tag)
        self.git("push", "origin", "--tags")
        result = self.prepare(repo=self.fresh_clone())
        self.assertEqual(result["root_tag"], "v0.1.1")
        self.assertEqual(result["client_tag"], "client/v0.1.1")
        self.assertEqual(result["sha"], self.source)
