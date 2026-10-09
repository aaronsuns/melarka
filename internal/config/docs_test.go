package config

import (
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

// yamlKeys lists every config.yaml key as a dotted path ("transcode.nice",
// "libraries[].download_target"), walking yaml tags and skipping "-".
func yamlKeys(t reflect.Type, prefix string) []string {
	var out []string
	for i := range t.NumField() {
		f := t.Field(i)
		name := strings.Split(f.Tag.Get("yaml"), ",")[0]
		if name == "" || name == "-" {
			continue
		}
		key := prefix + name
		out = append(out, key)
		ft := f.Type
		if ft.Kind() == reflect.Slice && ft.Elem().Kind() == reflect.Struct {
			out = append(out, yamlKeys(ft.Elem(), key+"[].")...)
		} else if ft.Kind() == reflect.Struct && ft != reflect.TypeOf(time.Duration(0)) {
			out = append(out, yamlKeys(ft, key+".")...)
		}
	}
	return out
}

func TestEveryConfigKeyAndEnvVarIsDocumented(t *testing.T) {
	readme, _ := os.ReadFile("../../README.md")
	ref, _ := os.ReadFile("../../docs/configuration.md")
	example, _ := os.ReadFile("../../deploy/config.example.yaml")
	for _, k := range yamlKeys(reflect.TypeOf(Config{}), "") {
		if !strings.Contains(string(ref), "`"+k+"`") {
			t.Errorf("docs/configuration.md does not document `%s`", k)
		}
		leaf := k[strings.LastIndexAny(k, ".]")+1:]
		if !strings.Contains(string(example), leaf+":") {
			t.Errorf("deploy/config.example.yaml lacks %s", k)
		}
	}
	for _, env := range []string{"LARK_DATA_DIR", "LARK_LISTEN", "LARK_CONFIG", "LARK_ADMIN_USER", "LARK_ADMIN_PASSWORD", "LARK_LANGUAGE", "LARK_LASTFM_API_KEY", "LARK_OFFLINE_CACHE", "LARK_WEB_DIR", "LARK_YOUTUBE_FEED_URL", "LARK_YOUTUBE_THUMB_URL"} {
		if !strings.Contains(string(ref), "`"+env+"`") {
			t.Errorf("docs/configuration.md does not document %s", env)
		}
	}
	for _, f := range []string{"deploy/docker-compose.example.yml", "deploy/config.example.yaml", "docs/configuration.md", "AGENTS.md", "LICENSE", "CONTRIBUTING.md"} {
		if !strings.Contains(string(readme), f) {
			t.Errorf("README.md does not point at %s", f)
		}
	}
}

func TestConfigExampleIsTheDefaults(t *testing.T) {
	t.Setenv("LARK_DATA_DIR", t.TempDir())
	def, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("LARK_CONFIG", "../../deploy/config.example.yaml")
	ex, err := Load()
	if err != nil {
		t.Fatalf("example does not load: %v", err)
	}
	def.Libraries = ex.Libraries // the example seeds two libraries; everything else must equal the defaults
	if !reflect.DeepEqual(def, ex) {
		t.Fatalf("example drifted from the defaults:\n%+v\n%+v", def, ex)
	}
}
