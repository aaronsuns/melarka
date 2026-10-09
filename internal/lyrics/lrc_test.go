package lyrics

import (
	"reflect"
	"testing"
)

func TestParseLRC(t *testing.T) {
	in := "\ufeff[ti:甜蜜蜜]\r\n[ar:邓丽君]\n[offset:500]\n[00:15.80]好像花儿开在春风里\n[00:08.31][00:30.5]甜蜜蜜\n[00:11.592]你笑得甜蜜蜜\n[01:02]\n[00:20.93]开在春风里"
	lines, synced := ParseLRC(in)
	want := []Line{{7810, "甜蜜蜜"}, {11092, "你笑得甜蜜蜜"}, {15300, "好像花儿开在春风里"}, {20430, "开在春风里"}, {30000, "甜蜜蜜"}, {61500, ""}}
	if !synced || !reflect.DeepEqual(lines, want) {
		t.Fatalf("synced=%v\n%v", synced, lines)
	}
	if _, synced := ParseLRC("[00:01.00]one\n[00:02.00]two"); synced {
		t.Error("two lines is not synced")
	}
	if _, synced := ParseLRC("just words\nmore words\nand more"); synced {
		t.Error("plain text is not synced")
	}
	if got := PlainText("[ti:x]\n[00:01.00]one\n\n[00:02.00]two"); got != "one\ntwo" {
		t.Errorf("plain %q", got)
	}
}
