import AuthenticationServices
import Foundation
import UIKit

final class AuthSessionBroker: NSObject, ASWebAuthenticationPresentationContextProviding {
    private var session: ASWebAuthenticationSession?

    func start(baseURL: URL) async throws -> String {
        var components = URLComponents(url: baseURL.appending(path: "/connect/strava/mobile"), resolvingAgainstBaseURL: false)
        components?.queryItems = [
            URLQueryItem(name: "app_redirect", value: "weirdstats://auth/strava")
        ]
        guard let startURL = components?.url else {
            throw AuthError.invalidStartURL
        }

        let callbackURL = try await withCheckedThrowingContinuation { (continuation: CheckedContinuation<URL, Error>) in
            let authSession = ASWebAuthenticationSession(url: startURL, callbackURLScheme: "weirdstats") { url, error in
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

        let callbackComponents = URLComponents(url: callbackURL, resolvingAgainstBaseURL: false)
        if let error = callbackComponents?.queryItems?.first(where: { $0.name == "error" })?.value, !error.isEmpty {
            throw AuthError.oauthFailure(error)
        }
        guard let grant = callbackComponents?.queryItems?.first(where: { $0.name == "grant" })?.value, !grant.isEmpty else {
            throw AuthError.invalidCallback
        }
        return grant
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

enum AuthError: LocalizedError {
    case invalidStartURL
    case invalidCallback
    case oauthFailure(String)

    var errorDescription: String? {
        switch self {
        case .invalidStartURL:
            return "The backend sign-in URL is invalid."
        case .invalidCallback:
            return "The sign-in callback did not include a grant."
        case let .oauthFailure(message):
            return "Strava sign-in failed: \(message)"
        }
    }
}
