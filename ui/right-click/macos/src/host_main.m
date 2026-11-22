// zpack Finder 菜单宿主： FinderSync 扩展必须挂在 .app 下；
// LSUIElement=1 无 Dock 图标。启动一次即完成扩展注册，之后可退出。
#import <AppKit/AppKit.h>
int main(int argc, const char *argv[]) {
    return NSApplicationMain(argc, argv);
}
