import SwiftUI

/// The Chinese guide to starting Melarka when the car connects: the car's own play command, and the Shortcuts
/// Bluetooth automation that runs 随机播放收藏.
enum ShortcutsGuide {
    static let text = """
连上车载蓝牙就自动播放 Melarka 收藏

方式一（大多数车不用设置）：用 Melarka 播放过音乐后，连上车载蓝牙时，车机通常会发送"播放"命令，iPhone 会把它交给最后播放的 App——Melarka 会在后台直接从缓存播放上次的队列；队列为空时随机播放收藏。注意：如果在多任务界面里把 Melarka 划掉，iOS 可能不会再唤醒它。

方式二（快捷指令自动化，最可靠）：
1. 打开"快捷指令" App，点底部的"自动化"。
2. 点右上角"+"，选择"蓝牙"。
3. 在"设备"里选你的车（例如 "My Car"），勾选"已连接"。
4. 选择"立即运行"，并关闭"运行时通知"（可选）。
5. 点"下一步"（如果先看到"新建空白自动化"，点它，再点"添加操作"）→ 搜索"Melarka" → 选择"随机播放收藏"（想接着上次听，就选"继续播放"）。
6. 点"完成"。以后一连上车载蓝牙，Melarka 就会在后台开始播放，不用解锁、不用点。

小贴士：收藏的歌会在连上 Wi‑Fi 时自动缓存到手机，没有网络也能播放；缓存大小在"设置 → 离线缓存"里调整。
"""
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
        .navigationTitle("车载自动播放")
        .navigationBarTitleDisplayMode(.inline)
    }
}
