import SwiftUI

/// The native settings sheet (the web's settings open it through `openSettings`).
struct SettingsView: View {
    @ObservedObject var services: AppServices
    @StateObject private var model: SettingsModel
    @Environment(\.dismiss) private var dismiss
    @State private var confirmClear = false
    @State private var confirmServer = false

    init(services: AppServices) {
        self.services = services
        _model = StateObject(wrappedValue: SettingsModel(services: services))
    }

    var body: some View {
        NavigationStack {
            Form {
                Section("Server") {
                    LabeledContent("Address", value: model.server)
                    Button("Change Server", role: .destructive) { confirmServer = true }
                }
                Section {
                    LabeledContent("Cache", value: model.usageText)
                    Picker("Cache Limit", selection: Binding(get: { model.capGB }, set: { model.setCap($0) })) {
                        ForEach(SettingsModel.capChoices, id: \.self) { Text(verbatim: "\($0) GB").tag($0) }
                    }
                    Button("Sync Favorites Now") { model.syncNow() }
                        .disabled(!model.signedIn)
                    Button("Clear Cache", role: .destructive) { confirmClear = true }
                        .disabled(!model.signedIn)
                } header: {
                    Text("Offline Cache")
                } footer: {
                    Text("Favorites are cached automatically on Wi‑Fi and play without a network.")
                }
                Section("Car Autoplay") {
                    NavigationLink("How to Set Up") { ShortcutsGuideView() }
                    Button("Test: Shuffle Favorites") { Task { await model.testShuffle() } }
                }
                Section("About") {
                    LabeledContent("Version", value: SettingsModel.versionText)
                }
                if let message = model.message {
                    Section { Text(message).font(.footnote).foregroundStyle(.secondary) }
                }
            }
            .disabled(services.changingServer)          // signing out of the old server: nothing else meanwhile
            .overlay { if services.changingServer { ProgressView("Signing out…") } }
            .interactiveDismissDisabled(services.changingServer)
            .navigationTitle("Melarka Settings")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .confirmationAction) { Button("Done") { dismiss() } }
            }
            .onAppear { model.refresh() }
            .confirmationDialog("Clear the offline cache?", isPresented: $confirmClear, titleVisibility: .visible) {
                Button("Clear Cache", role: .destructive) { model.clearCache() }
            } message: {
                Text("Cached songs are deleted; favorites are cached again on Wi‑Fi.")
            }
            .confirmationDialog("Change server?", isPresented: $confirmServer, titleVisibility: .visible) {
                Button("Change Server", role: .destructive) { Task { await model.changeServer() } }
            } message: {
                Text("Playback stops and you are signed out; then you enter the server address again.")
            }
        }
    }
}
