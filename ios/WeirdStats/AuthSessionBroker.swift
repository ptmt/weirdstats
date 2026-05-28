import AuthenticationServices
import Foundation
import UIKit

@MainActor
final class AuthSessionBroker: NSObject, ASWebAuthenticationPresentationContextProviding {
    private var session: ASWebAuthenticationSession?
    private var pendingExternalContinuation: CheckedContinuation<URL, Error>?
    private var foregroundObserver: NSObjectProtocol?
    private var foregroundCancelTask: Task<Void, Never>?

    func start(baseURL: URL) async throws -> String {
        let launch = try await fetchLaunch(baseURL: baseURL)
        let callbackURL: URL
        if let appOAuthURL = URL(string: launch.appOAuthURL), UIApplication.shared.canOpenURL(appOAuthURL) {
            callbackURL = try await startWithStravaApp(appOAuthURL)
        } else {
            callbackURL = try await startWebAuth(url: launch.webOAuthURL, callbackScheme: launch.callbackScheme)
        }

        return try extractGrant(from: callbackURL)
    }

    func handleIncomingURL(_ url: URL) {
        guard let continuation = pendingExternalContinuation else {
            return
        }
        foregroundCancelTask?.cancel()
        foregroundCancelTask = nil
        pendingExternalContinuation = nil
        removeForegroundObserver()
        continuation.resume(returning: url)
    }

    private func fetchLaunch(baseURL: URL) async throws -> MobileAuthLaunch {
        var components = URLComponents(url: baseURL.appending(path: "/connect/strava/mobile"), resolvingAgainstBaseURL: false)
        components?.queryItems = [
            URLQueryItem(name: "format", value: "json"),
            URLQueryItem(name: "app_redirect", value: "weirdstats://auth/strava")
        ]
        guard let startURL = components?.url else {
            throw AuthError.invalidStartURL
        }

        let (data, response) = try await URLSession.shared.data(from: startURL)
        guard let httpResponse = response as? HTTPURLResponse else {
            throw AuthError.invalidStartResponse
        }
        guard (200..<300).contains(httpResponse.statusCode) else {
            let message = String(data: data, encoding: .utf8) ?? "request failed"
            throw AuthError.startRequestFailed(message)
        }
        return try JSONDecoder().decode(MobileAuthLaunch.self, from: data)
    }

    private func startWebAuth(url: String, callbackScheme: String) async throws -> URL {
        guard let authURL = URL(string: url) else {
            throw AuthError.invalidStartURL
        }
        return try await withCheckedThrowingContinuation { (continuation: CheckedContinuation<URL, Error>) in
            let authSession = ASWebAuthenticationSession(url: authURL, callbackURLScheme: callbackScheme) { url, error in
                if let error {
                    continuation.resume(throwing: error)
                    return
                }
                guard let url else {
                    continuation.resume(throwing: AuthError.invalidCallback)
                    return
                }
                continuation.resume(returning: url)
            }
            authSession.presentationContextProvider = self
            authSession.prefersEphemeralWebBrowserSession = false
            session = authSession
            authSession.start()
        }
    }

    private func startWithStravaApp(_ url: URL) async throws -> URL {
        removeForegroundObserver()
        foregroundCancelTask?.cancel()
        foregroundObserver = NotificationCenter.default.addObserver(
            forName: UIApplication.didBecomeActiveNotification,
            object: nil,
            queue: .main
        ) { [weak self] _ in
            Task { @MainActor in
                self?.scheduleForegroundCancellation()
            }
        }

        return try await withCheckedThrowingContinuation { continuation in
            pendingExternalContinuation = continuation
            UIApplication.shared.open(url, options: [:]) { [weak self] success in
                Task { @MainActor in
                    guard let self else {
                        return
                    }
                    if success {
                        return
                    }
                    self.foregroundCancelTask?.cancel()
                    self.foregroundCancelTask = nil
                    self.removeForegroundObserver()
                    if let continuation = self.pendingExternalContinuation {
                        self.pendingExternalContinuation = nil
                        continuation.resume(throwing: AuthError.couldNotOpenStrava)
                    }
                }
            }
        }
    }

    private func extractGrant(from callbackURL: URL) throws -> String {
        let callbackComponents = URLComponents(url: callbackURL, resolvingAgainstBaseURL: false)
        if let error = callbackComponents?.queryItems?.first(where: { $0.name == "error" })?.value, !error.isEmpty {
            throw AuthError.oauthFailure(error)
        }
        guard let grant = callbackComponents?.queryItems?.first(where: { $0.name == "grant" })?.value, !grant.isEmpty else {
            throw AuthError.invalidCallback
        }
        return grant
    }

    private func removeForegroundObserver() {
        if let foregroundObserver {
            NotificationCenter.default.removeObserver(foregroundObserver)
            self.foregroundObserver = nil
        }
    }

    private func scheduleForegroundCancellation() {
        foregroundCancelTask?.cancel()
        foregroundCancelTask = Task { @MainActor in
            try? await Task.sleep(for: .seconds(1))
            guard let continuation = pendingExternalContinuation else {
                return
            }
            pendingExternalContinuation = nil
            removeForegroundObserver()
            continuation.resume(throwing: AuthError.cancelled)
        }
    }

    func presentationAnchor(for session: ASWebAuthenticationSession) -> ASPresentationAnchor {
        for scene in UIApplication.shared.connectedScenes {
            guard let windowScene = scene as? UIWindowScene else {
                continue
            }
            if let keyWindow = windowScene.windows.first(where: \.isKeyWindow) {
                return keyWindow
            }
        }
        return ASPresentationAnchor()
    }
}

private struct MobileAuthLaunch: Decodable {
    let appOAuthURL: String
    let webOAuthURL: String
    let callbackScheme: String

    private enum CodingKeys: String, CodingKey {
        case appOAuthURL = "app_oauth_url"
        case webOAuthURL = "web_oauth_url"
        case callbackScheme = "callback_scheme"
    }
}

enum AuthError: LocalizedError {
    case invalidStartURL
    case invalidStartResponse
    case startRequestFailed(String)
    case invalidCallback
    case couldNotOpenStrava
    case cancelled
    case oauthFailure(String)

    var errorDescription: String? {
        switch self {
        case .invalidStartURL:
            return "The backend sign-in URL is invalid."
        case .invalidStartResponse:
            return "The backend did not return a valid auth payload."
        case let .startRequestFailed(message):
            return "Could not start Strava sign-in: \(message)"
        case .invalidCallback:
            return "The sign-in callback did not include a grant."
        case .couldNotOpenStrava:
            return "The Strava app could not be opened."
        case .cancelled:
            return "The Strava sign-in was cancelled."
        case let .oauthFailure(message):
            return "Strava sign-in failed: \(message)"
        }
    }
}
