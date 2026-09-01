package sparkle

import (
	"go/build/constraint"
	"os"
	"strings"
	"testing"
)

func TestBridgeBuildConstraintsExcludeUnsupportedTargets(t *testing.T) {
	tests := []struct {
		file      string
		scenarios map[string]struct {
			tags map[string]bool
			want bool
		}
	}{
		{
			file: "driver_darwin_arm64.go",
			scenarios: map[string]struct {
				tags map[string]bool
				want bool
			}{
				"public macOS ARM64 with CGO": {tags: tagSet("public_macos_arm64", "darwin", "arm64", "cgo"), want: true},
				"public macOS Intel":          {tags: tagSet("public_macos_arm64", "darwin", "amd64", "cgo"), want: false},
				"public iOS ARM64":            {tags: tagSet("public_macos_arm64", "darwin", "arm64", "ios", "cgo"), want: false},
				"public Linux ARM64":          {tags: tagSet("public_macos_arm64", "linux", "arm64", "cgo"), want: false},
				"internal macOS ARM64":        {tags: tagSet("darwin", "arm64", "cgo"), want: false},
			},
		},
		{
			file: "bridge_darwin_arm64.m",
			scenarios: map[string]struct {
				tags map[string]bool
				want bool
			}{
				"public macOS ARM64 with CGO": {tags: tagSet("public_macos_arm64", "darwin", "arm64", "cgo"), want: true},
				"public macOS Intel":          {tags: tagSet("public_macos_arm64", "darwin", "amd64", "cgo"), want: false},
				"public iOS ARM64":            {tags: tagSet("public_macos_arm64", "darwin", "arm64", "ios", "cgo"), want: false},
				"public Linux ARM64":          {tags: tagSet("public_macos_arm64", "linux", "arm64", "cgo"), want: false},
			},
		},
		{
			file: "driver_stub.go",
			scenarios: map[string]struct {
				tags map[string]bool
				want bool
			}{
				"internal macOS ARM64":        {tags: tagSet("darwin", "arm64", "cgo"), want: true},
				"public macOS Intel":          {tags: tagSet("public_macos_arm64", "darwin", "amd64", "cgo"), want: true},
				"public iOS ARM64":            {tags: tagSet("public_macos_arm64", "darwin", "arm64", "ios", "cgo"), want: true},
				"public Linux ARM64":          {tags: tagSet("public_macos_arm64", "linux", "arm64", "cgo"), want: true},
				"public macOS ARM64 with CGO": {tags: tagSet("public_macos_arm64", "darwin", "arm64", "cgo"), want: false},
			},
		},
		{
			file: "driver_public_nocgo.go",
			scenarios: map[string]struct {
				tags map[string]bool
				want bool
			}{
				"public macOS ARM64 without CGO": {tags: tagSet("public_macos_arm64", "darwin", "arm64"), want: true},
				"public macOS ARM64 with CGO":    {tags: tagSet("public_macos_arm64", "darwin", "arm64", "cgo"), want: false},
				"internal macOS ARM64":           {tags: tagSet("darwin", "arm64"), want: false},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.file, func(t *testing.T) {
			expr := readBuildConstraint(t, test.file)
			for name, scenario := range test.scenarios {
				t.Run(name, func(t *testing.T) {
					if got := expr.Eval(func(tag string) bool { return scenario.tags[tag] }); got != scenario.want {
						t.Fatalf("constraint %q evaluated to %v, want %v", expr.String(), got, scenario.want)
					}
				})
			}
		})
	}
}

func TestNativeAdapterUsesSparkleWithoutItsOwnNetworkStack(t *testing.T) {
	goSource := readSource(t, "driver_darwin_arm64.go")
	objectiveCSource := readSource(t, "bridge_darwin_arm64.m")
	combined := goSource + "\n" + objectiveCSource

	for _, required := range []string{
		`#cgo CFLAGS: -fobjc-arc -fmodules -fblocks`,
		`#cgo LDFLAGS: -framework Cocoa -framework Sparkle`,
		`var _ updater.Driver = (*Driver)(nil)`,
		`#import <Sparkle/Sparkle.h>`,
		`SPUUpdater`,
		`SPUUserDriver`,
		`checkForUpdateInformation`,
		`showDownloadDidReceiveDataOfLength`,
		`showReadyToInstallAndRelaunch`,
		`SPUUserUpdateChoiceInstall`,
		`dispatch_get_main_queue`,
	} {
		if !strings.Contains(combined, required) {
			t.Errorf("native bridge is missing %q", required)
		}
	}

	for _, forbidden := range []string{"NSURLSession", "NSURLConnection", "net/http", "http://", "https://"} {
		if strings.Contains(combined, forbidden) {
			t.Errorf("native bridge contains forbidden direct-network token %q", forbidden)
		}
	}
}

func TestNativeAndObjectiveCBridgeExposeSameCContract(t *testing.T) {
	goSource := readSource(t, "driver_darwin_arm64.go")
	objectiveCSource := readSource(t, "bridge_darwin_arm64.m")

	for _, symbol := range []string{
		"tc_sparkle_create",
		"tc_sparkle_begin_check",
		"tc_sparkle_begin_download",
		"tc_sparkle_begin_install",
		"tc_sparkle_poll",
		"tc_sparkle_cancel",
		"tc_sparkle_destroy",
		"tc_sparkle_free_string",
	} {
		if !strings.Contains(goSource, symbol) {
			t.Errorf("Go bridge is missing C symbol %q", symbol)
		}
		if !strings.Contains(objectiveCSource, symbol) {
			t.Errorf("Objective-C bridge is missing C symbol %q", symbol)
		}
	}
}

func TestNativeBridgeRequiresPinnedSparkleMetadataFromBundle(t *testing.T) {
	goSource := readSource(t, "driver_darwin_arm64.go")
	objectiveCSource := readSource(t, "bridge_darwin_arm64.m")
	combined := goSource + "\n" + objectiveCSource

	for _, required := range []string{
		"expected_feed_url",
		`objectForInfoDictionaryKey:@"SUFeedURL"`,
		`objectForInfoDictionaryKey:@"SUPublicEDKey"`,
		"isEqualToString:expectedFeedURL",
	} {
		if !strings.Contains(combined, required) {
			t.Errorf("native bridge does not enforce %q", required)
		}
	}
}

func TestNativeBridgeClassifiesOnlyStableSparkleFailureCodes(t *testing.T) {
	goSource := readSource(t, "driver_darwin_arm64.go")
	objectiveCSource := readSource(t, "bridge_darwin_arm64.m")
	combined := goSource + "\n" + objectiveCSource

	for _, required := range []string{
		"SUSignatureError",
		"SUValidationError",
		"SURunningFromDiskImageError",
		"SUInstallationError",
		"update_signature_invalid",
		"update_validation_failed",
		"update_running_from_disk_image",
		"update_install_failed",
	} {
		if !strings.Contains(combined, required) {
			t.Errorf("native bridge is missing stable diagnostic mapping %q", required)
		}
	}
	for _, forbidden := range []string{"localizedDescription", "NSLog", "NSUnderlyingErrorKey"} {
		if strings.Contains(objectiveCSource, forbidden) {
			t.Errorf("native bridge must not expose native error detail through %q", forbidden)
		}
	}
}

func TestInformationalCheckCompletesOnlyAfterSparkleFinishesItsCycle(t *testing.T) {
	source := readSource(t, "bridge_darwin_arm64.m")
	foundCallback := sourceBetween(t, source,
		"didFindValidUpdate:(SUAppcastItem *)item {",
		"- (void)updaterDidNotFindUpdate:",
	)
	notFoundCallback := sourceBetween(t, source,
		"- (void)updaterDidNotFindUpdate:",
		"- (void)updater:(SPUUpdater *)updater didAbortWithError:",
	)
	userDriverNotFoundCallback := sourceBetween(t, source,
		"- (void)showUpdateNotFoundWithError:",
		"- (void)showUpdaterError:",
	)
	finishCallback := sourceBetween(t, source,
		"didFinishUpdateCycleForUpdateCheck:(SPUUpdateCheck)updateCheck",
		"#pragma mark - SPUUserDriver",
	)

	for name, callback := range map[string]string{
		"didFindValidUpdate":      foundCallback,
		"didNotFindUpdate":        notFoundCallback,
		"showUpdateNotFoundError": userDriverNotFoundCallback,
	} {
		if strings.Contains(callback, "self.state = TCSPStateDone") {
			t.Errorf("%s completes a probe before Sparkle finishes the update cycle", name)
		}
	}
	if !strings.Contains(finishCallback, "self.action == TCSPActionCheck") ||
		!strings.Contains(finishCallback, "self.state = TCSPStateDone") {
		t.Error("didFinishUpdateCycle callback does not complete an informational check")
	}
}

func TestInstallDoesNotCompleteBeforeSparkleReportsInstallation(t *testing.T) {
	source := readSource(t, "bridge_darwin_arm64.m")
	beginInstall := sourceBetween(t, source,
		"- (BOOL)beginInstallWithErrorCode:",
		"- (void)failWithCode:",
	)
	if !strings.Contains(beginInstall, "reply(SPUUserUpdateChoiceInstall)") {
		t.Fatal("install path must hand the decision back to Sparkle")
	}
	if strings.Contains(beginInstall, "self.state = TCSPStateDone") {
		t.Fatal("install path must wait for Sparkle's installing callback before completing")
	}
}

func TestInstallCompletionWaitsForTerminatedHandoffAndTerminalCallback(t *testing.T) {
	source := readSource(t, "bridge_darwin_arm64.m")
	installing := sourceBetween(t, source,
		"- (void)showInstallingUpdateWithApplicationTerminated:",
		"- (void)showUpdateInstalledAndRelaunched:",
	)
	if !strings.Contains(installing, "applicationTerminated") {
		t.Fatal("installing callback must inspect whether the application has terminated")
	}
	if !strings.Contains(strings.Join(strings.Fields(installing), " "),
		"if (self.action == TCSPActionInstall && applicationTerminated &&") {
		t.Fatal("installing callback must complete only after Sparkle confirms application termination")
	}

	installed := sourceBetween(t, source,
		"- (void)showUpdateInstalledAndRelaunched:",
		"- (void)dismissUpdateInstallation",
	)
	if !strings.Contains(installed, "self.action == TCSPActionInstall") ||
		!strings.Contains(installed, "self.state = TCSPStateDone") {
		t.Fatal("terminal installation callback must complete an install operation")
	}
}

func TestInstallErrorsRemainObservableAfterHandoff(t *testing.T) {
	source := readSource(t, "bridge_darwin_arm64.m")
	helper := sourceBetween(t, source,
		"- (BOOL)canFailCurrentOperation",
		"- (BOOL)beginCheckWithErrorCode:",
	)
	if !strings.Contains(helper, "TCSPActionInstall") ||
		!strings.Contains(helper, "TCSPStateDone") {
		t.Fatal("failure predicate must include an install handoff that has not finished safely")
	}
	for name, callback := range map[string]string{
		"didAbortWithError": sourceBetween(t, source,
			"- (void)updater:(SPUUpdater *)updater didAbortWithError:",
			"- (void)updater:(SPUUpdater *)updater\n    didFinishUpdateCycleForUpdateCheck:",
		),
		"showUpdaterError": sourceBetween(t, source,
			"- (void)showUpdaterError:",
			"- (void)showDownloadInitiatedWithCancellation:",
		),
	} {
		if !strings.Contains(callback, "canFailCurrentOperation") {
			t.Errorf("%s does not keep install failures observable after handoff", name)
		}
	}
}

func TestShutdownPathDoesNotSynchronouslyWaitForMainThread(t *testing.T) {
	source := readSource(t, "bridge_darwin_arm64.m")
	poll := sourceBetween(t, source, "int tc_sparkle_poll(", "void tc_sparkle_cancel(")
	cancel := sourceBetween(t, source, "void tc_sparkle_cancel(", "void tc_sparkle_destroy(")
	if strings.Contains(poll, "tc_on_main_sync") {
		t.Fatal("poll synchronously waits for the Cocoa main thread")
	}
	if strings.Contains(cancel, "tc_on_main_sync") {
		t.Fatal("cancellation synchronously waits for the Cocoa main thread")
	}
	if !strings.Contains(cancel, "tc_on_main_async") {
		t.Fatal("cancellation must be queued asynchronously on the Cocoa main thread")
	}
	for _, property := range []string{
		"@property(atomic) TCSPState state;",
		"@property(atomic) BOOL available;",
		"@property(atomic) NSInteger progress;",
		"@property(atomic, copy) NSString *version;",
		"@property(atomic, copy) NSString *errorCode;",
	} {
		if !strings.Contains(source, property) {
			t.Errorf("native snapshot is missing thread-safe property %q", property)
		}
	}
}

func readBuildConstraint(t *testing.T, path string) constraint.Expr {
	t.Helper()
	source := readSource(t, path)
	line, _, ok := strings.Cut(source, "\n")
	if !ok {
		t.Fatalf("%s has no build-constraint line", path)
	}
	expr, err := constraint.Parse(line)
	if err != nil {
		t.Fatalf("parse build constraint in %s: %v", path, err)
	}
	return expr
}

func readSource(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

func sourceBetween(t *testing.T, source, start, end string) string {
	t.Helper()
	startIndex := strings.Index(source, start)
	if startIndex < 0 {
		t.Fatalf("source is missing start marker %q", start)
	}
	endIndex := strings.Index(source[startIndex+len(start):], end)
	if endIndex < 0 {
		t.Fatalf("source is missing end marker %q", end)
	}
	return source[startIndex : startIndex+len(start)+endIndex]
}

func tagSet(tags ...string) map[string]bool {
	set := make(map[string]bool, len(tags))
	for _, tag := range tags {
		set[tag] = true
	}
	return set
}
