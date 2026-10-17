package lark.testing

import lark.auth.SecretStore

class MemorySecretStore : SecretStore {
    private val values = mutableMapOf<String, String>()
    override fun get(key: String): String? = values[key]
    override fun set(key: String, value: String?) { if (value == null) values.remove(key) else values[key] = value }
}
