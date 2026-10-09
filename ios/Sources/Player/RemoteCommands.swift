import MediaPlayer

/// Lock screen, Control Center, headphone and car buttons. The car's play goes to the last Now Playing app
/// even when it is suspended: with nothing loaded it resumes the saved queue or shuffles cached favorites.
/// Skip ±(30/15 s) replaces next/previous on the lock screen, so it is on only for 频道 episodes. Repeat and
/// shuffle (where the lock screen or the car shows them) are music's: on only while music is the active kind.
@MainActor final class RemoteCommands {
    static let skipForwardS: Double = 30
    static let skipBackwardS: Double = 15

    private let center: MPRemoteCommandCenter
    /// The engine for this command, resolved when it arrives: the app installs the commands once at launch,
    /// and the engine is built on first use and rebuilt for another server. Nil: nothing can play yet.
    private let resolve: @MainActor () -> PlayerEngine?
    private weak var attached: PlayerEngine?
    private var targets: [(MPRemoteCommand, Any)] = []

    convenience init(center: MPRemoteCommandCenter = .shared(), engine: PlayerEngine) {
        self.init(center: center, resolve: { [weak engine] in engine })
        attach(engine)
    }

    init(center: MPRemoteCommandCenter = .shared(), resolve: @escaping @MainActor () -> PlayerEngine?) {
        self.center = center
        self.resolve = resolve
        add(center.playCommand) { $0.playNow() }
        add(center.pauseCommand) { $0.pauseNow() }
        add(center.togglePlayPauseCommand) { $0.toggleNow() }
        add(center.nextTrackCommand) { $0.nextNow() }
        add(center.previousTrackCommand) { $0.previousNow() }
        add(center.changePlaybackPositionCommand) { r, e in
            guard let e = e as? MPChangePlaybackPositionCommandEvent else { return .commandFailed }
            return r.seekNow(seconds: e.positionTime)
        }
        add(center.skipForwardCommand) { r, e in r.skipNow(seconds: (e as? MPSkipIntervalCommandEvent)?.interval ?? Self.skipForwardS) }
        add(center.skipBackwardCommand) { r, e in r.skipNow(seconds: -((e as? MPSkipIntervalCommandEvent)?.interval ?? Self.skipBackwardS)) }
        add(center.changeRepeatModeCommand) { r, e in
            guard let e = e as? MPChangeRepeatModeCommandEvent else { return .commandFailed }
            return r.changeRepeatNow(e.repeatType)
        }
        add(center.changeShuffleModeCommand) { r, e in
            guard let e = e as? MPChangeShuffleModeCommandEvent else { return .commandFailed }
            return r.changeShuffleNow(e.shuffleType)
        }
        center.skipForwardCommand.preferredIntervals = [NSNumber(value: Self.skipForwardS)]
        center.skipBackwardCommand.preferredIntervals = [NSNumber(value: Self.skipBackwardS)]
        for c in [center.playCommand, center.pauseCommand, center.togglePlayPauseCommand, center.nextTrackCommand,
                  center.previousTrackCommand, center.changePlaybackPositionCommand] { c.isEnabled = true }
        update(for: .track)
    }

    /// A newly built engine: the skip buttons follow its active kind, repeat and shuffle its modes.
    func attach(_ engine: PlayerEngine) {
        attached?.onActiveChange = nil
        attached?.onModesChange = nil
        attached = engine
        engine.onActiveChange = { [weak self] in self?.update(for: $0) }
        engine.onModesChange = { [weak self] in self?.show($0) }
        update(for: engine.active)
        show(engine.modes)
    }

    /// Skip intervals for episodes only; repeat and shuffle for music only; next and previous always.
    func update(for kind: Item.Kind) {
        center.skipForwardCommand.isEnabled = kind == .episode
        center.skipBackwardCommand.isEnabled = kind == .episode
        center.changeRepeatModeCommand.isEnabled = kind == .track
        center.changeShuffleModeCommand.isEnabled = kind == .track
    }

    /// The modes as the lock screen and the car show them.
    func show(_ m: PlayModes) {
        center.changeRepeatModeCommand.currentRepeatType = Self.repeatType(m.repeatMode)
        center.changeShuffleModeCommand.currentShuffleType = m.shuffle ? .items : .off
    }

    static func repeatType(_ m: RepeatMode) -> MPRepeatType {
        switch m { case .off: return .off; case .all: return .all; case .one: return .one }
    }

    /// Removes every handler (tests: the command center is shared by the process).
    func detach() {
        for (c, t) in targets { c.removeTarget(t) }
        targets = []
        attached?.onActiveChange = nil
        attached?.onModesChange = nil
        attached = nil
    }

    // MARK: Handlers (internal for tests)

    func playNow() -> MPRemoteCommandHandlerStatus {
        guard let engine = resolve() else { return .noActionableNowPlayingItem }
        return Self.handlePlay(engine)
    }

    /// What the play command runs. A cold start (the car, after iOS launched or woke Lark in the background):
    /// the saved queue from disk, its current item from the cache, else cached favorites, all started inside
    /// this handler with no network call and no await. Only when nothing is on the phone does it ask the server,
    /// and then the session is activated first, before the Task: behind its awaits, iOS could suspend the app.
    @discardableResult
    static func handlePlay(_ engine: PlayerEngine) -> MPRemoteCommandHandlerStatus {
        if engine.isLoaded {
            engine.play()
        } else if !engine.resumeFromDisk() {
            engine.prepareForPlayback()
            Task { [engine] in await engine.shuffleFavorites() }
        }
        return .success
    }

    func pauseNow() -> MPRemoteCommandHandlerStatus {
        guard let engine = resolve(), engine.current != nil else { return .noActionableNowPlayingItem }
        engine.pause()
        return .success
    }

    func toggleNow() -> MPRemoteCommandHandlerStatus {
        resolve()?.playing == true ? pauseNow() : playNow()
    }

    func nextNow() -> MPRemoteCommandHandlerStatus {
        guard let engine = resolve(), engine.current != nil else { return .noActionableNowPlayingItem }
        engine.next()
        return .success
    }

    func previousNow() -> MPRemoteCommandHandlerStatus {
        guard let engine = resolve(), engine.current != nil else { return .noActionableNowPlayingItem }
        engine.prev()
        return .success
    }

    func seekNow(seconds: Double) -> MPRemoteCommandHandlerStatus {
        guard let engine = resolve(), engine.current != nil, seconds.isFinite else { return .noActionableNowPlayingItem }
        engine.seek(ms: Int((seconds * 1000).rounded()))
        return .success
    }

    func skipNow(seconds: Double) -> MPRemoteCommandHandlerStatus {
        guard let engine = resolve(), engine.current != nil, seconds.isFinite else { return .noActionableNowPlayingItem }
        engine.skip(ms: Int((seconds * 1000).rounded()))
        return .success
    }

    /// The lock screen's or the car's repeat button: the shuffle stays as it is. Episodes ignore it.
    func changeRepeatNow(_ type: MPRepeatType) -> MPRemoteCommandHandlerStatus {
        guard let engine = resolve(), engine.active == .track else { return .noActionableNowPlayingItem }
        let m: RepeatMode = type == .one ? .one : type == .all ? .all : .off
        engine.setModes(shuffle: engine.modes.shuffle, repeatMode: m)
        return .success
    }

    /// The shuffle button: any shuffle type but off turns it on. Episodes ignore it.
    func changeShuffleNow(_ type: MPShuffleType) -> MPRemoteCommandHandlerStatus {
        guard let engine = resolve(), engine.active == .track else { return .noActionableNowPlayingItem }
        engine.setModes(shuffle: type != .off, repeatMode: engine.modes.repeatMode)
        return .success
    }

    // MARK: -

    private func add(_ c: MPRemoteCommand, _ h: @escaping @MainActor (RemoteCommands) -> MPRemoteCommandHandlerStatus) {
        add(c) { r, _ in h(r) }
    }

    /// MediaPlayer calls handlers on the main thread.
    private func add(_ c: MPRemoteCommand,
                     _ h: @escaping @MainActor (RemoteCommands, MPRemoteCommandEvent) -> MPRemoteCommandHandlerStatus) {
        let t = c.addTarget { [weak self] e in
            MainActor.assumeIsolated {
                guard let self else { return .noActionableNowPlayingItem }
                return h(self, e)
            }
        }
        targets.append((c, t))
    }
}
