import json
import os
import plistlib
import subprocess
import sys
import tempfile
import unittest

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, HERE)
from make_source import ReleasesError, from_releases, load_template  # noqa: E402


def rel(version, published, body="notes", draft=False, prerelease=False, ipa=True):
    assets = [{"name": "notes.txt", "browser_download_url": "https://x/notes.txt", "size": 1}]
    if ipa:
        assets.append({"name": f"Melarka-{version}.ipa", "size": 1000 + int(version.split(".")[1]),
                       "browser_download_url": f"https://github.com/o/r/releases/download/v{version}/Melarka-{version}.ipa"})
    return {"tag_name": f"v{version}", "draft": draft, "prerelease": prerelease, "published_at": published,
            "body": body, "assets": assets}


FIXTURE = [rel("0.3.0", "2026-10-12T00:00:00Z", draft=True),
           rel("0.2.0", "2026-10-11T00:00:00Z", ipa=False),
           rel("0.1.0", "2026-10-10T00:00:00Z", body="x" * 600),
           rel("0.1.1", "2026-10-10T12:00:00Z", prerelease=True)]

ENTRY_0_1_0 = {"version": "0.1.0", "buildVersion": "0.1.0", "date": "2026-10-10T00:00:00Z",
               "downloadURL": "https://github.com/o/r/releases/download/v0.1.0/Melarka-0.1.0.ipa", "size": 1001,
               "minOSVersion": "17.0", "localizedDescription": "x" * 500}


class FromReleasesTests(unittest.TestCase):
    """GitHub Pages: source.json is rebuilt from the GitHub Releases API, newest 5, newest first."""

    def test_one_entry_per_published_release_with_an_ipa(self):
        app = from_releases(FIXTURE, "o/r")["apps"][0]
        self.assertEqual(app["iconURL"], "https://o.github.io/r/icon.png")
        self.assertEqual(app["versions"], [ENTRY_0_1_0])

    def test_melarka_identity(self):
        s = from_releases([], "aaronsuns/melarka")
        self.assertEqual(s["name"], "Melarka")
        self.assertEqual(s["identifier"], "io.github.aaronsuns.melarka.source")
        app = s["apps"][0]
        self.assertEqual(app["name"], "Melarka")
        self.assertEqual(app["bundleIdentifier"], "io.github.aaronsuns.melarka")
        self.assertEqual(app["iconURL"], "https://aaronsuns.github.io/melarka/icon.png")
        self.assertIn("Melarka", app["localizedDescription"])

    def test_newest_five_newest_first(self):
        rels = [rel(f"0.{i}.0", f"2026-10-{10 + i:02d}T00:00:00Z") for i in (3, 1, 7, 2, 5, 6, 4)]
        vs = [v["version"] for v in from_releases(rels, "o/r")["apps"][0]["versions"]]
        self.assertEqual(vs, ["0.7.0", "0.6.0", "0.5.0", "0.4.0", "0.3.0"])

    def test_no_release_gives_an_empty_list(self):
        self.assertEqual(from_releases([], "o/r")["apps"][0]["versions"], [])

    def test_not_a_release_list_aborts(self):
        for bad in ({}, "x", None, [{"no": "fields"}]):
            with self.assertRaises(ReleasesError, msg=repr(bad)):
                from_releases(bad, "o/r")

    def test_the_template_matches_the_apps_info_plist(self):
        """SideStore refuses an install whose declared privacy strings do not match the app's."""
        with open(os.path.join(HERE, "..", "Sources/App/Info.plist"), "rb") as f:
            info = plistlib.load(f)
        want = {k: v for k, v in info.items() if k.endswith("UsageDescription")}
        self.assertEqual(load_template()["apps"][0]["appPermissions"]["privacy"], want)
        self.assertEqual(load_template()["apps"][0]["name"], info["CFBundleDisplayName"])


class CommandLineTests(unittest.TestCase):
    def run_cli(self, releases, *args):
        with tempfile.NamedTemporaryFile("w", suffix=".json", delete=False) as f:
            json.dump(releases, f)
        self.addCleanup(os.unlink, f.name)
        return subprocess.run([sys.executable, os.path.join(HERE, "make_source.py"), "--releases", f.name, *args],
                              capture_output=True, text=True)

    def test_writes_source_json(self):
        p = self.run_cli(FIXTURE, "--repo", "aaronsuns/melarka")
        self.assertEqual(p.returncode, 0, p.stderr)
        s = json.loads(p.stdout)
        self.assertEqual(s["apps"][0]["bundleIdentifier"], "io.github.aaronsuns.melarka")
        self.assertEqual(s["apps"][0]["versions"], [ENTRY_0_1_0])

    def test_bad_input_does_not_publish(self):
        p = self.run_cli({"message": "Not Found"}, "--repo", "aaronsuns/melarka")
        self.assertNotEqual(p.returncode, 0)
        self.assertEqual(p.stdout, "")

    def test_repo_is_required(self):
        self.assertNotEqual(self.run_cli([]).returncode, 0)
        self.assertNotEqual(self.run_cli([], "--repo", "melarka").returncode, 0)


if __name__ == "__main__":
    unittest.main()
