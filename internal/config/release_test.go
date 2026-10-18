package config

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"testing"
)

// F-Droid builds the Android app from the release tag without -PlarkVersion, so it reads the version from
// ios/project.yml like a local build. Its update check cannot run the Gradle version function, so the
// versionCode is also written out in android/version.properties; F-Droid lists the store texts and the
// changelog from fastlane/metadata/android at the repository root.
func TestAndroidVersionForFDroid(t *testing.T) {
	yml, err := os.ReadFile("../../ios/project.yml")
	if err != nil {
		t.Skip("no ios/project.yml in this tree")
	}
	m := regexp.MustCompile(`MARKETING_VERSION:\s*"?([0-9]+)\.([0-9]+)\.([0-9]+)"?`).FindSubmatch(yml)
	if m == nil {
		t.Fatal("no MARKETING_VERSION in ios/project.yml")
	}
	var part [3]int
	for i := range part {
		part[i], _ = strconv.Atoi(string(m[i+1]))
	}
	want := part[0]*10000 + part[1]*100 + part[2] // android/buildSrc/src/main/kotlin/lark/build/LarkVersion.kt

	props, err := os.ReadFile("../../android/version.properties")
	if err != nil {
		t.Fatal(err)
	}
	pm := regexp.MustCompile(`(?m)^versionCode=([0-9]+)\s*$`).FindSubmatch(props)
	if pm == nil {
		t.Fatal("android/version.properties has no versionCode=N line")
	}
	if got, _ := strconv.Atoi(string(pm[1])); got != want {
		t.Errorf("android/version.properties says versionCode=%d, but MARKETING_VERSION %s.%s.%s needs %d: bump it with the version",
			got, m[1], m[2], m[3], want)
	}

	changelog := fmt.Sprintf("../../fastlane/metadata/android/en-US/changelogs/%d.txt", want)
	if _, err := os.Stat(changelog); err != nil {
		t.Errorf("no %s: every release has an English changelog for F-Droid", changelog[len("../../"):])
	}
}
