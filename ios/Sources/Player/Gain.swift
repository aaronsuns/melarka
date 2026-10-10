import Foundation

/// Loudness normalization: the server measures each track and sends `gain_db` with it (never above 0,
/// null until measured). Music plays at `10^(gain_db/20)` of full volume, so loud masters come down to the
/// level of quiet ones; nothing is ever boosted. Episodes are never touched.
enum Gain {
    /// Volume factor for a music item: 10^(gain_db/20), attenuation only; 1 for episodes, unmeasured tracks, or when off.
    static func factor(_ item: Item, enabled: Bool) -> Float {
        guard enabled, item.kind == .track else { return 1 }
        let db: Double
        switch item.meta["gain_db"] {
        case .int(let i)?: db = Double(i)
        case .double(let d)?: db = d
        default: return 1
        }
        guard db.isFinite, db < 0 else { return 1 }
        return Float(min(1, max(0, pow(10, db / 20))))
    }
}
