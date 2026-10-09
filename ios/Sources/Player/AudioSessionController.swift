import AVFoundation
import os

/// The app's audio session: `.playback` (keeps playing in the background and with the silent switch on),
/// interruptions (a call, Siri, an alarm) and route changes (headphones out, the car disconnecting).
///
/// - An interruption pauses playback as a system pause; when it ends with `.shouldResume`, playback resumes,
///   unless the user paused or played something else meanwhile (that clears the engine's `pausedBySystem`).
/// - The old output device going away pauses, as a user pause: nothing brings it back by itself.
/// - Launch only sets the category (`configure`): opening Lark to browse never stops another app's audio. The
///   session is activated synchronously on every path that starts sound (`engine.willStartPlayback`, and the
///   remote play before its cold-start Task), never behind an `await`. The category is non-mixable, so Lark
///   stays eligible as the Now Playing app the car's play goes to.
/// - A media services reset: the category again, a new player, and the current item reloaded where it was
///   (playing only if it was).
@MainActor final class AudioSessionController {
    private static let log = Logger(subsystem: BuildInfo.bundleID, category: "audio")
    private unowned let engine: PlayerEngine
    private let observers: ObserverBag
    /// The real session; tests replace both.
    var activateSession: () throws -> Void = { try AVAudioSession.sharedInstance().setActive(true) }
    var configureSession: () throws -> Void = {
        try AVAudioSession.sharedInstance().setCategory(.playback, mode: .default, options: [])
    }

    init(engine: PlayerEngine, center: NotificationCenter = .default) {
        self.engine = engine
        observers = ObserverBag(center)
        observers.add(center.addObserver(forName: AVAudioSession.interruptionNotification, object: nil, queue: nil) { [weak self] n in
            let type = (n.userInfo?[AVAudioSessionInterruptionTypeKey] as? UInt).flatMap(AVAudioSession.InterruptionType.init)
            let options = AVAudioSession.InterruptionOptions(rawValue: n.userInfo?[AVAudioSessionInterruptionOptionKey] as? UInt ?? 0)
            performOnMain { self?.interruption(type, options) }
        })
        observers.add(center.addObserver(forName: AVAudioSession.mediaServicesWereResetNotification, object: nil, queue: nil) { [weak self] _ in
            performOnMain { self?.mediaServicesReset() }
        })
        observers.add(center.addObserver(forName: AVAudioSession.routeChangeNotification, object: nil, queue: nil) { [weak self] n in
            let reason = (n.userInfo?[AVAudioSessionRouteChangeReasonKey] as? UInt).flatMap(AVAudioSession.RouteChangeReason.init)
            performOnMain { self?.routeChange(reason) }
        })
        engine.willStartPlayback = { [weak self] in self?.activateForPlayback() }
    }

    /// At launch, before any playback: `.playback`, mode `.default`, no options. It does not activate.
    func configure() {
        do { try configureSession() } catch { Self.log.error("audio session: could not set the category") }
    }

    /// Right before sound starts. A failure is logged and playback goes on: AVPlayer can still try.
    func activateForPlayback() {
        do { try activateSession() } catch { Self.log.error("audio session: could not activate") }
    }

    private func mediaServicesReset() {
        configure()
        engine.mediaServicesReset()
    }

    private func interruption(_ type: AVAudioSession.InterruptionType?, _ options: AVAudioSession.InterruptionOptions) {
        switch type {
        case .began:
            engine.systemPause()
        case .ended:
            guard options.contains(.shouldResume), engine.pausedBySystem else { return }
            engine.play()             // activates the session first (`willStartPlayback`)
        default:
            break
        }
    }

    private func routeChange(_ reason: AVAudioSession.RouteChangeReason?) {
        guard reason == .oldDeviceUnavailable, engine.playing || engine.pausedBySystem else { return }
        engine.pause()
    }
}

/// Notification observers, removed when their owner goes.
final class ObserverBag {
    private let center: NotificationCenter
    private var tokens: [NSObjectProtocol] = []
    init(_ center: NotificationCenter) { self.center = center }
    func add(_ t: NSObjectProtocol) { tokens.append(t) }
    deinit { for t in tokens { center.removeObserver(t) } }
}
