import SwiftUI
import UIKit

/// 离线模式: the native player over the failed page, opened from 播放离线收藏. Now playing (cover, title, progress,
/// transport, shuffle and repeat), the queue, and the cached favorites; every control goes to the engine through
/// `OfflinePlayerModel`, so the lock screen and the car stay in step. 重试连接 loads the web page again; the app
/// closes this screen once it has loaded, and the music plays on.
struct OfflinePlayerView: View {
    @ObservedObject var model: OfflinePlayerModel
    let server: URL
    let onClose: () -> Void

    var body: some View {
        VStack(spacing: 0) {
            header
            Divider()
            List {
                Section { NowPlayingCard(model: model) }
                if !model.upNext.isEmpty {
                    Section {
                        ForEach(model.upNext) { entry in
                            SongRow(model: model, item: entry.item, isCurrent: false) { model.jump(to: entry.index) }
                        }
                    } header: {
                        Text("Up Next")
                    }
                }
                Section {
                    if model.favorites.isEmpty {
                        Text("No favorites are cached on this iPhone yet.").foregroundStyle(.secondary)
                    }
                    ForEach(model.favorites) { item in
                        SongRow(model: model, item: item, isCurrent: item.id == model.current?.id) { model.play(item) }
                    }
                } header: {
                    Text("Offline Favorites")
                }
            }
            .listStyle(.insetGrouped)
        }
        .background(Color(.systemGroupedBackground).ignoresSafeArea())
        .onAppear { model.reload() }
    }

    private var header: some View {
        VStack(alignment: .leading, spacing: 8) {
            HStack(alignment: .firstTextBaseline, spacing: 12) {
                VStack(alignment: .leading, spacing: 2) {
                    Label { Text("Offline Mode") } icon: { Image(systemName: "wifi.slash") }
                        .font(.headline)
                    Text(server.host ?? server.absoluteString)
                        .font(.footnote).foregroundStyle(.secondary).lineLimit(1)
                }
                .accessibilityElement(children: .combine)
                .accessibilityAddTraits(.isHeader)
                Spacer(minLength: 8)
                Button(action: onClose) {
                    Image(systemName: "xmark.circle.fill").font(.title2).foregroundStyle(.secondary)
                }
                .accessibilityLabel(Text("Close"))
            }
            VStack(alignment: .leading, spacing: 6) {
                Button { model.retry() } label: {
                    HStack(spacing: 6) {
                        if model.connecting { ProgressView() } else { Image(systemName: "arrow.clockwise") }
                        model.connecting ? Text("Connecting…") : Text("Retry Connection")
                    }
                }
                .buttonStyle(.borderedProminent)
                .disabled(model.connecting)
                if model.stillOffline {
                    Text("Still can't connect to the server").font(.footnote).foregroundStyle(.secondary)
                }
            }
        }
        .padding(.horizontal, 16).padding(.vertical, 12)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(Color(.systemBackground))
    }
}

/// Cover, title and artist, the progress slider, ⏮ ▶/⏸ ⏭ and the 🔀 🔁 toggles.
private struct NowPlayingCard: View {
    @ObservedObject var model: OfflinePlayerModel
    @ScaledMetric(relativeTo: .title) private var coverSize: CGFloat = 200
    /// The slider's value while a finger is on it; nil otherwise (it follows the engine).
    @State private var scrubMs: Double?
    @State private var scrubbing = false

    var body: some View {
        if let item = model.current {
            VStack(spacing: 14) {
                Artwork(url: model.artworkURL(item), seed: item.id, title: item.title)
                    .frame(width: min(coverSize, 280), height: min(coverSize, 280))
                VStack(spacing: 4) {
                    Text(item.title).font(.title3.bold()).multilineTextAlignment(.center).lineLimit(3)
                    if !item.artist.isEmpty {
                        Text(item.artist).font(.body).foregroundStyle(.secondary).multilineTextAlignment(.center).lineLimit(2)
                    }
                }
                .accessibilityElement(children: .combine)
                progress
                transport
                if let error = model.error, !model.playing {
                    Text(error).font(.footnote).foregroundStyle(.secondary).multilineTextAlignment(.center)
                }
            }
            .frame(maxWidth: .infinity)
            .padding(.vertical, 8)
        } else {
            VStack(spacing: 6) {
                Text("Nothing playing").font(.headline)
                Text("Tap a song to play it.").font(.subheadline).foregroundStyle(.secondary)
            }
            .frame(maxWidth: .infinity)
            .padding(.vertical, 12)
            .accessibilityElement(children: .combine)
        }
    }

    private var duration: Int { max(model.durationMs, model.current?.durationMs ?? 0) }
    private var shownMs: Int { scrubMs.map { Int($0) } ?? model.positionMs }

    private var progress: some View {
        VStack(spacing: 2) {
            // A drag seeks once, when the finger lifts; VoiceOver's adjust (no editing) seeks at once.
            Slider(value: Binding(get: { Double(min(shownMs, max(duration, 1))) },
                                  set: { v in if scrubbing { scrubMs = v } else { model.seek(to: Int(v)) } }),
                   in: 0...Double(max(duration, 1)),
                   onEditingChanged: { editing in
                       scrubbing = editing
                       if !editing, let ms = scrubMs {
                           model.seek(to: Int(ms))
                           scrubMs = nil
                       }
                   })
            .disabled(duration <= 0)
            .accessibilityLabel(Text("Playback position"))
            .accessibilityValue(Text("\(OfflinePlayerModel.time(shownMs)) of \(OfflinePlayerModel.time(duration))"))
            HStack {
                Text(OfflinePlayerModel.time(shownMs))
                Spacer()
                Text("-" + OfflinePlayerModel.time(max(0, duration - shownMs)))
            }
            .font(.caption.monospacedDigit())
            .foregroundStyle(.secondary)
            .accessibilityHidden(true)
        }
    }

    private var transport: some View {
        HStack {
            Button { model.toggleShuffle() } label: {
                Image(systemName: "shuffle").font(.title3)
                    .foregroundStyle(model.shuffle ? Color.accentColor : Color.secondary)
            }
            .accessibilityLabel(Text("Shuffle"))
            .accessibilityValue(model.shuffle ? Text("On") : Text("Off"))
            .accessibilityAddTraits(model.shuffle ? .isSelected : [])
            Spacer()
            Button { model.previous() } label: { Image(systemName: "backward.fill").font(.title2) }
                .accessibilityLabel(Text("Previous"))
            Spacer()
            Button { model.togglePlay() } label: {
                Image(systemName: model.playing ? "pause.circle.fill" : "play.circle.fill").font(.system(size: 56))
            }
            .accessibilityLabel(model.playing ? Text("Pause") : Text("Play"))
            Spacer()
            Button { model.next() } label: { Image(systemName: "forward.fill").font(.title2) }
                .accessibilityLabel(Text("Next"))
            Spacer()
            Button { model.cycleRepeat() } label: {
                Image(systemName: model.repeatMode == .one ? "repeat.1" : "repeat").font(.title3)
                    .foregroundStyle(model.repeatMode == .off ? Color.secondary : Color.accentColor)
            }
            .accessibilityLabel(repeatLabel)
            .accessibilityAddTraits(model.repeatMode == .off ? [] : .isSelected)
        }
        .buttonStyle(.borderless)                 // each button its own tap target inside the list row
        .padding(.horizontal, 4)
    }

    private var repeatLabel: Text {
        switch model.repeatMode {
        case .off: return Text("Repeat: Off")
        case .all: return Text("Repeat All")
        case .one: return Text("Repeat One")
        }
    }
}

/// A song in 播放队列 or 离线收藏; a tap plays it.
private struct SongRow: View {
    @ObservedObject var model: OfflinePlayerModel
    let item: Item
    let isCurrent: Bool
    let action: () -> Void
    @ScaledMetric(relativeTo: .body) private var thumb: CGFloat = 44

    var body: some View {
        Button(action: action) {
            HStack(spacing: 12) {
                Artwork(url: model.artworkURL(item), seed: item.id, title: item.title)
                    .frame(width: thumb, height: thumb)
                VStack(alignment: .leading, spacing: 2) {
                    Text(item.title).font(.body).foregroundStyle(isCurrent ? Color.accentColor : Color.primary).lineLimit(2)
                    if !item.artist.isEmpty {
                        Text(item.artist).font(.subheadline).foregroundStyle(.secondary).lineLimit(1)
                    }
                }
                Spacer(minLength: 0)
                if isCurrent {
                    Image(systemName: model.playing ? "speaker.wave.2.fill" : "speaker.fill").foregroundStyle(Color.accentColor)
                }
            }
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .accessibilityElement(children: .combine)
        .accessibilityValue(isCurrent ? Text("Now Playing") : Text(verbatim: ""))
        .accessibilityHint(Text("Plays this song"))
        .accessibilityAddTraits(.isButton)
    }
}

/// The cached cover, or a tile drawn from the song (a colour from its id, the title's first letter).
private struct Artwork: View {
    let url: URL?
    let seed: String
    let title: String

    private static let images = NSCache<NSURL, UIImage>()

    private var image: UIImage? {
        guard let url else { return nil }
        if let hit = Self.images.object(forKey: url as NSURL) { return hit }
        guard let img = UIImage(contentsOfFile: url.path) else { return nil }
        Self.images.setObject(img, forKey: url as NSURL)
        return img
    }

    var body: some View {
        GeometryReader { geo in
            Group {
                if let image {
                    Image(uiImage: image).resizable().scaledToFill()
                } else {
                    ZStack {
                        LinearGradient(colors: [Self.color(seed, 0.55), Self.color(seed, 0.35)], startPoint: .topLeading, endPoint: .bottomTrailing)
                        if let letter = title.trimmingCharacters(in: .whitespaces).first {
                            Text(String(letter).uppercased())
                                .font(.system(size: geo.size.width * 0.42, weight: .semibold, design: .rounded))
                                .foregroundStyle(.white.opacity(0.9))
                        } else {
                            Image(systemName: "music.note").font(.system(size: geo.size.width * 0.4)).foregroundStyle(.white.opacity(0.9))
                        }
                    }
                }
            }
            .frame(width: geo.size.width, height: geo.size.height)
            .clipShape(RoundedRectangle(cornerRadius: max(4, geo.size.width * 0.08), style: .continuous))
        }
        .accessibilityHidden(true)
    }

    /// A stable hue per song (FNV-1a of its id), never random between launches.
    static func color(_ seed: String, _ brightness: Double) -> Color {
        var h: UInt32 = 2_166_136_261
        for b in seed.utf8 { h = (h ^ UInt32(b)) &* 16_777_619 }
        return Color(hue: Double(h % 360) / 360, saturation: 0.5, brightness: brightness + 0.25)
    }
}
