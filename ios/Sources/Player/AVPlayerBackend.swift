import AVFoundation

/// An AVPlayerItem that knows its source, the generation of the `load` it was loaded with (nil while it is
/// only preloaded), and its backend. Every callback about it reports that generation, never the current one.
final class LarkPlayerItem: AVPlayerItem {
    let source: MediaSource
    var generation: Int?
    /// Cleared when the item is replaced: from then on nothing about it is reported.
    weak var owner: AVPlayerBackend?
    var ended = false
    var failed = false
    /// A seek asked for and not done yet (before the item is ready, or in flight): the position reads this.
    var pendingSeekMs: Int?
    /// Counts the seeks handed to AVFoundation: only the newest one's completion may clear `pendingSeekMs`.
    var seekCount = 0
    var statusObservation: NSKeyValueObservation?
    /// The loudness gain asked for last (`AVPlayerBackend.applyGain`): 1 is no audio mix at all.
    var gain: Float = 1
    /// Its gain is below 1 and the mix is not on it yet: it must not sound (it would at full level).
    var mixPending = false

    init(source: MediaSource, asset: AVAsset) {
        self.source = source
        super.init(asset: asset, automaticallyLoadedAssetKeys: nil)
    }
}

/// The one audio player, on AVQueuePlayer: the current item plus the preloaded next one, so a change at the
/// end of a track is gapless.
///
/// - A periodic time observer (0.5 s) drives `backendTick`; it keeps firing in the background while audio
///   plays, which is what car lyrics need.
/// - `timeControlStatus`: `.playing` is `backendStarted`, `.waitingToPlayAtSpecifiedRate` is buffering, and a
///   `.paused` the engine did not ask for (an interruption, a route change) is `backendPaused`.
/// - An item that fails reports `network: true` only for a stream with an `NSURLErrorDomain` error in its chain;
///   a file never has a network error.
/// - Gapless: when the queue player moves to the preloaded item by itself, `backendFinished` tells the engine,
///   whose `load` of that same source takes the item over instead of loading it again. If the engine loads
///   something else, or nothing, the preloaded item is removed before it can play on by itself.
/// - Streams send the token as an `Authorization` header (`AVURLAssetHTTPHeaderFieldsKey`), never in the URL;
///   nothing here logs a source.
/// - Loudness: each item carries its own gain as an `AVAudioMix` on its audio track, so a preloaded item
///   starts at its own level with nothing to do at the change. An attenuated item never plays before its mix
///   is on (the mix needs the audio track loaded, which playback needs anyway); until then it is loading. `player.volume` is the master volume, which
///   only the sleep timer's fade moves; a rebuilt player gets it back.
@MainActor final class AVPlayerBackend: MediaBackend {
    weak var delegate: MediaBackendDelegate?
    /// Replaced by `rebuild()` after a media services reset.
    private(set) var player = AVQueuePlayer()
    private var preloaded: LarkPlayerItem?
    /// The engine wants it playing: a `.paused` while this is true was not asked for.
    private var wantsPlay = false
    private var lastStatus: (status: AVPlayer.TimeControlStatus, generation: Int)?
    private var statusObservation: NSKeyValueObservation?
    private var timeObserver: TimeObserverToken?
    private let observers = ObserverBag(.default)
    /// Preloaded items taken over by a `load` (tests).
    private(set) var adoptedPreloads = 0
    /// The master volume (`setVolume`), carried over to a rebuilt player.
    private var volume: Float = 1

    init() {
        setUpPlayer()
        observers.add(NotificationCenter.default.addObserver(forName: AVPlayerItem.didPlayToEndTimeNotification, object: nil, queue: nil) { [weak self] n in
            guard let item = n.object as? LarkPlayerItem else { return }
            performOnMain { self?.ended(item) }
        })
        observers.add(NotificationCenter.default.addObserver(forName: AVPlayerItem.failedToPlayToEndTimeNotification, object: nil, queue: nil) { [weak self] n in
            guard let item = n.object as? LarkPlayerItem else { return }
            let error = n.userInfo?[AVPlayerItemFailedToPlayToEndTimeErrorKey] as? Error
            performOnMain { self?.failed(item, error) }
        })
    }

    /// The time observer and the status KVO, on the current `player`. Callbacks from a replaced player are
    /// ignored: they check that they come from the player in use.
    private func setUpPlayer() {
        let p = player
        p.automaticallyWaitsToMinimizeStalling = true
        p.actionAtItemEnd = .advance
        let token = p.addPeriodicTimeObserver(forInterval: CMTime(value: 1, timescale: 2), queue: .main) { [weak self, weak p] _ in
            MainActor.assumeIsolated { if let self, self.player === p { self.tick() } }
        }
        timeObserver = TimeObserverToken(player: p, token: token)
        statusObservation = p.observe(\.timeControlStatus, options: [.new]) { [weak self, weak p] _, _ in
            performOnMain { if let self, self.player === p { self.timeControlChanged() } }
        }
    }

    // MARK: - MediaBackend

    func rebuild() {
        wantsPlay = false
        player.pause()
        discardAll()
        statusObservation = nil
        timeObserver = nil
        player = AVQueuePlayer()
        player.volume = volume
        setUpPlayer()
    }

    func load(_ s: MediaSource, startMs: Int, autoplay: Bool, rate: Double, gain: Float, generation: Int) {
        wantsPlay = autoplay
        setRate(rate)
        if let p = preloaded, p.source == s, p.generation == nil, !p.failed, p.status != .failed, player.items().contains(p) {
            applyGain(p, gain)           // preloaded with this gain already: unchanged, so nothing is touched
            adopt(p, startMs: startMs, autoplay: autoplay, generation: generation)
            return
        }
        discardAll()
        let item = Self.makeItem(s)
        applyGain(item, gain)
        item.generation = generation
        own(item)
        player.insert(item, after: nil)
        if startMs > 0 { seek(item, ms: startMs) }
        if !autoplay {
            player.pause()
        } else if canStart(item) {
            player.play()
        } else {
            // Plays once the start position is reached and its mix is on; until then it is loading.
            player.pause()
            later { [weak self] in
                guard let self, self.isCurrent(item), self.wantsPlay, !self.canStart(item) else { return }
                self.delegate?.backendBuffering(true, generation: generation)
            }
        }
    }

    func preload(_ s: MediaSource?, gain: Float) {
        if let p = preloaded {
            if player.currentItem !== p { player.remove(p); release(p) }
            preloaded = nil
        }
        guard let s, let cur = player.currentItem as? LarkPlayerItem, cur.generation != nil, cur.owner === self else { return }
        let p = Self.makeItem(s)
        applyGain(p, gain)
        own(p)
        guard player.canInsert(p, after: cur) else { return release(p) }
        player.insert(p, after: cur)
        preloaded = p
    }

    func play() {
        wantsPlay = true
        guard let item = current else { return }
        if canStart(item) { player.play() }     // else the seek's or the mix's completion starts it
    }

    func pause() {
        wantsPlay = false
        player.pause()
    }

    func seek(ms: Int) {
        guard let item = current else { return }
        seek(item, ms: max(0, ms))
    }

    func setGain(_ g: Float, next: Float) {
        if let item = current { applyGain(item, g) }
        if let p = preloaded, p.generation == nil { applyGain(p, next) }
    }

    func setVolume(_ v: Float) {
        volume = min(1, max(0, v))
        player.volume = volume
    }

    func setRate(_ r: Double) {
        player.defaultRate = Float(r)
        if player.rate != 0 { player.rate = Float(r) }
    }

    func stop() {
        wantsPlay = false
        player.pause()
        discardAll()
    }

    var positionMs: Int {
        guard let item = current else { return 0 }
        if let p = item.pendingSeekMs { return p }
        let t = item.currentTime().seconds
        return t.isFinite ? max(0, Int((t * 1000).rounded())) : 0
    }

    var durationMs: Int? {
        guard let d = current?.duration, d.isNumeric else { return nil }
        let s = d.seconds
        return s.isFinite && s > 0 ? Int((s * 1000).rounded()) : nil
    }

    var isPlaying: Bool { player.rate != 0 && current != nil }

    // MARK: - Items

    static func headers(for s: MediaSource) -> [String: String]? {
        guard case .remote(_, let token) = s else { return nil }
        return ["Authorization": "Bearer \(token)"]
    }

    static func makeItem(_ s: MediaSource) -> LarkPlayerItem {
        let asset: AVURLAsset
        switch s {
        case .file(let url): asset = AVURLAsset(url: url)
        case .remote(let url, _): asset = AVURLAsset(url: url, options: ["AVURLAssetHTTPHeaderFieldsKey": headers(for: s)!])
        }
        let item = LarkPlayerItem(source: s, asset: asset)
        item.audioTimePitchAlgorithm = .timeDomain      // speech at 1.5x keeps its pitch
        return item
    }

    /// The item's loudness gain as an audio mix on its audio track (none at 1: the item plays untouched).
    /// The track has to load first, so a stream may play its first moments at full level; a preloaded item
    /// has its mix long before it starts. Only the newest gain asked for is applied.
    /// While it loads the item waits (`mixPending`), and the current item starts once it is on, as after a seek.
    /// An asset whose tracks cannot load gets no mix and is let through, so it fails through its status.
    private func applyGain(_ item: LarkPlayerItem, _ gain: Float) {
        let g = min(1, max(0, gain))
        guard g != item.gain || (g < 1 && item.audioMix == nil && !item.mixPending) else { return }
        item.gain = g
        guard g < 1 else {
            item.audioMix = nil; item.mixPending = false
            startIfWaiting(item)
            return
        }
        item.mixPending = true
        Task { @MainActor [weak self, weak item] in
            guard let item else { return }
            let track = try? await item.asset.loadTracks(withMediaType: .audio).first
            guard item.gain == g else { return }               // a newer gain decides
            if let track {
                let p = AVMutableAudioMixInputParameters(track: track)
                p.setVolume(g, at: .zero)
                let mix = AVMutableAudioMix()
                mix.inputParameters = [p]
                item.audioMix = mix
            }
            item.mixPending = false
            self?.startIfWaiting(item)
        }
    }

    /// Nothing holds the item back: no seek in flight, no mix still loading.
    private func canStart(_ item: LarkPlayerItem) -> Bool { item.pendingSeekMs == nil && !item.mixPending }

    /// The current item was held back (a seek or its mix) while the engine wants it playing: it plays now.
    private func startIfWaiting(_ item: LarkPlayerItem) {
        guard isCurrent(item), item.generation != nil, wantsPlay, canStart(item), player.rate == 0 else { return }
        player.play()
    }

    /// The current item, if it is ours and loaded by the engine (not a preloaded one it has not taken over).
    private var current: LarkPlayerItem? {
        guard let item = player.currentItem as? LarkPlayerItem, item.owner === self, item.generation != nil else { return nil }
        return item
    }

    private func isCurrent(_ item: LarkPlayerItem) -> Bool { player.currentItem === item && item.owner === self }

    private func own(_ item: LarkPlayerItem) {
        item.owner = self
        item.statusObservation = item.observe(\.status, options: [.new]) { [weak self] it, _ in
            performOnMain { self?.statusChanged(it) }
        }
    }

    /// Silences an item for good: nothing about it is reported again.
    private func release(_ item: LarkPlayerItem) {
        item.owner = nil
        item.statusObservation = nil
    }

    private func discardAll() {
        for case let item as LarkPlayerItem in player.items() { release(item) }
        player.removeAllItems()
        if let p = preloaded { release(p) }
        preloaded = nil
        lastStatus = nil
    }

    /// The engine loads what was preloaded: the same AVPlayerItem goes on (it may already be playing, gapless).
    private func adopt(_ p: LarkPlayerItem, startMs: Int, autoplay: Bool, generation: Int) {
        preloaded = nil
        while let c = player.currentItem, c !== p {
            if let c = c as? LarkPlayerItem { release(c) }
            player.advanceToNextItem()
        }
        p.generation = generation
        adoptedPreloads += 1
        lastStatus = nil
        if startMs > 0 {
            // It may already be playing from 0: silent until the resume position is reached (the seek plays it).
            player.pause()
            seek(p, ms: startMs)
        }
        if !autoplay || !canStart(p) { player.pause() }      // a mix still loading: silent until it is on
        if autoplay && canStart(p) { player.play() }
        // Already playing: no status change will come, so the start is reported from here (after this call returns).
        later { [weak self] in
            guard let self, self.isCurrent(p), p.generation == generation else { return }
            self.timeControlChanged()
        }
    }

    private func seek(_ item: LarkPlayerItem, ms: Int) {
        item.pendingSeekMs = ms
        if item.status == .readyToPlay { performSeek(item) }
    }

    private func performSeek(_ item: LarkPlayerItem) {
        guard let ms = item.pendingSeekMs else { return }
        item.seekCount += 1
        let n = item.seekCount
        item.seek(to: CMTime(value: CMTimeValue(ms), timescale: 1000), toleranceBefore: .zero, toleranceAfter: .zero) { [weak self] _ in
            performOnMain {
                // A newer seek is on its way. Its target may be the same: the item's ready status can arrive
                // after an adopt already started the seek, and the second seek cancels the first, whose
                // completion would otherwise start playback from the old position before the second lands.
                guard item.seekCount == n, item.pendingSeekMs == ms else { return }
                item.pendingSeekMs = nil
                guard let self, self.isCurrent(item), self.wantsPlay, !item.mixPending, self.player.rate == 0 else { return }
                self.player.play()
            }
        }
    }

    // MARK: - Events

    private func statusChanged(_ item: LarkPlayerItem) {
        guard item.owner === self else { return }
        switch item.status {
        case .readyToPlay: performSeek(item)
        case .failed: failed(item, item.error)
        default: break
        }
    }

    private func timeControlChanged() {
        guard let item = current, let g = item.generation else { return }
        let status = player.timeControlStatus
        if let l = lastStatus, l.status == status, l.generation == g { return }
        switch status {
        case .playing:
            lastStatus = (status, g)
            delegate?.backendStarted(generation: g)
        case .waitingToPlayAtSpecifiedRate:
            guard player.reasonForWaitingToPlay != .noItemToPlay else { return }
            lastStatus = (status, g)
            delegate?.backendBuffering(true, generation: g)
        case .paused:
            // Judged a moment later: an end or a failure arriving meanwhile is not a pause.
            later { [weak self] in
                guard let self, self.isCurrent(item), self.player.timeControlStatus == .paused else { return }
                self.lastStatus = (.paused, g)
                guard self.wantsPlay, !item.ended, !item.failed, self.canStart(item), !self.atEnd(item) else { return }
                self.wantsPlay = false
                self.delegate?.backendPaused(generation: g)
            }
        @unknown default:
            break
        }
    }

    private func atEnd(_ item: LarkPlayerItem) -> Bool {
        let d = item.duration.seconds, t = item.currentTime().seconds
        return d.isFinite && t.isFinite && d > 0 && t >= d - 0.3
    }

    private func tick() {
        guard player.timeControlStatus == .playing, let item = current, item.pendingSeekMs == nil, let g = item.generation else { return }
        delegate?.backendTick(positionMs: positionMs, generation: g)
    }

    private func ended(_ item: LarkPlayerItem) {
        guard item.owner === self, !item.ended else { return }
        item.ended = true
        guard let g = item.generation else { return }
        let next = preloaded
        delegate?.backendFinished(generation: g)
        // The engine did not take the preloaded item over (it loaded nothing): it must not play on by itself.
        if let next, next.generation == nil, next.owner === self {
            if player.items().contains(next) { player.remove(next) }
            release(next)
            if preloaded === next { preloaded = nil }
            wantsPlay = false
            player.pause()
        }
    }

    private func failed(_ item: LarkPlayerItem, _ error: Error?) {
        guard item.owner === self, !item.failed else { return }
        item.failed = true
        guard let g = item.generation else { return }     // a preloaded item: the engine's load sees it failed
        var network = false
        if case .remote = item.source { network = Self.isNetworkError(error) }
        delegate?.backendFailed(network: network, generation: g)
    }

    /// An `NSURLErrorDomain` error anywhere in the chain (AVFoundation wraps them in its own).
    static func isNetworkError(_ error: Error?) -> Bool {
        var e = error.map { $0 as NSError }
        while let cur = e {
            if cur.domain == NSURLErrorDomain { return true }
            e = cur.userInfo[NSUnderlyingErrorKey] as? NSError
        }
        return false
    }

    private func later(_ f: @escaping @MainActor () -> Void) {
        DispatchQueue.main.async { MainActor.assumeIsolated(f) }
    }
}

/// Removes the periodic time observer when the backend goes.
private final class TimeObserverToken {
    let player: AVPlayer
    let token: Any
    init(player: AVPlayer, token: Any) { self.player = player; self.token = token }
    deinit { player.removeTimeObserver(token) }
}

/// Runs `f` on the main actor: at once when already on the main thread, else queued there.
/// AVFoundation and AVAudioSession call back on their own threads.
func performOnMain(_ f: @escaping @MainActor () -> Void) {
    if Thread.isMainThread { MainActor.assumeIsolated(f) } else { DispatchQueue.main.async { MainActor.assumeIsolated(f) } }
}
