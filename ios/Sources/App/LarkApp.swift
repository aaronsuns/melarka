import SwiftUI

@main struct LarkApp: App {
    @Environment(\.scenePhase) private var scenePhase
    @StateObject private var services = AppServices.shared

    init() {
        AppServices.shared.start()
    }

    var body: some Scene {
        WindowGroup {
            RootView(services: services)
                .onChange(of: scenePhase) { _, phase in services.scenePhaseChanged(phase) }
        }
    }
}

enum BuildInfo { static let bundleID = "io.github.aaronsuns.melarka" }
