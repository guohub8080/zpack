// zpack Finder 右键菜单（FinderSync 扩展）
// 菜单：「zpack ▸ 压缩为 ZIP / 压缩为 TAR.GZ | 解压到此处 / 测试归档」
// 引擎：调用内嵌的 zpack 二进制（Go 编译），JSON 契约消费

import FinderSync

class FinderSync: FIFinderSync {
    // 菜单构建时的选中快照（动作触发时菜单已关，selectedItemURLs 可能失效）
    private var selectionSnapshot: [String] = []

    // zpack 二进制定位（内嵌 > /usr/local/bin > 项目目录）
    private var zpackPath: String {
        let bundle = Bundle(for: FinderSync.self)
        // appex 在 Host.app/Contents/PlugIns/ 下，zpack 在 Host.app/Contents/Resources/
        let hostResources = bundle.bundleURL
            .deletingLastPathComponent()
            .deletingLastPathComponent()
            .appendingPathComponent("Contents/Resources/zpack").path
        if FileManager.default.isExecutableFile(atPath: hostResources) {
            return hostResources
        }
        if FileManager.default.isExecutableFile(atPath: "/usr/local/bin/zpack") {
            return "/usr/local/bin/zpack"
        }
        let expand = NSString(string: "~/WebstormProjects/zpack/bin/zpack").expandingTildeInPath
        if FileManager.default.isExecutableFile(atPath: expand) { return expand }
        return "/usr/local/bin/zpack"
    }
    
    override init() {
        super.init()
        FIFinderSyncController.default().directoryURLs = [URL(fileURLWithPath: "/")]
    }
    
    override func menu(for menuKind: FIMenuKind) -> NSMenu {
        let items = FIFinderSyncController.default().selectedItemURLs() ?? []
        guard !items.isEmpty else { return NSMenu() }
        // 菜单构建时快照选中路径——动作触发时（菜单已关）selectedItemURLs 可能拿不回
        selectionSnapshot = items.map(\.path)
        diag("menu 构建：选中 \(items.count) 项")
        
        // 分类：归档 vs 非归档（用 zpack list 魔数判定）
        var archives: [URL] = []
        var plain: [URL] = []
        for url in items {
            var isDir: ObjCBool = false
            FileManager.default.fileExists(atPath: url.path, isDirectory: &isDir)
            if isDir.boolValue { plain.append(url); continue }
            if isArchive(url) { archives.append(url) } else { plain.append(url) }
        }
        
        let submenu = NSMenu()

        // 子菜单纯文字（只有一级 zpack 入口挂图标——克制不喧宾夺主）
        if !plain.isEmpty {
            let zipItem = submenu.addItem(withTitle: "压缩为 ZIP", action: #selector(runZip(_:)), keyEquivalent: "")
            zipItem.target = self
            let tarItem = submenu.addItem(withTitle: "压缩为 TAR.GZ", action: #selector(runTarGz(_:)), keyEquivalent: "")
            tarItem.target = self
        }
        if !plain.isEmpty && !archives.isEmpty {
            submenu.addItem(NSMenuItem.separator())
        }
        if !archives.isEmpty {
            let extractItem = submenu.addItem(withTitle: "解压到此处", action: #selector(runExtract(_:)), keyEquivalent: "")
            extractItem.target = self
            let testItem = submenu.addItem(withTitle: "测试归档", action: #selector(runTest(_:)), keyEquivalent: "")
            testItem.target = self
        }
        
        let root = NSMenu()
        let zpackItem = root.addItem(withTitle: "zpack", action: nil, keyEquivalent: "")
        // 菜单挂「透明底造型版」zpack-menu.icns（SVG 原汁原味、无底板；
        // app 图标仍是圆角矩形版 zpack.icns——两套图各司其职）
        if let iconPath = Bundle(for: FinderSync.self).path(forResource: "zpack-menu", ofType: "icns"),
           let icon = NSImage(contentsOfFile: iconPath) {
            icon.size = NSSize(width: 16, height: 16)
            icon.isTemplate = false  // 保彩色造型（true 会变单色剪影）
            zpackItem.image = icon
        }
        zpackItem.submenu = submenu
        return root
    }
    
    // MARK: - 归档判定（zpack list 退出码 = 魔数识别）
    private func isArchive(_ url: URL) -> Bool {
        let task = Process()
        task.executableURL = URL(fileURLWithPath: zpackPath)
        task.arguments = ["list", url.path]
        task.standardOutput = Pipe()
        task.standardError = Pipe()
        do { try task.run(); task.waitUntilExit() } catch { return false }
        return task.terminationStatus == 0
    }
    
    // MARK: - 动作（不在沙箱扩展里干活——唤醒宿主 app 执行 + 弹窗，Bandizip 同构）

    @objc func runZip(_ sender: NSMenuItem) {
        diag("动作触发：zip")
        dispatchToHost(verb: "zip", paths: selectedPaths())
    }

    @objc func runTarGz(_ sender: NSMenuItem) {
        diag("动作触发：targz")
        dispatchToHost(verb: "targz", paths: selectedPaths())
    }

    @objc func runExtract(_ sender: NSMenuItem) {
        diag("动作触发：extract")
        dispatchToHost(verb: "extract", paths: selectedPaths())
    }

    @objc func runTest(_ sender: NSMenuItem) {
        diag("动作触发：test")
        dispatchToHost(verb: "test", paths: selectedPaths())
    }

    private func selectedPaths() -> [String] {
        // 快照优先；无快照兜底现取（带诊断日志定位空路径来源）
        let live = (FIFinderSyncController.default().selectedItemURLs() ?? []).map(\.path)
        let result = selectionSnapshot.isEmpty ? live : selectionSnapshot
        diag("selectedPaths：snapshot=\(selectionSnapshot.count) live=\(live.count) 用 \(result.count)")
        return result
    }

    // 分派主通道：任务文件 + LS 拉起宿主（全可靠原语——沙箱内写自家容器 100% 合法、
    // 宿主非沙箱可读任意路径、进程启动不走任何事件投递；GURL 在本机实测不可达）
    private func dispatchToHost(verb: String, paths: [String]) {
        diag("dispatch verb=\(verb) paths=\(paths)")
        guard !paths.isEmpty else { return }
        // 1) 任务 JSON 写进自家容器（NSHomeDirectory=沙箱容器根）
        let jobURL = URL(fileURLWithPath: NSHomeDirectory() + "/zpack-job.json")
        let job: [String: Any] = ["verb": verb, "paths": paths]
        guard JSONSerialization.isValidJSONObject(job),
              let data = try? JSONSerialization.data(withJSONObject: job) else {
            diag("任务 JSON 构造失败"); return
        }
        do { try data.write(to: jobURL) } catch {
            diag("任务文件写入失败：\(error.localizedDescription)"); return
        }
        diag("任务文件已写 \(jobURL.path)")
        // 2) 拉起宿主（不带参数——宿主自己读任务文件）
        let configuration = NSWorkspace.OpenConfiguration()
        configuration.activates = true
        NSWorkspace.shared.openApplication(at: appHostURL(), configuration: configuration) { runningApp, error in
            if let error = error {
                self.diag("拉起宿主失败：\(error.localizedDescription)")
            } else {
                self.diag("宿主已拉起 \(runningApp?.bundleIdentifier ?? "?")——它会来读任务文件")
            }
        }
    }

    // 宿主 app URL（PlugIns/<appex> → Host.app）
    private func appHostURL() -> URL {
        Bundle(for: FinderSync.self).bundleURL
            .deletingLastPathComponent()
            .deletingLastPathComponent()
            .deletingLastPathComponent()
    }

    // MARK: - 文件探针（沙箱容器内写自家=永远合法；统一日志在 appex 里不可靠）
    private func diag(_ message: String) {
        let stamp = DateFormatter.localizedString(from: Date(), dateStyle: .short, timeStyle: .medium)
        let line = "\(stamp) \(message)\n"
        let url = URL(fileURLWithPath: NSHomeDirectory() + "/zpack-diag.log")
        if let handle = FileHandle(forWritingAtPath: url.path) {
            handle.seekToEndOfFile()
            if let data = line.data(using: .utf8) { handle.write(data) }
            try? handle.close()
        } else {
            try? line.data(using: .utf8)?.write(to: url)
        }
    }
}
