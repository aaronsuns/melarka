package lark.auth

/** Small string secrets by key. Production is the Android Keystore ([KeystoreSecretStore]); tests use memory. */
interface SecretStore {
    fun get(key: String): String?
    fun set(key: String, value: String?)   // null deletes
}
