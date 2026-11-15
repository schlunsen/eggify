//
//  eggifyUITests.swift
//  eggifyUITests
//
//  End-to-end walk through the app against a running Eggify server.
//
//  The flow test needs a server and credentials, passed through the test
//  runner environment (xcodebuild forwards TEST_RUNNER_-prefixed variables):
//    TEST_RUNNER_EGGIFY_URL=localhost:3399 TEST_RUNNER_EGGIFY_USER=tester \
//    TEST_RUNNER_EGGIFY_PASSWORD=... TEST_RUNNER_EGGIFY_SHOTS=/tmp/shots xcodebuild test ...
//  Without them it is skipped.
//

import XCTest

final class eggifyUITests: XCTestCase {
    private var env: [String: String] { ProcessInfo.processInfo.environment }

    override func setUpWithError() throws {
        continueAfterFailure = false
    }

    @MainActor
    func testLaunchShowsLogin() throws {
        let app = XCUIApplication()
        app.launchArguments += ["-EggifyResetState"]
        app.launch()
        XCTAssertTrue(app.buttons["Login"].waitForExistence(timeout: 10))
    }

    @MainActor
    func testSignInAndUseASession() throws {
        guard let url = env["EGGIFY_URL"], let user = env["EGGIFY_USER"], let password = env["EGGIFY_PASSWORD"] else {
            throw XCTSkip("Set TEST_RUNNER_EGGIFY_URL / _USER / _PASSWORD to run against a server")
        }

        let app = XCUIApplication()
        app.launchArguments += ["-EggifyResetState", "-EggifyDemoRequests", "-EggifyUITesting"]
        app.launch()

        // Sign in
        let urlField = app.textFields.element(boundBy: 0)
        XCTAssertTrue(urlField.waitForExistence(timeout: 10))
        urlField.tap()
        urlField.clearAndType(url)
        let userField = app.textFields.element(boundBy: 1)
        userField.tap()
        userField.typeText(user)
        // With -EggifyUITesting the password is a plain text field (no save-password sheet).
        let passwordField = app.textFields.element(boundBy: 2)
        passwordField.tap()
        passwordField.typeText(password)
        shot(app, "00-login")
        app.buttons["Login"].tap()

        // Agents list
        let signedIn = app.navigationBars["Agents"].waitForExistence(timeout: 20)
        if !signedIn { shot(app, "00-login-failed") }
        XCTAssertTrue(signedIn, "signed in")
        dismissSavePasswordPrompt(app)
        let sessionCell = app.buttons.containing(NSPredicate(format: "label CONTAINS 'Fix login bug'")).firstMatch
        XCTAssertTrue(sessionCell.waitForExistence(timeout: 15), "seeded session listed")
        sleep(1)
        shot(app, "01-agents")

        // Open the chat; the demo question sheet appears.
        sessionCell.forceTap()
        let questionTitle = app.staticTexts["Should empty passwords return 400 or 422?"]
        XCTAssertTrue(questionTitle.waitForExistence(timeout: 15), "question sheet shown")
        shot(app, "02-question")
        app.buttons["Later"].tap()

        // Approval card is pinned above the composer.
        XCTAssertTrue(app.buttons["Approve"].waitForExistence(timeout: 5))
        sleep(1)
        shot(app, "03-chat-approval")
        app.buttons["Approve"].tap()
        XCTAssertTrue(app.buttons["The agent asked you a question"].waitForExistence(timeout: 5),
                      "deferred question can be reopened")
        sleep(1)
        shot(app, "04-chat")

        // Expand the folded tool activity and an edit.
        let activity = app.buttons.containing(NSPredicate(format: "label BEGINSWITH '4 tool steps'")).firstMatch
        app.swipeDown()
        if activity.waitForExistence(timeout: 5) {
            activity.forceTap()
            sleep(1)
            shot(app, "05-activity")
        }
        let edit = app.buttons.containing(NSPredicate(format: "label BEGINSWITH 'Edit'")).firstMatch
        if edit.waitForExistence(timeout: 5) {
            edit.forceTap()
            sleep(1)
            app.swipeUp()
            shot(app, "06-edit-diff")
        }

        // Reopen the question and answer it.
        let reopen = app.buttons["The agent asked you a question"]
        if reopen.waitForExistence(timeout: 3) {
            reopen.tap()
            app.buttons.containing(NSPredicate(format: "label CONTAINS '400 Bad Request'")).firstMatch.tap()
            shot(app, "07-question-selected")
            app.buttons["Send answer"].tap()
        }

        XCTAssertTrue(app.buttons["Send answer"].waitForNonExistence(timeout: 5), "answer sent, sheet closed")
    }

    /// Copying part of a reply, the chat menu and Zen, against the seeded
    /// session of scripts/ios-e2e-server.sh.
    @MainActor
    func testSelectTextMenuAndZen() throws {
        guard let url = env["EGGIFY_URL"], let user = env["EGGIFY_USER"], let password = env["EGGIFY_PASSWORD"] else {
            throw XCTSkip("Set TEST_RUNNER_EGGIFY_URL / _USER / _PASSWORD to run against a server")
        }
        let app = XCUIApplication()
        app.launchArguments += ["-EggifyResetState", "-EggifyUITesting", "-EggifyDemoCircus"]
        app.launch()
        signIn(app, url: url, user: user, password: password)

        let sessionCell = app.buttons.containing(NSPredicate(format: "label CONTAINS 'Fix login bug'")).firstMatch
        XCTAssertTrue(sessionCell.waitForExistence(timeout: 15))
        sessionCell.forceTap()

        // A reply: long-press → Select Text opens a native text view.
        let reply = app.staticTexts.containing(NSPredicate(format: "label CONTAINS 'Empty passwords now return'")).firstMatch
        XCTAssertTrue(reply.waitForExistence(timeout: 15), "reply shown")
        XCTAssertTrue(app.links.containing(NSPredicate(format: "label CONTAINS 'example.com'")).firstMatch.exists
                      || app.staticTexts.containing(NSPredicate(format: "label CONTAINS 'example.com'")).firstMatch.exists,
                      "bare URL shown")
        reply.press(forDuration: 1.0)
        let selectText = app.buttons["Select Text"]
        XCTAssertTrue(selectText.waitForExistence(timeout: 5), "Select Text in the menu")
        shot(app, "10-message-menu")
        selectText.tap()
        XCTAssertTrue(app.navigationBars["Select Text"].waitForExistence(timeout: 5), "Select Text sheet")
        // The sheet's text view, not the composer's behind it.
        let selectable = app.textViews.matching(NSPredicate(format: "identifier != 'composer-text'")).firstMatch
        XCTAssertTrue(selectable.waitForExistence(timeout: 5))
        let text = selectable.value as? String ?? ""
        XCTAssertTrue(text.contains("ErrEmptyPassword"), "code included")
        XCTAssertFalse(text.contains("```"), "no code fences")
        shot(app, "11-select-text")
        app.buttons["Done"].tap()

        // Chat menu: the new actions are there.
        app.buttons["Session options"].tap()
        XCTAssertTrue(app.buttons["Share transcript"].waitForExistence(timeout: 5))
        XCTAssertTrue(app.buttons["Delete session"].exists)
        XCTAssertTrue(app.buttons.containing(NSPredicate(format: "label BEGINSWITH 'Subagents'")).firstMatch.exists)
        shot(app, "12-chat-menu")

        // Zen: the scene keeps animating (two frames differ).
        app.buttons["Zen View"].tap()
        XCTAssertTrue(app.staticTexts["Resting, not dead"].waitForExistence(timeout: 5), "Circus caption")
        sleep(2)
        let first = app.screenshot().pngRepresentation
        shot(app, "13-zen")
        sleep(1)
        let second = app.screenshot().pngRepresentation
        XCTAssertNotEqual(first, second, "Zen animates")

        // Sending from Zen brings the cherub across.
        let field = app.textViews["composer-text"]
        XCTAssertTrue(field.waitForExistence(timeout: 5))
        field.tap()
        field.typeText("And now for something completely different")
        app.buttons["Send"].tap()
        usleep(900_000)
        shot(app, "14-zen-cherub")
        XCTAssertEqual((field.value as? String).map { $0.hasPrefix("And now") } ?? false, false, "composer emptied")

        // -EggifyDemoCircus: subagent characters on stage and the tool gags in turn.
        sleep(3)
        shot(app, "15-zen-cast")
        usleep(1_300_000)
        shot(app, "16-zen-gags")
    }

    private func signIn(_ app: XCUIApplication, url: String, user: String, password: String) {
        let urlField = app.textFields.element(boundBy: 0)
        XCTAssertTrue(urlField.waitForExistence(timeout: 10))
        urlField.tap()
        urlField.clearAndType(url)
        let userField = app.textFields.element(boundBy: 1)
        userField.tap()
        userField.typeText(user)
        let passwordField = app.textFields.element(boundBy: 2)
        passwordField.tap()
        passwordField.typeText(password)
        app.buttons["Login"].tap()
        XCTAssertTrue(app.navigationBars["Agents"].waitForExistence(timeout: 20), "signed in")
        dismissSavePasswordPrompt(app)
    }

    /// QR pairing through the scanner's "Paste pairing link" fallback (the
    /// Simulator has no camera). Needs a fresh pairing link:
    ///   TEST_RUNNER_EGGIFY_PAIR_URL='eggify://pair?u=…&c=…'
    @MainActor
    func testPairWithPastedLink() throws {
        guard let pairURL = env["EGGIFY_PAIR_URL"] else {
            throw XCTSkip("Set TEST_RUNNER_EGGIFY_PAIR_URL to a fresh pairing link to run")
        }
        let app = XCUIApplication()
        app.launchArguments += ["-EggifyResetState", "-EggifyUITesting"]
        app.launch()

        let scan = app.buttons["scan-qr-button"]
        XCTAssertTrue(scan.waitForExistence(timeout: 10))
        shot(app, "pair-00-login")
        scan.tap()

        // No camera in the Simulator: the paste field is shown right away.
        let field = app.textFields["paste-link-field"]
        if !field.waitForExistence(timeout: 5) {
            app.buttons["paste-link-toggle"].tap()
        }
        XCTAssertTrue(field.waitForExistence(timeout: 5))
        shot(app, "pair-01-scanner")
        field.tap()
        // The same server with a code it never issued.
        var bogus = URLComponents(string: pairURL)!
        bogus.queryItems = bogus.queryItems?.map { $0.name == "c" ? URLQueryItem(name: "c", value: "not-a-real-code") : $0 }
        field.typeText(bogus.string!)
        app.buttons["paste-link-submit"].tap()
        // The server refuses it: the "Code expired" state.
        let error = app.staticTexts["pairing-error"]
        if error.waitForExistence(timeout: 15) {
            shot(app, "pair-02-error")
            app.buttons["Close"].tap()
            app.buttons["scan-qr-button"].tap()
            if !field.waitForExistence(timeout: 5) { app.buttons["paste-link-toggle"].tap() }
        }

        field.tap()
        field.clearAndType(pairURL)
        app.buttons["paste-link-submit"].tap()

        let signedIn = app.navigationBars["Agents"].waitForExistence(timeout: 20)
        shot(app, signedIn ? "pair-03-signed-in" : "pair-03-failed")
        XCTAssertTrue(signedIn, "signed in by pairing")
    }

    /// The main path: the system Camera opens eggify://pair?… ("Open in
    /// Eggify") while the app is not running. Needs two fresh pairing links,
    /// the second for another address of the server (to see "Switch to …?"):
    ///   TEST_RUNNER_EGGIFY_DEEPLINK_PAIR_URL, TEST_RUNNER_EGGIFY_SWITCH_PAIR_URL
    @MainActor
    func testPairFromDeepLinkColdStart() throws {
        guard let first = env["EGGIFY_DEEPLINK_PAIR_URL"].flatMap(URL.init(string:)) else {
            throw XCTSkip("Set TEST_RUNNER_EGGIFY_DEEPLINK_PAIR_URL to a fresh pairing link to run")
        }
        let app = XCUIApplication()
        app.launchArguments += ["-EggifyResetState", "-EggifyUITesting"]
        app.launch()
        XCTAssertTrue(app.buttons["scan-qr-button"].waitForExistence(timeout: 10))
        app.terminate()

        // Cold start from the link.
        openLink(first)
        XCTAssertTrue(app.wait(for: .runningForeground, timeout: 10))
        if app.staticTexts["pairing-connecting"].waitForExistence(timeout: 3) { shot(app, "deeplink-01-connecting") }
        if app.staticTexts["pairing-success"].waitForExistence(timeout: 10) { shot(app, "deeplink-02-success") }
        XCTAssertTrue(app.navigationBars["Agents"].waitForExistence(timeout: 20), "signed in from the link")
        sleep(1)
        shot(app, "deeplink-03-signed-in")

        // The same (now used) code again: a clear "Code expired" state.
        openLink(first)
        XCTAssertTrue(app.staticTexts["pairing-error"].waitForExistence(timeout: 15))
        shot(app, "deeplink-04-expired")
        app.buttons["Close"].tap()

        // A code for another server address: ask before switching.
        if let other = env["EGGIFY_SWITCH_PAIR_URL"].flatMap(URL.init(string:)) {
            openLink(other)
            XCTAssertTrue(app.staticTexts["pairing-switch"].waitForExistence(timeout: 10))
            shot(app, "deeplink-05-switch")
            app.buttons["pairing-switch-confirm"].tap()
            XCTAssertTrue(app.staticTexts["pairing-success"].waitForExistence(timeout: 15))
            XCTAssertTrue(app.navigationBars["Agents"].waitForExistence(timeout: 20))
        }
    }

    /// Open a URL as the Camera app would, confirming iOS's "Open in …?" prompt.
    private func openLink(_ url: URL) {
        XCUIDevice.shared.system.open(url)
        let springboard = XCUIApplication(bundleIdentifier: "com.apple.springboard")
        let open = springboard.buttons["Open"]
        if open.waitForExistence(timeout: 5) { open.tap() }
    }

    /// iOS offers to save the password after a sign-in (autofill works on the form).
    private func dismissSavePasswordPrompt(_ app: XCUIApplication) {
        for candidate in [app, XCUIApplication(bundleIdentifier: "com.apple.springboard")] {
            let notNow = candidate.buttons["Not Now"]
            if notNow.waitForExistence(timeout: 4) {
                notNow.tap()
                return
            }
        }
    }

    private func shot(_ app: XCUIApplication, _ name: String) {
        let screenshot = app.screenshot()
        let attachment = XCTAttachment(screenshot: screenshot)
        attachment.name = name
        attachment.lifetime = .keepAlways
        add(attachment)
        if let dir = env["EGGIFY_SHOTS"] {
            try? screenshot.pngRepresentation.write(to: URL(fileURLWithPath: dir).appendingPathComponent("\(name).png"))
        }
    }
}

private extension XCUIElement {
    /// List rows are sometimes reported as not hittable while visible.
    func forceTap() {
        if isHittable { tap() } else { coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.5)).tap() }
    }

    func clearAndType(_ text: String) {
        if let current = value as? String, !current.isEmpty, current != placeholderValue {
            typeText(String(repeating: XCUIKeyboardKey.delete.rawValue, count: current.count))
        }
        typeText(text)
    }
}
