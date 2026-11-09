import XCTest
@testable import WeirdStats

@MainActor final class DetailTests: XCTestCase {
    func testIndependentDetailLoadingFailureRetryAndLogoutSafety() async throws {
        let config = URLSessionConfiguration.ephemeral
        config.protocolClasses = [HeldProtocol.self]
        HeldProtocol.requests = []
        let app = AppModel(apiClient: APIClient(session: URLSession(configuration: config)), tokenProvider: { "test-only" }, clearToken: {})
        let state = ActivityDetailModel()
        let first = Task { await state.load(id: 42, app: app) }
        while HeldProtocol.requests.count < 1 { await Task.yield() }
        XCTAssertTrue(state.isLoading)
        HeldProtocol.requests[0].finish(status: 500, body: "test failure")
        await first.value
        XCTAssertFalse(state.isLoading)
        XCTAssertNil(state.detail)
        XCTAssertFalse(state.errorMessage.isEmpty)
        let retry = Task { await state.load(id: 42, app: app) }
        while HeldProtocol.requests.count < 2 { await Task.yield() }
        HeldProtocol.requests[1].finish(status: 200, body: detailJSON)
        await retry.value
        XCTAssertEqual(state.detail?.activity.id, 42)
        XCTAssertTrue(state.errorMessage.isEmpty)
        let late = Task { await state.load(id: 43, app: app) }
        while HeldProtocol.requests.count < 3 { await Task.yield() }
        XCTAssertNil(state.detail, "never show previous activity under a new selection")
        app.signOut()
        HeldProtocol.requests[2].finish(status: 200, body: detailJSON.replacingOccurrences(of: "\"id\":42", with: "\"id\":43"))
        await late.value
        XCTAssertNil(state.detail)
        XCTAssertTrue(state.errorMessage.isEmpty)
        XCTAssertFalse(state.isLoading)
    }

    func testDetailCancellationNeverPublishesLateResponse() async {
        let config = URLSessionConfiguration.ephemeral
        config.protocolClasses = [HeldProtocol.self]
        HeldProtocol.requests = []
        let app = AppModel(apiClient: APIClient(session: URLSession(configuration: config)), tokenProvider: { "test-only" }, clearToken: {})
        let state = ActivityDetailModel()
        let task = Task { await state.load(id: 42, app: app) }
        while HeldProtocol.requests.count < 1 { await Task.yield() }
        state.cancel()
        HeldProtocol.requests[0].finish(status: 200, body: detailJSON)
        await task.value
        XCTAssertNil(state.detail)
        XCTAssertTrue(state.errorMessage.isEmpty)
        XCTAssertFalse(state.isLoading)
    }

    func testSupersededDetailDoesNotReplaceCurrentSelection() async {
        let config = URLSessionConfiguration.ephemeral
        config.protocolClasses = [HeldProtocol.self]
        HeldProtocol.requests = []
        let app = AppModel(apiClient: APIClient(session: URLSession(configuration: config)), tokenProvider: { "test-only" }, clearToken: {})
        let state = ActivityDetailModel()
        let old = Task { await state.load(id: 42, app: app) }
        while HeldProtocol.requests.count < 1 { await Task.yield() }
        let current = Task { await state.load(id: 43, app: app) }
        while HeldProtocol.requests.count < 2 { await Task.yield() }
        HeldProtocol.requests[1].finish(status: 200, body: detailJSON.replacingOccurrences(of: "\"id\":42", with: "\"id\":43"))
        await current.value
        HeldProtocol.requests[0].finish(status: 200, body: detailJSON)
        await old.value
        XCTAssertEqual(state.detail?.activity.id, 43)
        XCTAssertFalse(state.isLoading)
        XCTAssertTrue(state.errorMessage.isEmpty)
    }

    func testDetailFetchUsesSelectedIDAndBearer() async throws {
        let config = URLSessionConfiguration.ephemeral
        config.protocolClasses = [DetailProtocol.self]
        DetailProtocol.handler = { request in
            XCTAssertEqual(request.url?.path, "/api/mobile/activities/42")
            XCTAssertEqual(request.value(forHTTPHeaderField: "Authorization"), "Bearer test-only")
            return (200, detailJSON)
        }
        let api = APIClient(session: URLSession(configuration: config))
        let detail = try await api.fetchActivityDetail(baseURL: URL(string: "https://test.invalid")!, accessToken: "test-only", id: 42)
        XCTAssertEqual(detail.activity.id, 42)
        DetailProtocol.handler = { _ in (404, "activity not found") }
        do { _ = try await api.fetchActivityDetail(baseURL: URL(string: "https://test.invalid")!, accessToken: "test-only", id: 42); XCTFail("must throw") }
        catch APIError.server(let status, _) { XCTAssertEqual(status, 404) }
        DetailProtocol.handler = { _ in (200, detailJSON.replacingOccurrences(of: "\"id\":42", with: "\"id\":43")) }
        do { _ = try await api.fetchActivityDetail(baseURL: URL(string: "https://test.invalid")!, accessToken: "test-only", id: 42); XCTFail("must reject mismatched identity") }
        catch { XCTAssertTrue(error is APIError) }
    }

    func testDetailDecodesStoredStopsSegmentsAndOptionalMetrics() throws {
        let detail = try JSONDecoder().decode(MobileActivityDetail.self, from: Data(detailJSON.utf8))
        XCTAssertEqual(detail.activity.id, 42)
        XCTAssertEqual(detail.activity.facts?.first?.path?.count, 2)
        XCTAssertEqual(detail.stops.first?.durationSeconds, 12)
        XCTAssertEqual(detail.averageHeartRate, 141)
        XCTAssertNil(detail.averagePower)
        XCTAssertEqual(detail.pacing?.splits?.first?.speed, "20 km/h")
    }
}

let detailJSON = #"{"activity":{"id":42,"name":"Test ride","type":"Ride","type_label":"Ride","start_time":"2025-01-02T12:00:00Z","distance":"1 km","duration":"5m","stop_count":1,"light_stops":0,"road_crossings":0,"detected_fact_count":1,"route":[{"lat":52,"lon":13},{"lat":52.01,"lon":13.01}],"route_status":"available","facts_status":"available","facts":[{"id":"segment","kind":"segment","title":"Segment","summary":"Stored evidence","path":[{"lat":52,"lon":13},{"lat":52.01,"lon":13.01}]}]},"stats_status":"available","stop_total":"12s","average_heart_rate":141,"stops":[{"lat":52,"lon":13,"start_seconds":100,"duration":"12s","duration_seconds":12,"has_traffic_light":false,"has_road_crossing":false}],"pacing":{"Distance":"1 km","FastestFiveKM":"","FastestFiveFrom":"","FinishChange":"","FinishDetail":"","FastestSplit":"","Splits":[{"Label":"First quarter","Distance":"250 m","Duration":"1m","Speed":"20 km/h","Power":"","HeartRate":"","HasPower":false,"HasHeartRate":false,"Fastest":true}]}}"#
