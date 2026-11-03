import unittest

from release_version import latest_eligible, release_identity, validate_branch, validate_config, validate_release_version


class ReleasePolicyTests(unittest.TestCase):
    def test_module_identity(self):
        cases = [
            ("v1.6.1", ".", "github.com/Albert-Zhan/httpc"),
            ("v2.0.0-beta.1", ".", "github.com/Albert-Zhan/httpc/v2"),
            ("adapters/tlsclient/v2.0.0", "adapters/tlsclient", "github.com/Albert-Zhan/httpc/adapters/tlsclient/v2"),
        ]
        for tag, directory, module in cases:
            with self.subTest(tag=tag):
                version, _, _ = release_identity(tag, directory)
                source = f'const Version = "{version}"\n'
                validate_release_version(tag, source, f"module {module}\n\ngo 1.27\n", directory)
                with self.assertRaisesRegex(ValueError, "go.mod module"):
                    validate_release_version(tag, source, "module example.com/wrong\n", directory)

    def test_branch_policy(self):
        for branch, tag, directory, allowed in [
            ("master", "v1.6.1", ".", True),
            ("master", "v2.0.0", ".", True),
            ("release/v1", "v1.6.1", ".", True),
            ("release/v1", "v2.0.0", ".", False),
            ("release/v1", "v1.6.1-rc.1", ".", False),
            ("dev/v2", "v2.0.0-beta.1", ".", True),
            ("dev/v2", "v2.0.0", ".", False),
            ("dev/v2", "v1.6.1", ".", False),
            ("dev/v2", "adapters/tlsclient/v2.0.0-rc.1", "adapters/tlsclient", True),
            ("feature/test", "v2.0.0", ".", False),
        ]:
            with self.subTest(branch=branch, tag=tag):
                if allowed:
                    validate_branch(tag, directory, branch)
                else:
                    with self.assertRaises(ValueError):
                        validate_branch(tag, directory, branch)

    def test_latest_policy(self):
        self.assertTrue(latest_eligible("v2.0.0", ".", ["v1.6.0"]))
        self.assertTrue(latest_eligible("v1.6.1", ".", ["v1.6.0"]))
        self.assertFalse(latest_eligible("v1.6.1", ".", ["v2.0.0"]))
        self.assertFalse(latest_eligible("v2.0.1", ".", ["v2.1.0"]))
        self.assertFalse(latest_eligible("v2.0.0-rc.1", ".", ["v1.6.0"]))
        self.assertFalse(latest_eligible("adapters/tlsclient/v2.0.0", "adapters/tlsclient", []))

    def test_config_is_disabled_by_default_in_development(self):
        self.assertFalse(validate_config({"enabled": False}, "dev/v2"))
        for config in ({"enabled": "false"}, {"tag_name": "v2.0.0"}):
            with self.assertRaises(ValueError):
                validate_config(config, "dev/v2")

    def test_development_versions_and_wrong_prefixes(self):
        for tag, directory in [
            ("v2.0.0-dev", "."), ("v2.0.0+build", "."), ("v02.0.0", "."),
            ("v2.0.0-beta.01", "."), ("v2.0.0", "adapters/tlsclient"),
            ("adapters/tlsclient/v2.0.0", "."), ("v2.0.0", "../outside"),
        ]:
            with self.subTest(tag=tag), self.assertRaises(ValueError):
                release_identity(tag, directory)


if __name__ == "__main__":
    unittest.main()
