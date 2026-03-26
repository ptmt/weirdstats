import Foundation
import SwiftUI

@MainActor
final class AppModel: ObservableObject {
    @Published var serverURLText: String
    @Published var athleteName = ""
    @Published var activities: [MobileActivity] = []
    @Published var isLoading = false
    @Published var isAuthenticating = false
    @Published var errorMessage = ""

    private let apiClient = APIClient()
    private let authBroker = AuthSessionBroker()
    private let tokenStore = KeychainTokenStore()
    private let defaults = UserDefaults.standard
    private let serverURLKey = "weirdstats.prototype.server-url"
    private var didBootstrap = false

    init() {
        serverURLText = defaults.string(forKey: serverURLKey) ?? "http://localhost:8080"
    }

    var isSignedIn: Bool {
        !athleteName.isEmpty
    }

    func bootstrap() async {
        guard !didBootstrap else {
            return
        }
        didBootstrap = true
        guard tokenStore.readToken() != nil else {
            return
        }
        await refresh()
    }

    func signIn() async {
        guard let baseURL = normalizedBaseURL() else {
            errorMessage = "Enter a valid backend URL."
            return
        }
        isAuthenticating = true
        errorMessage = ""
        defaults.set(baseURL.absoluteString, forKey: serverURLKey)

        do {
            let grant = try await authBroker.start(baseURL: baseURL)
            let session = try await apiClient.exchangeGrant(baseURL: baseURL, grant: grant)
            try tokenStore.save(token: session.accessToken)
            athleteName = session.athlete.name
            await refresh()
        } catch {
            errorMessage = error.localizedDescription
        }

        isAuthenticating = false
    }

    func refresh() async {
        guard let baseURL = normalizedBaseURL(), let token = tokenStore.readToken() else {
            athleteName = ""
            activities = []
            return
        }
        isLoading = true
        errorMessage = ""

        do {
            async let me = apiClient.fetchMe(baseURL: baseURL, accessToken: token)
            async let recent = apiClient.fetchActivities(baseURL: baseURL, accessToken: token, limit: 20)
            let (profile, feed) = try await (me, recent)
            athleteName = profile.athlete.name
            activities = feed.activities
        } catch {
            errorMessage = error.localizedDescription
            athleteName = ""
            activities = []
            tokenStore.clear()
        }

        isLoading = false
    }

    func signOut() {
        tokenStore.clear()
        athleteName = ""
        activities = []
        errorMessage = ""
    }

    private func normalizedBaseURL() -> URL? {
        let trimmed = serverURLText.trimmingCharacters(in: .whitespacesAndNewlines)
        guard let url = URL(string: trimmed) else {
            return nil
        }
        guard let scheme = url.scheme?.lowercased(), scheme == "http" || scheme == "https" else {
            return nil
        }
        return url
    }
}
