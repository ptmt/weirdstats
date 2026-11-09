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

    @Published var history: MobileHistory?
    @Published var historyError = ""
    @Published var isLoadingHistory = false
    @Published var years: [Int] = [Calendar.current.component(.year, from: Date())]
    private var historyRequest = UUID()
    private let apiClient: APIClient
    private let tokenProvider: (() -> String?)?
    private var accessToken: String? { tokenProvider?() ?? tokenStore.readToken() }
    #if DEBUG
    private var isEmptyUITest: Bool { ProcessInfo.processInfo.arguments.contains("--ui-test-empty") }
    #else
    private var isEmptyUITest: Bool { false }
    #endif
    private let authBroker = AuthSessionBroker()
    private let tokenStore = KeychainTokenStore()
    private let defaults = UserDefaults.standard
    private let serverURLKey = "weirdstats.server-url"
    private let legacyServerURLKey = "weirdstats.prototype.server-url"
    private var didBootstrap = false
    private var sessionGeneration = UUID()

    private let authStart: ((URL) async throws -> String)?
    private let grantExchange: ((URL, String) async throws -> MobileSession)?
    private let saveToken: ((String) throws -> Void)?
    private let clearToken: (() -> Void)?
    init(apiClient: APIClient = APIClient(), tokenProvider: (() -> String?)? = nil,
         authStart: ((URL) async throws -> String)? = nil,
         grantExchange: ((URL, String) async throws -> MobileSession)? = nil,
         saveToken: ((String) throws -> Void)? = nil, clearToken: (() -> Void)? = nil) {
        self.authStart = authStart
        self.grantExchange = grantExchange
        self.saveToken = saveToken
        self.clearToken = clearToken
        self.apiClient = apiClient
        #if DEBUG
        let arguments = ProcessInfo.processInfo.arguments
        if let index = arguments.firstIndex(of: "--ui-test-token"), arguments.indices.contains(index + 1) {
            let testToken = arguments[index + 1]
            self.tokenProvider = { testToken }
        } else { self.tokenProvider = tokenProvider }
        #else
        self.tokenProvider = tokenProvider
        #endif
        let storedServerURL = defaults.string(forKey: serverURLKey)
        let legacyServerURL = defaults.string(forKey: legacyServerURLKey)
        if storedServerURL == nil, let legacyServerURL {
            defaults.set(legacyServerURL, forKey: serverURLKey)
        }
        serverURLText = storedServerURL ?? legacyServerURL ?? "https://weirdstats.com"
        #if DEBUG
        if let index = arguments.firstIndex(of: "--ui-test-server"), arguments.indices.contains(index + 1) { serverURLText = arguments[index + 1] }
        #endif
        hasSession = accessToken != nil || isEmptyUITest
    }

    var isSignedIn: Bool {
        hasSession
    }

    func bootstrap() async {
        guard !didBootstrap else {
            return
        }
        didBootstrap = true
        guard accessToken != nil else {
            return
        }
        await refresh()
    }

    private var authRequest = UUID()
    func signIn() async {
        guard !isAuthenticating else { return }
        let generation = sessionGeneration
        let requestID = UUID()
        authRequest = requestID
        defer { if authRequest == requestID { isAuthenticating = false } }
        guard let baseURL = normalizedBaseURL() else {
            errorMessage = "Enter a valid backend URL."
            return
        }
        isAuthenticating = true
        errorMessage = ""
        defaults.set(baseURL.absoluteString, forKey: serverURLKey)

        do {
            let grant: String
            if let authStart { grant = try await authStart(baseURL) }
            else { grant = try await authBroker.start(baseURL: baseURL) }
            guard generation == sessionGeneration, requestID == authRequest, !Task.isCancelled else { return }
            let session: MobileSession
            if let grantExchange { session = try await grantExchange(baseURL, grant) }
            else { session = try await apiClient.exchangeGrant(baseURL: baseURL, grant: grant) }
            guard generation == sessionGeneration, requestID == authRequest, !Task.isCancelled else { return }
            if let saveToken { try saveToken(session.accessToken) }
            else { try tokenStore.save(token: session.accessToken) }
            sessionGeneration = UUID()
            historyRequest = UUID()
            isLoading = false
            isLoadingHistory = false
            activities = []
            history = nil
            historyError = ""
            nextCursor = nil
            syncStatus = nil
            hasSession = true
            athleteName = session.athlete.name
            await refresh()
        } catch {
            guard generation == sessionGeneration, requestID == authRequest, !Task.isCancelled else { return }
            errorMessage = error.localizedDescription
        }
    }

    func refresh() async {
        guard !isLoading, !isEmptyUITest else { return }
        guard let baseURL = normalizedBaseURL(), let token = accessToken else {
            athleteName = ""
            activities = []
            hasSession = false
            return
        }
        let generation = sessionGeneration
        isLoading = true
        defer { if generation == sessionGeneration { isLoading = false } }
        errorMessage = ""

        do {
            async let me = apiClient.fetchMe(baseURL: baseURL, accessToken: token)
            async let recent = apiClient.fetchActivities(baseURL: baseURL, accessToken: token, limit: 20)
            let (profile, feed) = try await (me, recent)
            guard generation == sessionGeneration else { return }
            athleteName = profile.athlete.name
            activities = feed.activities
            syncStatus = feed.sync
            nextCursor = feed.nextCursor
        } catch {
            guard generation == sessionGeneration else { return }
            errorMessage = error.localizedDescription
            if case APIError.server(401, _) = error {
                signOut()
                errorMessage = "Your session expired. Please sign in again."
            }
        }

        isLoading = false
    }

    func signOut() {
        sessionGeneration = UUID()
        authRequest = UUID()
        authBroker.cancel()
        isAuthenticating = false
        isLoading = false
        years = [Calendar.current.component(.year, from: Date())]
        defaults.removeObject(forKey: "weirdstats.selected-year")
        if let clearToken { clearToken() } else { tokenStore.clear() }
        athleteName = ""
        activities = []
        errorMessage = ""
        syncStatus = nil
        nextCursor = nil
        historyRequest = UUID()
        history = nil
        historyError = ""
        isLoadingHistory = false
        defaults.removeObject(forKey: "weirdstats.selected-tab")
        hasSession = false
    }

    func loadHistory(year: Int) async {
        guard !isEmptyUITest, let baseURL = normalizedBaseURL(), let token = accessToken else { return }
        let requestID = UUID()
        historyRequest = requestID
        isLoadingHistory = true
        historyError = ""
        history = nil
        defer { if historyRequest == requestID { isLoadingHistory = false } }
        do {
            let feed = try await apiClient.fetchActivities(baseURL: baseURL, accessToken: token, limit: 1, year: year)
            guard historyRequest == requestID, isSignedIn else { return }
            guard let value = feed.history else {
                historyError = "This server does not provide activity history yet. Update the backend to view your year."
                return
            }
            history = value
            years = Array(Set((feed.years ?? []) + [year, Calendar.current.component(.year, from: Date())])).sorted(by: >)
        } catch {
            guard historyRequest == requestID else { return }
            historyError = error.localizedDescription
        }
    }

    func loadMore() async {
        guard !isLoading, let before = nextCursor,
              let baseURL = normalizedBaseURL(), let token = accessToken else { return }
        let generation = sessionGeneration
        isLoading = true
        errorMessage = ""
        defer { if generation == sessionGeneration { isLoading = false } }
        do {
            let feed = try await apiClient.fetchActivities(baseURL: baseURL, accessToken: token, limit: 20, before: before)
            guard generation == sessionGeneration else { return }
            let existing = Set(activities.map(\.id))
            activities.append(contentsOf: feed.activities.filter { !existing.contains($0.id) })
            nextCursor = feed.nextCursor
            syncStatus = feed.sync
        } catch { if generation == sessionGeneration { errorMessage = error.localizedDescription } }
    }

    func retryActivities() async {
        guard let baseURL = normalizedBaseURL(), let token = accessToken else { return }
        let generation = sessionGeneration
        errorMessage = ""
        do {
            let status = try await apiClient.retryActivities(baseURL: baseURL, accessToken: token)
            guard generation == sessionGeneration else { return }
            syncStatus = status
        } catch { if generation == sessionGeneration { errorMessage = error.localizedDescription } }
    }

    var detailSessionID: UUID { sessionGeneration }
    func fetchDetail(id: Int64) async throws -> MobileActivityDetail {
        guard isSignedIn, let baseURL = normalizedBaseURL(), let token = accessToken else { throw APIError.server(401, "Sign in to view this activity.") }
        let generation = sessionGeneration
        let result = try await apiClient.fetchActivityDetail(baseURL: baseURL, accessToken: token, id: id)
        guard generation == sessionGeneration, isSignedIn, !Task.isCancelled else { throw CancellationError() }
        return result
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
