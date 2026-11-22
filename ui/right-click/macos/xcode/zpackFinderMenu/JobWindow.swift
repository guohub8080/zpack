// zpack 任务进度窗（Bandizip 式）。
// 运行态：标题 + 进度条（仅大任务展示）+ 明细 + 取消钮；
// 完成态：产物摘要 + 「完成后打开文件夹」「完成后自动关闭」双对钩（UserDefaults 记忆）+ 确定（自动关闭=5s 倒计时）。
// 对钩语义 = 「完成后」动作：勾选在任务跑完那一刻生效（完成后打开=完成即亮显；自动关闭=完成即倒计时）。
import AppKit

final class JobWindowController: NSWindowController, NSWindowDelegate {

    // 大任务判定：输入 ≥64MiB 才展示进度条（小任务秒级完成，进度条只会闪一下）
    static let progressThresholdBytes: Int64 = 64 * 1024 * 1024
    static let countdownSeconds = 5

    private let showsProgress: Bool
    private var countdownTimer: Timer?
    private var secondsLeft = JobWindowController.countdownSeconds
    private var isRunning = true
    private var revealPath: String?

    // UserDefaults 键（对钩记忆）
    private static let openFolderKey = "jobWindow.openFolderAfterDone"
    private static let autoCloseKey = "jobWindow.autoCloseAfterDone"

    // UI
    private let statusLabel = NSTextField(labelWithString: "")
    private let percentLabel = NSTextField(labelWithString: "")
    private let progressBar = NSProgressIndicator()
    private let detailLabel = NSTextField(wrappingLabelWithString: "")
    private let summaryLabel = NSTextField(wrappingLabelWithString: "")
    private let openFolderCheck = NSButton(checkboxWithTitle: "完成后打开文件夹", target: nil, action: nil)
    private let autoCloseCheck = NSButton(checkboxWithTitle: "完成后自动关闭", target: nil, action: nil)
    private let actionButton = NSButton(title: "取消", target: nil, action: nil)

    /// 取消回调（App 层 terminate 进程 + 清半成品 + 退出）
    var onCancelled: (() -> Void)?

    init(jobTitle: String, inputBytes: Int64) {
        showsProgress = inputBytes >= JobWindowController.progressThresholdBytes

        let window = NSWindow(contentRect: NSRect(x: 0, y: 0, width: 380, height: 200),
                              styleMask: [.titled, .closable],
                              backing: .buffered, defer: false)
        window.title = "zpack"
        window.isRestorable = false
        window.level = .floating

        super.init(window: window)
        window.delegate = self

        // ── 布局 ──
        statusLabel.font = .systemFont(ofSize: 14, weight: .semibold)
        statusLabel.stringValue = jobTitle
        percentLabel.font = .monospacedDigitSystemFont(ofSize: 13, weight: .regular)
        percentLabel.textColor = .secondaryLabelColor

        progressBar.style = .bar
        progressBar.minValue = 0
        progressBar.maxValue = 100
        progressBar.doubleValue = 0
        progressBar.controlSize = .regular
        detailLabel.font = .systemFont(ofSize: 11)
        detailLabel.textColor = .secondaryLabelColor
        detailLabel.maximumNumberOfLines = 2

        summaryLabel.font = .systemFont(ofSize: 12)
        summaryLabel.maximumNumberOfLines = 4
        summaryLabel.isHidden = true

        // 对钩（记忆态；默认：打开文件夹=off，自动关闭=on——Bandizip 同款缺省）
        let defaults = UserDefaults.standard
        if defaults.object(forKey: JobWindowController.openFolderKey) == nil {
            openFolderCheck.state = .off
        } else {
            openFolderCheck.state = defaults.bool(forKey: JobWindowController.openFolderKey) ? .on : .off
        }
        if defaults.object(forKey: JobWindowController.autoCloseKey) == nil {
            autoCloseCheck.state = .on
        } else {
            autoCloseCheck.state = defaults.bool(forKey: JobWindowController.autoCloseKey) ? .on : .off
        }
        openFolderCheck.target = self
        openFolderCheck.action = #selector(checkToggled(_:))
        autoCloseCheck.target = self
        autoCloseCheck.action = #selector(checkToggled(_:))
        openFolderCheck.isHidden = true
        autoCloseCheck.isHidden = true

        actionButton.bezelStyle = .push
        actionButton.keyEquivalent = "\r"   // 回车=按钮（运行态=取消不回车触发；完成态=确定）
        actionButton.target = self
        actionButton.action = #selector(actionClicked(_:))

        let stack = NSStackView(views: showsProgress
            ? [statusLabel, percentLabel, progressBar, detailLabel, NSView(), summaryLabel, checksRow(), actionButton]
            : [statusLabel, NSView(), summaryLabel, checksRow(), actionButton])
        stack.orientation = .vertical
        stack.alignment = .leading
        stack.spacing = 8
        stack.translatesAutoresizingMaskIntoConstraints = false
        stack.setHuggingPriority(.required, for: .vertical)

        let content = window.contentView!
        content.addSubview(stack)
        NSLayoutConstraint.activate([
            stack.leadingAnchor.constraint(equalTo: content.leadingAnchor, constant: 20),
            stack.trailingAnchor.constraint(equalTo: content.trailingAnchor, constant: -20),
            stack.topAnchor.constraint(equalTo: content.topAnchor, constant: 16),
            stack.bottomAnchor.constraint(equalTo: content.bottomAnchor, constant: -16),
            progressBar.widthAnchor.constraint(equalTo: stack.widthAnchor),
            summaryLabel.widthAnchor.constraint(equalTo: stack.widthAnchor),
        ])

        window.center()
    }

    required init?(coder: NSCoder) { fatalError("不在用") }

    private func checksRow() -> NSView {
        let row = NSStackView(views: [openFolderCheck, autoCloseCheck])
        row.orientation = .horizontal
        row.spacing = 16
        return row
    }

    // ── 展示（焦点三件套齐上）──
    func show() {
        showWindow(nil)
        window?.makeKeyAndOrderFront(nil)
        NSApp.activate(ignoringOtherApps: true)
    }

    // ── 运行态：进度更新（stderr 进度行解析后的值）──
    func updateProgress(percent: Double, detail: String) {
        guard isRunning, showsProgress else { return }
        DispatchQueue.main.async {
            self.progressBar.doubleValue = min(max(percent, 0), 100)
            self.percentLabel.stringValue = String(format: "%.1f%%", percent)
            self.detailLabel.stringValue = detail
        }
    }

    // ── 完成态 ──
    func finish(success: Bool, title: String, body: String, revealPath: String?) {
        DispatchQueue.main.async {
            self.isRunning = false
            self.revealPath = revealPath
            self.statusLabel.stringValue = title
            self.summaryLabel.stringValue = body
            self.summaryLabel.isHidden = false
            if self.showsProgress {
                self.progressBar.doubleValue = success ? 100 : self.progressBar.doubleValue
                self.percentLabel.isHidden = !success
                if success { self.percentLabel.stringValue = "100.0%" }
                self.detailLabel.isHidden = true
            }
            self.openFolderCheck.isHidden = false
            self.autoCloseCheck.isHidden = false
            if success {
                self.actionButton.title = "确定 (\(JobWindowController.countdownSeconds))"
                if self.autoCloseCheck.state == .on { self.startCountdown() }
            } else {
                // 失败不自动关（用户要读原因）
                self.actionButton.title = "确定"
            }
            // 完成即打开文件夹（对钩在完成时刻生效）
            if success, self.openFolderCheck.state == .on, let path = revealPath {
                NSWorkspace.shared.selectFile(path, inFileViewerRootedAtPath:
                    ((path as NSString).deletingLastPathComponent))
            }
        }
    }

    // ── 倒计时（窗体非模态——default 模式即可跑）──
    private func startCountdown() {
        stopCountdown()
        secondsLeft = JobWindowController.countdownSeconds
        countdownTimer = Timer.scheduledTimer(withTimeInterval: 1.0, repeats: true) { [weak self] timer in
            guard let self = self else { timer.invalidate(); return }
            self.secondsLeft -= 1
            if self.secondsLeft <= 0 {
                timer.invalidate()
                self.confirmAndClose()
            } else {
                self.actionButton.title = "确定 (\(self.secondsLeft))"
            }
        }
    }

    private func stopCountdown() {
        countdownTimer?.invalidate()
        countdownTimer = nil
    }

    @objc private func checkToggled(_ sender: NSButton) {
        let defaults = UserDefaults.standard
        defaults.set(openFolderCheck.state == .on, forKey: JobWindowController.openFolderKey)
        defaults.set(autoCloseCheck.state == .on, forKey: JobWindowController.autoCloseKey)
        // 自动关闭对钩运行中切换：勾上→（已完成则）起倒计时；取消→停倒计时还原钮面
        if !isRunning {
            if autoCloseCheck.state == .on, countdownTimer == nil {
                actionButton.title = "确定 (\(JobWindowController.countdownSeconds))"
                startCountdown()
            } else if autoCloseCheck.state == .off {
                stopCountdown()
                actionButton.title = "确定"
            }
        }
    }

    @objc private func actionClicked(_ sender: NSButton) {
        if isRunning {
            // 取消：交给 App 层清场（terminate+删半成品+退出），窗口由其关闭
            let cancel = onCancelled
            onCancelled = nil
            cancel?()
        } else {
            confirmAndClose()
        }
    }

    private func confirmAndClose() {
        stopCountdown()
        // 手动点确定也尊重「打开文件夹」对钩（取消自动关闭但保留打开文件夹的场景）
        if openFolderCheck.state == .on, let path = revealPath, !isRunning {
            NSWorkspace.shared.selectFile(path, inFileViewerRootedAtPath:
                ((path as NSString).deletingLastPathComponent))
        }
        window?.close()
        NSApp.terminate(nil)
    }

    // 运行中点红钮=取消（同取消钮语义）
    func windowWillClose(_ notification: Notification) {
        if isRunning {
            let cancel = onCancelled
            onCancelled = nil
            cancel?()
        }
        stopCountdown()
    }
}
