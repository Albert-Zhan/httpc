"""Validate the library version from the exact commit being released."""

import re


def validate_release_version(tag, source):
    declarations = re.findall(
        r'^const Version = "([^"\\\r\n]+)"[ \t]*(?://[^\r\n]*)?$',
        source,
        re.MULTILINE,
    )
    if len(declarations) != 1:
        raise ValueError('version.go must declare one literal const Version = "X.Y.Z"')
    version = declarations[0]
    number = r"(?:0|[1-9][0-9]*)"
    if not re.fullmatch(rf"{number}\.{number}\.{number}", version):
        raise ValueError("release Version must be a stable X.Y.Z value, without a v prefix")
    if tag != "v" + version:
        raise ValueError(f"release tag {tag!r} does not match Version {version!r}")
