import Foundation
import BackgroundTasks
import os

/// One OS background launch, as `BackgroundRefresh` needs it (`BGAppRefreshTask` in the app; a fake in tests).
protocol RefreshTask: AnyObject, Sendable {
    func onExpiration(_ fn: @escaping @Sendable () -> Void)
    func complete(success: Bool)
}

private final class SystemRefreshTask: RefreshTask, @unchecked Sendable {
    let task: BGTask
    init(_ task: BGTask) { self.task = task }
    func onExpiration(_ fn: @escaping @Sendable () -> Void) { task.expirationHandler = fn }
    func complete(success: Bool) { task.setTaskCompleted(success: success) }
}

/// The favorites sync in the background, best effort: a `BGAppRefreshTask` (`<bundle id>.refresh`,
/// listed in Info.plist's `BGTaskSchedulerPermittedIdentifiers`) asked for no sooner than 6 h ahead, and asked
/// again each time it runs. iOS decides when (or whether) it runs; the sync itself still requires Wi-Fi.
/// The expiration handler cancels the run.
@MainActor final class BackgroundRefresh {
    static let identifier = BuildInfo.bundleID + ".refresh"
    static let interval: TimeInterval = 6 * 60 * 60
    private static let log = Logger(subsystem: BuildInfo.bundleID, category: "cache")
    /// `BGTaskScheduler.register` may be called once per identifier per process.
    private static var registered = false

    private let run: @MainActor () async -> Void
    private let submit: (Date) throws -> Void
    private let now: () -> Date

    init(run: @escaping @MainActor () async -> Void,
         submit: @escaping (Date) throws -> Void = BackgroundRefresh.systemSubmit,
         now: @escaping () -> Date = Date.init) {
        self.run = run; self.submit = submit; self.now = now
    }

    nonisolated static func systemSubmit(_ earliest: Date) throws {
        let r = BGAppRefreshTaskRequest(identifier: identifier)
        r.earliestBeginDate = earliest
        try BGTaskScheduler.shared.submit(r)
    }

    /// Must run before the app finishes launching (`LarkApp.init` → `AppServices.start`).
    func register() {
        guard !Self.registered else { return }
        Self.registered = BGTaskScheduler.shared.register(forTaskWithIdentifier: Self.identifier, using: nil) { [weak self] task in
            let t = SystemRefreshTask(task)
            Task { @MainActor in
                if let self { self.perform(t) } else { t.complete(success: false) }
            }
        }
        if !Self.registered { Self.log.error("background refresh not registered") }
    }

    /// Asks iOS for the next run, at least `interval` from now. A refusal (e.g. Background App Refresh off,
    /// or the simulator) is logged and otherwise ignored.
    func schedule() {
        do { try submit(now().addingTimeInterval(Self.interval)) } catch {
            Self.log.info("background refresh not scheduled: \(String(describing: error), privacy: .public)")
        }
    }

    /// One background run: the next is asked for first (so an expiry cannot lose it), then the sync runs
    /// until done or until iOS expires the task, which cancels it.
    func perform(_ task: RefreshTask) {
        schedule()
        let work = Task { @MainActor [run] in await run() }
        task.onExpiration { work.cancel() }
        Task { @MainActor in
            await work.value
            task.complete(success: !work.isCancelled)
        }
    }
}
