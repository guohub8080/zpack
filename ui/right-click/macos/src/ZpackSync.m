// zpack Finder 右键菜单（FinderSync 扩展主体）。
// 菜单形态：一级菜单「zpack」▸（选中含归档）解压到此处/测试归档｜（含非归档）压缩为 ZIP/TAR.GZ｜混合全显。
// 归档判定=驱动 zpack 二进制（魔数识别，与 CLI 契约同源——不按扩展名猜）。
#import <AppKit/AppKit.h>
#import <FinderSync/FinderSync.h>
#import <dispatch/dispatch.h>

// ZPACK_MENU_PROBE_CAP 菜单构建期归档探测的文件数上限（探测=zpack list 进程；超出按非归档计）
static const NSInteger ZPACK_MENU_PROBE_CAP = 20;

@interface ZpackSync : FIFinderSync <NSMenuItemValidation>
- (void)runCommand:(NSMenuItem *)sender;
@end

@implementation ZpackSync

#pragma mark - 生命周期

- (instancetype)init {
    self = [super init];
    if (self) {
        // 监视整个磁盘（右键任何位置都出菜单；不读取内容——只收选择事件）
        [[FIFinderSyncController defaultController] setDirectoryURLs:[NSSet setWithObject:[NSURL fileURLWithPath:@"/"]]];
    }
    return self;
}

#pragma mark - zpack 二进制定位（内嵌优先：Host.app/Contents/Resources/zpack）

- (NSString *)zpackPath {
    // 本类所在 bundle = appex；Host = 上两级（PlugIns/<appex> → Host.app）
    NSURL *appexURL = [[NSBundle bundleForClass:[self class]] bundleURL];
    NSURL *hostURL = [[appexURL URLByDeletingLastPathComponent] URLByDeletingLastPathComponent];
    NSArray *candidates = @[
        [hostURL URLByAppendingPathComponent:@"Contents/Resources/zpack"],
        [NSURL fileURLWithPath:@"/usr/local/bin/zpack"],
        [NSURL fileURLWithPath:[@"~/WebstormProjects/zpack/bin/zpack" stringByExpandingTildeInPath]],
    ];
    for (NSURL *candidate in candidates) {
        if ([[NSFileManager defaultManager] isExecutableFileAtPath:candidate.path]) {
            return candidate.path;
        }
    }
    return nil;
}

#pragma mark - 归档判定（魔数——zpack list 退出码；同步执行，菜单构建期）

- (BOOL)isArchive:(NSURL *)url {
    NSString *zpack = [self zpackPath];
    if (!zpack) return NO;
    NSTask *task = [[NSTask alloc] init];
    task.executableURL = [NSURL fileURLWithPath:zpack];
    task.arguments = @[@"list", url.path];
    task.standardOutput = [NSPipe pipe];
    task.standardError = [NSPipe pipe];
    NSError *runError = nil;
    [task launchAndReturnError:&runError];
    if (runError) return NO;
    [task waitUntilExit];
    return task.terminationStatus == 0;
}

#pragma mark - 菜单构建

- (NSMenu *)menuForMenuKind:(FIMenuKind)menuKind {
    NSArray<NSURL *> *selection = [self currentSelection];
    if (selection.count == 0) return nil; // 空选择（空白处右键）不显示

    NSMutableArray<NSURL *> *archives = [NSMutableArray array];
    NSMutableArray<NSURL *> *plainItems = [NSMutableArray array];
    NSInteger probeCount = 0;
    for (NSURL *url in selection) {
        BOOL isDir = NO;
        [[NSFileManager defaultManager] fileExistsAtPath:url.path isDirectory:&isDir];
        if (isDir) {
            [plainItems addObject:url]; // 目录永远走压缩侧
            continue;
        }
        if (probeCount < ZPACK_MENU_PROBE_CAP && [self isArchive:url]) {
            [archives addObject:url];
        } else {
            [plainItems addObject:url];
        }
        probeCount++;
    }
    if (archives.count == 0 && plainItems.count == 0) return nil;

    NSMenu *submenu = [[NSMenu alloc] init];
    if (plainItems.count > 0) {
        NSMenuItem *zipItem = [submenu addItemWithTitle:@"压缩为 ZIP" action:@selector(runCommand:) keyEquivalent:@""];
        zipItem.target = self;
        zipItem.representedObject = @{@"cmd": @"zip", @"urls": plainItems};
        NSMenuItem *tarItem = [submenu addItemWithTitle:@"压缩为 TAR.GZ" action:@selector(runCommand:) keyEquivalent:@""];
        tarItem.target = self;
        tarItem.representedObject = @{@"cmd": @"targz", @"urls": plainItems};
    }
    if (plainItems.count > 0 && archives.count > 0) {
        [submenu addItem:[NSMenuItem separatorItem]];
    }
    if (archives.count > 0) {
        NSMenuItem *extractItem = [submenu addItemWithTitle:@"解压到此处" action:@selector(runCommand:) keyEquivalent:@""];
        extractItem.target = self;
        extractItem.representedObject = @{@"cmd": @"extract", @"urls": archives};
        NSMenuItem *testItem = [submenu addItemWithTitle:@"测试归档" action:@selector(runCommand:) keyEquivalent:@""];
        testItem.target = self;
        testItem.representedObject = @{@"cmd": @"test", @"urls": archives};
    }

    NSMenu *root = [[NSMenu alloc] init];
    NSMenuItem *top = [root addItemWithTitle:@"zpack" action:NULL keyEquivalent:@""];
    top.submenu = submenu;
    return root;
}

- (NSArray<NSURL *> *)currentSelection {
#pragma clang diagnostic push
#pragma clang diagnostic ignored "-Wdeprecated-declarations"
    return [[FIFinderSyncController defaultController] selectedItemURLs];
#pragma clang diagnostic pop
}

#pragma mark - 动作执行（后台队列；完成经系统通知回报 JSON 结果）

- (void)runCommand:(NSMenuItem *)sender {
    NSDictionary *payload = sender.representedObject;
    NSString *command = payload[@"cmd"];
    NSArray<NSURL *> *urls = payload[@"urls"];
    // 拷贝进 block（菜单项生命周期短）
    NSString *commandCopy = [command copy];
    NSArray<NSURL *> *urlsCopy = [urls copy];
    NSString *zpack = [self zpackPath];
    dispatch_async(dispatch_get_global_queue(QOS_CLASS_USER_INITIATED, 0), ^{
        if (!zpack) {
            [self notify:@"未找到 zpack 二进制" body:@"请重装 zpack Finder 菜单应用"];
            return;
        }
        for (NSURL *url in urlsCopy) {
            [self runZpack:zpack command:commandCopy url:url];
        }
    });
}

- (void)runZpack:(NSString *)zpack command:(NSString *)command url:(NSURL *)url {
    NSTask *task = [[NSTask alloc] init];
    task.executableURL = [NSURL fileURLWithPath:zpack];
    NSString *parentDir = [[url URLByDeletingLastPathComponent] path];
    task.currentDirectoryURL = [NSURL fileURLWithPath:parentDir];
    task.arguments = [self argumentsForCommand:command url:url];
    NSPipe *outPipe = [NSPipe pipe];
    task.standardOutput = outPipe;
    task.standardError = [NSPipe pipe];
    NSError *runError = nil;
    [task launchAndReturnError:&runError];
    if (runError) {
        [self notify:@"启动失败" body:runError.localizedDescription];
        return;
    }
    NSData *outData = [[outPipe fileHandleForReading] readDataToEndOfFile];
    [task waitUntilExit];
    NSString *json = [[NSString alloc] initWithData:outData encoding:NSUTF8StringEncoding] ?: @"";
    NSString *verdict = (task.terminationStatus == 0) ? @"成功" : @"失败";
    NSString *detail = [self shortResultFromJSON:json fallback:url.lastPathComponent];
    [self notify:[NSString stringWithFormat:@"%@：%@", verdict, commandLabel(command)]
             body:detail];
}

- (NSArray<NSString *> *)argumentsForCommand:(NSString *)command url:(NSURL *)url {
    if ([command isEqualToString:@"extract"]) {
        return @[@"extract", url.path]; // 智能解压：单根直落、散包包一层（Finder 友善）
    }
    if ([command isEqualToString:@"test"]) {
        return @[@"test", url.path];
    }
    if ([command isEqualToString:@"targz"]) {
        NSString *outputName = [NSString stringWithFormat:@"%@.tar.gz", url.lastPathComponent.stringByDeletingPathExtension];
        return @[@"compress", url.path, @"--output", [[url URLByDeletingLastPathComponent].path stringByAppendingPathComponent:outputName]];
    }
    // zip：不给 --output——zpack 缺省 CWD/<首输入名>.zip（currentDirectory=父目录）
    return @[@"compress", url.path];
}

- (NSString *)shortResultFromJSON:(NSString *)json fallback:(NSString *)fallback {
    // 从信封 JSON 提取一行人类摘要：优先 error_info，否则 archive/output 路径
    NSRange infoRange = [json rangeOfString:@"\"error_info\":\""];
    if (infoRange.location != NSNotFound) {
        NSUInteger start = NSMaxRange(infoRange);
        NSUInteger end = [json rangeOfString:@"\"" options:0 range:NSMakeRange(start, json.length - start)].location;
        if (end > start) return [json substringWithRange:NSMakeRange(start, end - start)];
    }
    for (NSString *key in @[@"\"archive\":\"", @"\"output\":\""]) {
        NSRange range = [json rangeOfString:key];
        if (range.location != NSNotFound) {
            NSUInteger start = NSMaxRange(range);
            NSUInteger end = [json rangeOfString:@"\"" options:0 range:NSMakeRange(start, json.length - start)].location;
            if (end > start) return [json substringWithRange:NSMakeRange(start, end - start)];
        }
    }
    return fallback;
}

- (void)notify:(NSString *)title body:(NSString *)body {
    NSString *escapedTitle = [self appleScriptEscape:title];
    NSString *escapedBody = [self appleScriptEscape:body];
    NSString *script = [NSString stringWithFormat:@"display notification \"%@\" with title \"zpack\" subtitle \"%@\"", escapedBody, escapedTitle];
    NSTask *task = [[NSTask alloc] init];
    task.executableURL = [NSURL fileURLWithPath:@"/usr/bin/osascript"];
    task.arguments = @[@"-e", script];
    task.standardOutput = [NSPipe pipe];
    task.standardError = [NSPipe pipe];
    [task launchAndReturnError:nil];
    [task waitUntilExit];
}

- (NSString *)appleScriptEscape:(NSString *)text {
    return [[[text stringByReplacingOccurrencesOfString:@"\\\\" withString:@"\\\\\\\\"]
             stringByReplacingOccurrencesOfString:@"\"" withString:@"\\\\\""]
            stringByReplacingOccurrencesOfString:@"\n" withString:@" "];
}

static NSString *commandLabel(NSString *command) {
    if ([command isEqualToString:@"extract"]) return @"解压";
    if ([command isEqualToString:@"test"]) return @"测试归档";
    if ([command isEqualToString:@"targz"]) return @"压缩 TAR.GZ";
    return @"压缩 ZIP";
}

@end
