import Foundation
import Security

final class KeychainTokenStore {
    private let service = "com.ptmt.weirdstats"
    private let legacyService = "com.ptmt.weirdstats.prototype"
    private let account = "mobile-access-token"

    func save(token: String) throws {
        let data = Data(token.utf8)
        deleteToken(service: service)
        deleteToken(service: legacyService)
        let attributes: [String: Any] = [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: service,
            kSecAttrAccount as String: account,
            kSecValueData as String: data,
        ]
        let status = SecItemAdd(attributes as CFDictionary, nil)
        guard status == errSecSuccess else {
            throw KeychainError.saveFailed(status)
        }
    }

    func readToken() -> String? {
        readToken(service: service) ?? readToken(service: legacyService)
    }

    func clear() {
        deleteToken(service: service)
        deleteToken(service: legacyService)
    }

    private func readToken(service: String) -> String? {
        let query: [String: Any] = [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: service,
            kSecAttrAccount as String: account,
            kSecReturnData as String: true,
            kSecMatchLimit as String: kSecMatchLimitOne,
        ]
        var result: CFTypeRef?
        let status = SecItemCopyMatching(query as CFDictionary, &result)
        guard status == errSecSuccess, let data = result as? Data else {
            return nil
        }
        return String(data: data, encoding: .utf8)
    }

    private func deleteToken(service: String) {
        let query: [String: Any] = [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: service,
            kSecAttrAccount as String: account,
        ]
        SecItemDelete(query as CFDictionary)
    }
}

enum KeychainError: LocalizedError {
    case saveFailed(OSStatus)

    var errorDescription: String? {
        switch self {
        case let .saveFailed(status):
            return "Failed to save the backend session in Keychain (\(status))."
        }
    }
}
