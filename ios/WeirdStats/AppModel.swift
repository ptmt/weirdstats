import Foundation
import SwiftUI

struct ActivityRefreshFailure: Equatable {
    enum Operation: Equatable { case refresh, loadMore, sync }
    let operation: Operation
    let isOffline: Bool

    init(error: Error, operation: Operation) {
        self.operation = operation
        let error = error as NSError
        isOffline = error.domain == NSURLErrorDomain && error.code == NSURLErrorNotConnectedToInternet
    }
    var title: String {
        if isOffline { return "You’re offline" }
        return operation == .sync ? "Couldn’t sync with Strava" : "Couldn’t load activities"
    }
    var detail: String {
        if isOffline { return operation == .sync ? "Connect to the internet to sync with Strava." : "Connect to the internet to load your activities." }
        return operation == .sync ? "Your sync request didn’t complete. Please try again." : "Your activities couldn’t be refreshed. Please try again."
    }
    var systemImage: String { isOffline ? "wifi.slash" : "arrow.clockwise" }
    var retryTitle: String { operation == .sync ? "Retry sync" : "Try again" }
}

@MainActor
final class AppModel: ObservableObject {
    @Published var serverURLText: String
    @Published var athleteName = ""
    @Published var activities: [MobileActivity] = []
    @Published var isSyncing = false
    private var syncPoll: Task<Void, Never>?
    @Published var isLoading = false
    @Published var isAuthenticating = false
    @Published var errorMessage = ""
    @Published private(set) var activityFailure: ActivityRefreshFailure?
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
            activityFailure = nil
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
            #if DEBUG
            if ProcessInfo.processInfo.arguments.contains("--ui-test-offline-after-load"), !activities.isEmpty {
                throw URLError(.notConnectedToInternet)
            }
            #endif
            async let me = apiClient.fetchMe(baseURL: baseURL, accessToken: token)
            async let recent = apiClient.fetchActivities(baseURL: baseURL, accessToken: token, limit: 20)
            let (profile, feed) = try await (me, recent)
            guard generation == sessionGeneration else { return }
            athleteName = profile.athlete.name
            activities = feed.activities
            syncStatus = feed.sync
            nextCursor = feed.nextCursor
            if activityFailure?.operation != .sync { activityFailure = nil }
        } catch {
            guard generation == sessionGeneration else { return }
            errorMessage = error.localizedDescription
            if activityFailure?.operation != .sync {
                activityFailure = ActivityRefreshFailure(error: error, operation: .refresh)
            }
            if case APIError.server(401, _) = error {
                signOut()
                errorMessage = "Your session expired. Please sign in again."
            }
        }

        isLoading = false
    }

    func syncWithStrava() async {
        guard !isSyncing, !isLoading, !isEmptyUITest, let baseURL = normalizedBaseURL(), let token = accessToken else { return }
        let generation = sessionGeneration
        isSyncing = true
        defer { if generation == sessionGeneration { isSyncing = false } }
        errorMessage = ""
        do {
            let status = try await apiClient.syncLatest(baseURL: baseURL, accessToken: token)
            guard generation == sessionGeneration, !Task.isCancelled else { return }
            syncStatus = status
            activityFailure = nil
            await refresh()
            syncPoll?.cancel()
            syncPoll = Task { [weak self] in
                // Only GET the saved feed after the explicit POST; never auto-enqueue.
                for _ in 0..<12 {
                    guard let self, generation == self.sessionGeneration, self.syncStatus?.isImporting == true else { return }
                    do { try await Task.sleep(nanoseconds: 5_000_000_000) } catch { return }
                    guard !Task.isCancelled, generation == self.sessionGeneration else { return }
                    await self.refresh()
                }
            }
        } catch {
            guard generation == sessionGeneration else { return }
            errorMessage = error.localizedDescription
            activityFailure = ActivityRefreshFailure(error: error, operation: .sync)
        }
    }

    func signOut() {
        syncPoll?.cancel()
        syncPoll = nil
        isSyncing = false
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
        activityFailure = nil
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
            if activityFailure?.operation == .loadMore { activityFailure = nil }
        } catch {
            if generation == sessionGeneration {
                errorMessage = error.localizedDescription
                if activityFailure?.operation != .sync { activityFailure = ActivityRefreshFailure(error: error, operation: .loadMore) }
            }
        }
    }

    func retryActivityFailure() async {
        guard !isLoading, !isSyncing, let failure = activityFailure else { return }
        switch failure.operation {
        case .refresh: await refresh()
        case .loadMore: await loadMore()
        case .sync: await syncWithStrava()
        }
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
