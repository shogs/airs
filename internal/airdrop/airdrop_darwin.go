//go:build darwin && cgo

package airdrop

/*
#cgo CFLAGS: -x objective-c -fobjc-arc
#cgo LDFLAGS: -framework AppKit
#import <AppKit/AppKit.h>
#include <stdlib.h>

// 0 sent, 1 failed, 2 unavailable, 3 cancelled
static int airsResult = -1;

@interface AirsShareDelegate : NSObject <NSSharingServiceDelegate>
@end

@implementation AirsShareDelegate
- (void)finish:(int)r {
	airsResult = r;
	[NSApp stop:nil];
	// stop: takes effect after the next event; post one so run returns now.
	NSEvent *e = [NSEvent otherEventWithType:NSEventTypeApplicationDefined location:NSZeroPoint
		modifierFlags:0 timestamp:0 windowNumber:0 context:nil subtype:0 data1:0 data2:0];
	[NSApp postEvent:e atStart:YES];
}
- (void)sharingService:(NSSharingService *)s didShareItems:(NSArray *)items { [self finish:0]; }
- (void)sharingService:(NSSharingService *)s didFailToShareItems:(NSArray *)items error:(NSError *)err {
	[self finish:(err.code == NSUserCancelledError ? 3 : 1)];
}
@end

static int airsAirDrop(const char *u) {
	@autoreleasepool {
		[NSApplication sharedApplication];
		[NSApp setActivationPolicy:NSApplicationActivationPolicyAccessory];
		NSURL *url = [NSURL URLWithString:[NSString stringWithUTF8String:u]];
		NSSharingService *s = [NSSharingService sharingServiceNamed:NSSharingServiceNameSendViaAirDrop];
		if (url == nil || s == nil || ![s canPerformWithItems:@[url]]) return 2;
		AirsShareDelegate *d = [AirsShareDelegate new];
		s.delegate = d;
		airsResult = -1;
		[NSApp activateIgnoringOtherApps:YES];
		[s performWithItems:@[url]];
		[NSApp run];
		return airsResult;
	}
}
*/
import "C"

import (
	"errors"
	"unsafe"
)

// Send opens the AirDrop picker for url and blocks until it is sent or
// cancelled. It must be called on the main OS thread (cmd/airs locks it).
func Send(url string) error {
	cs := C.CString(url)
	defer C.free(unsafe.Pointer(cs))
	switch C.airsAirDrop(cs) {
	case 0:
		return nil
	case 2:
		return ErrUnavailable
	case 3:
		return ErrCancelled
	}
	return errors.New("AirDrop failed")
}
