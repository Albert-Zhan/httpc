import json
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch

import publish_release


class PublishTests(unittest.TestCase):
    def run_publish(self, tag, branch, published=(), existing_sha=None, dependency=True):
        directory = "adapters/tlsclient" if tag.startswith("adapters/") else "."
        version = tag.split("/v")[-1] if directory != "." else tag[1:]
        target = "a" * 40
        config = {"tag_name": tag, "target_commitish": target, "module_dir": directory, "name": tag, "body": "changes"}
        creates = []
        module = "github.com/Albert-Zhan/httpc" + ("/adapters/tlsclient" if directory != "." else "")
        if version.startswith("2."):
            module += "/v2"

        def source(path, ref):
            if path == ".github/release.json":
                return json.dumps(config)
            if directory != "." and path == "version.go":
                return 'const Version = "2.0.0"\n'
            if directory != "." and path == "go.mod":
                return "module github.com/Albert-Zhan/httpc/v2\n"
            if path.endswith("version.go"):
                return f'const Version = "{version}"\n'
            return f"module {module}\nrequire (\n github.com/Albert-Zhan/httpc/v2 v2.0.0\n)\n"

        def api(path, data=None, missing_ok=False):
            if path.startswith("/compare/"):
                return {"status": "ahead"}
            if path.startswith("/git/ref/"):
                return {"object": {"sha": existing_sha}} if existing_sha else None
            if path.startswith("/commits/"):
                return {"sha": existing_sha or target}
            if path == "/releases/tags/v2.0.0" and directory != ".":
                return {"draft": False, "prerelease": False} if dependency else None
            if path.startswith("/releases/tags/"):
                return None
            if path.startswith("/releases?"):
                return [{"tag_name": t, "draft": False, "prerelease": False} for t in published]
            if path == "/releases" and data:
                creates.append(data)
                return {"html_url": "https://example.com/release"}
            self.fail(f"unexpected API {path}")

        with tempfile.TemporaryDirectory() as tmp:
            env = {"GITHUB_SHA": "b" * 40, "GITHUB_REF_NAME": branch, "GITHUB_STEP_SUMMARY": str(Path(tmp) / "summary")}
            with patch.dict("os.environ", env), patch.object(publish_release, "source", source), patch.object(publish_release, "api", api):
                publish_release.publish()
        return creates

    def test_prerelease_flags(self):
        created = self.run_publish("v2.0.0-beta.1", "dev/v2")
        self.assertEqual(created[0]["make_latest"], "false")
        self.assertTrue(created[0]["prerelease"])

    def test_stable_latest_and_v1_maintenance(self):
        self.assertEqual(self.run_publish("v2.0.0", "master", ["v1.6.0"])[0]["make_latest"], "true")
        self.assertEqual(self.run_publish("v1.6.1", "release/v1", ["v2.0.0"])[0]["make_latest"], "false")

    def test_adapter_order_and_latest(self):
        created = self.run_publish("adapters/tlsclient/v2.0.0", "master")
        self.assertEqual(created[0]["make_latest"], "false")
        with self.assertRaisesRegex(SystemExit, "core version"):
            self.run_publish("adapters/tlsclient/v2.0.0", "master", dependency=False)

    def test_existing_tag_cannot_move(self):
        with self.assertRaisesRegex(SystemExit, "refusing to move"):
            self.run_publish("v2.0.0", "master", existing_sha="c" * 40)


if __name__ == "__main__":
    unittest.main()
