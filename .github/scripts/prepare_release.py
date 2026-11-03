"""Validate a pinned target without publishing or moving tags."""

import json
import os
import subprocess
from pathlib import Path

from release_version import validate_config, validate_release_version


def prepare():
    config = json.loads(Path(".github/release.json").read_text(encoding="utf-8"))
    enabled = validate_config(config, os.environ["GITHUB_REF_NAME"])
    with open(os.environ["GITHUB_OUTPUT"], "a", encoding="utf-8") as output:
        output.write(f"enabled={str(enabled).lower()}\n")
        if not enabled:
            print("Release configuration is disabled; nothing will be published.")
            return
        target = config["target_commitish"]
        subprocess.run(["git", "merge-base", "--is-ancestor", target, os.environ["GITHUB_SHA"]], check=True)
        module_dir = config.get("module_dir", ".")
        prefix = "" if module_dir == "." else module_dir + "/"
        def source(path):
            return subprocess.check_output(["git", "show", f"{target}:{prefix}{path}"]).decode("utf-8")
        validate_release_version(config["tag_name"], source("version.go"), source("go.mod"), module_dir)
        output.write(f"target={target}\nmodule_dir={module_dir}\n")


if __name__ == "__main__":
    prepare()
