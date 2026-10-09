package download

import "context"

// TrackIDs lists the usable tracks of userID's finished downloads — jobs they
// started and jobs their request deduped onto — newest request first; with
// all, everyone's. Hidden jobs still count: removing a job from the list
// tidies the list, it doesn't disown the song. limit ≤ 0 or > 1000 → 1000.
func (s *Service) TrackIDs(ctx context.Context, userID int64, all bool, limit int) ([]int64, error) {
	if limit <= 0 || limit > 1000 {
		limit = 1000
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT d.track_id, MAX(r.created_at) AS at
		FROM download_requests r JOIN downloads d ON d.id=r.download_id
		WHERE (? OR r.user_id=?) AND d.status='done' AND d.track_id IS NOT NULL AND `+trackUsable+`
		GROUP BY d.track_id ORDER BY at DESC, d.track_id DESC LIMIT ?`, all, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []int64{}
	for rows.Next() {
		var id, at int64
		if err := rows.Scan(&id, &at); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
