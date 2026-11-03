"""Publish exactly the validated commit, preserving existing tags and releases."""

import base64
import json
import os
import re
from pathlib import Path
from urllib.error import HTTPError
from urllib.parse import quote
from urllib.request import Request, urlopen

from release_version import latest_eligible, release_identity, validate_config, validate_release_version


def api(path, data=None, missing_ok=False):
    request = Request(
        os.environ["GH_API_URL"] + f"/repos/{os.environ['GH_REPO']}" + path,
        data=json.dumps(data).encode() if data is not None else None,
        headers={
            "Authorization": f"Bearer {os.environ['GH_TOKEN']}",
            "Accept": "application/vnd.github+json", "Content-Type": "application/json",
            "X-GitHub-Api-Version": "2022-11-28",
        },
    )
    try:
        with urlopen(request, timeout=30) as response:
            return json.load(response)
    except HTTPError as error:
        if missing_ok and error.code == 404:
            return None
        raise SystemExit(f"GitHub API request failed: {path} (HTTP {error.code})")


def source(path, ref):
    content = api(f"/contents/{path}?ref={quote(ref, safe='')}")
    return base64.b64decode(content["content"]).decode("utf-8")


def publish():
    config = json.loads(source(".github/release.json", os.environ["GITHUB_SHA"]))
    if not validate_config(config, os.environ["GITHUB_REF_NAME"]):
        return
    tag, target = config["tag_name"], config["target_commitish"]
    module_dir = config.get("module_dir", ".")
    prefix = "" if module_dir == "." else module_dir + "/"
    _, _, prerelease = release_identity(tag, module_dir)
    comparison = api(f"/compare/{target}...{os.environ['GITHUB_SHA']}")
    if comparison["status"] not in ("ahead", "identical"):
        raise SystemExit("Release target must be an ancestor of the configuration commit")
    module_source = source(prefix + "go.mod", target)
    validate_release_version(tag, source(prefix + "version.go", target), module_source, module_dir)
    if module_dir != ".":
        versions = re.findall(r"^\s*github\.com/Albert-Zhan/httpc/v2\s+(v\S+)\s*$", module_source, re.MULTILINE)
        if len(versions) != 1:
            raise SystemExit("adapter go.mod must pin one core v2 dependency")
        core = api(f"/releases/tags/{quote(versions[0], safe='')}", missing_ok=True)
        if not core or core["draft"] or (not prerelease and core["prerelease"]):
            raise SystemExit("Publish the required core version before the adapter")
        validate_release_version(versions[0], source("version.go", versions[0]), source("go.mod", versions[0]))
    existing_tag = api(f"/git/ref/tags/{quote(tag, safe='/')}", missing_ok=True)
    if existing_tag and api(f"/commits/{quote(tag, safe='')}")["sha"] != target:
        raise SystemExit("Existing tag points to another commit; refusing to move it")
    release = api(f"/releases/tags/{quote(tag, safe='')}", missing_ok=True)
    if release:
        if release["draft"] or release["prerelease"] != prerelease or not existing_tag:
            raise SystemExit("Existing release does not match the requested release type and tag")
        print(f"Already published: {release['html_url']}")
    else:
        published_tags = []
        page = 1
        while True:
            releases = api(f"/releases?per_page=100&page={page}")
            published_tags.extend(r["tag_name"] for r in releases if not r["draft"] and not r["prerelease"])
            if len(releases) < 100:
                break
            page += 1
        release = api("/releases", {
            "tag_name": tag, "target_commitish": target, "name": config["name"], "body": config["body"],
            "draft": False, "prerelease": prerelease,
            "make_latest": "true" if latest_eligible(tag, module_dir, published_tags) else "false",
        })
        print(f"Published: {release['html_url']}")
    if api(f"/commits/{quote(tag, safe='')}")["sha"] != target:
        raise SystemExit("Published tag does not match the requested commit")
    Path(os.environ["GITHUB_STEP_SUMMARY"]).write_text(
        f"## {tag}\n\nRelease: {release['html_url']}\n\nCommit: `{target}`\n", encoding="utf-8",
    )


if __name__ == "__main__":
    publish()
