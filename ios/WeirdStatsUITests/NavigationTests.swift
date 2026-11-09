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

    func testFourTabsAndHonestEmptyStates() {
        let app = XCUIApplication()
        app.launchArguments = ["--ui-test-empty"]
        app.launch()
        XCTAssertTrue(app.tabBars.buttons["Activities"].waitForExistence(timeout: 5))
        app.tabBars.buttons["Activities"].tap()
        XCTAssertTrue(app.staticTexts["No activities yet"].exists)
        save(app, "01-activities")
        app.tabBars.buttons["Year"].tap()
        XCTAssertTrue(app.staticTexts["Your year in motion"].exists)
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
