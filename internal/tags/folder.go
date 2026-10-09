package tags

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"strings"

	"github.com/aaronsuns/lark-server/internal/config"
)

// FolderTagger tags tracks from the names of the folders they live in.
type FolderTagger struct {
	Store *Store
	Rules []config.FolderRule
	Vocab *Vocab
}

// ValidateRules checks that every rule tag is a vocabulary slug.
func ValidateRules(rules []config.FolderRule, v *Vocab) error {
	for i, r := range rules {
		if len(r.Match) == 0 {
			return fmt.Errorf("folder rule %d: no match strings", i+1)
		}
		for _, t := range r.Tags {
			if _, ok := v.bySlug[t]; !ok {
				return fmt.Errorf("folder rule %d: %q is not a vocabulary slug", i+1, t)
			}
		}
	}
	return nil
}

// TagsFor matches the folder part of rel only (never the file name).
func (f *FolderTagger) TagsFor(rel string) []Tag {
	dir := path.Dir(rel)
	if dir == "." {
		return nil
	}
	dir = strings.ToLower(dir)
	seen := map[string]bool{}
	var out []Tag
	for _, r := range f.Rules {
		hit := false
		for _, m := range r.Match {
			if m != "" && strings.Contains(dir, strings.ToLower(m)) {
				hit = true
				break
			}
		}
		if !hit {
			continue
		}
		for _, slug := range r.Tags {
			e, ok := f.Vocab.Lookup(slug)
			if !ok || seen[e.Slug] {
				continue
			}
			seen[e.Slug] = true
			out = append(out, Tag{Name: e.Slug, Kind: e.Kind})
		}
	}
	return out
}

func (f *FolderTagger) Apply(ctx context.Context, trackID int64, rel string) error {
	return f.Store.Replace(ctx, trackID, "folder_rule", f.TagsFor(rel))
}

// RulesHash identifies a rule set, so Backfill runs once per distinct set.
func (f *FolderTagger) RulesHash() string {
	b, _ := json.Marshal(f.Rules)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// Backfill applies the rules to every existing non-trashed track, unless that
// exact rule set was already applied. Returns the number of tracks processed.
func (f *FolderTagger) Backfill(ctx context.Context) (int, error) {
	hash := f.RulesHash()
	var cur string
	err := f.Store.DB.QueryRowContext(ctx, `SELECT value FROM app_state WHERE key='folder_rules_hash'`).Scan(&cur)
	if err == nil && cur == hash {
		return 0, nil
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	}
	rows, err := f.Store.DB.QueryContext(ctx, `SELECT id FROM tracks WHERE status!='trashed' ORDER BY id`)
	if err != nil {
		return 0, err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, err
	}
	rows.Close()
	n := 0
	for _, id := range ids {
		// The startup scan runs concurrently: re-read each track's path just
		// before tagging it, so a track moved since the snapshot gets its new
		// folder's tags and one trashed meanwhile is skipped.
		var rel string
		err := f.Store.DB.QueryRowContext(ctx, `SELECT rel_path FROM tracks WHERE id=? AND status!='trashed'`, id).Scan(&rel)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return n, err
		}
		if err := f.Apply(ctx, id, rel); err != nil {
			return n, err
		}
		n++
	}
	_, err = f.Store.DB.ExecContext(ctx, `INSERT INTO app_state(key,value) VALUES ('folder_rules_hash',?)
		ON CONFLICT(key) DO UPDATE SET value=excluded.value`, hash)
	return n, err
}
