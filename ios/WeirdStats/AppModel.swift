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
    @Published var syncStatus: MobileSyncStatus?
    @Published var nextCursor: String?
    @Published private var hasSession = false

    private let apiClient = APIClient()
    private let authBroker = AuthSessionBroker()
    private let tokenStore = KeychainTokenStore()
    private let defaults = UserDefaults.standard
    private let serverURLKey = "weirdstats.server-url"
    private let legacyServerURLKey = "weirdstats.prototype.server-url"
    private var didBootstrap = false

    init() {
        let storedServerURL = defaults.string(forKey: serverURLKey)
        let legacyServerURL = defaults.string(forKey: legacyServerURLKey)
        if storedServerURL == nil, let legacyServerURL {
            defaults.set(legacyServerURL, forKey: serverURLKey)
        }
        serverURLText = storedServerURL ?? legacyServerURL ?? "https://weirdstats.com"
        hasSession = tokenStore.readToken() != nil
    }

    var isSignedIn: Bool {
        hasSession
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
            hasSession = true
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
            hasSession = false
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
            syncStatus = feed.sync
            nextCursor = feed.nextCursor
        } catch {
            errorMessage = error.localizedDescription
            if case APIError.server(401, _) = error {
                signOut()
                errorMessage = "Your session expired. Please sign in again."
            }
        }

        isLoading = false
    }

    func signOut() {
        tokenStore.clear()
        athleteName = ""
        activities = []
        errorMessage = ""
        syncStatus = nil
        nextCursor = nil
        hasSession = false
    }

    func loadMore() async {
        guard !isLoading, let before = nextCursor,
              let baseURL = normalizedBaseURL(), let token = tokenStore.readToken() else { return }
        isLoading = true
        defer { isLoading = false }
        do {
            let feed = try await apiClient.fetchActivities(baseURL: baseURL, accessToken: token, limit: 20, before: before)
            let existing = Set(activities.map(\.id))
            activities.append(contentsOf: feed.activities.filter { !existing.contains($0.id) })
            nextCursor = feed.nextCursor
            syncStatus = feed.sync
        } catch { errorMessage = error.localizedDescription }
    }

    func retryActivities() async {
        guard let baseURL = normalizedBaseURL(), let token = tokenStore.readToken() else { return }
        do {
            syncStatus = try await apiClient.retryActivities(baseURL: baseURL, accessToken: token)
        } catch { errorMessage = error.localizedDescription }
    }

    func handleOpenURL(_ url: URL) {
        authBroker.handleIncomingURL(url)
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
