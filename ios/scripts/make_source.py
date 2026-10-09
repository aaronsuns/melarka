#!/usr/bin/env python3
"""Build the SideStore (AltStore v2) source.json for Melarka from the GitHub Releases.

make_source.py --releases releases.json --repo OWNER/REPO > source.json

releases.json is the GitHub Releases API list (`gh api repos/OWNER/REPO/releases`). Each published,
non-prerelease release with a Melarka-X.Y.Z.ipa asset becomes one version entry; buildVersion is the
semver, as the release build sets CFBundleVersion to it. Keeps the 5 newest, newest first. The static
metadata (names, bundle id, declared privacy strings) comes from ../source-template.json.
"""
import argparse
import json
import os
import re
import sys

KEEP = 5
IPA_ASSET = re.compile(r"^Melarka-(\d+\.\d+\.\d+)\.ipa$")
DESCRIPTION_MAX = 500
MIN_OS = "17.0"
TEMPLATE = os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "source-template.json")


class ReleasesError(Exception):
    """The releases JSON is not what the GitHub Releases API returns: publishing would drop the history."""


def load_template():
    with open(TEMPLATE, encoding="utf-8") as f:
        return json.load(f)


def from_releases(releases, repo):
    """source.json from the GitHub Releases API list: the newest KEEP releases that ship an IPA."""
    if not isinstance(releases, list):
        raise ReleasesError("releases JSON is not a list")
    owner, name = repo.split("/", 1)
    entries = []
    for r in releases:
        try:
            if r["draft"] or r["prerelease"]:
                continue
            for a in r["assets"]:
                m = IPA_ASSET.match(a["name"])
                if m:
                    entries.append({
                        "version": m.group(1),
                        "buildVersion": m.group(1),
                        "date": r["published_at"],
                        "downloadURL": a["browser_download_url"],
                        "size": a["size"],
                        "minOSVersion": MIN_OS,
                        "localizedDescription": (r.get("body") or "")[:DESCRIPTION_MAX],
                    })
                    break
        except (TypeError, KeyError) as e:
            raise ReleasesError(f"unexpected release JSON: {e!r}") from e
    entries.sort(key=lambda v: v["date"], reverse=True)
    source = load_template()
    app = source["apps"][0]
    app["iconURL"] = f"https://{owner}.github.io/{name}/icon.png"
    app["versions"] = entries[:KEEP]
    return source


def main():
    p = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    p.add_argument("--releases", required=True, help="GitHub Releases API JSON (a list)")
    p.add_argument("--repo", required=True, help="OWNER/REPO")
    a = p.parse_args()
    if "/" not in a.repo:
        p.error("--repo must be OWNER/REPO")
    try:
        with open(a.releases, encoding="utf-8") as f:
            out = from_releases(json.load(f), a.repo)
    except (ValueError, ReleasesError) as e:
        sys.exit(f"make_source: {e}; not publishing")
    json.dump(out, sys.stdout, ensure_ascii=False, indent=2)
    sys.stdout.write("\n")


if __name__ == "__main__":
    main()
