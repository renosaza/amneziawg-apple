// SPDX-License-Identifier: MIT
// Copyright © 2018-2023 WireGuard LLC. All Rights Reserved.

import Cocoa

class ButtonRow: NSView {
    let button: NSButton = {
        let button = NSButton()
        button.title = ""
        button.setButtonType(.momentaryPushIn)
        button.bezelStyle = .rounded
        return button
    }()

    let secondaryButton: NSButton = {
        let button = NSButton()
        button.title = ""
        button.setButtonType(.momentaryPushIn)
        button.bezelStyle = .rounded
        button.isHidden = true
        return button
    }()

    var buttonTitle: String {
        get { return button.title }
        set(value) { button.title = value }
    }

    var isButtonEnabled: Bool {
        get { return button.isEnabled }
        set(value) { button.isEnabled = value }
    }

    var secondaryButtonTitle: String {
        get { return secondaryButton.title }
        set(value) { secondaryButton.title = value }
    }

    var isSecondaryButtonEnabled: Bool {
        get { return secondaryButton.isEnabled }
        set(value) { secondaryButton.isEnabled = value }
    }

    var isSecondaryButtonHidden: Bool {
        get { return secondaryButton.isHidden }
        set(value) { secondaryButton.isHidden = value }
    }

    var buttonToolTip: String {
        get { return button.toolTip ?? "" }
        set(value) { button.toolTip = value }
    }

    var onButtonClicked: (() -> Void)?
    var onSecondaryButtonClicked: (() -> Void)?
    var statusObservationToken: AnyObject?
    var isOnDemandEnabledObservationToken: AnyObject?
    var hasOnDemandRulesObservationToken: AnyObject?

    override var intrinsicContentSize: NSSize {
        return NSSize(width: NSView.noIntrinsicMetric, height: button.intrinsicContentSize.height)
    }

    init() {
        super.init(frame: CGRect.zero)

        button.target = self
        button.action = #selector(buttonClicked)
        secondaryButton.target = self
        secondaryButton.action = #selector(secondaryButtonClicked)

        addSubview(button)
        addSubview(secondaryButton)
        button.translatesAutoresizingMaskIntoConstraints = false
        secondaryButton.translatesAutoresizingMaskIntoConstraints = false

        NSLayoutConstraint.activate([
            button.centerYAnchor.constraint(equalTo: self.centerYAnchor),
            button.leadingAnchor.constraint(equalTo: self.leadingAnchor, constant: 155),
            button.widthAnchor.constraint(greaterThanOrEqualToConstant: 100),
            secondaryButton.centerYAnchor.constraint(equalTo: self.centerYAnchor),
            secondaryButton.leadingAnchor.constraint(equalTo: button.trailingAnchor, constant: 8),
            secondaryButton.widthAnchor.constraint(greaterThanOrEqualToConstant: 100)
        ])
    }

    required init?(coder decoder: NSCoder) {
        fatalError("init(coder:) has not been implemented")
    }

    @objc func buttonClicked() {
        onButtonClicked?()
    }

    @objc func secondaryButtonClicked() {
        onSecondaryButtonClicked?()
    }

    override func prepareForReuse() {
        buttonTitle = ""
        secondaryButtonTitle = ""
        isSecondaryButtonHidden = true
        buttonToolTip = ""
        onButtonClicked = nil
        onSecondaryButtonClicked = nil
        statusObservationToken = nil
        isOnDemandEnabledObservationToken = nil
        hasOnDemandRulesObservationToken = nil
    }
}
