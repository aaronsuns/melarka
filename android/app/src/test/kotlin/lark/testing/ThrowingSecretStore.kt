package lark.testing

import lark.auth.SecretStore

/** A [MemorySecretStore] whose writes fail on demand (a full disk, a lost Keystore key). */
class ThrowingSecretStore : SecretStore {
    private val inner = MemorySecretStore()
    var failSets = false
    override fun get(key: String): String? = inner.get(key)
    override fun set(key: String, value: String?) {
        if (failSets) throw java.io.IOException("disk full")
        inner.set(key, value)
    }
}
