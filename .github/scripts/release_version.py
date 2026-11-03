"""Release identity and branch policy shared by validation and publishing."""

import re

NUMBER = r"(?:0|[1-9][0-9]*)"
VERSION = rf"{NUMBER}\.{NUMBER}\.{NUMBER}(?:-(?:alpha|beta|rc)\.{NUMBER})?"
MODULES = {".": "", "adapters/tlsclient": "adapters/tlsclient/"}


def release_identity(tag, module_dir="."):
    if module_dir not in MODULES or not isinstance(tag, str):
        raise ValueError("unsupported module directory or tag")
    prefix = MODULES[module_dir]
    if not tag.startswith(prefix + "v") or not re.fullmatch(VERSION, tag[len(prefix) + 1:]):
        raise ValueError("tag must match the module prefix and X.Y.Z[-alpha.N|-beta.N|-rc.N]")
    version = tag[len(prefix) + 1:]
    major = int(version.split(".")[0])
    if major < 1 or (module_dir != "." and major != 2):
        raise ValueError("unsupported release major")
    return version, major, "-" in version


def validate_release_version(tag, source, module_source=None, module_dir="."):
    version, major, _ = release_identity(tag, module_dir)
    declarations = re.findall(
        r'^const Version = "([^"\\\r\n]+)"[ \t]*(?://[^\r\n]*)?$', source, re.MULTILINE,
    )
    if len(declarations) != 1:
        raise ValueError('version.go must declare one literal const Version = "X.Y.Z"')
    if declarations[0] != version:
        raise ValueError(f"release tag {tag!r} does not match Version {declarations[0]!r}")
    if module_source is not None:
        base = "github.com/Albert-Zhan/httpc"
        if module_dir != ".":
            base += "/" + module_dir
        expected = base + (f"/v{major}" if major >= 2 else "")
        modules = re.findall(r"^module\s+(\S+)\s*$", module_source, re.MULTILINE)
        if modules != [expected]:
            raise ValueError(f"go.mod module must be {expected}")


def validate_branch(tag, module_dir, branch):
    _, major, prerelease = release_identity(tag, module_dir)
    allowed = (
        (branch == "master" and major in (1, 2))
        or (branch == "release/v1" and module_dir == "." and major == 1 and not prerelease)
        or (branch == "dev/v2" and major == 2 and prerelease)
    )
    if not allowed:
        raise ValueError(f"{branch} cannot publish {tag}")


def validate_config(config, branch):
    if not isinstance(config, dict) or type(config.get("enabled", True)) is not bool:
        raise ValueError("enabled must be a boolean")
    if not config.get("enabled", True):
        return False
    module_dir = config.get("module_dir", ".")
    validate_branch(config.get("tag_name"), module_dir, branch)
    if not isinstance(config.get("target_commitish"), str) or not re.fullmatch(r"[0-9a-f]{40}", config["target_commitish"]):
        raise ValueError("target_commitish must be a full commit SHA")
    for field in ("name", "body"):
        if not isinstance(config.get(field), str) or not config[field].strip():
            raise ValueError(f"{field} must be a non-empty string")
    return True


def latest_eligible(tag, module_dir, published_tags):
    version, _, prerelease = release_identity(tag, module_dir)
    if module_dir != "." or prerelease:
        return False
    current = tuple(map(int, version.split(".")))
    for existing in published_tags:
        if re.fullmatch(rf"v{NUMBER}\.{NUMBER}\.{NUMBER}", existing):
            if tuple(map(int, existing[1:].split("."))) > current:
                return False
    return True
