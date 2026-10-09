import SwiftUI

struct RootView: View {
    @ObservedObject var services: AppServices

    var body: some View {
        Group {
            if let server = services.serverURL {
                // A new server is a new web view and coordinator (the coordinator holds the server URL).
                ZStack {
                    WebShell(webView: services.web(for: server), serverURL: server,
                             onCookiesChanged: { Task { await services.harvestCookies() } },
                             onLoadFailed: { services.pageDidFail($0) },
                             onLoaded: { services.pageDidLoad() })
                        .id(server)
                    if services.pageFailed { PageErrorView(services: services, server: server) }
                }
            } else {
                ServerURLView { url in Task { await services.setServerURL(url) } }
            }
        }
        .sheet(isPresented: $services.showSettings) {
            SettingsView(services: services)
        }
    }
}

/// The page could not load (offline, the server down): a native screen instead of a blank web view. It retries
/// by itself when the network comes back (`AppServices.networkChanged`).
struct PageErrorView: View {
    @ObservedObject var services: AppServices
    let server: URL

    var body: some View {
        VStack(spacing: 16) {
            Image(systemName: "wifi.exclamationmark").font(.system(size: 44)).foregroundStyle(.secondary)
            Text("Can't Connect to the Server").font(.title3.bold())
            Text(server.host ?? server.absoluteString).font(.footnote).foregroundStyle(.secondary)
            Button { services.retryPage() } label: { Text("Retry").frame(maxWidth: 220) }
                .buttonStyle(.borderedProminent)
            Button { services.openSettings() } label: { Text("Settings").frame(maxWidth: 220) }
                .buttonStyle(.bordered)
            if services.hasOfflineFavorites {
                Button { Task { await services.playOfflineFavorites() } } label: { Text("Play Offline Favorites").frame(maxWidth: 220) }
                    .buttonStyle(.bordered)
            }
        }
        .padding(24)
        .frame(maxWidth: .infinity, maxHeight: .infinity)
        .background(Color(.systemBackground))
    }
}
