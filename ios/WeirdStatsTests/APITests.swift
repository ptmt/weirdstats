import XCTest
@testable import WeirdStats

final class APITests: XCTestCase {
    func testOptionalMapErrorsAreNotFailedActivitiesAndLegacyFieldsDefault() throws {
        let current = Data(#"{"message":"Imported. Map details unavailable.","pending":0,"completed":7,"failed":0,"blocked":0,"map_failed":7,"publishing_failed":0,"publishing_blocked":0,"errors":[]}"#.utf8)
        let status = try JSONDecoder().decode(MobileSyncStatus.self, from: current)
        XCTAssertEqual(status.mapFailed, 7)
        XCTAssertEqual(status.failed, 0)
        XCTAssertTrue(status.canRetryWork)
        let legacy = Data(#"{"message":"Up to date","pending":0,"completed":7,"failed":0,"blocked":0,"errors":[]}"#.utf8)
        let old = try JSONDecoder().decode(MobileSyncStatus.self, from: legacy)
        XCTAssertEqual(old.mapFailed, 0)
        XCTAssertFalse(old.canRetryWork)
    }

    @MainActor func testRefreshOnlyReloadsImportedFeedAndDoesNotEnqueueStravaWork() async {
        let config = URLSessionConfiguration.ephemeral
        config.protocolClasses = [HeldProtocol.self]
        HeldProtocol.requests = []
        let model = AppModel(apiClient: APIClient(session: URLSession(configuration: config)), tokenProvider: { "test-only" })
        let task = Task { await model.refresh() }
        while HeldProtocol.requests.count < 2 { await Task.yield() }
        XCTAssertEqual(Set(HeldProtocol.requests.map { $0.request.httpMethod ?? "" }), ["GET"])
        XCTAssertEqual(HeldProtocol.requests.count, 2)
        XCTAssertEqual(Set(HeldProtocol.requests.compactMap { $0.request.url?.path }), ["/api/mobile/me", "/api/mobile/activities"])
        for request in HeldProtocol.requests { request.finish(status: 500, body: "test response") }
        await task.value
        XCTAssertFalse(model.isLoading)
    }

    @MainActor func testSyncRequestsOwnerEndpointBeforeReload() async {
        let config = URLSessionConfiguration.ephemeral
        config.protocolClasses = [HeldProtocol.self]
        HeldProtocol.requests = []
        let model = AppModel(apiClient: APIClient(session: URLSession(configuration: config)), tokenProvider: { "test-only" })
        let task = Task { await model.syncWithStrava() }
        while HeldProtocol.requests.isEmpty { await Task.yield() }
        let request = HeldProtocol.requests[0]
        XCTAssertEqual(request.request.httpMethod, "POST")
        XCTAssertEqual(request.request.url?.path, "/api/sync/latest")
        XCTAssertNil(request.request.url?.query)
        XCTAssertEqual(request.request.value(forHTTPHeaderField: "Authorization"), "Bearer test-only")
        request.finish(status: 202, body: #"{"message":"Finding activities","pending":0,"completed":0,"failed":0,"blocked":0,"discovering":0,"errors":[]}"#)
        while HeldProtocol.requests.count < 3 { await Task.yield() }
        for request in HeldProtocol.requests.dropFirst() { request.finish(status: 500, body: "test response") }
        await task.value
        XCTAssertFalse(model.isSyncing)
    }

    func testLegacyBackendDoesNotInventRoutesOrFacts() throws {
        let data = Data(#"{"activities":[{"id":42,"name":"Virtual Ride","type":"VirtualRide","type_label":"Virtual ride","start_time":"2026-01-15T12:00:00Z","distance":"1 km","duration":"10m","stop_count":0,"light_stops":0,"road_crossings":0,"detected_fact_count":0}]}"#.utf8)
        let feed = try JSONDecoder().decode(MobileActivities.self, from: data)
        XCTAssertNil(feed.activities[0].route)
        XCTAssertNil(feed.activities[0].facts)
    }

    func testMonthGridUsesMondayFirstDatesAndPreservesServerDays() {
        let leapDay = HistoryDay(date: "2024-02-29", tooltip: "Server effort", level: 7, inRange: true)
        let padding = HistoryDay(date: "2023-12-31", tooltip: "Outside year", level: 0, inRange: false)
        let months = YearCalendarMonth.make(year: 2024, days: [padding, leapDay])
        XCTAssertEqual(months.map(\.id), Array(1...12))
        XCTAssertEqual(months[0].cells.first?.date, "2024-01-01") // Monday
        XCTAssertEqual(months[1].cells.prefix(3).filter { $0.date == nil }.count, 3) // Thursday
        let feb29 = months[1].cells.first { $0.date == "2024-02-29" }
        XCTAssertEqual(feb29?.day?.tooltip, "Server effort")
        XCTAssertEqual(feb29?.day?.level, 7)
        XCTAssertEqual(months[1].cells.firstIndex { $0.date == "2024-02-29" }! % 7, 3)
        XCTAssertEqual(months.flatMap(\.cells).compactMap(\.date).count, 366)
        XCTAssertFalse(months.flatMap(\.cells).contains { $0.date == padding.date })
        XCTAssertTrue(months.allSatisfy { $0.cells.count % 7 == 0 })
        let common = YearCalendarMonth.make(year: 2025, days: [])
        XCTAssertEqual(common.flatMap(\.cells).compactMap(\.date).count, 365)
        XCTAssertFalse(common[1].cells.contains { $0.date == "2025-02-29" })
        XCTAssertEqual(common[0].cells.firstIndex { $0.date == "2025-01-01" }, 2) // Wednesday
        let century = YearCalendarMonth.make(year: 2100, days: [])
        XCTAssertEqual(century.flatMap(\.cells).compactMap(\.date).count, 365)
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
