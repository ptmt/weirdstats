import XCTest

final class NavigationTests: XCTestCase {
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
