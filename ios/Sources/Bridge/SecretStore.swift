import Foundation
import Security

/// Small string secrets by key. Production is the Keychain; tests use memory.
protocol SecretStore {
    func get(_ key: String) -> String?
    func set(_ key: String, _ value: String?)   // nil deletes
}

/// Generic-password items under one service, readable after the first unlock so the car path
/// can use the token while the phone is locked.
final class KeychainSecretStore: SecretStore {
    let service: String
    /// The OSStatus of the last Keychain call, for tests and diagnostics. Never contains the secret.
    private(set) var lastStatus: OSStatus = errSecSuccess

    init(service: String) { self.service = service }

    private func query(_ key: String) -> [String: Any] {
        [kSecClass as String: kSecClassGenericPassword,
         kSecAttrService as String: service,
         kSecAttrAccount as String: key]
    }

    func get(_ key: String) -> String? {
        var q = query(key)
        q[kSecReturnData as String] = true
        q[kSecMatchLimit as String] = kSecMatchLimitOne
        var out: CFTypeRef?
        lastStatus = SecItemCopyMatching(q as CFDictionary, &out)
        guard lastStatus == errSecSuccess, let data = out as? Data else { return nil }
        return String(data: data, encoding: .utf8)
    }

    func set(_ key: String, _ value: String?) {
        guard let value else {
            let s = SecItemDelete(query(key) as CFDictionary)
            lastStatus = s == errSecItemNotFound ? errSecSuccess : s
            return
        }
        let attrs: [String: Any] = [kSecValueData as String: Data(value.utf8),
                                    kSecAttrAccessible as String: kSecAttrAccessibleAfterFirstUnlock]
        lastStatus = SecItemUpdate(query(key) as CFDictionary, attrs as CFDictionary)
        if lastStatus == errSecItemNotFound {
            lastStatus = SecItemAdd(query(key).merging(attrs) { $1 } as CFDictionary, nil)
        }
    }
}

final class MemorySecretStore: SecretStore {
    private var values: [String: String] = [:]
    func get(_ key: String) -> String? { values[key] }
    func set(_ key: String, _ value: String?) { values[key] = value }
}
