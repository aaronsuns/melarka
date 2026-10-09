package db

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOpenMigratesAndIsIdempotent(t *testing.T) {
	ctx := context.Background()
	p := filepath.Join(t.TempDir(), "lark.db")
	d, err := Open(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	var mode string
	if err := d.QueryRow("PRAGMA journal_mode").Scan(&mode); err != nil || mode != "wal" {
		t.Fatalf("mode=%q err=%v", mode, err)
	}
	var fk int
	d.QueryRow("PRAGMA foreign_keys").Scan(&fk)
	if fk != 1 {
		t.Fatal("foreign_keys off")
	}
	d.Close()
	d, err = Open(ctx, p) // second open must not re-run any migration
	if err != nil {
		t.Fatal(err)
	}
	var n int
	d.QueryRow("SELECT COUNT(*) FROM schema_migrations").Scan(&n)
	if n != 24 {
		t.Fatalf("migrations=%d", n)
	}
}

func TestDownloadThumbnailBackfill(t *testing.T) {
	d, err := Open(context.Background(), filepath.Join(t.TempDir(), "lark.db")) // not testutil.DB: it imports this package
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	d.Exec(`INSERT INTO users(id,username,password_hash,role,created_at) VALUES (1,'a','h','member',0)`)
	d.Exec(`INSERT INTO downloads(user_id,url,video_id,thumbnail,status,created_at,updated_at) VALUES
		(1,'u','abcdefghijk','https://i.ytimg.com/vi/abcdefghijk/maxresdefault.jpg','done',0,0),
		(1,'u','','','failed',0,0)`)
	sqlText, err := os.ReadFile("migrations/0009_download_thumbnails.sql") // package dir is the test's cwd
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(string(sqlText)); err != nil { // idempotent: safe to run again here
		t.Fatal(err)
	}
	var a, b string
	d.QueryRow(`SELECT thumbnail FROM downloads WHERE video_id='abcdefghijk'`).Scan(&a)
	d.QueryRow(`SELECT thumbnail FROM downloads WHERE video_id=''`).Scan(&b)
	if a != "https://i.ytimg.com/vi/abcdefghijk/hqdefault.jpg" || b != "" {
		t.Fatalf("backfill: %q %q", a, b)
	}
}

// 0023 rebuilds previews: its INSERT … SELECT must carry every column of 0020.
func TestPreviewHDMigrationKeepsEveryColumn(t *testing.T) {
	b, err := os.ReadFile("migrations/0023_preview_hd.sql")
	if err != nil {
		t.Fatal(err)
	}
	sqlText := string(b)
	i := strings.Index(sqlText, "INSERT INTO previews_new(")
	if i < 0 {
		t.Fatal("no INSERT INTO previews_new")
	}
	ins := sqlText[i : i+strings.Index(sqlText[i:], "FROM previews;")]
	for _, col := range []string{"id", "video_id", "media", "status", "path", "size", "total", "error", "title", "channel", "channel_id", "duration_s", "user_id", "created_at", "accessed_at"} {
		if !strings.Contains(ins, ","+col+",") && !strings.Contains(ins, "("+col+",") && !strings.Contains(ins, ","+col+")") &&
			!strings.Contains(ins, "SELECT "+col+",") && !strings.Contains(ins, ","+col+" FROM") {
			t.Errorf("0023 does not copy column %q", col)
		}
	}
}

// 0023 rebuilds previews; ids of deleted rows must not come back (AUTOINCREMENT).
func TestPreviewHDMigrationKeepsTheIDSequence(t *testing.T) {
	d, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "x.db")+"?_pragma=foreign_keys(ON)")
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	d.SetMaxOpenConns(1)
	run := func(file string) {
		b, err := os.ReadFile("migrations/" + file)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := d.Exec(string(b)); err != nil {
			t.Fatalf("%s: %v", file, err)
		}
	}
	if _, err := d.Exec(`CREATE TABLE users (id INTEGER PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	run("0020_previews.sql")
	for i := range 3 {
		if _, err := d.Exec(`INSERT INTO previews(video_id,media,status,created_at,accessed_at) VALUES (?, 'audio','done',0,0)`, fmt.Sprintf("v%d", i)); err != nil {
			t.Fatal(err)
		}
	}
	d.Exec(`DELETE FROM previews WHERE id=3`)
	run("0023_preview_hd.sql")
	r, err := d.Exec(`INSERT INTO previews(video_id,media,status,created_at,accessed_at) VALUES ('w','hd','done',0,0)`)
	if err != nil {
		t.Fatal(err)
	}
	if id, _ := r.LastInsertId(); id <= 3 {
		t.Fatalf("new id %d reuses one that was given out", id)
	}
}
