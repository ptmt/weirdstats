import XCTest
@testable import WeirdStats

final class APITests: XCTestCase {
    func testLegacyBackendDoesNotInventRoutesOrFacts() throws {
        let data = Data(#"{"activities":[{"id":42,"name":"Virtual Ride","type":"VirtualRide","type_label":"Virtual ride","start_time":"2026-01-15T12:00:00Z","distance":"1 km","duration":"10m","stop_count":0,"light_stops":0,"road_crossings":0,"detected_fact_count":0}]}"#.utf8)
        let feed = try JSONDecoder().decode(MobileActivities.self, from: data)
        XCTAssertNil(feed.activities[0].route)
        XCTAssertNil(feed.activities[0].facts)
    }

    func testWebsiteCalendarWireFormat() throws {
        let data = Data(#"{"activities":[],"years":[2026,2024],"history":{"Year":2024,"TotalEffort":1.5,"Days":[{"Date":"2024-02-29","Tooltip":"Feb 29 · 1.5 effort","Level":3,"InRange":true}]}}"#.utf8)
        let feed = try JSONDecoder().decode(MobileActivities.self, from: data)
        XCTAssertEqual(feed.history?.year, 2024)
        XCTAssertEqual(feed.history?.days.first?.date, "2024-02-29")
        XCTAssertEqual(feed.history?.totalEffort, 1.5)
    }

    @MainActor func testRefreshFailureEndsLoadingAndPreservesData() async {
        let model = AppModel(apiClient: APIClient(session: .shared), tokenProvider: { "test-only-invalid-token" })
        model.serverURLText = "http://127.0.0.1:1"
        await model.refresh()
        XCTAssertFalse(model.isLoading)
        XCTAssertFalse(model.errorMessage.isEmpty)
    }

    func testInvalidGPSCoordinatesAreRejected() throws {
        let points = try JSONDecoder().decode([RoutePoint].self, from: Data(#"[{"lat":91,"lon":10},{"lat":52,"lon":181},{"lat":52,"lon":13}]"#.utf8))
        XCTAssertEqual(points.filter(\.isValid).count, 1)
    }

    @MainActor func testHistoryFailureDoesNotLeaveSpinnerOrClearFeed() async {
        let model = AppModel(tokenProvider: { "test-only-token" })
        model.serverURLText = "http://127.0.0.1:1"
        await model.loadHistory(year: 2024)
        XCTAssertFalse(model.isLoadingHistory)
        XCTAssertFalse(model.historyError.isEmpty)
        XCTAssertTrue(model.errorMessage.isEmpty)
    }

    @MainActor func testSignOutIgnoresInFlightRefresh() async {
        let configuration = URLSessionConfiguration.ephemeral
        configuration.protocolClasses = [DelayedFailureProtocol.self]
        let model = AppModel(apiClient: APIClient(session: URLSession(configuration: configuration)), tokenProvider: { "test-only-token" }, clearToken: {})
        model.serverURLText = "https://test.invalid"
        let task = Task { await model.refresh() }
        try? await Task.sleep(nanoseconds: 50_000_000)
        model.signOut()
        await task.value
        XCTAssertEqual(model.errorMessage, "")
        XCTAssertTrue(model.activities.isEmpty)
        XCTAssertFalse(model.isLoading)
    }

    func testDecodesActualRouteAndCachedFacts() throws {
        let data = Data(#"{"activities":[{"id":42,"name":"Ride","type":"Ride","type_label":"Ride","start_time":"2026-01-15T12:00:00Z","distance":"1 km","duration":"10m","stop_count":0,"light_stops":0,"road_crossings":0,"detected_fact_count":1,"route":[{"lat":52.5,"lon":13.4},{"lat":52.6,"lon":13.5}],"route_status":"available","facts":[{"id":"stops","kind":"stops","title":"Stops","summary":"Cached fact","color":"green"}],"facts_status":"available"}]}"#.utf8)
        let feed = try JSONDecoder().decode(MobileActivities.self, from: data)
        XCTAssertEqual(feed.activities[0].route?.count, 2)
        XCTAssertEqual(feed.activities[0].facts?.first?.summary, "Cached fact")
        XCTAssertEqual(feed.activities[0].routeStatus, "available")
    }
}
