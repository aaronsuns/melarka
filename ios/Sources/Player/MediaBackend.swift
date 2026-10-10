import Foundation

/// Where an item's audio comes from: a file in the offline cache, or the server with the bearer token
/// (sent as a header by the AVFoundation backend, never in the URL).
enum MediaSource: Equatable {
    case file(URL)
    case remote(URL, token: String)
}

/// What the backend reports. All calls arrive on the main actor.
///
/// Every callback carries the `generation` of the `load` it is about. The engine drops any callback whose
/// generation is not the one it loaded last, so a late KVO or notification from a replaced item (its
/// failure, its end, its pause) can never act on the item that replaced it. The AVFoundation backend stamps
/// each AVPlayerItem with the generation it was loaded with and reports that, not the current one.
@MainActor protocol MediaBackendDelegate: AnyObject {
    func backendStarted(generation: Int)
    /// Only a pause the engine did not ask for (an interruption, a route change). The generation check
    /// already drops a replaced item's pause; this rule is defence in depth.
    func backendPaused(generation: Int)
    func backendFinished(generation: Int)
    func backendFailed(network: Bool, generation: Int)
    func backendBuffering(_ on: Bool, generation: Int)
    func backendTick(positionMs: Int, generation: Int)       // ~2 Hz while playing (periodic time observer)
}

/// The one audio player. AVPlayerBackend implements it on AVQueuePlayer; engine tests use `FakeBackend`.
@MainActor protocol MediaBackend: AnyObject {
    var delegate: MediaBackendDelegate? { get set }
    /// `generation` increases with every load (a retry of the same item too); callbacks report it back.
    /// `gain` is the item's own loudness factor (`Gain.factor`, 0...1), applied to that item alone.
    func load(_ s: MediaSource, startMs: Int, autoplay: Bool, rate: Double, gain: Float, generation: Int)
    /// The next item, for a gapless change (AVQueuePlayer), with its own gain, so it starts at its level.
    func preload(_ s: MediaSource?, gain: Float)
    /// The loaded item's gain changed (the loudness switch): applied in place, nothing is reloaded.
    func setGain(_ g: Float)
    /// The master volume, for the sleep timer's fade; independent of each item's gain, kept across items.
    func setVolume(_ v: Float)
    func play()
    func pause()
    func seek(ms: Int)
    func setRate(_ r: Double)
    func stop()
    /// A media services reset: the player is dead. Drops it (and every item, silently) for a new one; the engine
    /// then loads the current item again with a new generation.
    func rebuild()
    var positionMs: Int { get }
    var durationMs: Int? { get }
    var isPlaying: Bool { get }
}

/// A backend that plays nothing: the default for `AppServices` built by tests (the app uses `AVPlayerBackend`).
@MainActor final class SilentBackend: MediaBackend {
    weak var delegate: MediaBackendDelegate?
    private(set) var positionMs = 0
    var durationMs: Int? { nil }
    private(set) var isPlaying = false
    func load(_ s: MediaSource, startMs: Int, autoplay: Bool, rate: Double, gain: Float, generation: Int) { positionMs = startMs; isPlaying = false }
    func preload(_ s: MediaSource?, gain: Float) {}
    func setGain(_ g: Float) {}
    func setVolume(_ v: Float) {}
    func play() {}
    func pause() { isPlaying = false }
    func seek(ms: Int) { positionMs = ms }
    func setRate(_ r: Double) {}
    func stop() { isPlaying = false; positionMs = 0 }
    func rebuild() { isPlaying = false }
}
