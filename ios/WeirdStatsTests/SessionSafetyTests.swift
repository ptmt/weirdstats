import XCTest
@testable import WeirdStats

@MainActor final class SessionSafetyTests: XCTestCase {
    func testLogoutDuringOAuthDoesNotExchangeOrSaveLateGrant() async {
        let gate = AsyncGate<String>()
        var saved = false
        var exchanged = false
        let model = AppModel(tokenProvider: { "test-only" }, authStart: { _ in await gate.wait() }, grantExchange: { _, _ in
            exchanged = true
            return Self.session()
        }, saveToken: { _ in saved = true }, clearToken: {})
        let task = Task { await model.signIn() }
        await gate.started()
        model.signOut()
        await gate.resolve("test-grant")
        await task.value
        XCTAssertFalse(model.isSignedIn)
        XCTAssertFalse(model.isAuthenticating)
        XCTAssertFalse(exchanged)
        XCTAssertFalse(saved)
    }

    func testLogoutDuringGrantExchangeDoesNotSaveLateCredentials() async {
        let gate = AsyncGate<MobileSession>()
        var saved = false
        let model = AppModel(tokenProvider: { "test-only" }, authStart: { _ in "test-grant" }, grantExchange: { _, _ in await gate.wait() }, saveToken: { _ in saved = true }, clearToken: {})
        let task = Task { await model.signIn() }
        await gate.started()
        model.signOut()
        await gate.resolve(Self.session())
        await task.value
        XCTAssertFalse(saved)
        XCTAssertFalse(model.isSignedIn)
        XCTAssertTrue(model.errorMessage.isEmpty)
    }

    func testReplacementSessionInvalidatesOldRefresh() async {
        let config = URLSessionConfiguration.ephemeral
        config.protocolClasses = [HeldProtocol.self]
        HeldProtocol.requests = []
        let model = AppModel(apiClient: APIClient(session: URLSession(configuration: config)), tokenProvider: { "test-only" }, authStart: { _ in "grant" }, grantExchange: { _, _ in Self.session() }, saveToken: { _ in }, clearToken: {})
        let refresh = Task { await model.refresh() }
        while HeldProtocol.requests.count < 2 { await Task.yield() }
        let oldRequests = HeldProtocol.requests
        let reconnect = Task { await model.signIn() }
        for _ in 0..<2000 { if HeldProtocol.requests.count >= 4 { break }; await Task.yield() }
        XCTAssertEqual(HeldProtocol.requests.count, 4, "replacement must start its own refresh")
        for request in oldRequests { request.finish(status: 500, body: "obsolete session failure") }
        await refresh.value
        XCTAssertTrue(model.errorMessage.isEmpty)
        XCTAssertTrue(model.isLoading)
        for request in HeldProtocol.requests.dropFirst(2) { request.finish(status: 500, body: "current failure") }
        await reconnect.value
        XCTAssertFalse(model.isLoading)
        XCTAssertTrue(model.errorMessage.contains("current failure"))
    }

    static func session() -> MobileSession {
        try! JSONDecoder().decode(MobileSession.self, from: Data(#"{"access_token":"test-replacement","token_type":"Bearer","expires_at":9999999999,"athlete":{"id":1,"name":"Test"}}"#.utf8))
    }
}

actor AsyncGate<T> {
    private var continuation: CheckedContinuation<T, Never>?
    func wait() async -> T { await withCheckedContinuation { continuation = $0 } }
    func started() async { while continuation == nil { await Task.yield() } }
    func resolve(_ value: T) { continuation?.resume(returning: value); continuation = nil }
}

final class HeldProtocol: URLProtocol {
    static var requests: [HeldProtocol] = []
    override class func canInit(with request: URLRequest) -> Bool { true }
    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }
    override func startLoading() { DispatchQueue.main.async { Self.requests.append(self) } }
    override func stopLoading() {}
    func finish(status: Int, body: String) {
        client?.urlProtocol(self, didReceive: HTTPURLResponse(url: request.url!, statusCode: status, httpVersion: nil, headerFields: nil)!, cacheStoragePolicy: .notAllowed)
        client?.urlProtocol(self, didLoad: Data(body.utf8))
        client?.urlProtocolDidFinishLoading(self)
    }
}
