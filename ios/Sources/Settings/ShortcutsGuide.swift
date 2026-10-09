import SwiftUI

/// The guide to starting Melarka when the car connects: the car's own play command, and the Shortcuts Bluetooth
/// automation that runs Shuffle Favorites. The text, English and Chinese, is the `shortcutsGuide.text` entry in
/// Localizable.xcstrings; it names the Shortcuts app's own labels, so each language quotes iOS in that language.
enum ShortcutsGuide {
    static var text: String { String(localized: "shortcutsGuide.text", comment: "The car autoplay guide in the settings sheet") }
}

/// The guide as a page in the settings sheet.
struct ShortcutsGuideView: View {
    var body: some View {
        ScrollView {
            Text(ShortcutsGuide.text)
                .frame(maxWidth: .infinity, alignment: .leading)
                .textSelection(.enabled)
                .padding()
        }
        .navigationTitle("Car Autoplay")
        .navigationBarTitleDisplayMode(.inline)
    }
}
