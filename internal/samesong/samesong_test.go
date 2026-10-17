package samesong

import "testing"

func TestSame(t *testing.T) {
	v := FromVideo("王菲 Faye Wong -《流年》(Official Music Video) [HD]", "FayeWongVEVO")
	for _, c := range []struct {
		title, artist string
		want          bool
	}{
		{"流年", "王菲", true},
		{"流年", "Faye Wong", true},    // the name in the raw title
		{"流年", "李荣浩", false},        // another singer's 流年
		{"流年 (Live)", "王菲", true},  // brackets don't count
		{"人间", "王菲", false},
	} {
		if got := v.Same(c.title, c.artist); got != c.want {
			t.Errorf("%s/%s = %v", c.title, c.artist, got)
		}
	}
	if !FromVideo("鄧麗君 - 甜蜜蜜", "Teresa Teng").Same("甜蜜蜜", "邓丽君") {
		t.Error("traditional vs simplified")
	}
	if FromVideo("", "").Same("", "") {
		t.Error("empty titles match")
	}
}
