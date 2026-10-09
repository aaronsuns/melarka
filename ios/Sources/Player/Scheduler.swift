import Foundation

/// A timer that can be cancelled.
@MainActor protocol Cancellable: AnyObject { func cancel() }

/// Delayed work on the main actor: network retries, the `/queue` debounce, refill retries.
/// Production uses `MainScheduler`; tests use `FakeScheduler` and move time by hand.
@MainActor protocol Scheduler: AnyObject {
    @discardableResult func after(_ seconds: Double, _ fn: @escaping @MainActor () -> Void) -> Cancellable
}

@MainActor final class MainScheduler: Scheduler {
    private final class Item: Cancellable {
        let work: DispatchWorkItem
        init(_ work: DispatchWorkItem) { self.work = work }
        func cancel() { work.cancel() }
    }

    func after(_ seconds: Double, _ fn: @escaping @MainActor () -> Void) -> Cancellable {
        let work = DispatchWorkItem { MainActor.assumeIsolated { fn() } }
        DispatchQueue.main.asyncAfter(deadline: .now() + seconds, execute: work)
        return Item(work)
    }
}

/// Runs `op` but waits for it at most `seconds`; `op` itself goes on in the background after a timeout.
@MainActor func withTimeout(_ seconds: Double, _ op: @escaping @MainActor () async -> Void) async {
    @MainActor final class Once { var done = false }
    await withCheckedContinuation { (c: CheckedContinuation<Void, Never>) in
        let once = Once()
        let finish: @MainActor () -> Void = {
            guard !once.done else { return }
            once.done = true
            c.resume()
        }
        Task { @MainActor in await op(); finish() }
        Task { @MainActor in try? await Task.sleep(nanoseconds: UInt64(max(0, seconds) * 1_000_000_000)); finish() }
    }
}

/// The async work a component has started, so tests can wait for all of it (`idle()`) and `reset()` can drop it.
@MainActor final class TaskBag {
    private var tasks: [UUID: Task<Void, Never>] = [:]

    func run(_ op: @escaping @MainActor () async -> Void) {
        let id = UUID()
        tasks[id] = Task { @MainActor [weak self] in
            await op()
            self?.tasks[id] = nil
        }
    }

    var isEmpty: Bool { tasks.isEmpty }

    /// Waits until every task started so far, and every task those started, has finished.
    func idle() async {
        while let t = tasks.values.first { await t.value }
    }

    func cancelAll() {
        for t in tasks.values { t.cancel() }
        tasks = [:]
    }
}
