import Foundation

/// The offline cache as the engine sees it (`CacheStore` per user, behind `UserCache`); tests use `FakeCache`.
@MainActor protocol CacheProviding: AnyObject {
    /// The cached file for a track, or nil when it has to stream. Only a file that is still on disk:
    /// iOS may purge `Library/Caches` at any time.
    func localURL(trackID: Int) -> URL?
    /// The track was just played from the cache (LRU bookkeeping).
    func touch(trackID: Int)
    /// Every cached favorite with its raw JSON (`Item.meta`), for the offline fallback and a no-network shuffle.
    /// From the index alone: no network, no waiting.
    func cachedFavorites() -> [(Track, JSONValue)]
    /// Download these tracks ahead (the lookahead). Items, not ids, so the cache can keep their metadata.
    func prefetch(_ items: [Item])
}

/// No cache.
@MainActor final class NoCache: CacheProviding {
    func localURL(trackID: Int) -> URL? { nil }
    func touch(trackID: Int) {}
    func cachedFavorites() -> [(Track, JSONValue)] { [] }
    func prefetch(_ items: [Item]) {}
}
