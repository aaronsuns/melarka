package ytdlp

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"
)

// fakeRunner records the argv it was called with and returns/streams
// whatever the test configured, so Client's argv-building and JSON/line
// parsing can be tested without a real yt-dlp binary.
type fakeRunner struct {
	gotArgs []string
	calls   [][]string

	output    []byte
	outputErr error
	// outFor, when set, answers Output instead of output/outputErr.
	outFor func(args []string) ([]byte, error)

	lines     []string
	streamErr error
}

func (f *fakeRunner) Output(ctx context.Context, args []string) ([]byte, error) {
	f.gotArgs = args
	f.calls = append(f.calls, args)
	if f.outFor != nil {
		return f.outFor(args)
	}
	return f.output, f.outputErr
}

func (f *fakeRunner) Stream(ctx context.Context, args []string, onLine func(string)) error {
	f.gotArgs = args
	for _, l := range f.lines {
		if onLine != nil {
			onLine(l)
		}
	}
	return f.streamErr
}

func readTestdata(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read testdata %s: %v", name, err)
	}
	return b
}

func TestClientSearch(t *testing.T) {
	fr := &fakeRunner{output: readTestdata(t, "search.json")}
	c := &Client{Runner: fr}

	videos, err := c.Search(context.Background(), "teresa teng")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}

	wantArgs := []string{"--js-runtimes", "node", "--flat-playlist", "-J", "--no-warnings", "ytsearch10:teresa teng"}
	if !reflect.DeepEqual(fr.gotArgs, wantArgs) {
		t.Errorf("argv = %v, want %v", fr.gotArgs, wantArgs)
	}

	if len(videos) != 3 {
		t.Fatalf("got %d videos, want 3", len(videos))
	}
	v0 := videos[0]
	if v0.ID != "x1_0000000x" || v0.Title != "邓丽君 - 甜蜜蜜" || v0.Channel != "Teresa Teng" ||
		v0.URL != "https://www.youtube.com/watch?v=x1_0000000x" || v0.DurationS != 215 ||
		v0.Thumbnail != "https://i.ytimg.com/vi/x1_0000000x/hq.jpg" {
		t.Errorf("videos[0] = %+v", v0)
	}
	vb := videos[1] // channel falls back to uploader; thumbnail falls back to the single "thumbnail" field
	if vb.Channel != "Alan Walker" || vb.Thumbnail != "https://i.ytimg.com/vi/x2_0000000x/default.jpg" {
		t.Errorf("videos[1] = %+v", vb)
	}
}

func TestClientSearch_BadQuery(t *testing.T) {
	fr := &fakeRunner{}
	c := &Client{Runner: fr}
	if _, err := c.Search(context.Background(), "   "); !errors.Is(err, ErrBadQuery) {
		t.Errorf("err = %v, want ErrBadQuery", err)
	}
	if fr.gotArgs != nil {
		t.Errorf("runner should not have been called, got args %v", fr.gotArgs)
	}
}

func TestClientResolve_Playlist(t *testing.T) {
	fr := &fakeRunner{output: readTestdata(t, "playlist.json")}
	c := &Client{Runner: fr}

	videos, err := c.Resolve(context.Background(), "https://www.youtube.com/playlist?list=PL1", 10)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	wantArgs := []string{"--js-runtimes", "node", "--flat-playlist", "-J", "--no-warnings", "--playlist-end", "10", "https://www.youtube.com/playlist?list=PL1"}
	if !reflect.DeepEqual(fr.gotArgs, wantArgs) {
		t.Errorf("argv = %v, want %v", fr.gotArgs, wantArgs)
	}

	if len(videos) != 2 {
		t.Fatalf("got %d videos, want 2 (the [Private video] entry must be skipped)", len(videos))
	}
	if videos[0].ID != "p1_0000000x" || videos[1].ID != "p3_0000000x" {
		t.Errorf("videos = %+v", videos)
	}
	if videos[1].Channel != "Artist C" { // uploader fallback
		t.Errorf("videos[1].Channel = %q, want Artist C", videos[1].Channel)
	}
}

func TestClientResolve_SingleVideo(t *testing.T) {
	fr := &fakeRunner{output: readTestdata(t, "video.json")}
	c := &Client{Runner: fr}

	videos, err := c.Resolve(context.Background(), "https://www.youtube.com/watch?v=v1_0000000x", 5)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(videos) != 1 {
		t.Fatalf("got %d videos, want 1", len(videos))
	}
	v := videos[0]
	if v.ID != "v1_0000000x" || v.Title != "Solo Video Title" || v.Channel != "Solo Channel" ||
		v.DurationS != 305 || v.Thumbnail != "https://i.ytimg.com/vi/v1_0000000x/hq.jpg" {
		t.Errorf("video = %+v", v)
	}
	// A fully-resolved single video's "url" is the direct (googlevideo)
	// media stream, not a watch page; "webpage_url" must win.
	if v.URL != "https://www.youtube.com/watch?v=v1_0000000x" {
		t.Errorf("video.URL = %q, want the webpage_url, not the googlevideo url", v.URL)
	}
}

func TestClientResolve_BadURL(t *testing.T) {
	fr := &fakeRunner{}
	c := &Client{Runner: fr}
	if _, err := c.Resolve(context.Background(), "https://evil.com/watch?v=abc", 5); !errors.Is(err, ErrBadURL) {
		t.Errorf("err = %v, want ErrBadURL", err)
	}
	if fr.gotArgs != nil {
		t.Errorf("runner should not have been called, got args %v", fr.gotArgs)
	}
}

func TestClientResolve_CapsAtMax(t *testing.T) {
	json := []byte(`{"_type":"playlist","entries":[
		{"id":"a_00000000x","title":"A","channel":"C","url":"https://www.youtube.com/watch?v=a_00000000x"},
		{"id":"b_00000000x","title":"B","channel":"C","url":"https://www.youtube.com/watch?v=b_00000000x"},
		{"id":"c_00000000x","title":"C","channel":"C","url":"https://www.youtube.com/watch?v=c_00000000x"},
		{"id":"d_00000000x","title":"D","channel":"C","url":"https://www.youtube.com/watch?v=d_00000000x"}
	]}`)
	fr := &fakeRunner{output: json}
	c := &Client{Runner: fr}
	videos, err := c.Resolve(context.Background(), "https://www.youtube.com/playlist?list=PL2", 2)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(videos) != 2 {
		t.Errorf("got %d videos, want 2 (capped at max)", len(videos))
	}
}

func TestClientDownload(t *testing.T) {
	fr := &fakeRunner{lines: []string{
		"[download] Destination: /x.webm",
		"LARKPROG   10.0%",
		"LARKPROG 100.0%",
		"/music/youtube/Chan/Song [x1_0000000x].m4a",
	}}
	c := &Client{Runner: fr}

	var progress []float64
	v := Video{ID: "x1_0000000x", URL: "https://www.youtube.com/watch?v=x1_0000000x"}
	finalPath, err := c.Download(context.Background(), v, "/music/youtube/Chan/Song", func(pct float64) {
		progress = append(progress, pct)
	})
	if err != nil {
		t.Fatalf("Download: %v", err)
	}

	wantArgs := []string{
		"--js-runtimes", "node",
		"--newline", "--no-playlist", "--max-downloads", "1", "--no-overwrites", "--no-warnings", "--progress",
		"-f", "bestaudio[ext=m4a]/bestaudio/best",
		"-x", "--audio-format", "m4a",
		"--embed-thumbnail", "--convert-thumbnails", "jpg",
		"--embed-metadata",
		"--progress-template", "download:LARKPROG %(progress._percent_str)s",
		"--print", "after_move:filepath",
		"-o", "/music/youtube/Chan/Song.%(ext)s",
		"--", "https://www.youtube.com/watch?v=x1_0000000x",
	}
	if !reflect.DeepEqual(fr.gotArgs, wantArgs) {
		t.Errorf("argv = %#v, want %#v", fr.gotArgs, wantArgs)
	}

	if !reflect.DeepEqual(progress, []float64{10.0, 100.0}) {
		t.Errorf("progress = %v, want [10 100]", progress)
	}
	if finalPath != "/music/youtube/Chan/Song [x1_0000000x].m4a" {
		t.Errorf("finalPath = %q", finalPath)
	}
}

func TestClientDownload_ToleratesNAProgress(t *testing.T) {
	fr := &fakeRunner{lines: []string{
		"LARKPROG  NA%",
		"/out/path.m4a",
	}}
	c := &Client{Runner: fr}
	var calls int
	finalPath, err := c.Download(context.Background(), Video{URL: "https://www.youtube.com/watch?v=x1_0000000x"}, "/out/path",
		func(pct float64) { calls++ })
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if calls != 0 {
		t.Errorf("onProgress called %d times for a NA line, want 0", calls)
	}
	if finalPath != "/out/path.m4a" {
		t.Errorf("finalPath = %q", finalPath)
	}
}

func TestClientDownload_StreamError(t *testing.T) {
	fr := &fakeRunner{streamErr: errors.New("yt-dlp: ERROR: Video unavailable")}
	c := &Client{Runner: fr}
	_, err := c.Download(context.Background(), Video{URL: "https://www.youtube.com/watch?v=x1_0000000x"}, "/out/path", nil)
	if err == nil || !strings.Contains(err.Error(), "Video unavailable") {
		t.Errorf("err = %v, want it to contain %q", err, "Video unavailable")
	}
}

func TestClientDownload_ValidatesURL(t *testing.T) {
	fr := &fakeRunner{}
	c := &Client{Runner: fr}
	_, err := c.Download(context.Background(), Video{URL: "https://evil.com/watch?v=abc"}, "/out/path", nil)
	if !errors.Is(err, ErrBadURL) {
		t.Errorf("err = %v, want ErrBadURL", err)
	}
	if fr.gotArgs != nil {
		t.Errorf("runner should not have been called, got args %v", fr.gotArgs)
	}
}

func TestClientVersion(t *testing.T) {
	fr := &fakeRunner{output: []byte("2024.01.01\n")}
	c := &Client{Runner: fr}
	v, err := c.Version(context.Background())
	if err != nil {
		t.Fatalf("Version: %v", err)
	}
	if v != "2024.01.01" {
		t.Errorf("Version = %q", v)
	}
	if !reflect.DeepEqual(fr.gotArgs, []string{"--version"}) {
		t.Errorf("argv = %v, want [--version]", fr.gotArgs)
	}
}

// --- ExecRunner: exercised against a tiny shell script, no real yt-dlp. ---

func writeScript(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fake-yt-dlp.sh")
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatalf("write script: %v", err)
	}
	return path
}

// TestKillProcessGroup_MapsESRCHToErrProcessDone pins the Cmd.Cancel
// contract: os/exec only suppresses Cancel's error (letting Wait report the
// command's real, successful exit) when that error is exactly
// os.ErrProcessDone. A raw ESRCH from killing an already-gone process group
// — the ordinary case where yt-dlp finished right as ctx was cancelled —
// would otherwise surface as a spurious Wait error and make Download throw
// away a valid finalPath.
func TestKillProcessGroup_MapsESRCHToErrProcessDone(t *testing.T) {
	script := writeScript(t, "#!/bin/sh\nexit 0\n")
	cmd := exec.Command(script)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Run(); err != nil {
		t.Fatalf("run: %v", err)
	}
	pid := cmd.Process.Pid // already exited and reaped by Run's Wait

	err := killProcessGroup(pid)
	if !errors.Is(err, os.ErrProcessDone) {
		t.Errorf("killProcessGroup(reaped pid) = %v, want os.ErrProcessDone", err)
	}
}

func TestExecRunner_Stream_StreamsStdoutAndKeepsStderr(t *testing.T) {
	script := writeScript(t, "#!/bin/sh\necho line1\necho line2\necho warn 1>&2\nexit 0\n")
	r := ExecRunner{Bin: func() string { return script }}

	var lines []string
	err := r.Stream(context.Background(), nil, func(l string) { lines = append(lines, l) })
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if !reflect.DeepEqual(lines, []string{"line1", "line2"}) {
		t.Errorf("lines = %v, want [line1 line2]", lines)
	}
}

func TestExecRunner_Output_IncludesStderrOnFailure(t *testing.T) {
	script := writeScript(t, "#!/bin/sh\necho ok\necho 'ERROR: boom' 1>&2\nexit 1\n")
	r := ExecRunner{Bin: func() string { return script }}

	_, err := r.Output(context.Background(), nil)
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Errorf("err = %v, want it to contain %q", err, "boom")
	}
}

// grandchildSleepScript deliberately does NOT `exec sleep 30`: it forks
// sleep as a real child of the shell (same process group, different pid),
// mirroring yt-dlp's PyInstaller build which forks its own children rather
// than exec'ing into them. Killing only cmd.Process (the shell) would leave
// that child running and Wait blocked on it; this is what Setpgid+Cancel
// (killing the whole -pgid) has to handle.
const grandchildSleepScript = "#!/bin/sh\nsleep 30\n"

func TestExecRunner_Stream_KilledOnContextCancel(t *testing.T) {
	script := writeScript(t, grandchildSleepScript)
	r := ExecRunner{Bin: func() string { return script }}

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	err := r.Stream(ctx, nil, nil)
	elapsed := time.Since(start)

	if err == nil {
		t.Error("Stream: expected an error when the process is killed by ctx cancellation")
	}
	if elapsed > 2*time.Second {
		t.Errorf("Stream took %v to return after ctx cancel, want ~2s even with a grandchild `sleep 30` still running", elapsed)
	}
}

func TestExecRunner_Output_KilledOnContextCancel(t *testing.T) {
	script := writeScript(t, grandchildSleepScript)
	r := ExecRunner{Bin: func() string { return script }}

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	_, err := r.Output(ctx, nil)
	elapsed := time.Since(start)

	if err == nil {
		t.Error("Output: expected an error when the process is killed by ctx cancellation")
	}
	if elapsed > 2*time.Second {
		t.Errorf("Output took %v to return after ctx cancel, want ~2s even with a grandchild `sleep 30` still running", elapsed)
	}
}

func TestExecRunner_Stream_DrainsStdoutAfterScanError(t *testing.T) {
	// A single "line" (no newline) bigger than the scanner's 1 MiB buffer
	// forces bufio.ErrTooLong. Once that stops us reading, the script is
	// still trying to write "done\n": if Stream doesn't drain the rest of
	// stdout before Wait, that write blocks on the full pipe and Wait hangs
	// forever. The ctx timeout is a safety net, not the expected path.
	script := writeScript(t, "#!/bin/sh\ndd if=/dev/zero bs=65536 count=40 2>/dev/null\necho done\nexit 0\n")
	r := ExecRunner{Bin: func() string { return script }}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	start := time.Now()
	err := r.Stream(ctx, nil, nil)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("Stream: expected a scanner error (an oversized line with no newline)")
	}
	if elapsed > 5*time.Second {
		t.Errorf("Stream took %v; want it to drain stdout and return quickly instead of blocking on Wait", elapsed)
	}
}

func TestExecRunner_BinResolvedEachCall(t *testing.T) {
	script1 := writeScript(t, "#!/bin/sh\necho one\n")
	script2 := writeScript(t, "#!/bin/sh\necho two\n")
	calls := 0
	r := ExecRunner{Bin: func() string {
		calls++
		if calls == 1 {
			return script1
		}
		return script2
	}}
	out1, err := r.Output(context.Background(), nil)
	if err != nil {
		t.Fatalf("Output 1: %v", err)
	}
	out2, err := r.Output(context.Background(), nil)
	if err != nil {
		t.Fatalf("Output 2: %v", err)
	}
	if strings.TrimSpace(string(out1)) != "one" || strings.TrimSpace(string(out2)) != "two" {
		t.Errorf("out1=%q out2=%q, want one/two (Bin() should be called fresh each time)", out1, out2)
	}
}

func TestClientSearchMore(t *testing.T) {
	entries := func(n int) []byte {
		var b strings.Builder
		b.WriteString(`{"_type":"playlist","entries":[`)
		for i := range n {
			if i > 0 {
				b.WriteString(",")
			}
			fmt.Fprintf(&b, `{"id":"m%010d","title":"T%d","channel":"C","duration":60}`, i, i)
		}
		b.WriteString("]}")
		return []byte(b.String())
	}
	for _, c := range []struct {
		n, got   int
		wantArg  string
		wantMore bool
	}{
		{n: 20, got: 20, wantArg: "ytsearch20:q", wantMore: true},  // a full page: there may be more
		{n: 20, got: 13, wantArg: "ytsearch20:q", wantMore: false}, // YouTube ran out
		{n: 50, got: 50, wantArg: "ytsearch50:q", wantMore: false}, // the cap: never more
		{n: 500, got: 50, wantArg: "ytsearch50:q", wantMore: false},
		{n: 0, got: 1, wantArg: "ytsearch1:q", wantMore: true},
	} {
		fr := &fakeRunner{output: entries(c.got)}
		videos, more, err := (&Client{Runner: fr}).SearchMore(context.Background(), " q ", c.n)
		if err != nil {
			t.Fatal(err)
		}
		want := []string{"--js-runtimes", "node", "--flat-playlist", "-J", "--no-warnings", c.wantArg}
		if !reflect.DeepEqual(fr.gotArgs, want) {
			t.Errorf("n=%d: argv = %v, want %v", c.n, fr.gotArgs, want)
		}
		if len(videos) != c.got || more != c.wantMore {
			t.Errorf("n=%d got=%d: %d videos, more=%v; want more=%v", c.n, c.got, len(videos), more, c.wantMore)
		}
	}
	if _, _, err := (&Client{Runner: &fakeRunner{}}).SearchMore(context.Background(), "", 20); !errors.Is(err, ErrBadQuery) {
		t.Errorf("empty query: %v", err)
	}
}
