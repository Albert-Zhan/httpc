"""Use the checked-out core for optional adapter development and CI."""

import re
import subprocess
from pathlib import Path


def setup():
    root = Path(__file__).resolve().parents[2]
    adapter = root / "adapters/tlsclient/go.mod"
    if not adapter.exists():
        return
    matches = re.findall(r"^\s*github\.com/Albert-Zhan/httpc/v2\s+(v\S+)\s*$", adapter.read_text(), re.MULTILINE)
    if len(matches) != 1:
        raise SystemExit("adapter must require exactly one core v2 version")
    if not (root / "go.work").exists():
        subprocess.run(["go", "work", "init"], cwd=root, check=True)
    subprocess.run(["go", "work", "use", ".", "./adapters/tlsclient"], cwd=root, check=True)
    subprocess.run(["go", "work", "edit", f"-replace=github.com/Albert-Zhan/httpc/v2@{matches[0]}=."], cwd=root, check=True)


if __name__ == "__main__":
    setup()
