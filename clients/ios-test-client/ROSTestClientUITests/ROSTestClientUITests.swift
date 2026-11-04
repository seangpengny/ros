import XCTest

final class ROSTestClientUITests: XCTestCase {
    func testIndependentAPIFlow() {
        let app = XCUIApplication()
        app.launchArguments += ["-apiBaseURL", "http://127.0.0.1:18081"]
        app.launch()

        let button = app.buttons["runFlowButton"]
        XCTAssertTrue(button.waitForExistence(timeout: 10))
        button.tap()

        let status = app.staticTexts["statusLabel"]
        let passPredicate = NSPredicate(format: "label BEGINSWITH 'PASS:'")
        let passExpectation = expectation(for: passPredicate, evaluatedWith: status)
        wait(for: [passExpectation], timeout: 30)
        XCTAssertTrue(status.label.hasPrefix("PASS:"), "independent client flow failed: \(status.label)")
    }

    func testPlayableStoreAndGameFlow() {
        let app = XCUIApplication()
        app.launchArguments += [
            "-apiBaseURL", "http://127.0.0.1:18081",
            "-gameHost", "127.0.0.1",
            "-gamePort", "9000"
        ]
        app.launch()

        let loadStore = app.buttons["loadStoreButton"]
        XCTAssertTrue(loadStore.waitForExistence(timeout: 10))
        loadStore.tap()

        let buy = app.buttons["buyStarterItemButton"]
        XCTAssertTrue(buy.waitForExistence(timeout: 30))
        buy.tap()

        let status = app.staticTexts["statusLabel"]
        let boughtPredicate = NSPredicate(format: "label CONTAINS 'Bought starter_medkit'")
        let boughtExpectation = expectation(for: boughtPredicate, evaluatedWith: status)
        wait(for: [boughtExpectation], timeout: 30)
        XCTAssertTrue(status.label.contains("Bought starter_medkit"), "purchase failed: \(status.label)")

        let enterGame = app.buttons["enterGameButton"]
        XCTAssertTrue(enterGame.waitForExistence(timeout: 10))
        enterGame.tap()

        let gameStatus = app.staticTexts["gameStatusLabel"]
        let livePredicate = NSPredicate(format: "label BEGINSWITH 'LIVE:'")
        let liveExpectation = expectation(for: livePredicate, evaluatedWith: gameStatus)
        wait(for: [liveExpectation], timeout: 15)
        XCTAssertTrue(gameStatus.label.hasPrefix("LIVE:"), "game did not enter live state: \(gameStatus.label)")

        let moveRight = app.buttons["moveRightButton"]
        XCTAssertTrue(moveRight.waitForExistence(timeout: 10))
        moveRight.tap()
        XCTAssertTrue(app.buttons["pickupButton"].exists)
        app.buttons["disconnectGameButton"].tap()
    }
}
