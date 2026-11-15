import XCTest

final class NavigationTests: XCTestCase {
    func testMainScreensDoNotShowStandaloneRefreshControls() throws {
        let server = try RideTestServer()
        let app = XCUIApplication()
        app.launchArguments = ["--ui-test-server", server.url, "--ui-test-token", "synthetic-only"]
        app.launch()
        app.tabBars.buttons["Activities"].tap()
        XCTAssertTrue(app.buttons["activity-card-42"].waitForExistence(timeout: 8))
        XCTAssertFalse(app.buttons["Sync with Strava"].exists)
        XCTAssertFalse(app.otherElements["feed-sync-status"].exists)
        save(app, "quiet-activities-synthetic-fixture")
        app.tabBars.buttons["Profile"].tap()
        XCTAssertFalse(app.buttons["Reload profile and imported activities"].exists)
        XCTAssertFalse(app.staticTexts["Refresh to check your import status."].exists)
        // Import is an explicit upstream mutation, not another local refresh button.
        XCTAssertTrue(app.buttons["Sync with Strava"].exists)
        save(app, "quiet-profile-synthetic-fixture")
    }

    func testDelayedPullRefreshKeepsContentWithoutDuplicateRequests() throws {
        let server = try RideTestServer()
        let app = XCUIApplication()
        app.launchArguments = ["--ui-test-server", server.url, "--ui-test-token", "synthetic-only"]
        app.launch()
        app.tabBars.buttons["Activities"].tap()
        XCTAssertTrue(app.buttons["activity-card-42"].waitForExistence(timeout: 8))
        XCTAssertEqual(server.requests.filter { !$0.contains("year=") }.count, 2, "Automatic bootstrap reads profile and feed once")
        server.delayResponses(4)
        for tab in ["Activities", "Facts", "Profile", "Year"] {
            app.tabBars.buttons[tab].tap()
            if tab == "Year" { XCTAssertTrue(app.staticTexts["annual-effort"].waitForExistence(timeout: 10)) }
            let count = server.requests.count
            let scroll = (tab == "Activities" || tab == "Year") ? app.scrollViews.firstMatch : app.collectionViews.firstMatch
            pull(scroll)
            waitForRequests(count + (tab == "Year" ? 1 : 2), server)
            XCTAssertFalse(app.staticTexts["Couldn’t refresh"].exists)
            XCTAssertFalse(app.staticTexts["Couldn’t load activities"].exists)
            for text in ["Refreshing activities…", "Refreshing…", "Loading facts…", "Loading year…"] {
                XCTAssertFalse(app.staticTexts[text].exists)
            }
            if tab == "Activities" { XCTAssertTrue(app.buttons["activity-card-42"].exists) }
            if tab == "Facts" { XCTAssertTrue(app.buttons["activity-fact-42-segment"].exists) }
            if tab == "Year" { XCTAssertTrue(app.staticTexts["annual-effort"].exists) }
            save(app, "quiet-delayed-pull-\(tab.lowercased())-synthetic-fixture")
        }
        XCTAssertTrue(server.requests.allSatisfy { $0.hasPrefix("GET ") }, "Pull must never trigger Strava POSTs")
    }

    private func pull(_ scroll: XCUIElement) {
        XCTAssertTrue(scroll.exists)
        scroll.coordinate(withNormalizedOffset: CGVector(dx: 0.97, dy: 0.25))
            .press(forDuration: 0.1, thenDragTo: scroll.coordinate(withNormalizedOffset: CGVector(dx: 0.97, dy: 0.8)))
    }

    private func waitForRequests(_ count: Int, _ server: RideTestServer) {
        let received = XCTNSPredicateExpectation(predicate: NSPredicate { _, _ in server.requests.count >= count }, object: nil)
        XCTAssertEqual(XCTWaiter.wait(for: [received], timeout: 8), .completed)
        XCTAssertEqual(server.requests.count, count)
    }

    func testOfflineEmptyStateLight() { verifyOffline(dark: false, largeText: false) }
    func testOfflineEmptyStateDark() { verifyOffline(dark: true, largeText: false) }
    func testOfflineEmptyStateAccessibility() { verifyOffline(dark: false, largeText: true) }

    private func verifyOffline(dark: Bool, largeText: Bool) {
        let app = XCUIApplication()
        app.launchArguments = ["--ui-test-offline", "--ui-test-server", "http://127.0.0.1:1", "--ui-test-token", "synthetic-only",
                               "-AppleLanguages", "(en)", "-AppleLocale", "en_US",
                               "-UIUserInterfaceStyle", dark ? "Dark" : "Light"]
        if dark { app.launchArguments += ["--ui-test-dark"] }
        if largeText { app.launchArguments += ["--ui-test-large-type"] }
        app.launch()
        app.tabBars.buttons["Activities"].tap()
        let title = app.staticTexts["You’re offline"]
        let retry = app.buttons["activity-refresh-retry"]
        XCTAssertTrue(title.waitForExistence(timeout: 8))
        XCTAssertTrue(retry.isHittable)
        XCTAssertGreaterThanOrEqual(retry.frame.height, 44)
        XCTAssertFalse(app.buttons["Sync with Strava"].exists)
        XCTAssertGreaterThan(title.frame.minY, app.navigationBars.firstMatch.frame.maxY + 40)
        XCTAssertFalse(app.staticTexts["No activities yet"].exists)
        XCTAssertFalse(app.staticTexts["Couldn’t refresh"].exists)
        save(app, "offline-\(dark ? "dark" : "light")-\(largeText ? "accessibility" : "default")-synthetic-fixture")
        retry.tap()
        XCTAssertTrue(title.waitForExistence(timeout: 5))
    }

    func testLoadedFeedSurvivesOfflineRefresh() throws {
        let server = try RideTestServer()
        let app = XCUIApplication()
        app.launchArguments = ["--ui-test-server", server.url, "--ui-test-token", "synthetic-only", "--ui-test-offline-after-load"]
        app.launch()
        app.tabBars.buttons["Activities"].tap()
        XCTAssertTrue(app.buttons["activity-card-42"].waitForExistence(timeout: 8))
        let scroll = app.scrollViews.firstMatch
        scroll.coordinate(withNormalizedOffset: CGVector(dx: 0.97, dy: 0.25))
            .press(forDuration: 0.1, thenDragTo: scroll.coordinate(withNormalizedOffset: CGVector(dx: 0.97, dy: 0.8)))
        XCTAssertTrue(app.staticTexts["You’re offline"].waitForExistence(timeout: 8))
        XCTAssertTrue(app.buttons["activity-card-42"].exists)
        XCTAssertTrue(app.buttons["activity-refresh-retry"].isHittable)
        save(app, "offline-loaded-feed-synthetic-fixture")
        app.buttons["activity-refresh-retry"].tap()
        XCTAssertTrue(app.staticTexts["You’re offline"].waitForExistence(timeout: 5))
        XCTAssertTrue(app.buttons["activity-card-42"].exists)
    }

    func testSyncFailureKeepsFeedAndOffersExplicitSyncRetry() throws {
        let server = try RideTestServer()
        let app = XCUIApplication()
        app.launchArguments = ["--ui-test-server", server.url, "--ui-test-token", "synthetic-only", "--ui-test-sync-offline"]
        app.launch()
        app.tabBars.buttons["Activities"].tap()
        XCTAssertTrue(app.buttons["activity-card-42"].waitForExistence(timeout: 8))
        app.tabBars.buttons["Profile"].tap()
        app.buttons["Sync with Strava"].tap()
        app.tabBars.buttons["Activities"].tap()
        XCTAssertTrue(app.staticTexts["You’re offline"].waitForExistence(timeout: 5))
        XCTAssertEqual(app.buttons["activity-refresh-retry"].label, "Retry sync")
        XCTAssertTrue(app.buttons["activity-card-42"].exists)
        save(app, "offline-sync-loaded-feed-synthetic-fixture")
    }

    func testEveryActivityAndFactNavigatesToItsOwnDetail() throws {
        let server = try RideTestServer()
        let app = XCUIApplication()
        app.launchArguments = ["--ui-test-server", server.url, "--ui-test-token", "test-only"]
        app.launch()
        app.tabBars.buttons["Activities"].tap()
        XCTAssertTrue(app.buttons["activity-card-42"].waitForExistence(timeout: 10))
        app.buttons["activity-card-42"].tap()
        XCTAssertTrue(app.staticTexts["Ride details"].waitForExistence(timeout: 5))
        XCTAssertTrue(app.staticTexts["Test ride 42"].exists)
        XCTAssertTrue(app.otherElements["activity-detail-map"].waitForExistence(timeout: 5))
        save(app, "05-test-fixture-detail-route")
        app.navigationBars.buttons.element(boundBy: 0).tap()
        app.buttons["activity-card-43"].tap()
        XCTAssertTrue(app.staticTexts["No GPS route recorded"].waitForExistence(timeout: 5))
        XCTAssertTrue(app.staticTexts["Test ride 43"].exists)
        save(app, "06-test-fixture-detail-no-gps")
        app.tabBars.buttons["Facts"].tap()
        app.buttons["activity-fact-42-segment"].tap()
        XCTAssertTrue(app.staticTexts["Ride details"].waitForExistence(timeout: 5))
        XCTAssertTrue(app.staticTexts["Test ride 42"].exists)
        XCTAssertTrue(app.staticTexts["Stored test segment"].exists)
    }

    func testYearMonthsScrollVerticallyAndDaysUseServerEffort() throws {
        try verifyYearCalendar(accessibilityText: false)
    }

    func testYearCalendarAtAccessibilityTextSize() throws {
        try verifyYearCalendar(accessibilityText: true)
    }

    private func verifyYearCalendar(accessibilityText: Bool) throws {
        let server = try RideTestServer()
        let app = XCUIApplication()
        app.launchArguments = ["--ui-test-server", server.url, "--ui-test-token", "test-only",
                               "-AppleLanguages", "(en)", "-AppleLocale", "en_US"]
        if accessibilityText {
            app.launchArguments += ["-UIPreferredContentSizeCategoryName", "UICTContentSizeCategoryAccessibilityXXXL"]
        }
        app.launch()
        app.tabBars.buttons["Year"].tap()
        XCTAssertTrue(app.staticTexts["year-month-1"].waitForExistence(timeout: 10))
        app.buttons["year-picker"].tap()
        app.buttons["2024"].tap()
        XCTAssertTrue(app.buttons["year-day-2024-01-01"].waitForExistence(timeout: 10))
        XCTAssertEqual(app.staticTexts["year-month-1"].label, "January")
        XCTAssertFalse(app.navigationBars["Year"].exists)
        for copy in ["Your year in motion", "The same effort calendar", "Swipe across to explore", "Effort uses the website"] {
            XCTAssertFalse(app.staticTexts.containing(NSPredicate(format: "label CONTAINS %@", copy)).firstMatch.exists)
        }
        let first = app.buttons["year-day-2024-01-01"]
        let seventh = app.buttons["year-day-2024-01-07"]
        XCTAssertTrue(first.exists)
        XCTAssertEqual(first.frame.minY, seventh.frame.minY, accuracy: 1)
        XCTAssertGreaterThanOrEqual(first.frame.minX, 0)
        XCTAssertLessThanOrEqual(seventh.frame.maxX, app.frame.maxX)
        XCTAssertGreaterThanOrEqual(first.frame.height, 44)
        first.tap()
        XCTAssertEqual(app.staticTexts["selected-day-effort"].label, "Jan 1 · 2.5 effort hours · fixture")
        save(app, accessibilityText ? "year-accessibility-synthetic-fixture" : "year-top-synthetic-fixture")
        let scroll = app.scrollViews["year-calendar"]
        let leapDay = app.buttons["year-day-2024-02-29"]
        for _ in 0..<8 {
            if leapDay.isHittable { break }
            scroll.swipeUp()
        }
        XCTAssertTrue(leapDay.isHittable)
        leapDay.tap()
        XCTAssertEqual(app.staticTexts["selected-day-effort"].label, "Feb 29 · 1.5 effort hours · fixture")
        let december = app.staticTexts["year-month-12"]
        for _ in 0..<32 {
            if december.isHittable { break }
            scroll.swipeUp()
        }
        XCTAssertTrue(december.isHittable)
        let lastDay = app.buttons["year-day-2024-12-31"]
        for _ in 0..<5 {
            if lastDay.isHittable && lastDay.frame.maxY < app.tabBars.firstMatch.frame.minY { break }
            scroll.swipeUp()
        }
        XCTAssertTrue(lastDay.isHittable)
        XCTAssertLessThanOrEqual(lastDay.frame.maxY, app.tabBars.firstMatch.frame.minY)
        save(app, accessibilityText ? "year-december-accessibility-synthetic-fixture" : "year-december-synthetic-fixture")
        for _ in 0..<32 {
            if app.buttons["year-picker"].isHittable { break }
            scroll.swipeDown()
        }
        app.buttons["year-picker"].tap()
        app.buttons["2025"].tap()
        XCTAssertTrue(app.buttons["year-day-2025-01-01"].waitForExistence(timeout: 10))
        XCTAssertFalse(app.staticTexts["selected-day-effort"].exists)
        XCTAssertEqual(app.staticTexts["annual-effort"].label, "3.0 effort hours")
        app.tabBars.buttons["Activities"].tap()
        XCTAssertTrue(app.navigationBars["Activities"].exists)
    }

    func testFourTabsAndHonestEmptyStates() {
        let app = XCUIApplication()
        app.launchArguments = ["--ui-test-empty"]
        app.launch()
        XCTAssertTrue(app.tabBars.buttons["Activities"].waitForExistence(timeout: 5))
        app.tabBars.buttons["Activities"].tap()
        XCTAssertTrue(app.staticTexts["No activities yet"].exists)
        save(app, "01-activities")
        app.tabBars.buttons["Year"].tap()
        XCTAssertTrue(app.buttons["year-picker"].exists)
        XCTAssertFalse(app.navigationBars["Year"].exists)
        XCTAssertFalse(app.staticTexts["Your year in motion"].exists)
        save(app, "02-year")
        app.tabBars.buttons["Facts"].tap()
        XCTAssertTrue(app.staticTexts["No collected facts yet"].exists)
        save(app, "03-facts")
        app.tabBars.buttons["Profile"].tap()
        XCTAssertTrue(app.buttons["Sign Out"].exists)
        save(app, "04-profile")
        app.terminate()
        app.launch()
        XCTAssertTrue(app.buttons["Sign Out"].waitForExistence(timeout: 5))
    }
    private func save(_ app: XCUIApplication, _ name: String) {
        let attachment = XCTAttachment(screenshot: app.screenshot())
        attachment.name = name
        attachment.lifetime = .keepAlways
        add(attachment)
    }
}
