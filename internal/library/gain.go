package library

import (
	"database/sql"
	"math"
)

// GainDB is the attenuation that brings a track to -14 LUFS without its true peak going above -1 dBTP.
// Never positive (quiet tracks are not boosted); nil when the track has not been measured.
// Rounded to 0.1 dB toward more attenuation, so rounding never loosens the peak cap.
const targetLUFS, peakCeiling = -14.0, -1.0

func GainDB(lufs, peak sql.NullFloat64) *float64 {
	if !lufs.Valid {
		return nil
	}
	g := math.Min(0, targetLUFS-lufs.Float64)
	if peak.Valid {
		g = math.Min(g, peakCeiling-peak.Float64)
	}
	// The epsilon keeps float noise (e.g. -1-0.8 = -1.8000000000000003)
	// from flooring an exact tenth one step further down.
	g = math.Floor(g*10+1e-9) / 10
	if g == 0 {
		g = 0 // no "-0"
	}
	return &g
}
