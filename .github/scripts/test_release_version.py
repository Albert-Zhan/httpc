import unittest

from release_version import validate_release_version


class ReleaseVersionTests(unittest.TestCase):
    def test_matching_release(self):
        validate_release_version(
            "v1.4.1", 'package httpc\n\n// Version is the library version.\nconst Version = "1.4.1"\n'
        )

    def test_invalid_versions_are_rejected(self):
        for version in ("1.4.1-dev", "v1.4.1", "01.4.1", "1.4", "1.4.1+build", ""):
            with self.subTest(version=version), self.assertRaises(ValueError):
                validate_release_version("v1.4.1", f'const Version = "{version}"\n')

    def test_mismatched_tag_is_rejected(self):
        with self.assertRaisesRegex(ValueError, "does not match"):
            validate_release_version("v1.4.2", 'const Version = "1.4.1"\n')

    def test_missing_or_ambiguous_declarations_are_rejected(self):
        for source in (
            'package httpc\n// const Version = "1.4.1"\n',
            'const DefaultUserAgent = "httpc/1.4.1"\n',
            'const Version = "1.4.1"\nconst Version = "1.4.2"\n',
        ):
            with self.subTest(source=source), self.assertRaises(ValueError):
                validate_release_version("v1.4.1", source)


if __name__ == "__main__":
    unittest.main()
