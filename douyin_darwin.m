// Mac：隐藏的 WKWebView 打开抖音网页（douyin.go 说明）。窗口默认不显示，抖音要求验证时才显示出来让用户手动过。
#import <Cocoa/Cocoa.h>
#import <WebKit/WebKit.h>

extern void sphdlDouyinMsg(char *s);

@interface SphdlDyHandler : NSObject <WKScriptMessageHandler>
@end
@implementation SphdlDyHandler
- (void)userContentController:(WKUserContentController *)c didReceiveScriptMessage:(WKScriptMessage *)m {
	if ([m.body isKindOfClass:[NSString class]]) sphdlDouyinMsg((char *)[(NSString *)m.body UTF8String]);
}
@end

static NSWindow *dyWin;
static WKWebView *dyView;

void sphdlDouyinOpen(const char *url, const char *script) {
	NSString *u = [NSString stringWithUTF8String:url];
	NSString *js = [NSString stringWithUTF8String:script];
	dispatch_async(dispatch_get_main_queue(), ^{
		if (dyWin) { [dyWin orderOut:nil]; dyWin = nil; dyView = nil; }
		WKWebViewConfiguration *cfg = [[WKWebViewConfiguration alloc] init];
		cfg.websiteDataStore = [WKWebsiteDataStore defaultDataStore]; // 记住抖音给的游客身份，下次更快
		cfg.mediaTypesRequiringUserActionForPlayback = WKAudiovisualMediaTypeAll; // 别自动播放出声
		[cfg.userContentController addScriptMessageHandler:[[SphdlDyHandler alloc] init] name:@"sphdl"];
		[cfg.userContentController addUserScript:[[WKUserScript alloc] initWithSource:js
			injectionTime:WKUserScriptInjectionTimeAtDocumentStart forMainFrameOnly:YES]];
		NSRect r = NSMakeRect(0, 0, 1100, 760);
		dyWin = [[NSWindow alloc] initWithContentRect:r
			styleMask:NSWindowStyleMaskTitled | NSWindowStyleMaskClosable | NSWindowStyleMaskResizable
			backing:NSBackingStoreBuffered defer:NO];
		dyWin.releasedWhenClosed = NO;
		dyWin.title = @"抖音验证：完成后会自动继续";
		dyView = [[WKWebView alloc] initWithFrame:r configuration:cfg];
		dyWin.contentView = dyView;
		[dyView loadRequest:[NSURLRequest requestWithURL:[NSURL URLWithString:u]]];
	});
}

void sphdlDouyinShow(void) {
	dispatch_async(dispatch_get_main_queue(), ^{
		if (!dyWin) return;
		[dyWin center];
		[dyWin makeKeyAndOrderFront:nil];
		[NSApp activateIgnoringOtherApps:YES];
	});
}

void sphdlDouyinClose(void) {
	dispatch_async(dispatch_get_main_queue(), ^{
		if (!dyWin) return;
		[dyView stopLoading];
		[dyView loadHTMLString:@"" baseURL:nil];
		[dyView.configuration.userContentController removeScriptMessageHandlerForName:@"sphdl"];
		[dyWin orderOut:nil];
		dyWin = nil;
		dyView = nil;
	});
}

// 只给测试用：没有主窗口时跑 Mac 的事件循环（WKWebView 要靠它加载网页）
void sphdlRunLoopForTest(void) {
	[NSApplication sharedApplication];
	[NSApp setActivationPolicy:NSApplicationActivationPolicyAccessory];
	[NSApp run];
}
