//go:build darwin && cgo

#import <Cocoa/Cocoa.h>
#include <stdint.h>

typedef void *TCWakeObserverRef;

extern void goWakeSignal(uintptr_t signal);

@interface TCWakeObserver : NSObject
@property(nonatomic, strong) id token;
@property(nonatomic) uintptr_t signal;
- (instancetype)initWithSignal:(uintptr_t)signal;
- (void)stop;
@end

@implementation TCWakeObserver

- (instancetype)initWithSignal:(uintptr_t)signal {
    self = [super init];
    if (self == nil) {
        return nil;
    }
    _signal = signal;
    __weak TCWakeObserver *weakSelf = self;
    _token = [NSWorkspace.sharedWorkspace.notificationCenter
        addObserverForName:NSWorkspaceDidWakeNotification
                    object:nil
                     queue:nil
                usingBlock:^(NSNotification *notification) {
                    TCWakeObserver *observer = weakSelf;
                    @synchronized(observer) {
                        if (observer != nil && observer.signal != 0) {
                            goWakeSignal(observer.signal);
                        }
                    }
                }];
    return self;
}

- (void)stop {
    @synchronized(self) {
        _signal = 0;
        if (_token != nil) {
            [NSWorkspace.sharedWorkspace.notificationCenter removeObserver:_token];
            _token = nil;
        }
    }
}

@end

TCWakeObserverRef tc_wake_create(uintptr_t signal) {
    TCWakeObserver *observer = [[TCWakeObserver alloc] initWithSignal:signal];
    return (__bridge_retained TCWakeObserverRef)observer;
}

void tc_wake_destroy(TCWakeObserverRef ref) {
    if (ref == NULL) {
        return;
    }
    TCWakeObserver *observer = (__bridge_transfer TCWakeObserver *)ref;
    [observer stop];
}

void tc_wake_test_post(void) {
    [NSWorkspace.sharedWorkspace.notificationCenter postNotificationName:NSWorkspaceDidWakeNotification object:nil];
}
