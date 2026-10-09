import Foundation
import os

/// Brings the cache's favorites up to date: the server's list → the flags → every missing favorite downloaded.
/// Wi-Fi only (online and not expensive), checked before the list and again before each download, so moving
/// onto cellular (or Low Data Mode) mid-sync starts nothing more. Favorites fill to `fill` (90%) of the cap, so a
/// library bigger than the cap does not churn favorites in and out on every sync, and the lookahead's tracks and
/// estimate errors fit beside them. A download also needs the volume's free-space reserve, and the first
/// out-of-space error ends the sync. Cancelling the calling task (the background refresh expiring) cancels the
/// sync's own downloads only: a lookahead download of the same track is left alone.
@MainActor final class FavoritesSync {
    private static let log = Logger(subsystem: BuildInfo.bundleID, category: "cache")
    private let api: LarkAPIProtocol
    private let cache: CacheStore
    private let network: NetworkStatus
    /// The share of the cap favorites may fill.
    static let fill = 0.9

    init(api: LarkAPIProtocol, cache: CacheStore, network: NetworkStatus) {
        self.api = api; self.cache = cache; self.network = network
    }

    private var onWiFi: Bool { network.answered && network.isOnline && !network.isExpensive }

    /// Returns when every download it started has finished (or been skipped).
    func run() async {
        guard onWiFi, api.token != nil, !Task.isCancelled else { return }
        let favorites: [(Track, JSONValue)]
        do {
            favorites = try await api.favorites()
        } catch {
            Self.log.error("favorites list failed: \(String(describing: error), privacy: .public)")
            return    // a failed list is not "no favorites": the flags stay as they are
        }
        guard !Task.isCancelled else { return }
        cache.setFavorites(Set(favorites.map(\.0.id)))
        cache.refresh(favorites)
        cache.resetOutOfSpace()        // space may have been freed since the last sync
        let downloads = favorites.compactMap { t, meta in
            cache.download(t, meta: meta, favorite: true, shouldStart: { [weak self] in self?.mayStart(t) ?? false })
        }
        let own = downloads.filter(\.created).map(\.task)
        await withTaskCancellationHandler {
            for d in downloads { await d.task.value }
        } onCancel: {
            for t in own { t.cancel() }
        }
    }

    /// Asked when a download gets its slot: still on Wi-Fi, no out-of-space error yet, and room for it beside
    /// the favorites already cached and those downloading now, under 90% of the cap and on the volume.
    private func mayStart(_ t: Track) -> Bool {
        guard onWiFi, !Task.isCancelled, !cache.outOfSpace else { return false }
        let need = Int64(cache.activeDownloads + 1) * CacheStore.estimatedBytes(t)
        return cache.favoriteBytes + need <= Int64(Double(cache.capBytes) * Self.fill) && cache.hasRoom(for: need)
    }
}
