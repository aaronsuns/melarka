import SwiftUI

/// The native settings sheet (the web's 设置 opens it through `openSettings`).
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
                Section("服务器") {
                    LabeledContent("地址", value: model.server)
                    Button("更换服务器", role: .destructive) { confirmServer = true }
                }
                Section {
                    LabeledContent("缓存", value: model.usageText)
                    Picker("缓存上限", selection: Binding(get: { model.capGB }, set: { model.setCap($0) })) {
                        ForEach(SettingsModel.capChoices, id: \.self) { Text("\($0) GB").tag($0) }
                    }
                    Button("立即同步收藏") { model.syncNow() }
                        .disabled(!model.signedIn)
                    Button("清空缓存", role: .destructive) { confirmClear = true }
                        .disabled(!model.signedIn)
                } header: {
                    Text("离线缓存")
                } footer: {
                    Text("收藏的歌会在连上 Wi‑Fi 时自动缓存，没有网络也能播放。")
                }
                Section("车载自动播放") {
                    NavigationLink("如何设置") { ShortcutsGuideView() }
                    Button("测试：随机播放收藏") { Task { await model.testShuffle() } }
                }
                Section("关于") {
                    LabeledContent("版本", value: SettingsModel.versionText)
                }
                if let message = model.message {
                    Section { Text(message).font(.footnote).foregroundStyle(.secondary) }
                }
            }
            .disabled(services.changingServer)          // signing out of the old server: nothing else meanwhile
            .overlay { if services.changingServer { ProgressView("正在退出登录…") } }
            .interactiveDismissDisabled(services.changingServer)
            .navigationTitle("Melarka 设置")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .confirmationAction) { Button("完成") { dismiss() } }
            }
            .onAppear { model.refresh() }
            .confirmationDialog("清空离线缓存？", isPresented: $confirmClear, titleVisibility: .visible) {
                Button("清空缓存", role: .destructive) { model.clearCache() }
            } message: {
                Text("已缓存的歌曲会被删除，收藏会在连上 Wi‑Fi 时重新缓存。")
            }
            .confirmationDialog("更换服务器？", isPresented: $confirmServer, titleVisibility: .visible) {
                Button("更换服务器", role: .destructive) { Task { await model.changeServer() } }
            } message: {
                Text("会停止播放并退出登录，然后重新输入服务器地址。")
            }
        }
    }
}
