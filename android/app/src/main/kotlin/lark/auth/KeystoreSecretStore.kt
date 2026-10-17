package lark.auth

import android.security.keystore.KeyGenParameterSpec
import android.security.keystore.KeyProperties
import org.json.JSONObject
import java.io.File
import java.security.KeyStore
import javax.crypto.Cipher
import javax.crypto.KeyGenerator
import javax.crypto.SecretKey
import javax.crypto.spec.GCMParameterSpec

/**
 * Secrets in one small file: a JSON map, encrypted with AES/GCM under a key that never leaves the Android Keystore.
 * The file is `iv (12 bytes) + ciphertext`, rewritten atomically on every change. A file that cannot be decrypted
 * (the key is gone, the file is damaged) reads as empty: the user signs in again, nothing crashes.
 * Not unit-tested under Robolectric (it has no AndroidKeyStore); the logic above it is tested through [SecretStore] fakes.
 */
class KeystoreSecretStore(private val file: File, private val alias: String = "lark_secrets") : SecretStore {
    private val lock = Any()
    private var cache: MutableMap<String, String>? = null

    override fun get(key: String): String? = synchronized(lock) { load()[key] }

    override fun set(key: String, value: String?) { synchronized(lock) {
        val m = load()
        if (value == null) {
            if (m.remove(key) == null) return
            // Erasing must not fail: if the rewrite does, delete the ciphertext file (the other secrets go with it).
            try { save(m) } catch (e: Exception) { file.delete(); File(file.path + ".tmp").delete() }
        } else {
            m[key] = value
            save(m)
        }
    } }

    private fun load(): MutableMap<String, String> {
        cache?.let { return it }
        val m = mutableMapOf<String, String>()
        try {
            if (file.exists()) {
                val raw = file.readBytes()
                val c = Cipher.getInstance(TRANSFORM)
                c.init(Cipher.DECRYPT_MODE, key(), GCMParameterSpec(TAG_BITS, raw, 0, IV_BYTES))
                val o = JSONObject(String(c.doFinal(raw, IV_BYTES, raw.size - IV_BYTES), Charsets.UTF_8))
                o.keys().forEach { m[it] = o.getString(it) }
            }
        } catch (_: Exception) { m.clear() }
        cache = m
        return m
    }

    private fun save(m: Map<String, String>) {
        val c = Cipher.getInstance(TRANSFORM)
        c.init(Cipher.ENCRYPT_MODE, key())   // the Keystore picks a fresh IV for every encryption
        val ct = c.doFinal(JSONObject(m).toString().toByteArray(Charsets.UTF_8))
        file.parentFile?.mkdirs()
        val tmp = File(file.path + ".tmp")
        tmp.writeBytes(c.iv + ct)
        if (!tmp.renameTo(file)) { tmp.delete(); throw java.io.IOException("could not write the secrets file") }
    }

    private fun key(): SecretKey {
        val ks = KeyStore.getInstance(PROVIDER).apply { load(null) }
        (ks.getKey(alias, null) as? SecretKey)?.let { return it }
        val g = KeyGenerator.getInstance(KeyProperties.KEY_ALGORITHM_AES, PROVIDER)
        g.init(KeyGenParameterSpec.Builder(alias, KeyProperties.PURPOSE_ENCRYPT or KeyProperties.PURPOSE_DECRYPT)
            .setBlockModes(KeyProperties.BLOCK_MODE_GCM)
            .setEncryptionPaddings(KeyProperties.ENCRYPTION_PADDING_NONE)
            .setKeySize(256)
            .build())
        return g.generateKey()
    }

    private companion object {
        const val PROVIDER = "AndroidKeyStore"
        const val TRANSFORM = "AES/GCM/NoPadding"
        const val IV_BYTES = 12
        const val TAG_BITS = 128
    }
}
