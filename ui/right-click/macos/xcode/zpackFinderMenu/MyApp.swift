// zpack 宿主 app（无窗口载体 + 任务执行器）
// FinderSync 扩展（沙箱内）不自己干活——经 NSWorkspace.openApplication(arguments:)
// 唤醒本宿主（非沙箱），由这里跑内嵌 zpack 并弹原生结果对话框（Bandizip 同构架构）。
import AppKit
import SwiftUI

@main
struct MyApp: App {
    // 冷启动参数即任务（NSWorkspace.OpenConfiguration.arguments 只在进程启动时送达；
    // 宿主做完一单即退出——每单冷启动，参数永不丢失）
    @NSApplicationDelegateAdaptor(AppDelegate.self) var appDelegate

    var body: some Scene {
        Settings {
            EmptyView()
                .onOpenURL { url in
                    // SwiftUI 的 URL 接收点（GURL 事件被 SwiftUI 层接管时落到这里）
                    AppDelegate.sharedRef?.handleJobURL(url.absoluteString)
                }
        }
    }
}

class AppDelegate: NSObject, NSApplicationDelegate {
    static weak var sharedRef: AppDelegate?
    override init() {
        super.init()
        AppDelegate.sharedRef = self
    }
    private func diag(_ message: String) {
        let line = DateFormatter.localizedString(from: Date(), dateStyle: .short, timeStyle: .medium) + " " + message + "\n"
        let url = URL(fileURLWithPath: "/tmp/zpack-host.log")
        if let handle = FileHandle(forWritingAtPath: url.path) {
            handle.seekToEndOfFile()
            if let data = line.data(using: .utf8) { handle.write(data) }
            try? handle.close()
        } else {
            try? line.data(using: .utf8)?.write(to: url)
        }
    }

    func applicationWillFinishLaunching(_ notification: Notification) {
        diag("will-launch argv=\(ProcessInfo.processInfo.arguments.dropFirst().joined(separator: " "))")
        // 主通道：zpack://job?verb=zip&paths=a%0Ab（GURL AppleEvent——扩展→宿主的可靠握手；
        // 沙箱扩展发起的 LS 启动会丢 OpenConfiguration.arguments，URL 通道不受影响）。
        // 必须在 will 阶段注册：冷启动的 GURL 事件比 didFinishLaunching 先到，晚了会被丢
        NSAppleEventManager.shared().setEventHandler(self,
                                                     andSelector: #selector(handleGetURL(_:withReplyEvent:)),
                                                     forEventClass: AEEventClass(kCoreEventClass),
                                                     andEventID: AEEventID(kAEGetURL))
    }

    func applicationDidFinishLaunching(_ notification: Notification) {
        diag("did-launch")
        // 任务宿主要有前台态：启动即升 regular（Dock 图标短现，任务结束即退）——
        // accessory 态临到弹窗才升，窗口服务器激活注册常来不及
        NSApp.setActivationPolicy(.regular)
        // 冷启动兜底：初始 GURL 事件可能在 delegate 安装前就进队列——直接读 currentAppleEvent
        if let event = NSAppleEventManager.shared().currentAppleEvent,
           event.eventClass == AEEventClass(kCoreEventClass),
           event.eventID == AEEventID(kAEGetURL),
           let urlString = event.paramDescriptor(forKeyword: keyDirectObject)?.stringValue {
            diag("currentAppleEvent 兜底命中: \(urlString.prefix(100))")
            handleJobURL(urlString)
            return
        }
        // 双保险通道：冷启动 argv（Terminal/open --args 测试用）
        let arguments = ProcessInfo.processInfo.arguments
        if let jobIndex = arguments.firstIndex(of: "--zpack-job"), arguments.count > jobIndex + 1 {
            runJob(Array(arguments[(jobIndex + 1)...]))
            return
        }
        // 主通道：轮询 appex 容器任务文件（appex 写自家容器+拉起本宿主——全可靠原语）
        pollJobFile()
    }

    // appex 沙箱容器内的任务文件（appex bundle id 固定；宿主非沙箱可读任意用户路径）
    private static let appexJobFile = NSHomeDirectory()
        + "/Library/Containers/devplaceholder.UGFOSNW3.zpackFinderMenu.zpackSync/Data/zpack-job.json"

    private func pollJobFile() {
        var attempts = 0
        DispatchQueue.global(qos: .userInitiated).async { [weak self] in
            while attempts < 50 {   // 5 秒窗口（0.1s 间隔）
                attempts += 1
                if let data = try? Data(contentsOf: URL(fileURLWithPath: AppDelegate.appexJobFile)),
                   let json = (try? JSONSerialization.jsonObject(with: data)) as? [String: Any],
                   let verb = json["verb"] as? String,
                   let paths = json["paths"] as? [String] {
                    try? FileManager.default.removeItem(atPath: AppDelegate.appexJobFile)  // 取单即删（防重复执行）
                    self?.diag("任务文件命中：verb=\(verb) paths=\(paths)")
                    self?.runJob([verb] + paths)
                    return
                }
                Thread.sleep(forTimeInterval: 0.1)
            }
            // 5 秒无单 = 裸启动（双击/误触）——退出防吞下一单
            self?.diag("5 秒无任务——裸启动退出")
            DispatchQueue.main.async { NSApp.terminate(nil) }
        }
    }

    private var gotJob = false
    private let hasJobLock = NSLock()

    // GURL AppleEvent 落点
    @objc func handleGetURL(_ event: NSAppleEventDescriptor, withReplyEvent replyEvent: NSAppleEventDescriptor) {
        let urlString = event.paramDescriptor(forKeyword: keyDirectObject)?.stringValue ?? "(空)"
        diag("GURL 收到: \(urlString.prefix(100))")
        guard let received = event.paramDescriptor(forKeyword: keyDirectObject)?.stringValue else { return }
        handleJobURL(received)
    }

    // zpack://job?verb=<verb>&paths=<p1%0Ap2>
    func handleJobURL(_ urlString: String) {
        hasJobLock.lock(); gotJob = true; hasJobLock.unlock()
        guard let url = URL(string: urlString),
              url.scheme?.lowercased() == "zpack", url.host == "job",
              let components = URLComponents(url: url, resolvingAgainstBaseURL: false) else { return }
        let verb = components.queryItems?.first(where: { $0.name == "verb" })?.value ?? "test"
        let joined = components.queryItems?.first(where: { $0.name == "paths" })?.value ?? ""
        let paths = joined.isEmpty ? [] : joined.components(separatedBy: "\n")
        guard !paths.isEmpty else { NSApp.terminate(nil); return }
        runJob([verb] + paths)
    }

    // 任务参数：verb(--zpack-job 后第一个) + paths...
    private func runJob(_ jobArguments: [String]) {
        guard !jobArguments.isEmpty else { NSApp.terminate(nil); return }
        let verb = jobArguments[0]
        let paths = Array(jobArguments[1...])

        DispatchQueue.global(qos: .userInitiated).async { [self] in
            let zpack = hostedZpackPath()
            guard !zpack.isEmpty else {
                presentFailure("zpack ❌ 失败", "未找到内嵌 zpack 二进制")
                return
            }
            var arguments: [String]
            switch verb {
            case "zip", "targz":
                arguments = buildCompressArguments(verb: verb, paths: paths)
            case "extract":
                arguments = ["extract"] + paths
            default:  // test
                arguments = ["test"] + paths
            }
            // 智能解压落点相对 CWD——钉到首路径父目录（Finder 语义：解压到此处）
            if verb == "extract", let firstPath = paths.first {
                let dir = (firstPath as NSString).deletingLastPathComponent
                var isDir: ObjCBool = false
                if FileManager.default.fileExists(atPath: dir, isDirectory: &isDir), isDir.boolValue {
                    FileManager.default.changeCurrentDirectoryPath(dir)
                }
            }

            // 进度窗：标题按动作；大任务判定用输入体量
            let jobTitle: String
            switch verb {
            case "zip": jobTitle = "正在压缩为 ZIP…"
            case "targz": jobTitle = "正在压缩为 TAR.GZ…"
            case "extract": jobTitle = "正在解压…"
            default: jobTitle = "正在测试归档…"
            }
            let inputBytes = Self.inputBytes(verb: verb, paths: paths)
            let task = Process()
            task.executableURL = URL(fileURLWithPath: zpack)
            task.arguments = arguments
            let outPipe = Pipe()
            let errPipe = Pipe()
            task.standardOutput = outPipe
            task.standardError = errPipe
            // 窗口先建（readabilityHandler 与取消线都要引用它）
            let partialOutput: String? = (verb == "zip" || verb == "targz")
                ? outputValue(in: arguments) : nil
            let window = JobWindowController(jobTitle: jobTitle, inputBytes: inputBytes)
            DispatchQueue.main.async {
                window.onCancelled = { [weak self] in
                    self?.cancelJob(task: task, partialOutput: partialOutput)
                }
                window.show()
            }
            // stderr 增量读：进度行（[时间] 压缩中 ██ 25.0% | 500MB/2GB | …）→ 更新进度条
            errPipe.fileHandleForReading.readabilityHandler = { handle in
                let chunk = handle.availableData
                guard !chunk.isEmpty else { handle.readabilityHandler = nil; return }
                if let text = String(data: chunk, encoding: .utf8) {
                    for line in text.split(separator: "\n") {
                        if let progress = Self.parseProgressLine(String(line)) {
                            window.updateProgress(percent: progress.percent, detail: progress.detail)
                        }
                    }
                }
            }
            do { try task.run() } catch {
                presentFailure("zpack ❌ 失败", "启动 zpack 失败：\(error.localizedDescription)")
                return
            }
            let data = outPipe.fileHandleForReading.readDataToEndOfFile()
            task.waitUntilExit()
            errPipe.fileHandleForReading.readabilityHandler = nil

            let success = task.terminationStatus == 0
            let envelope = try? JSONDecoder().decode(ZpackEnvelope.self, from: data)
            let (title, body, revealPath) = Self.resultSummary(success: success, verb: verb,
                                                               envelope: envelope,
                                                               fallbackText: arguments.joined(separator: " "))
            window.finish(success: success, title: title, body: body, revealPath: revealPath)
        }
    }

    // zpack 输出契约信封（stdout 唯一 JSON）
    private struct ZpackEnvelope: Decodable {
        let is_success: Bool?
        let data: ZpackData?
        let error_info: String?
    }
    private struct ZpackData: Decodable {
        let archive: String?
        let output: String?
        let file_count: Int?
        let size_bytes: Int?
        let skipped: Int?
        let tested_entries: Int?
    }

    // 人读字节（B/KB/MB/GB，一位小数）
    private func humanSize(_ bytes: Int) -> String {
        let units = ["B", "KB", "MB", "GB", "TB"]
        var value = Double(bytes)
        var unitIndex = 0
        while value >= 1024, unitIndex < units.count - 1 {
            value /= 1024
            unitIndex += 1
        }
        return unitIndex == 0 ? "\(bytes) B" : String(format: "%.1f %@", value, units[unitIndex])
    }

    // 从构造好的参数取 --output 的值（取消删半成品用）
    private func outputValue(in arguments: [String]) -> String? {
        guard let flagIndex = arguments.firstIndex(of: "--output"),
              arguments.count > flagIndex + 1 else { return nil }
        return arguments[flagIndex + 1]
    }

    private func cancelJob(task: Process, partialOutput: String?) {
        task.terminate()
        task.waitUntilExit()
        if let partial = partialOutput {
            try? FileManager.default.removeItem(atPath: partial)
        }
        DispatchQueue.main.async { NSApp.terminate(nil) }
    }

    private func presentFailure(_ title: String, _ body: String) {
        // 极早期失败（引擎缺失/启动失败）：直接进度窗完成态展示
        let window = JobWindowController(jobTitle: title, inputBytes: 0)
        DispatchQueue.main.async {
            window.show()
            window.finish(success: false, title: title, body: body, revealPath: nil)
        }
    }

    // 输入体量（大任务判定）：压缩=各路径递归求和；解压/测试=归档文件大小
    private static func inputBytes(verb: String, paths: [String]) -> Int64 {
        let fm = FileManager.default
        var total: Int64 = 0
        func addTree(_ path: String) {
            var isDir: ObjCBool = false
            guard fm.fileExists(atPath: path, isDirectory: &isDir) else { return }
            if isDir.boolValue {
                guard let enumerator = fm.enumerator(atPath: path) else { return }
                for case let sub as String in enumerator {
                    let full = path + "/" + sub
                    if let size = (try? fm.attributesOfItem(atPath: full))?[.size] as? Int64 {
                        total += size
                    }
                }
            } else if let size = (try? fm.attributesOfItem(atPath: path))?[.size] as? Int64 {
                total += size
            }
        }
        if verb == "zip" || verb == "targz" {
            paths.forEach(addTree)
        } else if let first = paths.first {
            addTree(first)   // 归档本体大小
        }
        return total
    }

    // stderr 进度行解析：`[时间] 压缩中 ██ 25.0% | 500.0MB/2.0GB | 100.0MB/s | 1/4 文件 | 剩余 15秒`
    private static func parseProgressLine(_ line: String) -> (percent: Double, detail: String)? {
        // 去时间戳前缀
        var body = line
        if let close = line.firstIndex(of: "]") {
            body = String(line[line.index(after: close)...])
        }
        guard body.contains("中"), body.contains("%") else { return nil }
        // 百分比：第一个 N.N%
        guard let percentRange = body.range(of: #"([0-9]+(?:\.[0-9]+)?)%"#, options: .regularExpression) else { return nil }
        let percent = Double(body[percentRange].dropLast()) ?? 0
        // 明细：百分比之后的 `| a | b | c` → `a · b · c`
        var detail = String(body[percentRange.upperBound...])
        detail = detail.replacingOccurrences(of: "|", with: "·")
        detail = detail.trimmingCharacters(in: .whitespacesAndNewlines)
            .replacingOccurrences(of: "  +", with: " ", options: .regularExpression)
        return (percent, detail)
    }

    private func buildCompressArguments(verb: String, paths: [String]) -> [String] {
        // 输出钉到首路径同目录（不依赖 CWD）；多文件平铺进包根；
        // 文件名=去扩展名（照片.jpg → 照片.zip，绝不 .jpg.zip）
        guard let firstPath = paths.first else { return [] }
        let dir = (firstPath as NSString).deletingLastPathComponent
        let name = ((firstPath as NSString).lastPathComponent as NSString).deletingPathExtension
        let ext = (verb == "targz") ? "tar.gz" : "zip"
        let target = dir + "/" + name + "." + ext
        return ["compress"] + paths + ["--output", uniquePath(for: target)]
    }

    // 同名不覆盖（Finder 语义）：照片.zip 已在 → 照片 2.zip → 照片 3.zip…
    private func uniquePath(for path: String) -> String {
        let fm = FileManager.default
        guard fm.fileExists(atPath: path) else { return path }
        let dir = (path as NSString).deletingLastPathComponent
        let name = ((path as NSString).lastPathComponent as NSString).deletingPathExtension
        let ext = (path as NSString).pathExtension
        for index in 2...9999 {
            let candidate = ext.isEmpty
                ? dir + "/" + name + " \(index)"
                : dir + "/" + name + " \(index)." + ext
            if !fm.fileExists(atPath: candidate) { return candidate }
        }
        return dir + "/" + name + "-" + String(Int(Date().timeIntervalSince1970)) + "." + ext  // 兜底
    }

    // 内嵌引擎：本 app Resources/zpack（宿主自身 bundle）
    private func hostedZpackPath() -> String {
        // 2026-11-24 引擎改名 zpack-cli；旧名 zpack 兜底
        let resourcePath = Bundle.main.path(forResource: "zpack-cli", ofType: nil)
            ?? Bundle.main.path(forResource: "zpack", ofType: nil)
        if let enginePath = resourcePath, FileManager.default.isExecutableFile(atPath: enginePath) {
            return enginePath
        }
        return ""
    }

    // 从信封 JSON 裸提取首个命中的字符串字段（无 JSON 解析依赖）
    private func extractField(from json: String, keys: [String]) -> String? {
        for key in keys {
            if let range = json.range(of: key) {
                let start = range.upperBound
                if let end = json[start...].range(of: "\"")?.lowerBound {
                    return String(json[start..<end])
                }
            }
        }
        return nil
    }

    // 结果文案（标题说动作，正文说清产物——文件名+规模+位置）
    private static func resultSummary(success: Bool, verb: String,
                                      envelope: ZpackEnvelope?, fallbackText: String)
        -> (title: String, body: String, revealPath: String?) {
        let payload = envelope?.data
        let productPath = payload?.archive ?? payload?.output
        guard success else {
            return ("zpack ❌ 失败", envelope?.error_info ?? fallbackText, nil)
        }
        let appDelegate = AppDelegate.sharedRef
        let name = (productPath as NSString?)?.lastPathComponent ?? ""
        let location = productPath.map { ($0 as NSString).deletingLastPathComponent } ?? ""
        var scale = ""
        if let files = payload?.file_count {
            scale = "\(files) 个文件"
            if let bytes = payload?.size_bytes, bytes > 0, let sizeText = appDelegate?.humanSize(bytes) {
                scale += " · \(sizeText)"
            }
        }
        switch verb {
        case "zip", "targz":
            let body = name.isEmpty ? fallbackText : "\(name)\(scale.isEmpty ? "" : "\n\(scale)")\n位置：\(location)"
            return ("zpack ✅ 已生成归档", body, productPath)
        case "extract":
            var line = scale
            if let skipped = payload?.skipped, skipped > 0 { line += "\n跳过已存在：\(skipped)" }
            let body = line.isEmpty ? fallbackText : "\(line)\n位置：\(location)"
            return ("zpack ✅ 已解压", body, productPath)
        default:
            let tested = payload?.tested_entries.map { "\($0) 个条目完好" } ?? ""
            return ("zpack ✅ 测试通过", tested.isEmpty ? fallbackText : "\(tested)\n\(name)", productPath)
        }
    }
}
