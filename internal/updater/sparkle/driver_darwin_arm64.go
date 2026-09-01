//go:build public_macos_arm64 && darwin && arm64 && !ios && cgo

package sparkle

/*
#cgo CFLAGS: -fobjc-arc -fmodules -fblocks
#cgo LDFLAGS: -framework Cocoa -framework Sparkle

#include <stdint.h>
#include <stdlib.h>

typedef void *TCSPBridgeRef;

TCSPBridgeRef tc_sparkle_create(const char *expected_feed_url, char **error_code);
int tc_sparkle_begin_check(TCSPBridgeRef bridge, char **error_code);
int tc_sparkle_begin_download(TCSPBridgeRef bridge, char **error_code);
int tc_sparkle_begin_install(TCSPBridgeRef bridge, char **error_code);
int tc_sparkle_poll(
    TCSPBridgeRef bridge,
    int *state,
    int *available,
    int *progress,
    char **version,
    char **error_code
);
void tc_sparkle_cancel(TCSPBridgeRef bridge);
void tc_sparkle_destroy(TCSPBridgeRef bridge);
void tc_sparkle_free_string(char *value);
*/
import "C"

import (
	"context"
	"errors"
	"sync"
	"time"
	"unsafe"

	"telegram-companion/internal/updater"
)

var (
	errSparkleUnavailable = errors.New("sparkle updater is unavailable")
	errSparkleBusy        = errors.New("sparkle updater is busy")
	errSparkleNoUpdate    = errors.New("sparkle update is unavailable")
	errSparkleDownload    = errors.New("sparkle update download failed")
	errSparkleInstall     = errors.New("sparkle update installation failed")
	errSparkleStopped     = errors.New("sparkle updater is stopped")
)

// Driver serializes the Go state machine onto Sparkle's main-thread API.
// Sparkle owns appcast transport, signature validation, extraction and install.
type Driver struct {
	operation sync.Mutex
	bridge    C.TCSPBridgeRef
	initErr   error
	stopped   bool
}

// NewDriver constructs a native Sparkle driver. Initialization failures are
// retained as safe errors so the updater remains fail-closed.
func NewDriver(appcastURL string) updater.Driver {
	expectedFeedURL := C.CString(appcastURL)
	defer C.free(unsafe.Pointer(expectedFeedURL))
	var errorCode *C.char
	bridge := C.tc_sparkle_create(expectedFeedURL, &errorCode)
	driver := &Driver{bridge: bridge}
	if bridge == nil {
		driver.initErr = mapNativeError(takeCString(errorCode), nativeOperationCheck)
	}
	return driver
}

func (d *Driver) Check(ctx context.Context) (updater.Update, error) {
	result, err := d.run(ctx, nativeOperationCheck, nil)
	if err != nil {
		return updater.Update{}, err
	}
	if !result.available {
		return updater.Update{}, nil
	}
	if result.version == "" {
		return updater.Update{}, errSparkleUnavailable
	}
	return updater.Update{Available: true, Version: result.version}, nil
}

func (d *Driver) Download(ctx context.Context, progress func(int)) error {
	_, err := d.run(ctx, nativeOperationDownload, progress)
	return err
}

func (d *Driver) Install(ctx context.Context) error {
	_, err := d.run(ctx, nativeOperationInstall, nil)
	return err
}

// Stop waits for an active method, then cancels and releases the native bridge.
// Service.Stop already cancels and waits for active operations before calling it.
func (d *Driver) Stop() error {
	d.operation.Lock()
	defer d.operation.Unlock()
	if d.stopped {
		return nil
	}
	d.stopped = true
	if d.bridge != nil {
		C.tc_sparkle_cancel(d.bridge)
		C.tc_sparkle_destroy(d.bridge)
		d.bridge = nil
	}
	return nil
}

type nativeResult struct {
	available bool
	progress  int
	version   string
}

func (d *Driver) run(ctx context.Context, operation int, progress func(int)) (nativeResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nativeResult{}, err
	}

	d.operation.Lock()
	defer d.operation.Unlock()
	if d.stopped {
		return nativeResult{}, errSparkleStopped
	}
	if d.initErr != nil || d.bridge == nil {
		if d.initErr != nil {
			return nativeResult{}, d.initErr
		}
		return nativeResult{}, errSparkleUnavailable
	}

	var errorCode *C.char
	if C.int(1) != d.begin(operation, &errorCode) {
		return nativeResult{}, mapNativeError(takeCString(errorCode), operation)
	}

	ticker := time.NewTicker(40 * time.Millisecond)
	defer ticker.Stop()
	lastProgress := -1
	for {
		result, state, err := d.poll(operation)
		if err != nil {
			return nativeResult{}, err
		}
		if operation == nativeOperationDownload && progress != nil && result.progress != lastProgress {
			progress(result.progress)
			lastProgress = result.progress
		}
		switch state {
		case nativeStateDone:
			return result, nil
		case nativeStateFailed:
			return nativeResult{}, mapNativeError("operation_failed", operation)
		case nativeStatePending:
		default:
			C.tc_sparkle_cancel(d.bridge)
			return nativeResult{}, errSparkleUnavailable
		}

		select {
		case <-ctx.Done():
			C.tc_sparkle_cancel(d.bridge)
			return nativeResult{}, ctx.Err()
		case <-ticker.C:
		}
	}
}

func (d *Driver) begin(operation int, errorCode **C.char) C.int {
	switch operation {
	case nativeOperationCheck:
		return C.tc_sparkle_begin_check(d.bridge, errorCode)
	case nativeOperationDownload:
		return C.tc_sparkle_begin_download(d.bridge, errorCode)
	case nativeOperationInstall:
		return C.tc_sparkle_begin_install(d.bridge, errorCode)
	default:
		return 0
	}
}

func (d *Driver) poll(operation int) (nativeResult, int, error) {
	var state C.int
	var available C.int
	var progress C.int
	var version *C.char
	var errorCode *C.char
	if C.int(1) != C.tc_sparkle_poll(d.bridge, &state, &available, &progress, &version, &errorCode) {
		return nativeResult{}, 0, mapNativeError(takeCString(errorCode), operation)
	}

	result := nativeResult{
		available: available == 1,
		progress:  clampProgress(int(progress)),
		version:   takeCString(version),
	}
	code := takeCString(errorCode)
	if int(state) == nativeStateFailed {
		return nativeResult{}, int(state), mapNativeError(code, operation)
	}
	return result, int(state), nil
}

func takeCString(value *C.char) string {
	if value == nil {
		return ""
	}
	result := C.GoString(value)
	C.tc_sparkle_free_string(value)
	return result
}

func clampProgress(progress int) int {
	if progress < 0 {
		return 0
	}
	if progress > 100 {
		return 100
	}
	return progress
}

func mapNativeError(code string, operation int) error {
	switch code {
	case "busy":
		return errSparkleBusy
	case "no_update":
		return errSparkleNoUpdate
	case "stopped":
		return errSparkleStopped
	case "update_signature_invalid":
		return updater.NewCodedError(updater.CodeSignatureInvalid, errSparkleDownload)
	case "update_validation_failed":
		return updater.NewCodedError(updater.CodeValidationFailed, errSparkleDownload)
	case "update_running_from_disk_image":
		return updater.NewCodedError(updater.CodeRunningFromDiskImage, errSparkleDownload)
	case "update_install_failed":
		return updater.NewCodedError(updater.CodeInstallFailed, errSparkleInstall)
	case "download_failed", "information_only", "installing_update":
		return errSparkleDownload
	case "install_failed", "not_ready":
		return errSparkleInstall
	}
	switch operation {
	case nativeOperationDownload:
		return errSparkleDownload
	case nativeOperationInstall:
		return errSparkleInstall
	default:
		return errSparkleUnavailable
	}
}

var _ updater.Driver = (*Driver)(nil)
