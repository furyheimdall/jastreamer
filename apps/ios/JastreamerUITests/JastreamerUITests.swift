import Foundation
import XCTest

final class JastreamerUITests: XCTestCase {
    private let actualOrigin = "http://127.0.0.1:18080"
    private let firstBoundaryOrigin = "http://127.0.0.1:18081"
    private let secondBoundaryOrigin = "http://127.0.0.1:18082"
    private let username = "ios-smoke"
    private let password = "ios-smoke-fixture-password"
    private var checkedLocalNetworkPrompt = false
    private var checkedTestRunnerNetworkPrompt = false

    override func setUpWithError() throws {
        continueAfterFailure = false
    }

    func testActualWebUIAndWebKitSecurityBoundaries() throws {
        let app = XCUIApplication()
        app.launchArguments = ["-AppleLanguages", "(en)", "-AppleLocale", "en_US"]
        app.launch()
        allowLocalNetworkAccessIfRequested()

        let address = app.textFields["server-address"]
        XCTAssertTrue(address.waitForExistence(timeout: 10), "The native server chooser must be visible")
        address.tap()
        address.typeText("https://user@example.invalid")
        XCTAssertTrue(app.keyboards.firstMatch.waitForExistence(timeout: 5), "The native address field must use the software keyboard")
        attachScreenshot(name: "native-address-keyboard")
        app.keyboards.buttons["Go"].tap()
        let invalidAlert = app.alerts.firstMatch
        XCTAssertTrue(invalidAlert.waitForExistence(timeout: 10), "An invalid manual URL must report a native validation error")
        XCTAssertTrue(address.exists, "An invalid URL must remain on the chooser")
        XCTAssertFalse(app.descendants(matching: .any)["web-control"].exists, "An invalid URL must never create Web content")
        invalidAlert.buttons["Dismiss"].tap()

        connect(app, to: actualOrigin)
        let web = app.webViews.firstMatch
        XCTAssertTrue(app.descendants(matching: .any)["web-control"].waitForExistence(timeout: 25), "The real Server must create the Web control")
        XCTAssertTrue(web.waitForExistence(timeout: 10), "The connected control must be a WKWebView")
        XCTAssertTrue(web.staticTexts["Create an administrator account"].waitForExistence(timeout: 25), "The actual embedded Web UI must show first-account setup")

        let webUsername = web.textFields["Username"]
        XCTAssertTrue(webUsername.waitForExistence(timeout: 10))
        webUsername.tap()
        XCTAssertTrue(app.keyboards.firstMatch.waitForExistence(timeout: 5), "Tapping the actual Web form must show the software keyboard")
        XCTAssertLessThanOrEqual(webUsername.frame.maxY, app.keyboards.firstMatch.frame.minY + 1, "The focused Web field must remain above the keyboard")
        attachScreenshot(name: "actual-web-account-keyboard")
        webUsername.typeText(username)
        XCUIDevice.shared.orientation = .landscapeLeft
        defer { XCUIDevice.shared.orientation = .portrait }
        wait(for: [XCTNSPredicateExpectation(
            predicate: NSPredicate { _, _ in app.frame.width > app.frame.height },
            object: nil
        )], timeout: 10)
        XCTAssertEqual(webUsername.value as? String, username, "Rotation must retain unsaved Web form input")
        attachScreenshot(name: "actual-web-account-landscape")
        XCUIDevice.shared.orientation = .portrait
        wait(for: [XCTNSPredicateExpectation(
            predicate: NSPredicate { _, _ in app.frame.height > app.frame.width },
            object: nil
        )], timeout: 10)
        web.secureTextFields["Password"].tap()
        web.secureTextFields["Password"].typeText(password)
        web.secureTextFields["Confirm password"].tap()
        web.secureTextFields["Confirm password"].typeText(password)
        web.buttons["Create account"].tap()

        let tabNames = ["Library", "Playlists", "Queue", "Settings"]
        XCTAssertTrue(web.buttons["Library"].waitForExistence(timeout: 25), "Account creation must reach the actual phone Web UI")
        for name in tabNames {
            let tab = web.buttons[name]
            XCTAssertTrue(tab.exists, "Missing actual Web phone tab: \(name)")
            XCTAssertTrue(tab.isHittable, "Actual Web phone tab is not hittable: \(name)")
            XCTAssertGreaterThanOrEqual(tab.frame.width, 44)
            XCTAssertGreaterThanOrEqual(tab.frame.height, 44)
            XCTAssertLessThanOrEqual(tab.frame.maxY, app.frame.maxY + 1)
        }
        attachScreenshot(name: "actual-web-phone-library")
        try assertServerStopped()
        XCTAssertTrue(app.buttons["switch-server"].exists, "The current Server controls must remain reachable beside the Web UI")
        XCTAssertTrue(app.descendants(matching: .any)["current-server"].exists, "The current Server must remain reachable beside the Web UI")
        XCTAssertTrue(app.buttons["language-menu"].exists, "The native language menu must remain reachable beside the Web UI")
        app.buttons["switch-server"].tap()
        XCTAssertTrue(app.textFields["server-address"].waitForExistence(timeout: 10), "Switch Server must return to the native chooser")
        XCTAssertTrue(app.buttons["language-menu"].exists, "The native language menu must remain reachable on the chooser")
        try assertServerStopped()

        connect(app, to: actualOrigin)
        XCTAssertTrue(web.buttons["Library"].waitForExistence(timeout: 20), "The Server profile must retain its authenticated WebKit session")
        XCTAssertFalse(web.staticTexts["Create an administrator account"].exists)
        XCUIDevice.shared.press(.home)
        app.activate()
        XCTAssertTrue(web.buttons["Library"].waitForExistence(timeout: 20), "Backgrounding must retain the selected Server session")
        try assertServerStopped()

        web.buttons["Settings"].tap()
        let webLanguage = web.popUpButtons["Language / 언어"]
        XCTAssertTrue(webLanguage.waitForExistence(timeout: 10), "The actual Web language setting must be reachable")
        webLanguage.tap()
        let picker = app.pickerWheels.firstMatch
        if picker.waitForExistence(timeout: 3) {
            picker.adjust(toPickerWheelValue: "한국어")
            app.buttons["Done"].tap()
        } else {
            let koreanOption = app.buttons["한국어"]
            XCTAssertTrue(koreanOption.waitForExistence(timeout: 3))
            koreanOption.tap()
        }
        XCTAssertTrue(web.buttons["보관함"].waitForExistence(timeout: 15), "The Web language setting must take effect")
        app.buttons["switch-server"].tap()
        XCTAssertTrue(app.staticTexts["서버 선택"].waitForExistence(timeout: 10), "Web cookie observation must persist the language back to native state")
        XCTAssertTrue(app.buttons["language-menu"].exists)
        connect(app, to: actualOrigin)
        XCTAssertTrue(web.buttons["보관함"].waitForExistence(timeout: 20), "The isolated profile must retain its Korean Web language")
        attachScreenshot(name: "actual-web-phone-korean")
        app.buttons["language-menu"].tap()
        XCTAssertTrue(app.buttons["English"].waitForExistence(timeout: 5))
        app.buttons["English"].tap()
        XCTAssertTrue(web.buttons["Library"].waitForExistence(timeout: 15), "Native language changes must update the actual Web UI through the language cookie")

        app.buttons["switch-server"].tap()
        connect(app, to: firstBoundaryOrigin)
        XCTAssertTrue(web.staticTexts["Boundary 18081"].waitForExistence(timeout: 15))
        web.buttons["Store boundary"].tap()
        XCTAssertTrue(web.staticTexts["stored|stored"].waitForExistence(timeout: 5))

        app.buttons["switch-server"].tap()
        connect(app, to: secondBoundaryOrigin)
        XCTAssertTrue(web.staticTexts["Boundary 18082"].waitForExistence(timeout: 15))
        XCTAssertTrue(web.staticTexts["|"].waitForExistence(timeout: 5), "A different port must receive an isolated cookie and DOM storage profile")

        app.buttons["switch-server"].tap()
        connect(app, to: firstBoundaryOrigin)
        XCTAssertTrue(web.staticTexts["stored|stored"].waitForExistence(timeout: 15), "The same canonical Server profile must retain cookie and DOM storage state")

        XCUIDevice.shared.press(.home)
        try request(path: "/rotate-id", origin: firstBoundaryOrigin, method: "POST")
        app.activate()
        XCTAssertTrue(app.buttons["retry-web"].waitForExistence(timeout: 10), "Foreground return must reject a replaced Server")
        XCTAssertFalse(web.staticTexts["stored|stored"].exists, "An unverified page must remain inaccessible")
        app.buttons["language-menu"].tap()
        app.buttons["한국어"].tap()
        XCTAssertTrue(app.buttons["retry-web"].exists, "A language change must not dismiss the identity failure")
        XCTAssertFalse(web.staticTexts["stored|stored"].exists)
        app.buttons["language-menu"].tap()
        app.buttons["English"].tap()
        app.buttons["switch-server"].tap()
        let savedServer = app.buttons.matching(
            NSPredicate(format: "label BEGINSWITH %@", "Boundary 18081, ")
        ).firstMatch
        XCTAssertTrue(savedServer.waitForExistence(timeout: 10))
        savedServer.tap()
        let identityAlert = app.alerts.firstMatch
        XCTAssertTrue(identityAlert.waitForExistence(timeout: 10), "A saved Server must reject a changed UUID")
        XCTAssertFalse(app.descendants(matching: .any)["web-control"].exists)
        identityAlert.buttons["Dismiss"].tap()
        connect(app, to: firstBoundaryOrigin)
        XCTAssertTrue(web.staticTexts["|"].waitForExistence(timeout: 15), "A new Server UUID at the same origin must receive a distinct WebKit profile")

        let upload = web.descendants(matching: .any).matching(identifier: "Upload file").firstMatch
        XCTAssertTrue(upload.waitForExistence(timeout: 5))
        upload.tap()
        XCTAssertTrue(web.staticTexts["File picker requested"].waitForExistence(timeout: 5))
        XCTAssertFalse(app.sheets.firstMatch.waitForExistence(timeout: 2), "Remote content must not open the native file-source chooser")
        XCTAssertFalse(app.navigationBars["Browse"].exists, "Remote content must not open the document browser")
        XCTAssertFalse(app.alerts.firstMatch.exists, "Remote content must not ask for device permissions")

        XCTAssertEqual(try hostileCount(), 0)
        web.buttons["Cross-origin fetch"].tap()
        XCTAssertTrue(web.staticTexts["Cross-origin request rejected"].waitForExistence(timeout: 10))
        XCTAssertEqual(try hostileCount(), 0, "Cross-origin Web content requests must not reach the hostile origin")
        web.buttons["Cross-origin navigation"].tap()
        XCTAssertTrue(app.buttons["retry-web"].waitForExistence(timeout: 10), "Blocked navigation must report its native error")
        XCTAssertEqual(try hostileCount(), 0, "Cross-origin main-frame navigation must not reach the hostile origin")
        attachScreenshot(name: "webkit-boundary-isolation")
    }

    private func connect(_ app: XCUIApplication, to origin: String) {
        let address = app.textFields["server-address"]
        XCTAssertTrue(address.waitForExistence(timeout: 10))
        replaceText(in: address, with: origin)
        app.buttons["connect-server"].tap()
        allowLocalNetworkAccessIfRequested()
        XCTAssertTrue(app.buttons["switch-server"].waitForExistence(timeout: 25), "Failed to select \(origin)")
    }

    private func replaceText(in field: XCUIElement, with value: String) {
        field.tap()
        let existing = field.value as? String ?? ""
        if !existing.isEmpty, existing != field.placeholderValue {
            field.press(forDuration: 1)
            let selectAll = XCUIApplication().descendants(matching: .any)
                .matching(identifier: "Select All").firstMatch
            XCTAssertTrue(selectAll.waitForExistence(timeout: 5), "Select the complete previous address before replacing it")
            selectAll.tap()
        }
        field.typeText(value)
        XCTAssertEqual(field.value as? String, value, "The entered address must exactly match the intended Server")
    }

    private func allowLocalNetworkAccessIfRequested() {
        guard !checkedLocalNetworkPrompt else { return }
        checkedLocalNetworkPrompt = acceptLocalNetworkPromptIfPresent(timeout: 5)
    }

    @discardableResult
    private func acceptLocalNetworkPromptIfPresent(timeout: TimeInterval) -> Bool {
        let alert = XCUIApplication(bundleIdentifier: "com.apple.springboard").alerts.firstMatch
        guard alert.waitForExistence(timeout: timeout) else { return false }
        let text = alert.descendants(matching: .staticText).allElementsBoundByIndex.map(\.label).joined(separator: " ")
        XCTAssertTrue(text.localizedCaseInsensitiveContains("local network"), "Only the iOS local-network permission alert may be accepted: \(text)")
        let allow = alert.buttons["Allow"]
        XCTAssertTrue(allow.exists, "The local-network permission alert must offer Allow")
        allow.tap()
        return true
    }

    private func assertServerStopped() throws {
        let credentials = try JSONSerialization.data(withJSONObject: ["username": username, "password": password])
        let login = try request(
            path: "/api/v1/login",
            origin: actualOrigin,
            method: "POST",
            body: credentials
        )
        XCTAssertEqual(login.statusCode, 200)
        let player = try request(path: "/api/v1/player", origin: actualOrigin)
        XCTAssertEqual(player.statusCode, 200)
        let json = try JSONSerialization.jsonObject(with: player.data)
        let value = try XCTUnwrap(json as? [String: Any])
        XCTAssertEqual(value["state"] as? String, "stopped", "Closing, switching, reconnecting, and backgrounding must not resume playback")
        XCTAssertEqual(value["renderer_id"] as? String, "", "Client lifecycle must not select an output")
    }

    @discardableResult
    private func request(path: String, origin: String, method: String = "GET", body: Data? = nil) throws -> (data: Data, statusCode: Int) {
        let completed = expectation(description: "\(method) \(path)")
        var outcome: Result<(Data, Int), Error>?
        var request = URLRequest(url: URL(string: origin + path)!)
        request.httpMethod = method
        request.httpBody = body
        request.setValue("application/json", forHTTPHeaderField: "Content-Type")
        request.setValue("web", forHTTPHeaderField: "X-Jastreamer-Request")
        let task = URLSession.shared.dataTask(with: request) { data, response, error in
            if let error {
                outcome = .failure(error)
            } else if let response = response as? HTTPURLResponse {
                outcome = .success((data ?? Data(), response.statusCode))
            } else {
                outcome = .failure(URLError(.badServerResponse))
            }
            completed.fulfill()
        }
        task.resume()
        if !checkedTestRunnerNetworkPrompt {
            checkedTestRunnerNetworkPrompt = true
            acceptLocalNetworkPromptIfPresent(timeout: 3)
        }
        wait(for: [completed], timeout: 10)
        let result = try XCTUnwrap(outcome).get()
        return (result.0, result.1)
    }

    private func hostileCount() throws -> Int {
        let response = try request(path: "/hostile-count", origin: secondBoundaryOrigin)
        XCTAssertEqual(response.statusCode, 200)
        return try XCTUnwrap(Int(String(decoding: response.data, as: UTF8.self)))
    }

    private func attachScreenshot(name: String) {
        let attachment = XCTAttachment(screenshot: XCUIScreen.main.screenshot())
        attachment.name = name
        attachment.lifetime = .keepAlways
        add(attachment)
    }
}
