import SwiftUI

enum ServerURLValidator {
    /// The first-launch field starts empty: there is no default server. The placeholder is only an example.
    static let initialText = ""
    static let placeholder = "https://music.example.com"

    /// `https://host[:port]/`, or plain http for development hosts (`localhost`, `*.local`). Nil if invalid.
    static func normalize(_ text: String) -> URL? {
        guard let c = URLComponents(string: text.trimmingCharacters(in: .whitespacesAndNewlines)),
              let scheme = c.scheme?.lowercased(), let host = c.host?.lowercased(), !host.isEmpty,
              c.user == nil, c.password == nil else { return nil }
        switch scheme {
        case "https": break
        case "http" where host == "localhost" || host.hasSuffix(".local"): break
        default: return nil
        }
        var out = URLComponents()
        out.scheme = scheme; out.host = host; out.port = c.port; out.path = "/"
        return out.url
    }

    static func infoURL(_ server: URL) -> URL { server.appendingPathComponent("api/v1/info") }

    /// True when `GET /api/v1/info` answers 200 (it is unauthenticated).
    static func check(_ server: URL) async -> Bool {
        var req = URLRequest(url: infoURL(server), cachePolicy: .reloadIgnoringLocalCacheData, timeoutInterval: 10)
        req.httpMethod = "GET"
        guard let (_, resp) = try? await URLSession(configuration: .ephemeral).data(for: req) else { return false }
        return (resp as? HTTPURLResponse)?.statusCode == 200
    }
}

/// First launch: ask for the server URL.
struct ServerURLView: View {
    let onConnected: (URL) -> Void
    @State private var text = ServerURLValidator.initialText
    @State private var checking = false
    @State private var error: String?

    var body: some View {
        VStack(alignment: .leading, spacing: 16) {
            Text(verbatim: "Melarka").font(.largeTitle.bold())
            Text("Server Address").font(.headline)
            TextField(ServerURLValidator.placeholder, text: $text)
                .textFieldStyle(.roundedBorder)
                .keyboardType(.URL).textContentType(.URL)
                .textInputAutocapitalization(.never).autocorrectionDisabled()
                .onSubmit(connect)
            if let error { Text(error).foregroundStyle(.red).font(.footnote) }
            Button(action: connect) {
                if checking { ProgressView() } else { Text("Connect").frame(maxWidth: .infinity) }
            }
            .buttonStyle(.borderedProminent)
            .disabled(checking)
            Spacer()
        }
        .padding(24)
    }

    private func connect() {
        guard let url = ServerURLValidator.normalize(text) else {
            error = String(localized: "Enter an address that starts with https:// (for development, http://…local or localhost also works)")
            return
        }
        checking = true; error = nil
        Task {
            let ok = await ServerURLValidator.check(url)
            checking = false
            if ok { onConnected(url) } else { error = String(localized: "Can't connect to this server") }
        }
    }
}
