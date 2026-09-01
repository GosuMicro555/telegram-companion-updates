package app

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"telegram-companion/internal/bootstrapstate"

	"github.com/gofrs/flock"
	_ "modernc.org/sqlite"
)

var (
	ErrInvalidFirstLaunchSeed       = errors.New("first launch seed: invalid bundle contents")
	ErrFirstLaunchSeedTargetChanged = errors.New("first launch seed: target changed during import")
)

const (
	firstLaunchSeedLockName = ".first-launch-seed.lock"
	firstLaunchDiskReserve  = uint64(512 << 20)
	firstLaunchSecretSize   = 32
)

// Keep this in sync with the seed inserted by migration 000013.
const firstLaunchDefaultAnalyticsServiceWords = 88

var errInvalidDiskSpaceProbe = errors.New("first launch seed: invalid disk space result")

// FirstLaunchTargetState is the closed result of inspecting a seed target.
// Blocked targets are never treated as existing profiles because their shape is
// not sufficiently well understood to start or mutate safely.
type FirstLaunchTargetState uint8

const (
	FirstLaunchTargetRequiresSeed FirstLaunchTargetState = iota
	FirstLaunchTargetExistingProfile
	FirstLaunchTargetBlocked
)

type InsufficientSpaceError struct {
	RequiredBytes  uint64
	AvailableBytes uint64
}

func (e *InsufficientSpaceError) Error() string {
	return "first launch seed: insufficient storage space"
}

type StorageProbeError struct {
	cause error
}

func (e *StorageProbeError) Error() string {
	return "first launch seed: storage availability check failed"
}

func (e *StorageProbeError) Unwrap() error {
	return e.cause
}

type firstLaunchDiskSpace struct {
	AvailableBytes uint64
	BlockSize      uint64
}

var firstLaunchDiskSpaceProbe = probeFirstLaunchDiskSpace

var renameFirstLaunchPath = os.Rename

var renameFirstLaunchNoReplace = firstLaunchRenameNoReplace

var exchangeFirstLaunchPaths = firstLaunchExchangePaths

// Test seam called after the final target state/identity observation and
// immediately before the first target mutation.
var afterFirstLaunchTargetObserved func()

// Test seam called immediately after both owned target namespaces have been
// published and before their final validation.
var afterFirstLaunchMarkerPublished func()

type firstLaunchTreeFingerprintEntry struct {
	path   string
	mode   fs.FileMode
	size   int64
	digest [sha256.Size]byte
	info   os.FileInfo
}

type firstLaunchTargetObservation struct {
	dataExists      bool
	dataInfo        os.FileInfo
	dataFingerprint []firstLaunchTreeFingerprintEntry
}

type firstLaunchPublishedObservation struct {
	dataFingerprint   []firstLaunchTreeFingerprintEntry
	markerFingerprint []firstLaunchTreeFingerprintEntry
}

type firstLaunchSecretBatchWriter interface {
	SetBatch(context.Context, map[string][]byte, func() error) error
}

// FirstLaunchSeedConfig identifies a recipient-encrypted seed and its local destination.
type FirstLaunchSeedConfig struct {
	BundlePath string
	TargetRoot string
	BundleID   string
	AppVersion string
	License    string
	Key        []byte
	Secrets    bootstrapstate.SecretWriter
}

var firstLaunchOperationalSecrets = map[string]struct{}{
	"scout-message-key":               {},
	"outbound-target-key":             {},
	"proxy-credentials-v1":            {},
	"telegram-account-credentials-v1": {},
}

var firstLaunchSeedFiles = map[string]struct{}{
	"app.db":                 {},
	"application-state.bolt": {},
}

var firstLaunchSeedDirectories = map[string]struct{}{
	"gotd-import-staging": {},
	"sessions":            {},
	"tdata":               {},
}

// ImportFirstLaunchSeed installs a recipient-encrypted seed only before local data exists.
// The imported data is staged and renamed into place so partial state is never visible.
func ImportFirstLaunchSeed(ctx context.Context, config FirstLaunchSeedConfig) (bool, error) {
	if err := validateFirstLaunchSeedLocationConfig(config); err != nil {
		return false, err
	}
	if err := os.MkdirAll(config.TargetRoot, 0o700); err != nil {
		return false, err
	}
	targetRoot, err := canonicalFirstLaunchTargetRoot(config.TargetRoot)
	if err != nil {
		return false, err
	}
	importLock := flock.New(filepath.Join(filepath.Dir(targetRoot), "."+filepath.Base(targetRoot)+firstLaunchSeedLockName))
	locked, err := importLock.TryLockContext(ctx, 25*time.Millisecond)
	if err != nil {
		return false, err
	}
	if !locked {
		return false, errors.New("first launch seed: import lock unavailable")
	}
	defer func() { _ = importLock.Close() }()

	empty, err := FirstLaunchSeedRequired(targetRoot)
	if err != nil {
		return false, err
	}
	if !empty {
		return false, nil
	}
	if err := validateFirstLaunchSeedImportConfig(config); err != nil {
		return false, err
	}
	targetData := filepath.Join(targetRoot, "data")
	stagingRoot, err := os.MkdirTemp(filepath.Dir(targetRoot), "."+filepath.Base(targetRoot)+".first-launch-seed-")
	if err != nil {
		return false, err
	}
	cleanupStaging := true
	defer func() {
		if cleanupStaging {
			_ = os.RemoveAll(stagingRoot)
		}
	}()

	stagedSecrets := newFirstLaunchSecretStaging()
	defer stagedSecrets.clear()
	imported, err := bootstrapstate.Import(ctx, bootstrapstate.ImportConfig{
		BundlePath: config.BundlePath,
		TargetRoot: filepath.Join(stagingRoot, "import"),
		BundleID:   config.BundleID,
		AppVersion: config.AppVersion,
		License:    config.License,
		Key:        config.Key,
		Secrets:    stagedSecrets,
		Preflight: func(ctx context.Context, footprint bootstrapstate.BundleFootprint) error {
			return preflightFirstLaunchDiskSpace(ctx, filepath.Clean(config.TargetRoot), footprint)
		},
	})
	if err != nil || !imported {
		return imported, err
	}

	importedData := filepath.Join(stagingRoot, "import", "data")
	if err := validateFirstLaunchSeedData(importedData); err != nil {
		return false, err
	}
	stagedData := filepath.Join(stagingRoot, "data")
	if err := renameFirstLaunchPath(importedData, stagedData); err != nil {
		return false, err
	}
	stagedMarker := filepath.Join(stagingRoot, "import", "bootstrap-state")
	err = stagedSecrets.flush(ctx, config.Secrets, func() error {
		observation, observeErr := observeFirstLaunchSeedTarget(targetRoot, targetData)
		if observeErr != nil {
			return observeErr
		}
		if afterFirstLaunchTargetObserved != nil {
			afterFirstLaunchTargetObserved()
		}
		preserve, installErr := installFirstLaunchSeedData(
			targetData, stagedData, stagedMarker, targetRoot, observation,
		)
		if preserve {
			cleanupStaging = false
		}
		return installErr
	})
	if err != nil {
		return false, err
	}
	return true, nil
}

// canonicalFirstLaunchTargetRoot resolves symlinks in the parent path so
// Darwin's RENAME_NOFOLLOW_ANY does not reject macOS system aliases such as
// /var -> /private/var. The profile directory itself remains the final,
// unresolved component, so a symlink target root is still inspected and
// rejected rather than followed.
func canonicalFirstLaunchTargetRoot(root string) (string, error) {
	clean := filepath.Clean(root)
	parent, err := filepath.EvalSymlinks(filepath.Dir(clean))
	if err != nil {
		return "", err
	}
	return filepath.Join(parent, filepath.Base(clean)), nil
}

func preflightFirstLaunchDiskSpace(ctx context.Context, targetRoot string, footprint bootstrapstate.BundleFootprint) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	space, err := firstLaunchDiskSpaceProbe(filepath.Clean(targetRoot))
	if err != nil {
		return &StorageProbeError{cause: err}
	}
	required, ok := firstLaunchRequiredDiskBytes(footprint, space.BlockSize)
	if !ok {
		return &StorageProbeError{cause: errInvalidDiskSpaceProbe}
	}
	if space.AvailableBytes < required {
		return &InsufficientSpaceError{RequiredBytes: required, AvailableBytes: space.AvailableBytes}
	}
	return nil
}

func firstLaunchRequiredDiskBytes(footprint bootstrapstate.BundleFootprint, blockSize uint64) (uint64, bool) {
	if blockSize == 0 {
		return 0, false
	}
	regularRounding, ok := checkedMultiplyUint64(footprint.RegularFiles, blockSize-1)
	if !ok {
		return 0, false
	}
	directoryAllocation, ok := checkedMultiplyUint64(footprint.Directories, blockSize)
	if !ok {
		return 0, false
	}
	reserve := firstLaunchDiskReserve
	if footprint.DatabaseBytes > reserve {
		reserve = footprint.DatabaseBytes
	}
	required, ok := checkedAddUint64(footprint.DataBytes, regularRounding)
	if !ok {
		return 0, false
	}
	required, ok = checkedAddUint64(required, directoryAllocation)
	if !ok {
		return 0, false
	}
	return checkedAddUint64(required, reserve)
}

func checkedMultiplyUint64(left, right uint64) (uint64, bool) {
	if left != 0 && right > ^uint64(0)/left {
		return 0, false
	}
	return left * right, true
}

func firstLaunchAvailableBytes(availableBlocks, blockSize uint64) (uint64, bool) {
	return checkedMultiplyUint64(availableBlocks, blockSize)
}

func checkedAddUint64(left, right uint64) (uint64, bool) {
	if right > ^uint64(0)-left {
		return 0, false
	}
	return left + right, true
}

func observeFirstLaunchSeedTarget(targetRoot, targetData string) (firstLaunchTargetObservation, error) {
	state, err := InspectFirstLaunchSeedTarget(targetRoot)
	if err != nil {
		return firstLaunchTargetObservation{}, err
	}
	if state != FirstLaunchTargetRequiresSeed {
		return firstLaunchTargetObservation{}, ErrFirstLaunchSeedTargetChanged
	}
	info, err := os.Lstat(targetData)
	if errors.Is(err, os.ErrNotExist) {
		return firstLaunchTargetObservation{}, nil
	}
	if err != nil {
		return firstLaunchTargetObservation{}, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return firstLaunchTargetObservation{}, ErrFirstLaunchSeedTargetChanged
	}
	fingerprint, err := fingerprintFirstLaunchTree(targetData)
	if err != nil {
		return firstLaunchTargetObservation{}, err
	}
	return firstLaunchTargetObservation{
		dataExists: true, dataInfo: info, dataFingerprint: fingerprint,
	}, nil
}

func installFirstLaunchSeedData(
	targetData string,
	stagedData string,
	stagedMarker string,
	targetRoot string,
	observation firstLaunchTargetObservation,
) (bool, error) {
	published, err := observeFirstLaunchPublishedState(stagedData, stagedMarker)
	if err != nil {
		return false, err
	}
	// These name transitions close the create-after-observation window without
	// ever renaming an unverified destination into disposable staging. A
	// non-cooperating writer that keeps an already-open descriptor and mutates
	// indefinitely after the atomic transition remains outside this path-based
	// protocol; every rollback after exposure therefore quarantines, never
	// deletes, the displaced tree.
	if observation.dataExists {
		return installFirstLaunchSeedOverPristineData(
			targetData, stagedData, stagedMarker, targetRoot, observation, published,
		)
	}
	if err := renameFirstLaunchNoReplace(stagedData, targetData); err != nil {
		return false, classifyFirstLaunchAtomicTransitionError(err)
	}
	return publishFirstLaunchMarkerAfterNewData(
		targetData, stagedData, stagedMarker, targetRoot, published,
	)
}

func installFirstLaunchSeedOverPristineData(
	targetData string,
	stagedData string,
	stagedMarker string,
	targetRoot string,
	observation firstLaunchTargetObservation,
	published firstLaunchPublishedObservation,
) (bool, error) {
	if err := exchangeFirstLaunchPaths(stagedData, targetData); err != nil {
		return false, classifyFirstLaunchAtomicTransitionError(err)
	}
	matches, err := firstLaunchObservedDataMatches(stagedData, observation)
	if err == nil && matches {
		matches, err = firstLaunchDataIsReplaceable(stagedData)
	}
	if err != nil || !matches {
		rollbackErr := exchangeFirstLaunchPaths(stagedData, targetData)
		if rollbackErr != nil {
			return true, errors.Join(ErrFirstLaunchSeedTargetChanged, err, rollbackErr)
		}
		// The staged seed was briefly visible. Quarantine it even after a
		// successful rollback so writes through a raced pathname are retained.
		return true, errors.Join(ErrFirstLaunchSeedTargetChanged, err)
	}

	markerErr := renameFirstLaunchNoReplace(stagedMarker, filepath.Join(targetRoot, "bootstrap-state"))
	if markerErr == nil {
		if afterFirstLaunchMarkerPublished != nil {
			afterFirstLaunchMarkerPublished()
		}
		if validationErr := validateFirstLaunchPublishedState(
			targetRoot, targetData, published,
		); validationErr != nil {
			return rollbackFirstLaunchPublishedState(
				targetData, stagedData, stagedMarker, targetRoot, true, validationErr,
			)
		}
		// The displaced pristine tree remains reachable as quarantine. Besides
		// preserving its exact identity, this prevents a late writer holding an
		// already-open descriptor from writing into an inode deleted by cleanup.
		return true, nil
	}
	markerErr = classifyFirstLaunchAtomicTransitionError(markerErr)
	if rollbackErr := exchangeFirstLaunchPaths(stagedData, targetData); rollbackErr != nil {
		return true, errors.Join(markerErr, rollbackErr)
	}
	// Restore the exact original namespace and quarantine the seed plus any
	// concurrent writes that landed while it was published.
	return true, markerErr
}

func publishFirstLaunchMarkerAfterNewData(
	targetData string,
	stagedData string,
	stagedMarker string,
	targetRoot string,
	published firstLaunchPublishedObservation,
) (bool, error) {
	markerErr := renameFirstLaunchNoReplace(stagedMarker, filepath.Join(targetRoot, "bootstrap-state"))
	if markerErr == nil {
		if afterFirstLaunchMarkerPublished != nil {
			afterFirstLaunchMarkerPublished()
		}
		if validationErr := validateFirstLaunchPublishedState(
			targetRoot, targetData, published,
		); validationErr != nil {
			return rollbackFirstLaunchPublishedState(
				targetData, stagedData, stagedMarker, targetRoot, false, validationErr,
			)
		}
		return false, nil
	}
	markerErr = classifyFirstLaunchAtomicTransitionError(markerErr)
	if rollbackErr := renameFirstLaunchNoReplace(targetData, stagedData); rollbackErr != nil {
		return true, errors.Join(markerErr, rollbackErr)
	}
	// The published tree is quarantined rather than deleted, even if it still
	// matches the seed, because it was externally addressable before rollback.
	return true, markerErr
}

func observeFirstLaunchPublishedState(
	stagedData string,
	stagedMarker string,
) (firstLaunchPublishedObservation, error) {
	dataFingerprint, err := fingerprintFirstLaunchTree(stagedData)
	if err != nil {
		return firstLaunchPublishedObservation{}, err
	}
	markerFingerprint, err := fingerprintFirstLaunchTree(stagedMarker)
	if err != nil {
		return firstLaunchPublishedObservation{}, err
	}
	return firstLaunchPublishedObservation{
		dataFingerprint: dataFingerprint, markerFingerprint: markerFingerprint,
	}, nil
}

func validateFirstLaunchPublishedState(
	targetRoot string,
	targetData string,
	published firstLaunchPublishedObservation,
) error {
	state, err := inspectFirstLaunchPublishedTarget(targetRoot)
	if err != nil {
		return errors.Join(ErrFirstLaunchSeedTargetChanged, err)
	}
	if state != FirstLaunchTargetExistingProfile {
		return ErrFirstLaunchSeedTargetChanged
	}
	dataMatches, err := firstLaunchFingerprintMatches(targetData, published.dataFingerprint)
	if err != nil || !dataMatches {
		return errors.Join(ErrFirstLaunchSeedTargetChanged, err)
	}
	markerMatches, err := firstLaunchFingerprintMatches(
		filepath.Join(targetRoot, "bootstrap-state"), published.markerFingerprint,
	)
	if err != nil || !markerMatches {
		return errors.Join(ErrFirstLaunchSeedTargetChanged, err)
	}
	return nil
}

func inspectFirstLaunchPublishedTarget(root string) (FirstLaunchTargetState, error) {
	rootInfo, err := os.Lstat(root)
	if errors.Is(err, os.ErrNotExist) {
		return FirstLaunchTargetRequiresSeed, nil
	}
	if err != nil {
		return FirstLaunchTargetBlocked, err
	}
	if rootInfo.Mode()&os.ModeSymlink != 0 || !rootInfo.IsDir() {
		return FirstLaunchTargetBlocked, nil
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return FirstLaunchTargetBlocked, err
	}
	hasData := false
	hasMarker := false
	for _, entry := range entries {
		if entry.Type()&os.ModeSymlink != 0 {
			return FirstLaunchTargetBlocked, nil
		}
		switch entry.Name() {
		case "license":
			safe, safeErr := inspectFirstLaunchRuntimeFiles(
				filepath.Join(root, entry.Name()), entry,
				func(name string) bool {
					return name == "license.tcomplicense" ||
						(strings.HasPrefix(name, ".license-") && len(name) > len(".license-"))
				},
			)
			if safeErr != nil {
				return FirstLaunchTargetBlocked, safeErr
			}
			if !safe {
				return FirstLaunchTargetBlocked, nil
			}
		case "logs":
			safe, safeErr := inspectFirstLaunchRuntimeFiles(
				filepath.Join(root, entry.Name()), entry,
				func(name string) bool { return name == "desktop.log" },
			)
			if safeErr != nil {
				return FirstLaunchTargetBlocked, safeErr
			}
			if !safe {
				return FirstLaunchTargetBlocked, nil
			}
		case "data":
			info, infoErr := entry.Info()
			if infoErr != nil {
				return FirstLaunchTargetBlocked, infoErr
			}
			if !info.IsDir() {
				return FirstLaunchTargetBlocked, nil
			}
			hasData = true
		case "bootstrap-state":
			info, infoErr := entry.Info()
			if infoErr != nil {
				return FirstLaunchTargetBlocked, infoErr
			}
			if !info.IsDir() {
				return FirstLaunchTargetBlocked, nil
			}
			hasMarker = true
		default:
			return FirstLaunchTargetBlocked, nil
		}
	}
	if hasData && hasMarker {
		return FirstLaunchTargetExistingProfile, nil
	}
	if !hasData && !hasMarker {
		return FirstLaunchTargetRequiresSeed, nil
	}
	return FirstLaunchTargetBlocked, nil
}

func rollbackFirstLaunchPublishedState(
	targetData string,
	stagedData string,
	stagedMarker string,
	targetRoot string,
	restorePristineData bool,
	cause error,
) (bool, error) {
	markerErr := renameFirstLaunchNoReplace(
		filepath.Join(targetRoot, "bootstrap-state"), stagedMarker,
	)
	var dataErr error
	if restorePristineData {
		dataErr = exchangeFirstLaunchPaths(stagedData, targetData)
	} else {
		dataErr = renameFirstLaunchNoReplace(targetData, stagedData)
	}
	return true, errors.Join(
		cause,
		classifyFirstLaunchAtomicTransitionError(markerErr),
		classifyFirstLaunchAtomicTransitionError(dataErr),
	)
}

func firstLaunchObservedDataMatches(path string, observation firstLaunchTargetObservation) (bool, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return false, err
	}
	if !info.IsDir() || !os.SameFile(observation.dataInfo, info) {
		return false, nil
	}
	return firstLaunchFingerprintMatches(path, observation.dataFingerprint)
}

func firstLaunchFingerprintMatches(path string, want []firstLaunchTreeFingerprintEntry) (bool, error) {
	got, err := fingerprintFirstLaunchTree(path)
	if err != nil {
		return false, err
	}
	return equalFirstLaunchTreeFingerprint(got, want), nil
}

func fingerprintFirstLaunchTree(root string) ([]firstLaunchTreeFingerprintEntry, error) {
	return firstLaunchFingerprintTree(root)
}

func equalFirstLaunchTreeFingerprint(left, right []firstLaunchTreeFingerprintEntry) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index].path != right[index].path ||
			left[index].mode != right[index].mode ||
			left[index].size != right[index].size ||
			left[index].digest != right[index].digest ||
			!os.SameFile(left[index].info, right[index].info) {
			return false
		}
	}
	return true
}

func classifyFirstLaunchAtomicTransitionError(err error) error {
	if errors.Is(err, os.ErrExist) || errors.Is(err, os.ErrNotExist) {
		return ErrFirstLaunchSeedTargetChanged
	}
	return err
}

func validateFirstLaunchSeedLocationConfig(config FirstLaunchSeedConfig) error {
	if strings.TrimSpace(config.BundlePath) == "" || strings.TrimSpace(config.TargetRoot) == "" ||
		strings.TrimSpace(config.AppVersion) == "" {
		return errors.New("first launch seed: configuration is incomplete")
	}
	return nil
}

func validateFirstLaunchSeedImportConfig(config FirstLaunchSeedConfig) error {
	hasLicense := strings.TrimSpace(config.License) != ""
	hasKey := len(config.Key) > 0
	_, hasBatchWriter := config.Secrets.(firstLaunchSecretBatchWriter)
	if hasLicense == hasKey || (hasKey && len(config.Key) != 32) || config.Secrets == nil || !hasBatchWriter {
		return errors.New("first launch seed: import credentials are incomplete")
	}
	return nil
}

// FirstLaunchSeedRequired reports whether the bundled seed may initialize this root.
func FirstLaunchSeedRequired(targetRoot string) (bool, error) {
	state, err := InspectFirstLaunchSeedTarget(targetRoot)
	return state == FirstLaunchTargetRequiresSeed, err
}

// InspectFirstLaunchSeedTarget distinguishes a clean seed target from a
// recognized existing profile and an ambiguous or unsafe tree. It is read-only.
func InspectFirstLaunchSeedTarget(targetRoot string) (FirstLaunchTargetState, error) {
	if strings.TrimSpace(targetRoot) == "" {
		return FirstLaunchTargetBlocked, errors.New("first launch seed: target root is required")
	}
	return inspectFirstLaunchTarget(filepath.Clean(targetRoot))
}

func pathExists(path string) (bool, error) {
	_, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func firstLaunchTargetIsEmpty(root string) (bool, error) {
	state, err := inspectFirstLaunchTarget(root)
	return state == FirstLaunchTargetRequiresSeed, err
}

func inspectFirstLaunchTarget(root string) (FirstLaunchTargetState, error) {
	rootInfo, err := os.Lstat(root)
	if errors.Is(err, os.ErrNotExist) {
		return FirstLaunchTargetRequiresSeed, nil
	}
	if err != nil {
		return FirstLaunchTargetBlocked, err
	}
	if rootInfo.Mode()&os.ModeSymlink != 0 || !rootInfo.IsDir() {
		return FirstLaunchTargetBlocked, nil
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return FirstLaunchTargetBlocked, err
	}
	existing := false
	hasImportMarker := false
	hasRuntimeOwnedRoot := false
	for _, entry := range entries {
		if entry.Type()&os.ModeSymlink != 0 {
			return FirstLaunchTargetBlocked, nil
		}
		switch entry.Name() {
		case "license":
			safe, safeErr := inspectFirstLaunchRuntimeFiles(
				filepath.Join(root, entry.Name()), entry,
				func(name string) bool {
					return name == "license.tcomplicense" ||
						(strings.HasPrefix(name, ".license-") && len(name) > len(".license-"))
				},
			)
			if safeErr != nil {
				return FirstLaunchTargetBlocked, safeErr
			}
			if !safe {
				return FirstLaunchTargetBlocked, nil
			}
			continue
		case "logs":
			safe, safeErr := inspectFirstLaunchRuntimeFiles(
				filepath.Join(root, entry.Name()), entry,
				func(name string) bool { return name == "desktop.log" },
			)
			if safeErr != nil {
				return FirstLaunchTargetBlocked, safeErr
			}
			if !safe {
				return FirstLaunchTargetBlocked, nil
			}
			continue
		case "bootstrap-state":
			markerState, markerErr := inspectFirstLaunchMarker(filepath.Join(root, entry.Name()), entry)
			if markerErr != nil {
				return FirstLaunchTargetBlocked, markerErr
			}
			if markerState == FirstLaunchTargetBlocked {
				return markerState, nil
			}
			hasImportMarker = true
			continue
		case "proxy":
			proxyState, proxyErr := inspectFirstLaunchProxy(filepath.Join(root, entry.Name()), entry)
			if proxyErr != nil {
				return FirstLaunchTargetBlocked, proxyErr
			}
			if proxyState == FirstLaunchTargetBlocked {
				return proxyState, nil
			}
			hasRuntimeOwnedRoot = true
			continue
		case "data":
			info, infoErr := entry.Info()
			if infoErr != nil {
				return FirstLaunchTargetBlocked, infoErr
			}
			if !info.IsDir() {
				return FirstLaunchTargetBlocked, nil
			}
			dataState, inspectErr := inspectFirstLaunchData(filepath.Join(root, entry.Name()))
			if inspectErr != nil {
				return FirstLaunchTargetBlocked, inspectErr
			}
			if dataState == FirstLaunchTargetBlocked {
				return dataState, nil
			}
			existing = existing || dataState == FirstLaunchTargetExistingProfile
			continue
		}
		return FirstLaunchTargetBlocked, nil
	}
	if (hasImportMarker || hasRuntimeOwnedRoot) && !existing {
		return FirstLaunchTargetBlocked, nil
	}
	if existing {
		return FirstLaunchTargetExistingProfile, nil
	}
	return FirstLaunchTargetRequiresSeed, nil
}

func inspectFirstLaunchProxy(path string, entry fs.DirEntry) (FirstLaunchTargetState, error) {
	info, err := entry.Info()
	if err != nil {
		return FirstLaunchTargetBlocked, err
	}
	if !info.IsDir() {
		return FirstLaunchTargetBlocked, nil
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return FirstLaunchTargetBlocked, err
	}
	if len(entries) != 1 || entries[0].Name() != "tor-snowflake" || entries[0].Type()&os.ModeSymlink != 0 {
		return FirstLaunchTargetBlocked, nil
	}
	torInfo, err := entries[0].Info()
	if err != nil {
		return FirstLaunchTargetBlocked, err
	}
	if !torInfo.IsDir() {
		return FirstLaunchTargetBlocked, nil
	}
	torRoot := filepath.Join(path, entries[0].Name())
	torEntries, err := os.ReadDir(torRoot)
	if err != nil {
		return FirstLaunchTargetBlocked, err
	}
	for _, torEntry := range torEntries {
		if torEntry.Type()&os.ModeSymlink != 0 {
			return FirstLaunchTargetBlocked, nil
		}
		switch torEntry.Name() {
		case "data":
			info, infoErr := torEntry.Info()
			if infoErr != nil {
				return FirstLaunchTargetBlocked, infoErr
			}
			if !info.IsDir() {
				return FirstLaunchTargetBlocked, nil
			}
			safe, safeErr := firstLaunchSafeTree(filepath.Join(torRoot, torEntry.Name()))
			if safeErr != nil {
				return FirstLaunchTargetBlocked, safeErr
			}
			if !safe {
				return FirstLaunchTargetBlocked, nil
			}
		case "torrc", "tor-output.log", "bootstrap.log":
			info, infoErr := torEntry.Info()
			if infoErr != nil {
				return FirstLaunchTargetBlocked, infoErr
			}
			if !info.Mode().IsRegular() {
				return FirstLaunchTargetBlocked, nil
			}
		default:
			return FirstLaunchTargetBlocked, nil
		}
	}
	return FirstLaunchTargetRequiresSeed, nil
}

func inspectFirstLaunchRuntimeFiles(path string, entry fs.DirEntry, allowed func(string) bool) (bool, error) {
	info, err := entry.Info()
	if err != nil {
		return false, err
	}
	if !info.IsDir() {
		return false, nil
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return false, err
	}
	for _, child := range entries {
		if child.Type()&os.ModeSymlink != 0 || !allowed(child.Name()) {
			return false, nil
		}
		childInfo, infoErr := child.Info()
		if infoErr != nil {
			return false, infoErr
		}
		if !childInfo.Mode().IsRegular() {
			return false, nil
		}
	}
	return true, nil
}

func firstLaunchSafeTree(root string) (bool, error) {
	safe := true
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == root {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			safe = false
			return fs.SkipAll
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.IsDir() && !info.Mode().IsRegular() {
			safe = false
			return fs.SkipAll
		}
		return nil
	})
	return safe, err
}

func inspectFirstLaunchMarker(path string, entry fs.DirEntry) (FirstLaunchTargetState, error) {
	info, err := entry.Info()
	if err != nil {
		return FirstLaunchTargetBlocked, err
	}
	if !info.IsDir() {
		return FirstLaunchTargetBlocked, nil
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return FirstLaunchTargetBlocked, err
	}
	if len(entries) != 1 || entries[0].Name() != "imported.json" || entries[0].Type()&os.ModeSymlink != 0 {
		return FirstLaunchTargetBlocked, nil
	}
	markerInfo, err := entries[0].Info()
	if err != nil {
		return FirstLaunchTargetBlocked, err
	}
	if !markerInfo.Mode().IsRegular() || markerInfo.Size() == 0 {
		return FirstLaunchTargetBlocked, nil
	}
	if markerInfo.Size() > 4096 {
		return FirstLaunchTargetBlocked, nil
	}
	markerFile, err := os.Open(filepath.Join(path, entries[0].Name()))
	if err != nil {
		return FirstLaunchTargetBlocked, err
	}
	defer markerFile.Close()
	var marker struct {
		SchemaVersion int       `json:"schema_version"`
		BundleID      string    `json:"bundle_id"`
		AppVersion    string    `json:"app_version"`
		CreatedAt     time.Time `json:"created_at"`
	}
	decoder := json.NewDecoder(markerFile)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&marker); err != nil {
		return FirstLaunchTargetBlocked, nil
	}
	var trailing any
	if err := decoder.Decode(&trailing); err == nil {
		return FirstLaunchTargetBlocked, nil
	} else if !errors.Is(err, io.EOF) {
		return FirstLaunchTargetBlocked, nil
	}
	if marker.SchemaVersion != bootstrapstate.SchemaVersion || strings.TrimSpace(marker.BundleID) == "" ||
		strings.TrimSpace(marker.AppVersion) == "" || marker.CreatedAt.IsZero() {
		return FirstLaunchTargetBlocked, nil
	}
	return FirstLaunchTargetExistingProfile, nil
}

func inspectFirstLaunchData(root string) (FirstLaunchTargetState, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return FirstLaunchTargetBlocked, err
	}
	// SQLite can consult or checkpoint sidecars even when opened through a
	// read-only connection. Reject the ambiguous tree before opening app.db so
	// inspection remains byte-for-byte read-only.
	for _, entry := range entries {
		if entry.Name() == "app.db-shm" || entry.Name() == "app.db-wal" {
			return FirstLaunchTargetBlocked, nil
		}
	}
	existing := false
	allowedDirectories := map[string]struct{}{
		"backups": {}, "exports": {}, "gotd-import-staging": {}, "import-snapshots": {}, "sessions": {}, "tdata": {},
	}
	for _, entry := range entries {
		if entry.Type()&os.ModeSymlink != 0 {
			return FirstLaunchTargetBlocked, nil
		}
		if entry.IsDir() {
			if _, allowed := allowedDirectories[entry.Name()]; !allowed {
				return FirstLaunchTargetBlocked, nil
			}
			treeState, treeErr := inspectFirstLaunchRuntimeTree(filepath.Join(root, entry.Name()))
			if treeErr != nil {
				return FirstLaunchTargetBlocked, treeErr
			}
			if treeState == FirstLaunchTargetBlocked {
				return treeState, nil
			}
			existing = existing || treeState == FirstLaunchTargetExistingProfile
			continue
		}
		info, infoErr := entry.Info()
		if infoErr != nil {
			return FirstLaunchTargetBlocked, infoErr
		}
		if !info.Mode().IsRegular() {
			return FirstLaunchTargetBlocked, nil
		}
		switch entry.Name() {
		case "application-state.bolt":
			existing = existing || info.Size() > 0
		case "app.db":
			replaceable, databaseErr := firstLaunchDatabaseIsReplaceable(filepath.Join(root, entry.Name()))
			if databaseErr != nil {
				return FirstLaunchTargetBlocked, nil
			}
			existing = existing || !replaceable
		default:
			return FirstLaunchTargetBlocked, nil
		}
	}
	if existing {
		return FirstLaunchTargetExistingProfile, nil
	}
	return FirstLaunchTargetRequiresSeed, nil
}

func inspectFirstLaunchRuntimeTree(root string) (FirstLaunchTargetState, error) {
	existing := false
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == root {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fs.ErrInvalid
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.IsDir() && !info.Mode().IsRegular() {
			return fs.ErrInvalid
		}
		if info.Mode().IsRegular() {
			existing = true
		}
		return nil
	})
	if errors.Is(err, fs.ErrInvalid) {
		return FirstLaunchTargetBlocked, nil
	}
	if err != nil {
		return FirstLaunchTargetBlocked, err
	}
	if existing {
		return FirstLaunchTargetExistingProfile, nil
	}
	return FirstLaunchTargetRequiresSeed, nil
}

func firstLaunchDataIsReplaceable(root string) (bool, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return false, err
	}
	// A sidecar can contain committed state not visible in app.db and opening
	// the database may checkpoint it. Reject before any SQLite access.
	for _, entry := range entries {
		if entry.Name() == "app.db-shm" || entry.Name() == "app.db-wal" {
			return false, nil
		}
	}
	var databasePath string
	allowedFiles := map[string]struct{}{
		"app.db":                 {},
		"app.db-shm":             {},
		"app.db-wal":             {},
		"application-state.bolt": {},
	}
	allowedEmptyDirectories := map[string]struct{}{
		"backups":             {},
		"exports":             {},
		"gotd-import-staging": {},
		"import-snapshots":    {},
		"sessions":            {},
		"tdata":               {},
	}
	for _, entry := range entries {
		if entry.Type()&os.ModeSymlink != 0 {
			return false, nil
		}
		if entry.IsDir() {
			if _, ok := allowedEmptyDirectories[entry.Name()]; !ok {
				return false, nil
			}
			empty, emptyErr := firstLaunchTreeIsEmpty(filepath.Join(root, entry.Name()))
			if emptyErr != nil {
				return false, emptyErr
			}
			if !empty {
				return false, nil
			}
			continue
		}
		if _, ok := allowedFiles[entry.Name()]; !ok {
			return false, nil
		}
		if entry.Name() == "application-state.bolt" {
			info, infoErr := entry.Info()
			if infoErr != nil {
				return false, infoErr
			}
			if info.Size() > 0 {
				return false, nil
			}
		}
		if entry.Name() == "app.db" {
			databasePath = filepath.Join(root, entry.Name())
		}
	}
	if databasePath != "" {
		return firstLaunchDatabaseIsReplaceable(databasePath)
	}
	return true, nil
}

func firstLaunchDatabaseIsReplaceable(path string) (bool, error) {
	databasePath := filepath.ToSlash(path)
	if filepath.VolumeName(path) != "" && !strings.HasPrefix(databasePath, "/") {
		databasePath = "/" + databasePath
	}
	databaseURL := &url.URL{Scheme: "file", Path: databasePath}
	query := databaseURL.Query()
	query.Set("mode", "ro")
	databaseURL.RawQuery = query.Encode()

	db, err := sql.Open("sqlite", databaseURL.String())
	if err != nil {
		return false, err
	}
	defer db.Close()

	rows, err := db.Query(`SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%'`)
	if err != nil {
		return false, err
	}
	defer rows.Close()

	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return false, err
		}
		if name != "goose_db_version" {
			tables = append(tables, name)
		}
	}
	if err := rows.Err(); err != nil {
		return false, err
	}
	if err := rows.Close(); err != nil {
		return false, err
	}

	for _, table := range tables {
		switch table {
		case "analytics_scheduler_settings":
			replaceable, err := firstLaunchAnalyticsSettingsAreDefault(db)
			if err != nil || !replaceable {
				return replaceable, err
			}
			continue
		case "analytics_service_words":
			replaceable, err := firstLaunchServiceWordsAreDefault(db)
			if err != nil || !replaceable {
				return replaceable, err
			}
			continue
		}
		quotedTable := `"` + strings.ReplaceAll(table, `"`, `""`) + `"`
		var hasRows bool
		if err := db.QueryRow(`SELECT EXISTS(SELECT 1 FROM ` + quotedTable + ` LIMIT 1)`).Scan(&hasRows); err != nil {
			return false, err
		}
		if hasRows {
			return false, nil
		}
	}
	return true, nil
}

func firstLaunchAnalyticsSettingsAreDefault(db *sql.DB) (bool, error) {
	var rows, nonDefault int
	err := db.QueryRow(`SELECT COUNT(*), COALESCE(SUM(CASE
		WHEN singleton = 1 AND enabled = 1 AND interval_minutes = 10 THEN 0 ELSE 1 END), 0)
		FROM analytics_scheduler_settings`).Scan(&rows, &nonDefault)
	return rows <= 1 && nonDefault == 0, err
}

func firstLaunchServiceWordsAreDefault(db *sql.DB) (bool, error) {
	var rows, creationTimes, modified int
	err := db.QueryRow(`SELECT COUNT(*), COUNT(DISTINCT created_at), COALESCE(SUM(CASE
		WHEN created_at = updated_at THEN 0 ELSE 1 END), 0)
		FROM analytics_service_words`).Scan(&rows, &creationTimes, &modified)
	return rows == firstLaunchDefaultAnalyticsServiceWords && creationTimes == 1 && modified == 0, err
}

func firstLaunchTreeIsEmpty(root string) (bool, error) {
	empty := true
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == root {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 || !entry.IsDir() {
			empty = false
			return fs.SkipAll
		}
		return nil
	})
	return empty, err
}

func validateFirstLaunchSeedData(root string) error {
	entries, err := os.ReadDir(root)
	if err != nil {
		return fmt.Errorf("%w: read staged data: %v", ErrInvalidFirstLaunchSeed, err)
	}
	hasDatabase := false
	for _, entry := range entries {
		name := entry.Name()
		path := filepath.Join(root, name)
		info, err := os.Lstat(path)
		if err != nil {
			return fmt.Errorf("%w: inspect %s: %v", ErrInvalidFirstLaunchSeed, name, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("%w: symbolic link %s", ErrInvalidFirstLaunchSeed, name)
		}
		if _, ok := firstLaunchSeedFiles[name]; ok {
			if !info.Mode().IsRegular() {
				return fmt.Errorf("%w: %s must be a regular file", ErrInvalidFirstLaunchSeed, name)
			}
			hasDatabase = hasDatabase || name == "app.db"
			continue
		}
		if _, ok := firstLaunchSeedDirectories[name]; ok {
			if !info.IsDir() {
				return fmt.Errorf("%w: %s must be a directory", ErrInvalidFirstLaunchSeed, name)
			}
			if err := validateFirstLaunchSeedTree(path); err != nil {
				return err
			}
			continue
		}
		return fmt.Errorf("%w: forbidden path %s", ErrInvalidFirstLaunchSeed, name)
	}
	if !hasDatabase {
		return fmt.Errorf("%w: app.db is missing", ErrInvalidFirstLaunchSeed)
	}
	return nil
}

func validateFirstLaunchSeedTree(root string) error {
	return filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return fmt.Errorf("%w: walk seed data: %v", ErrInvalidFirstLaunchSeed, walkErr)
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("%w: symbolic link %s", ErrInvalidFirstLaunchSeed, path)
		}
		info, err := entry.Info()
		if err != nil {
			return fmt.Errorf("%w: inspect %s: %v", ErrInvalidFirstLaunchSeed, path, err)
		}
		if !info.IsDir() && !info.Mode().IsRegular() {
			return fmt.Errorf("%w: unsupported file %s", ErrInvalidFirstLaunchSeed, path)
		}
		return nil
	})
}

type firstLaunchSecretStaging struct {
	values map[string][]byte
}

func newFirstLaunchSecretStaging() *firstLaunchSecretStaging {
	return &firstLaunchSecretStaging{values: make(map[string][]byte)}
}

func (s *firstLaunchSecretStaging) Set(_ context.Context, name string, value []byte) error {
	if _, allowed := firstLaunchOperationalSecrets[name]; !allowed || len(value) != firstLaunchSecretSize {
		return ErrInvalidFirstLaunchSeed
	}
	s.values[name] = append([]byte(nil), value...)
	return nil
}

func (s *firstLaunchSecretStaging) flush(ctx context.Context, destination bootstrapstate.SecretWriter, commit func() error) error {
	if len(s.values) != len(firstLaunchOperationalSecrets) {
		return ErrInvalidFirstLaunchSeed
	}
	for name := range firstLaunchOperationalSecrets {
		if len(s.values[name]) != firstLaunchSecretSize {
			return ErrInvalidFirstLaunchSeed
		}
	}
	writer, ok := destination.(firstLaunchSecretBatchWriter)
	if !ok {
		return errors.New("first launch seed: atomic secret store is required")
	}
	if err := writer.SetBatch(ctx, s.values, commit); err != nil {
		return fmt.Errorf("first launch seed: install operational secrets: %w", err)
	}
	return nil
}

func (s *firstLaunchSecretStaging) clear() {
	for name, value := range s.values {
		clear(value)
		delete(s.values, name)
	}
}
