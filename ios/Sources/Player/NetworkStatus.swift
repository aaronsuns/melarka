import Foundation
import Network

/// Online / expensive (cellular) state. Production uses `NWPathMonitor`; tests use `FakeNetwork`.
/// Several parts react to changes (the engine, the cache's Wi-Fi sync), so it is a multicast: each adds its
/// own observer and none can replace another's.
@MainActor protocol NetworkStatus: AnyObject {
    var isOnline: Bool { get }
    /// Cellular, a personal hotspot, or Low Data Mode (`NWPath.isConstrained`): no bulk downloads.
    var isExpensive: Bool { get }
    /// False until the first path update: until then `isOnline`/`isExpensive` are guesses.
    var answered: Bool { get }
    /// `fn` runs on every change of `isOnline` or `isExpensive`, for the life of the status object.
    func observe(_ fn: @escaping @MainActor () -> Void)
}

extension NetworkStatus {
    /// Waits for the first path update, at most `timeout` seconds (a cold background launch decides on it).
    func firstAnswer(timeout: TimeInterval) async {
        let deadline = Date().addingTimeInterval(timeout)
        while !answered, Date() < deadline, !Task.isCancelled {
            try? await Task.sleep(nanoseconds: 50_000_000)
        }
    }
}

@MainActor final class PathNetworkStatus: NetworkStatus {
    private let monitor = NWPathMonitor()
    private(set) var isOnline = true
    /// Expensive until the first path update: a sync at launch must not run on cellular before the monitor
    /// has answered. The first update to Wi-Fi is a change, so a sync skipped meanwhile runs then.
    private(set) var isExpensive = true
    private(set) var answered = false
    private var observers: [@MainActor () -> Void] = []

    init() {
        monitor.pathUpdateHandler = { [weak self] path in
            let (online, expensive) = PathNetworkStatus.classify(satisfied: path.status == .satisfied, expensive: path.isExpensive,
                                                    constrained: path.isConstrained)
            Task { @MainActor in
                guard let self, !self.answered || self.isOnline != online || self.isExpensive != expensive else { return }
                self.answered = true
                self.isOnline = online; self.isExpensive = expensive
                self.observers.forEach { $0() }
            }
        }
        monitor.start(queue: DispatchQueue(label: BuildInfo.bundleID + ".network"))
    }

    func observe(_ fn: @escaping @MainActor () -> Void) { observers.append(fn) }

    /// Low Data Mode counts as expensive: the user asked for no bulk downloads.
    nonisolated static func classify(satisfied: Bool, expensive: Bool, constrained: Bool) -> (online: Bool, expensive: Bool) {
        (satisfied, expensive || constrained)
    }

    deinit { monitor.cancel() }
}
