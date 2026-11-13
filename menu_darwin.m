// Mac 菜单栏：没有“编辑”菜单的话，⌘V/⌘C/⌘A 在窗口里都不起作用。
#import <Cocoa/Cocoa.h>

extern void sphdlWillTerminate(void);

static NSMenuItem *item(NSString *title, SEL action, NSString *key) {
	return [[NSMenuItem alloc] initWithTitle:title action:action keyEquivalent:key];
}

void sphdlSetupMenu(void) {
	NSMenu *bar = [[NSMenu alloc] init];

	NSMenuItem *appItem = [[NSMenuItem alloc] init];
	NSMenu *appMenu = [[NSMenu alloc] init];
	[appMenu addItem:item(@"隐藏视频号下载器", @selector(hide:), @"h")];
	[appMenu addItem:[NSMenuItem separatorItem]];
	[appMenu addItem:item(@"退出视频号下载器", @selector(terminate:), @"q")];
	[appItem setSubmenu:appMenu];
	[bar addItem:appItem];

	NSMenuItem *editItem = [[NSMenuItem alloc] init];
	NSMenu *edit = [[NSMenu alloc] initWithTitle:@"编辑"];
	[edit addItem:item(@"撤销", @selector(undo:), @"z")];
	[edit addItem:item(@"重做", @selector(redo:), @"Z")];
	[edit addItem:[NSMenuItem separatorItem]];
	[edit addItem:item(@"剪切", @selector(cut:), @"x")];
	[edit addItem:item(@"拷贝", @selector(copy:), @"c")];
	[edit addItem:item(@"粘贴", @selector(paste:), @"v")];
	[edit addItem:item(@"全选", @selector(selectAll:), @"a")];
	[editItem setSubmenu:edit];
	[bar addItem:editItem];

	NSMenuItem *winItem = [[NSMenuItem alloc] init];
	NSMenu *win = [[NSMenu alloc] initWithTitle:@"窗口"];
	[win addItem:item(@"最小化", @selector(performMiniaturize:), @"m")];
	[winItem setSubmenu:win];
	[bar addItem:winItem];

	[NSApp setMainMenu:bar];

	// ⌘Q / 程序坞“退出”时先还原网络设置
	[[NSNotificationCenter defaultCenter] addObserverForName:NSApplicationWillTerminateNotification
	                                                  object:nil queue:nil
	                                              usingBlock:^(NSNotification *n) { sphdlWillTerminate(); }];
}
