//go:build public_macos_arm64 && darwin && arm64 && !ios && cgo

#import <Cocoa/Cocoa.h>
#import <Sparkle/Sparkle.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>

typedef void *TCSPBridgeRef;

typedef NS_ENUM(NSInteger, TCSPAction) {
    TCSPActionNone = 0,
    TCSPActionCheck = 1,
    TCSPActionDownload = 2,
    TCSPActionInstall = 3,
};

typedef NS_ENUM(NSInteger, TCSPState) {
    TCSPStateIdle = 0,
    TCSPStatePending = 1,
    TCSPStateDone = 2,
    TCSPStateFailed = 3,
};

static char *tc_copy_string(NSString *value) {
    if (value == nil) {
        return NULL;
    }
    return strdup(value.UTF8String);
}

// Reduce Sparkle's NSError surface to a small, stable allowlist.  Never pass
// localized descriptions, paths, or userInfo values across the C boundary.
static NSString *tc_safe_sparkle_error_code(NSError *error, TCSPAction action) {
    if ([error.domain isEqualToString:SUSparkleErrorDomain]) {
        switch (error.code) {
            case SUSignatureError:
                return @"update_signature_invalid";
            case SUValidationError:
                return @"update_validation_failed";
            case SURunningFromDiskImageError:
            case SURunningTranslocated:
                return @"update_running_from_disk_image";
            case SUFileCopyFailure:
            case SUAuthenticationFailure:
            case SUMissingUpdateError:
            case SUMissingInstallerToolError:
            case SURelaunchError:
            case SUInstallationError:
            case SUDowngradeError:
            case SUInstallationCanceledError:
            case SUInstallationAuthorizeLaterError:
            case SUNotValidUpdateError:
            case SUAgentInvalidationError:
            case SUInstallationWriteNoPermissionError:
                return @"update_install_failed";
            default:
                break;
        }
    }
    switch (action) {
        case TCSPActionInstall:
            return @"update_install_failed";
        case TCSPActionDownload:
            return @"download_failed";
        default:
            return @"operation_failed";
    }
}

static void tc_on_main_sync(dispatch_block_t block) {
    if (NSThread.isMainThread) {
        block();
    } else {
        dispatch_sync(dispatch_get_main_queue(), block);
    }
}

static void tc_on_main_async(dispatch_block_t block) {
    if (NSThread.isMainThread) {
        block();
    } else {
        dispatch_async(dispatch_get_main_queue(), block);
    }
}

@interface TCSPBridge : NSObject <SPUUpdaterDelegate, SPUUserDriver>
@property(nonatomic, strong) SPUUpdater *updater;
@property(nonatomic) TCSPAction action;
@property(atomic) TCSPState state;
@property(atomic) BOOL available;
@property(atomic) NSInteger progress;
@property(nonatomic) uint64_t expectedBytes;
@property(nonatomic) uint64_t receivedBytes;
@property(atomic, copy) NSString *version;
@property(atomic, copy) NSString *errorCode;
@property(nonatomic, copy) void (^cancellation)(void);
@property(nonatomic, copy) void (^readyReply)(SPUUserUpdateChoice);
- (nullable instancetype)initWithExpectedFeedURL:(NSString *)expectedFeedURL
                                       errorCode:(NSString **)errorCode;
- (BOOL)beginCheckWithErrorCode:(NSString **)errorCode;
- (BOOL)beginDownloadWithErrorCode:(NSString **)errorCode;
- (BOOL)beginInstallWithErrorCode:(NSString **)errorCode;
- (void)cancelCurrent;
- (void)stop;
@end

@implementation TCSPBridge

- (nullable instancetype)initWithExpectedFeedURL:(NSString *)expectedFeedURL
                                       errorCode:(NSString **)errorCode {
    self = [super init];
    if (self == nil) {
        if (errorCode != NULL) {
            *errorCode = @"create_failed";
        }
        return nil;
    }

    NSBundle *bundle = NSBundle.mainBundle;
    NSString *feedURL = [bundle objectForInfoDictionaryKey:@"SUFeedURL"];
    NSString *publicKey = [bundle objectForInfoDictionaryKey:@"SUPublicEDKey"];
    if (![feedURL isKindOfClass:NSString.class] || feedURL.length == 0 ||
        ![publicKey isKindOfClass:NSString.class] || publicKey.length == 0) {
        if (errorCode != NULL) {
            *errorCode = @"metadata_missing";
        }
        return nil;
    }
    if (expectedFeedURL.length == 0 || ![feedURL isEqualToString:expectedFeedURL]) {
        if (errorCode != NULL) {
            *errorCode = @"feed_mismatch";
        }
        return nil;
    }
    _updater = [[SPUUpdater alloc] initWithHostBundle:bundle
                                    applicationBundle:bundle
                                            userDriver:self
                                              delegate:self];
    NSError *startError = nil;
    if (![_updater startUpdater:&startError]) {
        _updater = nil;
        if (errorCode != NULL) {
            *errorCode = @"start_failed";
        }
        return nil;
    }
    _state = TCSPStateIdle;
    return self;
}

- (void)resetForAction:(TCSPAction)action {
    self.action = action;
    self.state = TCSPStatePending;
    self.available = NO;
    self.progress = 0;
    self.expectedBytes = 0;
    self.receivedBytes = 0;
    self.version = nil;
    self.errorCode = nil;
    self.cancellation = nil;
}

- (BOOL)canBeginWithErrorCode:(NSString **)errorCode {
    if (self.updater == nil) {
        if (errorCode != NULL) {
            *errorCode = @"stopped";
        }
        return NO;
    }
    if (self.state == TCSPStatePending || !self.updater.canCheckForUpdates) {
        if (errorCode != NULL) {
            *errorCode = @"busy";
        }
        return NO;
    }
    return YES;
}

- (BOOL)canFailCurrentOperation {
    // Once Sparkle has acknowledged the termination handoff, the bridge may
    // briefly remain alive while the installer reports its final result. Keep
    // that late error observable instead of treating the handoff as proof of
    // successful installation.
    return self.state == TCSPStatePending ||
        (self.action == TCSPActionInstall && self.state == TCSPStateDone);
}

- (BOOL)beginCheckWithErrorCode:(NSString **)errorCode {
    if (![self canBeginWithErrorCode:errorCode]) {
        return NO;
    }
    [self resetForAction:TCSPActionCheck];
    [self.updater checkForUpdateInformation];
    return YES;
}

- (BOOL)beginDownloadWithErrorCode:(NSString **)errorCode {
    if (![self canBeginWithErrorCode:errorCode]) {
        return NO;
    }
    [self resetForAction:TCSPActionDownload];
    [self.updater checkForUpdates];
    return YES;
}

- (BOOL)beginInstallWithErrorCode:(NSString **)errorCode {
    if (self.updater == nil) {
        if (errorCode != NULL) {
            *errorCode = @"stopped";
        }
        return NO;
    }
    if (self.readyReply == nil || self.state != TCSPStateDone) {
        if (errorCode != NULL) {
            *errorCode = @"not_ready";
        }
        return NO;
    }
    void (^reply)(SPUUserUpdateChoice) = self.readyReply;
    self.readyReply = nil;
    self.action = TCSPActionInstall;
    self.state = TCSPStatePending;
    reply(SPUUserUpdateChoiceInstall);
    return YES;
}

- (void)failWithCode:(NSString *)code {
    self.errorCode = code;
    self.state = TCSPStateFailed;
    self.cancellation = nil;
}

- (void)cancelCurrent {
    void (^cancel)(void) = self.cancellation;
    self.cancellation = nil;
    if (cancel != nil) {
        cancel();
    }
    void (^reply)(SPUUserUpdateChoice) = self.readyReply;
    self.readyReply = nil;
    if (reply != nil) {
        reply(SPUUserUpdateChoiceSkip);
    }
    if (self.state == TCSPStatePending) {
        [self failWithCode:@"cancelled"];
    }
}

- (void)stop {
    [self cancelCurrent];
    self.updater = nil;
    self.action = TCSPActionNone;
}

#pragma mark - SPUUpdaterDelegate

- (void)updater:(SPUUpdater *)updater didFindValidUpdate:(SUAppcastItem *)item {
    if (self.action == TCSPActionCheck && self.state == TCSPStatePending) {
        self.available = YES;
        self.version = item.displayVersionString;
    }
}

- (void)updaterDidNotFindUpdate:(SPUUpdater *)updater error:(NSError *)error {
    if (self.action == TCSPActionCheck && self.state == TCSPStatePending) {
        self.available = NO;
    } else if (self.action == TCSPActionDownload && self.state == TCSPStatePending) {
        [self failWithCode:@"no_update"];
    }
}

- (void)updater:(SPUUpdater *)updater didAbortWithError:(NSError *)error {
    BOOL informationalNoUpdate = self.action == TCSPActionCheck &&
        self.state == TCSPStatePending &&
        [error.domain isEqualToString:SUSparkleErrorDomain] &&
        error.code == SUNoUpdateError;
    if (informationalNoUpdate) {
        return;
    }
    if ([self canFailCurrentOperation]) {
        [self failWithCode:tc_safe_sparkle_error_code(error, self.action)];
    }
}

- (void)updater:(SPUUpdater *)updater
    didFinishUpdateCycleForUpdateCheck:(SPUUpdateCheck)updateCheck
                                 error:(NSError *)error {
    if (self.action == TCSPActionCheck && self.state == TCSPStatePending) {
        BOOL noUpdate = [error.domain isEqualToString:SUSparkleErrorDomain] && error.code == SUNoUpdateError;
        if (error == nil || noUpdate) {
            self.state = TCSPStateDone;
        } else {
            [self failWithCode:tc_safe_sparkle_error_code(error, self.action)];
        }
    } else if (error != nil && [self canFailCurrentOperation]) {
        [self failWithCode:tc_safe_sparkle_error_code(error, self.action)];
    } else if (error == nil && self.action == TCSPActionInstall &&
               self.state == TCSPStatePending) {
        self.state = TCSPStateDone;
    }
}

#pragma mark - SPUUserDriver

- (void)showUpdatePermissionRequest:(SPUUpdatePermissionRequest *)request
                              reply:(void (^)(SUUpdatePermissionResponse *))reply {
    SUUpdatePermissionResponse *response =
        [[SUUpdatePermissionResponse alloc] initWithAutomaticUpdateChecks:NO
                                               automaticUpdateDownloading:@NO
                                                        sendSystemProfile:NO];
    reply(response);
}

- (void)showUserInitiatedUpdateCheckWithCancellation:(void (^)(void))cancellation {
    self.cancellation = cancellation;
}

- (void)showUpdateFoundWithAppcastItem:(SUAppcastItem *)appcastItem
                                 state:(SPUUserUpdateState *)state
                                 reply:(void (^)(SPUUserUpdateChoice))reply {
    if (self.action != TCSPActionDownload || self.state != TCSPStatePending) {
        reply(SPUUserUpdateChoiceDismiss);
        return;
    }
    if (appcastItem.informationOnlyUpdate) {
        reply(SPUUserUpdateChoiceDismiss);
        [self failWithCode:@"information_only"];
        return;
    }
    if (state.stage == SPUUserUpdateStageInstalling) {
        reply(SPUUserUpdateChoiceSkip);
        [self failWithCode:@"installing_update"];
        return;
    }
    self.version = appcastItem.displayVersionString;
    reply(SPUUserUpdateChoiceInstall);
}

- (void)showUpdateReleaseNotesWithDownloadData:(SPUDownloadData *)downloadData {}

- (void)showUpdateReleaseNotesFailedToDownloadWithError:(NSError *)error {}

- (void)showUpdateNotFoundWithError:(NSError *)error
                    acknowledgement:(void (^)(void))acknowledgement {
    acknowledgement();
    if (self.action == TCSPActionCheck && self.state == TCSPStatePending) {
        self.available = NO;
    } else if (self.action == TCSPActionDownload && self.state == TCSPStatePending) {
        [self failWithCode:@"no_update"];
    }
}

- (void)showUpdaterError:(NSError *)error acknowledgement:(void (^)(void))acknowledgement {
    acknowledgement();
    if ([self canFailCurrentOperation]) {
        [self failWithCode:tc_safe_sparkle_error_code(error, self.action)];
    }
}

- (void)showDownloadInitiatedWithCancellation:(void (^)(void))cancellation {
    self.cancellation = cancellation;
    self.progress = 0;
}

- (void)showDownloadDidReceiveExpectedContentLength:(uint64_t)expectedContentLength {
    self.expectedBytes = expectedContentLength;
    self.receivedBytes = 0;
}

- (void)showDownloadDidReceiveDataOfLength:(uint64_t)length {
    self.receivedBytes += length;
    if (self.expectedBytes > 0) {
        double ratio = MIN(1.0, (double)self.receivedBytes / (double)self.expectedBytes);
        self.progress = (NSInteger)(ratio * 89.0);
    }
}

- (void)showDownloadDidStartExtractingUpdate {
    self.cancellation = nil;
    self.progress = MAX(self.progress, 90);
}

- (void)showExtractionReceivedProgress:(double)progress {
    double bounded = MIN(1.0, MAX(0.0, progress));
    self.progress = 90 + (NSInteger)(bounded * 9.0);
}

- (void)showReadyToInstallAndRelaunch:(void (^)(SPUUserUpdateChoice))reply {
    if (self.action != TCSPActionDownload || self.state != TCSPStatePending) {
        reply(SPUUserUpdateChoiceDismiss);
        return;
    }
    self.readyReply = reply;
    self.progress = 100;
    self.state = TCSPStateDone;
}

- (void)showInstallingUpdateWithApplicationTerminated:(BOOL)applicationTerminated
                          retryTerminatingApplication:(void (^)(void))retryTerminatingApplication {
    if (self.action == TCSPActionInstall && applicationTerminated &&
        self.state == TCSPStatePending) {
        self.state = TCSPStateDone;
    }
}

- (void)showUpdateInstalledAndRelaunched:(BOOL)relaunched
                          acknowledgement:(void (^)(void))acknowledgement {
    if (self.action == TCSPActionInstall && self.state == TCSPStatePending) {
        self.state = TCSPStateDone;
    }
    acknowledgement();
}

- (void)dismissUpdateInstallation {
    self.cancellation = nil;
    self.readyReply = nil;
}

- (void)showUpdateInFocus {}

@end

TCSPBridgeRef tc_sparkle_create(const char *expected_feed_url, char **error_code) {
    __block TCSPBridge *bridge = nil;
    __block NSString *code = nil;
    NSString *expectedFeedURL = expected_feed_url == NULL
        ? nil
        : [NSString stringWithUTF8String:expected_feed_url];
    tc_on_main_sync(^{
        bridge = [[TCSPBridge alloc] initWithExpectedFeedURL:expectedFeedURL errorCode:&code];
    });
    if (bridge == nil) {
        if (error_code != NULL) {
            *error_code = tc_copy_string(code ?: @"create_failed");
        }
        return NULL;
    }
    return (__bridge_retained TCSPBridgeRef)bridge;
}

static int tc_sparkle_begin(TCSPBridgeRef ref, TCSPAction action, char **error_code) {
    if (ref == NULL) {
        if (error_code != NULL) {
            *error_code = tc_copy_string(@"stopped");
        }
        return 0;
    }
    __block BOOL started = NO;
    __block NSString *code = nil;
    tc_on_main_sync(^{
        TCSPBridge *bridge = (__bridge TCSPBridge *)ref;
        switch (action) {
            case TCSPActionCheck:
                started = [bridge beginCheckWithErrorCode:&code];
                break;
            case TCSPActionDownload:
                started = [bridge beginDownloadWithErrorCode:&code];
                break;
            case TCSPActionInstall:
                started = [bridge beginInstallWithErrorCode:&code];
                break;
            default:
                code = @"operation_failed";
                break;
        }
    });
    if (!started && error_code != NULL) {
        *error_code = tc_copy_string(code ?: @"operation_failed");
    }
    return started ? 1 : 0;
}

int tc_sparkle_begin_check(TCSPBridgeRef bridge, char **error_code) {
    return tc_sparkle_begin(bridge, TCSPActionCheck, error_code);
}

int tc_sparkle_begin_download(TCSPBridgeRef bridge, char **error_code) {
    return tc_sparkle_begin(bridge, TCSPActionDownload, error_code);
}

int tc_sparkle_begin_install(TCSPBridgeRef bridge, char **error_code) {
    return tc_sparkle_begin(bridge, TCSPActionInstall, error_code);
}

int tc_sparkle_poll(TCSPBridgeRef ref, int *state, int *available, int *progress,
                    char **version, char **error_code) {
    if (ref == NULL) {
        if (error_code != NULL) {
            *error_code = tc_copy_string(@"stopped");
        }
        return 0;
    }
    TCSPBridge *bridge = (__bridge TCSPBridge *)ref;
    if (state != NULL) {
        *state = (int)bridge.state;
    }
    if (available != NULL) {
        *available = bridge.available ? 1 : 0;
    }
    if (progress != NULL) {
        *progress = (int)bridge.progress;
    }
    if (version != NULL) {
        *version = tc_copy_string(bridge.version);
    }
    if (error_code != NULL) {
        *error_code = tc_copy_string(bridge.errorCode);
    }
    return 1;
}

void tc_sparkle_cancel(TCSPBridgeRef ref) {
    if (ref == NULL) {
        return;
    }
    tc_on_main_async(^{
        TCSPBridge *bridge = (__bridge TCSPBridge *)ref;
        [bridge cancelCurrent];
    });
}

void tc_sparkle_destroy(TCSPBridgeRef ref) {
    if (ref == NULL) {
        return;
    }
    tc_on_main_sync(^{
        TCSPBridge *bridge = (__bridge_transfer TCSPBridge *)ref;
        [bridge stop];
    });
}

void tc_sparkle_free_string(char *value) {
    free(value);
}
