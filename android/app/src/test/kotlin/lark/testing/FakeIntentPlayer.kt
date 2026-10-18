package lark.testing

import lark.car.IntentPlayer
import lark.car.IntentResult

/** Records what an intent asks of the player (iOS `FakeIntentPlayer`). `calls` holds the playing calls, `log` everything in order. */
class FakeIntentPlayer : IntentPlayer {
    val calls = mutableListOf<String>()
    val log = mutableListOf<String>()
    var blocker: IntentResult? = null
    var startsPlaying = true
    override var playing = false
    override var failureText: String? = null

    override fun intentBlocker(resume: Boolean): IntentResult? { log.add("intentBlocker"); return blocker }
    override fun prepareForPlayback() { log.add("prepareForPlayback") }
    override suspend fun shuffleFavorites() { calls.add("shuffleFavorites"); log.add("shuffleFavorites"); playing = startsPlaying }
    override suspend fun resumeOrShuffleFavorites() {
        calls.add("resumeOrShuffleFavorites"); log.add("resumeOrShuffleFavorites"); playing = startsPlaying
    }
}
