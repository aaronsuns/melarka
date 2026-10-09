package lyrics

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/aaronsuns/lark-server/internal/library"
	"github.com/aaronsuns/lark-server/internal/tags"
)

// Result is what the lyrics endpoint answers for a track.
type Result struct {
	Found bool `json:"found"`
	// ID is the shown candidate's id: "wrong lyrics" and the offset name it,
	// so they never act on lyrics someone else has replaced meanwhile.
	ID           int64  `json:"id,omitempty"`
	Instrumental bool   `json:"instrumental,omitempty"`
	Source       string `json:"source,omitempty"`
	Synced       bool   `json:"synced"`
	Lines        []Line `json:"lines,omitempty"`
	Text         string `json:"text,omitempty"`
	// OffsetMS shifts the synced lines for everyone: a line shows at
	// t_ms + OffsetMS. 0 for anything but the selected lyrics.
	OffsetMS int `json:"offset_ms"`
}

// MaxOffsetMS bounds a lyrics offset either way.
const MaxOffsetMS = 30000

// Stored is one stored candidate, as the admin picker lists it.
type Stored struct {
	ID       int64  `json:"id"`
	Source   string `json:"source"`
	Synced   bool   `json:"synced"`
	Selected bool   `json:"selected"`
	Preview  string `json:"preview"` // first three non-empty lines, plain
}

// ErrNotFound: the track doesn't exist (or is trashed/missing), or the
// candidate doesn't belong to it.
var ErrNotFound = errors.New("lyrics: not found")

// ErrNoLyrics: the track has no selected lyrics to shift or report.
var ErrNoLyrics = errors.New("lyrics: nothing selected")

// ErrLyricsChanged: the named lyrics are no longer the selected ones.
var ErrLyricsChanged = errors.New("lyrics: other lyrics are shown now")

// ErrForbidden: only the reporter (or an admin) may undo a report.
var ErrForbidden = errors.New("lyrics: not your report")

// Service looks lyrics up across providers and remembers the answers.
type Service struct {
	DB        *sql.DB
	Library   *library.Store
	Providers []Provider
	Log       *slog.Logger
	Budget    time.Duration // whole lookup; default 6s
	MissRetry time.Duration // clean miss remembered; default 7 * 24h
	FailRetry time.Duration // every provider errored: in-memory backoff (prefetch doubles it per consecutive failure, up to MissRetry); default 10m
	Now       func() time.Time

	mu     sync.Mutex
	calls  map[callKey]*lookupCall // one lookup per track (and override) at a time
	failed Backoff                 // tracks whose providers all failed lately
}

type callKey struct {
	track    int64
	o        string // "" without an override
	force    bool
	explicit bool
}

// Override replaces the searched title and/or artist (admin "Change lyrics"
// → Search). A nil field keeps SearchQueries(q)[0]'s value; Artist ""
// searches by title alone. Broad (admin "Broad search") matches loosely
// (MatchBroad), ranks with RankBroad and keeps up to BroadKeep candidates.
type Override struct {
	Title, Artist *string
	Broad         bool
}

// BroadKeep caps how many candidates one broad search stores.
const BroadKeep = 10

func (o Override) set() bool { return o.Title != nil || o.Artist != nil }

func (o Override) key() string {
	b := ""
	if o.Broad {
		b = "\x02"
	}
	if !o.set() {
		return b
	}
	v := func(p *string) string {
		if p == nil {
			return "\x01"
		}
		return *p
	}
	return b + v(o.Title) + "\x00" + v(o.Artist)
}

type lookupCall struct {
	done chan struct{}
	res  Result
	err  error
}

func (s *Service) budget() time.Duration    { return orDefault(s.Budget, 6*time.Second) }
func (s *Service) missRetry() time.Duration { return orDefault(s.MissRetry, 7*24*time.Hour) }
func (s *Service) failRetry() time.Duration { return orDefault(s.FailRetry, 10*time.Minute) }

func orDefault(d, def time.Duration) time.Duration {
	if d <= 0 {
		return def
	}
	return d
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Service) log() *slog.Logger {
	if s.Log != nil {
		return s.Log
	}
	return slog.Default()
}

// Get answers from what is stored — the selected lyrics, else "instrumental",
// else a remembered miss or a provider-outage backoff — and only otherwise
// looks the track up.
func (s *Service) Get(ctx context.Context, trackID int64) (Result, error) {
	return s.single(ctx, trackID, false, false, Override{})
}

// Lookup asks every provider in parallel (within the budget) regardless of
// what is stored, keeping an admin's pick. Tracks tagged instrumental are
// never looked up.
func (s *Service) Lookup(ctx context.Context, trackID int64) (Result, error) {
	return s.LookupWith(ctx, trackID, Override{})
}

// LookupWith is Lookup asking the providers with the override's title and/or
// artist instead of the cleaned ones (one query, no fallbacks). Lookup and
// LookupWith are explicit (an admin asked): they may replace an admin's "no
// lyrics" with something new they found.
func (s *Service) LookupWith(ctx context.Context, trackID int64, o Override) (Result, error) {
	return s.single(ctx, trackID, true, true, o)
}

// autoLookup is prefetch's Lookup: automatic, so it never stores anything
// over an admin's "no lyrics" (Get is automatic too).
func (s *Service) autoLookup(ctx context.Context, trackID int64) (Result, error) {
	return s.single(ctx, trackID, true, false, Override{})
}

// single runs at most one resolve per track at a time; concurrent callers
// share its answer. The resolve runs detached from any one caller's ctx (a
// caller hanging up must not abort the lookup the others wait for, nor
// leave its result unstored); the budget still bounds it.
func (s *Service) single(ctx context.Context, trackID int64, force, explicit bool, o Override) (Result, error) {
	s.mu.Lock()
	if s.calls == nil {
		s.calls = map[callKey]*lookupCall{}
	}
	k := callKey{trackID, o.key(), force, explicit}
	c, ok := s.calls[k]
	if !ok {
		c = &lookupCall{done: make(chan struct{})}
		s.calls[k] = c
		go func() {
			defer close(c.done)
			c.res, c.err = s.resolve(context.WithoutCancel(ctx), trackID, force, explicit, o)
			s.mu.Lock()
			delete(s.calls, k)
			s.mu.Unlock()
		}()
	}
	s.mu.Unlock()
	select {
	case <-c.done:
		return c.res, c.err
	case <-ctx.Done():
		return Result{}, ctx.Err()
	}
}

func (s *Service) resolve(ctx context.Context, trackID int64, force, explicit bool, o Override) (Result, error) {
	q, err := BuildQuery(ctx, s.DB, s.Library, trackID)
	if err != nil {
		return Result{}, err
	}
	if !force {
		if r, ok, err := s.selected(ctx, trackID); err != nil || ok {
			return r, err
		}
	}
	if inst, err := s.instrumental(ctx, trackID); err != nil {
		return Result{}, err
	} else if inst {
		return Result{Instrumental: true}, nil
	}
	if !force {
		var at int64
		var none bool // the admin said "no lyrics": only an explicit refresh looks again
		err := s.DB.QueryRowContext(ctx, `SELECT attempted_at, manual FROM lyrics_lookup WHERE track_id=? AND found=0`, trackID).Scan(&at, &none)
		switch {
		case err == nil && (none || s.now().Before(time.Unix(at, 0).Add(s.missRetry()))):
			return Result{}, nil
		case err != nil && !errors.Is(err, sql.ErrNoRows):
			return Result{}, err
		}
		if s.inBackoff(trackID) {
			return Result{}, nil
		}
	}
	qs := SearchQueries(q)
	if o.set() {
		v := qs[0]
		v.ChannelArtist = false
		if o.Title != nil {
			v.Title = strings.TrimSpace(*o.Title)
		}
		if o.Artist != nil {
			v.Artist = strings.TrimSpace(*o.Artist)
		}
		qs = []Query{v}
	}
	return s.lookup(ctx, q, qs, o.Broad, explicit)
}

func (s *Service) instrumental(ctx context.Context, trackID int64) (bool, error) {
	ts, err := (&tags.Store{DB: s.DB}).ForTrack(ctx, trackID)
	if err != nil {
		return false, err
	}
	for _, t := range ts {
		if strings.EqualFold(t.Name, "instrumental") {
			return true, nil
		}
	}
	return false, nil
}

type answer struct {
	provider string
	cands    []Candidate
	err      error
}

// lookup fans out to every provider under one budget. Answers come back on a
// channel read with a select on the budget's Done, so a provider that hangs
// (even one ignoring ctx) can't hold the result back. Matching candidates
// are stored and the best is selected (unless an admin's pick still
// exists). With no match, a lookup where some remote provider answered
// cleanly (or, with only local providers enabled, a local one did) is
// remembered as a miss; one where every remote provider errored or timed out
// is not — it only backs off in memory for FailRetry (longer for prefetch, see Backoff). Each provider is asked
// the queries qs in turn (searchOne), all within the one budget.
//
// broad: match with MatchBroad, rank with RankBroad against qs[0], and store
// at most BroadKeep candidates; the miss contract is the same.
func (s *Service) lookup(ctx context.Context, q Query, qs []Query, broad, explicit bool) (Result, error) {
	bctx, cancel := context.WithTimeout(ctx, s.budget())
	defer cancel()
	ch := make(chan answer, len(s.Providers)) // buffered: late answers never block
	order := make([]string, len(s.Providers))
	for i, p := range s.Providers {
		order[i] = p.Name()
		go func() {
			defer func() {
				if v := recover(); v != nil {
					ch <- answer{provider: p.Name(), err: fmt.Errorf("panic: %v", v)}
				}
			}()
			cs, err := searchOne(bctx, p, qs, broad)
			ch <- answer{provider: p.Name(), cands: cs, err: err}
		}()
	}

	// Only remote providers' clean answers count toward "nobody had it"; a
	// local (embedded) clean miss counts only when no remote one is enabled.
	remote, remoteAnswered, localAnswered := 0, 0, 0
	local := map[string]bool{}
	for _, p := range s.Providers {
		if isLocal(p) {
			local[p.Name()] = true
		} else {
			remote++
		}
	}
	var matched []Candidate
	seen := map[[2]string]bool{}
collect:
	for range s.Providers {
		select {
		case a := <-ch:
			if a.err != nil {
				s.log().Warn("lyrics provider failed", "provider", a.provider, "track", q.TrackID, "err", a.err)
				continue
			}
			if local[a.provider] {
				localAnswered++
			} else {
				remoteAnswered++
			}
			for _, c := range a.cands { // already matched by searchOne
				k := [2]string{c.Source, c.ExternalID}
				if seen[k] {
					continue
				}
				seen[k] = true
				matched = append(matched, c)
			}
		case <-bctx.Done():
			s.log().Warn("lyrics lookup budget exhausted", "track", q.TrackID, "budget", s.budget())
			break collect
		}
	}

	now := s.now()
	cleanMiss := remoteAnswered > 0 || (remote == 0 && localAnswered > 0)
	if len(matched) == 0 && !cleanMiss {
		s.backoff(q.TrackID)
		r, _, err := s.selected(ctx, q.TrackID)
		return r, err
	}
	keep := 0
	if broad {
		RankBroad(qs[0], matched, order)
		keep = BroadKeep
	} else {
		Rank(q, matched, order)
	}
	if err := s.store(ctx, q.TrackID, matched, keep, explicit, now); err != nil {
		return Result{}, err
	}
	s.failed.Clear(q.TrackID)
	r, _, err := s.selected(ctx, q.TrackID)
	return r, err
}

// searchOne asks p with each query in turn (a local provider once, it
// ignores titles) and returns the first query's matching candidates. With no
// match it errs if any query failed (a provider that answered
// one variant cleanly but errored on another hasn't said "nobody has it", so
// it mustn't feed a 7-day miss); a cancelled ctx stops it at once.
func searchOne(ctx context.Context, p Provider, qs []Query, broad bool) ([]Candidate, error) {
	if isLocal(p) {
		qs = qs[:1]
	}
	var firstErr error
	for _, v := range qs {
		cs, err := p.Search(ctx, v)
		if err != nil {
			if ctx.Err() != nil {
				return nil, err
			}
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if m := matching(v, p.Name(), cs, broad); len(m) > 0 {
			return m, nil
		}
	}
	return nil, firstErr
}

// matching keeps the non-empty candidates that match v (MatchBroad when
// broad), with Source defaulted and Synced re-derived.
func matching(v Query, provider string, cs []Candidate, broad bool) []Candidate {
	match := Match
	if broad {
		match = MatchBroad
	}
	var out []Candidate
	for _, c := range cs {
		if c.Source == "" {
			c.Source = provider
		}
		if strings.TrimSpace(PlainText(c.Text)) == "" || !match(v, c) {
			continue
		}
		_, c.Synced = ParseLRC(c.Text)
		out = append(out, c)
	}
	return out
}

// store upserts the ranked matches (at most keep of them when keep > 0;
// candidates the admin deleted are skipped, see rejected) and settles the
// selection and the lyrics_lookup row in one transaction. An admin's pick is
// kept. An admin's "no lyrics" (manual, nothing selected) is kept unless an
// explicit lookup inserted a candidate that wasn't stored before: the best
// new one is then selected. The candidates the admin declined by marking
// "no lyrics" are never re-selected, and an automatic lookup (explicit
// false: prefetch, Get) never touches a "no lyrics" track at all.
func (s *Service) store(ctx context.Context, trackID int64, ranked []Candidate, keep int, explicit bool, now time.Time) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// Write first: a transaction that opens with a read and later writes can
	// fail with SQLITE_BUSY_SNAPSHOT under WAL, which busy_timeout doesn't
	// retry. This upsert takes the write lock (found/manual are kept and set
	// properly by the final upsert below).
	if _, err := tx.ExecContext(ctx, `INSERT INTO lyrics_lookup(track_id,attempted_at,found,manual) VALUES (?,?,0,0)
		ON CONFLICT(track_id) DO UPDATE SET attempted_at=excluded.attempted_at`, trackID, now.Unix()); err != nil {
		return err
	}
	var manual bool
	if err := tx.QueryRowContext(ctx, `SELECT manual FROM lyrics_lookup WHERE track_id=?`, trackID).Scan(&manual); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	var haveSelected bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM lyrics WHERE track_id=? AND selected=1)`, trackID).Scan(&haveSelected); err != nil {
		return err
	}
	none := manual && !haveSelected
	if none && !explicit {
		return tx.Commit() // only attempted_at moved
	}
	var best, bestNew int64
	stored := 0
	for _, c := range ranked {
		if keep > 0 && stored == keep {
			break
		}
		if rej, err := rejected(ctx, tx, trackID, c.Source, c.ExternalID, c.Text); err != nil {
			return err
		} else if rej {
			continue
		}
		var existed bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM lyrics WHERE track_id=? AND source=? AND external_id=?)`,
			trackID, c.Source, c.ExternalID).Scan(&existed); err != nil {
			return err
		}
		var id int64
		err := tx.QueryRowContext(ctx, `INSERT INTO lyrics(track_id,source,external_id,synced,text,duration_s,created_at) VALUES (?,?,?,?,?,?,?)
			ON CONFLICT(track_id,source,external_id) DO UPDATE SET synced=excluded.synced, text=excluded.text, duration_s=excluded.duration_s RETURNING id`,
			trackID, c.Source, c.ExternalID, c.Synced, c.Text, max(c.DurationS, 0), now.Unix()).Scan(&id)
		if err != nil {
			return err
		}
		if stored == 0 {
			best = id
		}
		if !existed && bestNew == 0 {
			bestNew = id
		}
		stored++
	}
	if none {
		best = bestNew // declined candidates never come back by themselves
	}
	keepManual := manual && (haveSelected || best == 0)
	if !keepManual && best != 0 {
		if err := selectRow(ctx, tx, trackID, best); err != nil {
			return err
		}
		haveSelected = true
	}
	// Lyrics selected again: a "wrong lyrics" flag (wrong_at) is settled.
	if _, err := tx.ExecContext(ctx, `INSERT INTO lyrics_lookup(track_id,attempted_at,found,manual) VALUES (?,?,?,?)
		ON CONFLICT(track_id) DO UPDATE SET attempted_at=excluded.attempted_at, found=excluded.found, manual=excluded.manual,
		wrong_at=CASE WHEN excluded.found THEN NULL ELSE wrong_at END`,
		trackID, now.Unix(), haveSelected, keepManual); err != nil {
		return err
	}
	return tx.Commit()
}

// textHash identifies lyrics by their words: sha256 of the plain text, so
// synced and plain copies (and other timestamps) agree.
func textHash(text string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(PlainText(text))))
	return hex.EncodeToString(sum[:])
}

// rejected: the admin deleted this candidate before — the same provider id
// (never for the file's own lyrics, which are matched by words only), or the
// same words from anywhere.
func rejected(ctx context.Context, tx *sql.Tx, trackID int64, source, externalID, text string) (bool, error) {
	var yes bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM lyrics_rejected WHERE track_id=?
		AND ((source=? AND external_id=?) OR text_hash=?))`, trackID, source, externalID, textHash(text)).Scan(&yes)
	return yes, err
}

// selected turns the track's selected row into a Result (ok=false: none).
func (s *Service) selected(ctx context.Context, trackID int64) (Result, bool, error) {
	var src, text string
	var synced bool
	var offset int
	var id int64
	err := s.DB.QueryRowContext(ctx, `SELECT id, source, synced, text, offset_ms FROM lyrics WHERE track_id=? AND selected=1 LIMIT 1`, trackID).Scan(&id, &src, &synced, &text, &offset)
	if errors.Is(err, sql.ErrNoRows) {
		return Result{}, false, nil
	}
	if err != nil {
		return Result{}, false, err
	}
	r := Result{Found: true, ID: id, Source: src, OffsetMS: offset}
	if synced {
		if lines, ok := ParseLRC(text); ok {
			r.Synced, r.Lines = true, lines
			return r, true, nil
		}
	}
	r.Text = PlainText(text)
	return r, true, nil
}

// Candidates lists every stored candidate of a visible track, the selected
// one first.
func (s *Service) Candidates(ctx context.Context, trackID int64) ([]Stored, error) {
	if _, err := BuildQuery(ctx, s.DB, s.Library, trackID); err != nil {
		return nil, err
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT id, source, synced, selected, text FROM lyrics WHERE track_id=?
		ORDER BY selected DESC, synced DESC, id`, trackID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Stored{}
	for rows.Next() {
		var st Stored
		var text string
		if err := rows.Scan(&st.ID, &st.Source, &st.Synced, &st.Selected, &text); err != nil {
			return nil, err
		}
		lines := strings.SplitN(PlainText(text), "\n", 4)
		st.Preview = strings.Join(lines[:min(3, len(lines))], "\n")
		out = append(out, st)
	}
	return out, rows.Err()
}

// Select makes candidateID the track's lyrics and marks the choice manual,
// so later lookups keep it while the row exists.
func (s *Service) Select(ctx context.Context, trackID, candidateID int64) error {
	if _, err := BuildQuery(ctx, s.DB, s.Library, trackID); err != nil {
		return err
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// Write first (see store): the existence check rides on the UPDATE.
	res, err := tx.ExecContext(ctx, `UPDATE lyrics SET `+selectSet+` WHERE track_id=?2
		AND EXISTS (SELECT 1 FROM lyrics WHERE id=?1 AND track_id=?2)`, candidateID, trackID)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil {
		return err
	} else if n == 0 {
		return ErrNotFound
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO lyrics_lookup(track_id,attempted_at,found,manual) VALUES (?,?,1,1)
		ON CONFLICT(track_id) DO UPDATE SET attempted_at=excluded.attempted_at, found=1, manual=1, wrong_at=NULL`, trackID, s.now().Unix()); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	s.failed.Clear(trackID)
	return nil
}

// DeleteCandidate removes one of a track's stored lyrics for good: later
// lookups never store it again, by provider id or by its words
// (lyrics_rejected, see rejected; an admin can lift it, see Unreject). If it
// was the selected one, the next best left (see nextBest) is selected. When
// no candidate is left the track has no lyrics, recorded like MarkNone so
// automatic lookups don't look again; an explicit refresh still does.
func (s *Service) DeleteCandidate(ctx context.Context, trackID, candidateID int64) error {
	q, err := BuildQuery(ctx, s.DB, s.Library, trackID)
	if err != nil {
		return err
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := s.now().Unix()
	// Write first (see store).
	var selected bool
	r := storedRow{id: candidateID}
	err = tx.QueryRowContext(ctx, `DELETE FROM lyrics WHERE id=? AND track_id=? RETURNING selected, `+rowCols,
		candidateID, trackID).Scan(&selected, &r.source, &r.externalID, &r.text, &r.synced, &r.durationS, &r.offsetMS)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if _, err := reject(ctx, tx, trackID, r, now, sql.NullInt64{}, 0); err != nil {
		return err
	}
	rest, err := remaining(ctx, tx, trackID)
	if err != nil {
		return err
	}
	switch {
	case len(rest) == 0:
		if err := markNone(ctx, tx, trackID, now); err != nil {
			return err
		}
	case selected:
		if err := selectRow(ctx, tx, trackID, nextBest(q.DurationS, rest).id); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO lyrics_lookup(track_id,attempted_at,found,manual) VALUES (?,?,1,0)
			ON CONFLICT(track_id) DO UPDATE SET found=1, manual=0`, trackID, now); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ReportWrong is anyone's "wrong lyrics" for the shown lyrics lyricsID
// (ErrLyricsChanged when others are shown now): they are rejected by their
// words (every stored copy of those words goes, from any source; see
// rejected), recorded as one report by userID, and the next best candidate
// left is selected. With none left nothing is selected and the track is
// flagged (wrong_at) for the lyrics agent's missing list; found=0 like a
// miss, so automatic lookups wait as usual, and they can never store the
// rejected words again. Answers the new lyrics (or found:false) and the
// report's id, which UndoWrong takes.
func (s *Service) ReportWrong(ctx context.Context, trackID, lyricsID, userID int64) (Result, int64, error) {
	q, err := BuildQuery(ctx, s.DB, s.Library, trackID)
	if err != nil {
		return Result{}, 0, err
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return Result{}, 0, err
	}
	defer tx.Rollback()
	now := s.now().Unix()
	// Write first (see store); the version check rides on the DELETE.
	shown := storedRow{id: lyricsID}
	err = tx.QueryRowContext(ctx, `DELETE FROM lyrics WHERE track_id=? AND selected=1 AND id=? RETURNING `+rowCols,
		trackID, lyricsID).Scan(&shown.source, &shown.externalID, &shown.text, &shown.synced, &shown.durationS, &shown.offsetMS)
	if errors.Is(err, sql.ErrNoRows) {
		var any bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM lyrics WHERE track_id=? AND selected=1)`, trackID).Scan(&any); err != nil {
			return Result{}, 0, err
		}
		if any {
			return Result{}, 0, ErrLyricsChanged
		}
		return Result{}, 0, ErrNoLyrics
	}
	if err != nil {
		return Result{}, 0, err
	}
	by := sql.NullInt64{Int64: userID, Valid: userID > 0}
	report, err := reject(ctx, tx, trackID, shown, now, by, 0)
	if err != nil {
		return Result{}, 0, err
	}
	all, err := remaining(ctx, tx, trackID)
	if err != nil {
		return Result{}, 0, err
	}
	var rest []storedRow
	words := textHash(shown.text)
	for _, r := range all {
		if textHash(r.text) != words {
			rest = append(rest, r)
			continue
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM lyrics WHERE id=?`, r.id); err != nil {
			return Result{}, 0, err
		}
		if _, err := reject(ctx, tx, trackID, r, now, by, report); err != nil {
			return Result{}, 0, err
		}
	}
	if len(rest) > 0 {
		if err := selectRow(ctx, tx, trackID, nextBest(q.DurationS, rest).id); err != nil {
			return Result{}, 0, err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO lyrics_lookup(track_id,attempted_at,found,manual) VALUES (?,?,1,0)
			ON CONFLICT(track_id) DO UPDATE SET found=1, manual=0`, trackID, now)
	} else {
		_, err = tx.ExecContext(ctx, `INSERT INTO lyrics_lookup(track_id,attempted_at,found,manual,wrong_at) VALUES (?1,?2,0,0,?2)
			ON CONFLICT(track_id) DO UPDATE SET attempted_at=?2, found=0, manual=0, wrong_at=?2`, trackID, now)
	}
	if err != nil {
		return Result{}, 0, err
	}
	if err := tx.Commit(); err != nil {
		return Result{}, 0, err
	}
	s.failed.Clear(trackID)
	r, _, err := s.selected(ctx, trackID)
	return r, report, err
}

// UndoWrong takes a report back (the reporter, or an admin): its tombstones
// go, the words are stored again and the reported lyrics are selected with
// the offset they had. ErrNotFound for an unknown (or already undone) report.
func (s *Service) UndoWrong(ctx context.Context, trackID, reportID, userID int64, admin bool) (Result, error) {
	if _, err := BuildQuery(ctx, s.DB, s.Library, trackID); err != nil {
		return Result{}, err
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return Result{}, err
	}
	defer tx.Rollback()
	// Write first (see store); the owner check follows (and rolls back).
	rows, err := tx.QueryContext(ctx, `DELETE FROM lyrics_rejected WHERE track_id=? AND report_id=?
		RETURNING id, COALESCE(reported_by,0), source, COALESCE(lyrics_external_id,''), text, synced, duration_s, offset_ms`, trackID, reportID)
	if err != nil {
		return Result{}, err
	}
	type tomb struct {
		storedRow
		tid, by int64
		known   bool
	}
	var ts []tomb
	for rows.Next() {
		var t tomb
		var text sql.NullString
		if err := rows.Scan(&t.tid, &t.by, &t.source, &t.externalID, &text, &t.synced, &t.durationS, &t.offsetMS); err != nil {
			rows.Close()
			return Result{}, err
		}
		t.text, t.known = text.String, text.Valid
		ts = append(ts, t)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return Result{}, err
	}
	if len(ts) == 0 || ts[0].by == 0 {
		return Result{}, ErrNotFound // not a report (an admin's delete is lifted with Unreject)
	}
	if ts[0].by != userID && !admin {
		return Result{}, ErrForbidden
	}
	now := s.now().Unix()
	var shown int64
	for _, t := range ts {
		if !t.known {
			continue
		}
		id, err := restoreRow(ctx, tx, trackID, t.storedRow, now)
		if err != nil {
			return Result{}, err
		}
		if t.tid == reportID {
			shown = id
			if err := selectRow(ctx, tx, trackID, id); err != nil {
				return Result{}, err
			}
			if _, err := tx.ExecContext(ctx, `UPDATE lyrics SET offset_ms=? WHERE id=?`, t.offsetMS, id); err != nil {
				return Result{}, err
			}
		}
	}
	if shown != 0 {
		if _, err := tx.ExecContext(ctx, `INSERT INTO lyrics_lookup(track_id,attempted_at,found,manual) VALUES (?,?,1,0)
			ON CONFLICT(track_id) DO UPDATE SET found=1, manual=0, wrong_at=NULL`, trackID, now); err != nil {
			return Result{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return Result{}, err
	}
	r, _, err := s.selected(ctx, trackID)
	return r, err
}

// Rejected is one rejection, as the admin's recovery list shows it.
type Rejected struct {
	ID         int64   `json:"id"`
	Source     string  `json:"source"`
	Preview    string  `json:"preview"`     // "" for rejections older than previews
	ReportedBy *string `json:"reported_by"` // "wrong lyrics" reporter; nil for an admin's delete (or a deleted user)
	RejectedAt int64   `json:"rejected_at"`
	Restorable bool    `json:"restorable"` // the words are kept, so lifting it stores them again
}

// Rejected lists a visible track's rejections, newest first.
func (s *Service) Rejected(ctx context.Context, trackID int64) ([]Rejected, error) {
	if _, err := BuildQuery(ctx, s.DB, s.Library, trackID); err != nil {
		return nil, err
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT r.id, r.source, r.preview, u.username, r.rejected_at, r.text IS NOT NULL
		FROM lyrics_rejected r LEFT JOIN users u ON u.id=r.reported_by WHERE r.track_id=? ORDER BY r.rejected_at DESC, r.id`, trackID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Rejected{}
	for rows.Next() {
		var x Rejected
		var by sql.NullString
		if err := rows.Scan(&x.ID, &x.Source, &x.Preview, &by, &x.RejectedAt, &x.Restorable); err != nil {
			return nil, err
		}
		if by.Valid {
			x.ReportedBy = &by.String
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

// Unreject lifts rejection rid (and every other rejection of the same words
// on the track, which would block them just the same): words that were kept
// come back as candidates, not selected, and later lookups may store them
// again.
func (s *Service) Unreject(ctx context.Context, trackID, rid int64) error {
	if _, err := BuildQuery(ctx, s.DB, s.Library, trackID); err != nil {
		return err
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `DELETE FROM lyrics_rejected WHERE track_id=?1
		AND text_hash=(SELECT text_hash FROM lyrics_rejected WHERE id=?2 AND track_id=?1)
		RETURNING source, COALESCE(lyrics_external_id,''), text, synced, duration_s`, trackID, rid)
	if err != nil {
		return err
	}
	var back []storedRow
	n := 0
	for rows.Next() {
		n++
		var r storedRow
		var text sql.NullString
		if err := rows.Scan(&r.source, &r.externalID, &text, &r.synced, &r.durationS); err != nil {
			rows.Close()
			return err
		}
		if text.Valid {
			r.text = text.String
			back = append(back, r)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	now := s.now().Unix()
	for _, r := range back {
		if _, err := restoreRow(ctx, tx, trackID, r, now); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// restoreRow stores a removed row again (unselected; an existing row with
// the same provider id gets the text back) and returns its id.
func restoreRow(ctx context.Context, tx *sql.Tx, trackID int64, r storedRow, now int64) (int64, error) {
	var id int64
	err := tx.QueryRowContext(ctx, `INSERT INTO lyrics(track_id,source,external_id,synced,text,duration_s,created_at) VALUES (?,?,?,?,?,?,?)
		ON CONFLICT(track_id,source,external_id) DO UPDATE SET synced=excluded.synced, text=excluded.text RETURNING id`,
		trackID, r.source, r.externalID, r.synced, r.text, r.durationS, now).Scan(&id)
	return id, err
}

// SetOffset shifts the track's selected lyrics for everyone by ms (clamped to
// ±MaxOffsetMS): a line shows at its time + ms. lyricsID names the lyrics the
// caller sees (0: whatever is selected); ErrLyricsChanged when others are
// selected now, ErrNoLyrics when none are.
func (s *Service) SetOffset(ctx context.Context, trackID, lyricsID int64, ms int) error {
	if _, err := BuildQuery(ctx, s.DB, s.Library, trackID); err != nil {
		return err
	}
	ms = min(max(ms, -MaxOffsetMS), MaxOffsetMS)
	res, err := s.DB.ExecContext(ctx, `UPDATE lyrics SET offset_ms=?1 WHERE track_id=?2 AND selected=1 AND (?3=0 OR id=?3)`, ms, trackID, lyricsID)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil {
		return err
	} else if n == 0 {
		var any bool
		if err := s.DB.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM lyrics WHERE track_id=? AND selected=1)`, trackID).Scan(&any); err != nil {
			return err
		}
		if any {
			return ErrLyricsChanged
		}
		return ErrNoLyrics
	}
	return nil
}

// selectSet makes row ?1 the selected one; the offset stays only on a row
// that was selected already and still is (another selection starts at 0).
const selectSet = `offset_ms=CASE WHEN selected=1 AND id=?1 THEN offset_ms ELSE 0 END, selected=(id=?1)`

func selectRow(ctx context.Context, tx *sql.Tx, trackID, id int64) error {
	_, err := tx.ExecContext(ctx, `UPDATE lyrics SET `+selectSet+` WHERE track_id=?2`, id, trackID)
	return err
}

// reject remembers removed lyrics so lookups never store them again (the
// file's own lyrics by words only, so a corrected .lrc is picked up), with
// the removed row itself and a preview, for undo and the admin's recovery
// list. by is the "wrong lyrics" reporter (invalid for an admin's delete);
// report 0 starts a report (this tombstone's id becomes its report id),
// else the tombstone joins that report. Returns the report id (0 without a
// reporter).
func reject(ctx context.Context, tx *sql.Tx, trackID int64, r storedRow, now int64, by sql.NullInt64, report int64) (int64, error) {
	extID := sql.NullString{String: r.externalID, Valid: r.source != "embedded"}
	lines := strings.SplitN(PlainText(r.text), "\n", 4)
	preview := strings.Join(lines[:min(3, len(lines))], "\n")
	var id int64
	err := tx.QueryRowContext(ctx, `INSERT INTO lyrics_rejected(track_id,source,external_id,text_hash,rejected_at,
		reported_by,report_id,preview,text,lyrics_external_id,synced,duration_s,offset_ms) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?) RETURNING id`,
		trackID, r.source, extID, textHash(r.text), now, by, sql.NullInt64{Int64: report, Valid: report != 0},
		preview, r.text, r.externalID, r.synced, r.durationS, r.offsetMS).Scan(&id)
	if err != nil || !by.Valid {
		return 0, err
	}
	if report == 0 {
		report = id
		_, err = tx.ExecContext(ctx, `UPDATE lyrics_rejected SET report_id=? WHERE id=?`, id, id)
	}
	return report, err
}

// rowCols are a lyrics row's columns that storedRow keeps, after the id.
const rowCols = `source, external_id, text, synced, duration_s, offset_ms`

type storedRow struct {
	id                 int64
	source, externalID string
	text               string
	synced             bool
	durationS          int
	offsetMS           int
}

func remaining(ctx context.Context, tx *sql.Tx, trackID int64) ([]storedRow, error) {
	rows, err := tx.QueryContext(ctx, `SELECT id, `+rowCols+` FROM lyrics WHERE track_id=? ORDER BY id`, trackID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []storedRow
	for rows.Next() {
		var r storedRow
		if err := rows.Scan(&r.id, &r.source, &r.externalID, &r.text, &r.synced, &r.durationS, &r.offsetMS); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// nextBest picks among stored rows (non-empty, in id order) like Rank:
// synced first, then the duration closest to the track's (unknown last),
// then the oldest.
func nextBest(trackS int, rows []storedRow) storedRow {
	best := rows[0]
	for _, r := range rows[1:] {
		if r.synced != best.synced {
			if r.synced {
				best = r
			}
			continue
		}
		if durationGap(trackS, r.durationS) < durationGap(trackS, best.durationS) {
			best = r
		}
	}
	return best
}

// MarkNone records the admin's "this song has no lyrics": nothing selected
// (the candidates stay, for the picker), and lyrics_lookup found=0 manual=1,
// which Get and prefetch never look past and the missing list leaves out.
// Select, or an explicit refresh that inserts a candidate not stored before,
// undoes it; the candidates already there are never re-selected by a lookup.
func (s *Service) MarkNone(ctx context.Context, trackID int64) error {
	if _, err := BuildQuery(ctx, s.DB, s.Library, trackID); err != nil {
		return err
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `UPDATE lyrics SET selected=0, offset_ms=0 WHERE track_id=? AND selected=1`, trackID); err != nil {
		return err
	}
	if err := markNone(ctx, tx, trackID, s.now().Unix()); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	s.failed.Clear(trackID)
	return nil
}

func markNone(ctx context.Context, tx *sql.Tx, trackID, now int64) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO lyrics_lookup(track_id,attempted_at,found,manual) VALUES (?,?,0,1)
		ON CONFLICT(track_id) DO UPDATE SET attempted_at=excluded.attempted_at, found=0, manual=1`, trackID, now)
	return err
}
