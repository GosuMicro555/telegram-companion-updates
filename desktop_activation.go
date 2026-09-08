//go:build desktop

package main

import (
	"context"
	"crypto/ed25519"
	cryptorand "crypto/rand"
	"encoding/base64"
	"errors"
	"math/big"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/linux"
	"github.com/wailsapp/wails/v2/pkg/options/mac"

	appbootstrap "telegram-companion/internal/app"
	"telegram-companion/internal/bootstrapstate"
	"telegram-companion/internal/buildinfo"
	"telegram-companion/internal/license"
	platformwake "telegram-companion/internal/platform/wake"
	"telegram-companion/internal/revocation"
	secretservice "telegram-companion/internal/service/secrets"
	wailsbindings "telegram-companion/internal/transport/wails"
	"telegram-companion/internal/updater"
	"telegram-companion/internal/updater/sparkle"
)

func newProductionDesktopLicenseGate(
	info buildinfo.Info,
	tokens license.TokenStore,
	secrets *secretservice.SecretStore,
) (*license.Gate, revocation.DecisionChecker, error) {
	if info.Channel == buildinfo.ChannelInternal {
		return license.NewGate(info, tokens), nil, nil
	}
	if secrets == nil {
		return nil, nil, errors.New("desktop revocation state is unavailable")
	}
	fetcher, err := revocation.NewHTTPFetcher(info.RevocationManifestURL)
	if err != nil {
		return nil, nil, errors.New("desktop revocation transport is invalid")
	}
	return newDesktopLicenseGateWithDependencies(
		info,
		tokens,
		secretservice.NewRevocationStateStore(secrets),
		fetcher,
		license.MachineID,
		license.ParseAndVerify,
		time.Now,
	)
}

func newDesktopLicenseGateWithDependencies(
	info buildinfo.Info,
	tokens license.TokenStore,
	state revocation.StateStore,
	fetcher revocation.Fetcher,
	machineID license.MachineIDProvider,
	verify license.Verifier,
	now func() time.Time,
) (*license.Gate, revocation.DecisionChecker, error) {
	if info.Channel == buildinfo.ChannelInternal {
		return license.NewGateWithDependencies(info, tokens, machineID, verify), nil, nil
	}
	if info.Channel != buildinfo.ChannelPublicMacOSARM64 || state == nil || fetcher == nil || now == nil ||
		info.RevocationManifestURL != buildinfo.ExpectedRevocationManifestURL ||
		info.RevocationKeyID != buildinfo.ExpectedRevocationKeyID {
		return nil, nil, errors.New("desktop revocation configuration is invalid")
	}
	publicKey, err := base64.StdEncoding.DecodeString(info.RevocationPublicKey)
	if err != nil || len(publicKey) != ed25519.PublicKeySize || base64.StdEncoding.EncodeToString(publicKey) != info.RevocationPublicKey {
		return nil, nil, errors.New("desktop revocation public key is invalid")
	}
	checker, err := revocation.NewChecker(state, fetcher, info.RevocationKeyID, ed25519.PublicKey(publicKey), now)
	if err != nil {
		return nil, nil, errors.New("desktop revocation checker is invalid")
	}
	gate := license.NewGateWithRevocationDependencies(info, tokens, machineID, verify, checker)
	return gate, checker, nil
}

type desktopMode string

const (
	desktopModeActivation desktopMode = "activation"
	desktopModeRecovery   desktopMode = "recovery"
	desktopModeWorkspace  desktopMode = "workspace"
)

type desktopProgram struct {
	mode         desktopMode
	app          *DesktopApp
	recoveryCode desktopRecoveryCode
	revocation   *desktopRevocationRuntime
}

type desktopTerminalGateTarget interface {
	ApplyRevocationDecision(revocation.Decision) license.Snapshot
}

type desktopRevocationTarget interface {
	RejectNewOperations(context.Context) error
	Shutdown(context.Context)
}

type desktopRelaunchRequester interface {
	Request(context.Context) error
}

type desktopWakeObserver interface {
	Start()
	Stop()
}

type desktopRevocationRuntime struct {
	gate          desktopTerminalGateTarget
	target        desktopRevocationTarget
	relauncher    desktopRelaunchRequester
	quit          func(context.Context)
	supervisor    *revocation.Supervisor
	wake          desktopWakeObserver
	rejectTimeout time.Duration

	rootMu sync.RWMutex
	root   context.Context

	lifecycleMu  sync.Mutex
	started      bool
	stopped      bool
	terminalOnce sync.Once
	cancel       context.CancelFunc
}

func newDesktopRevocationRuntime(
	gate desktopTerminalGateTarget,
	target desktopRevocationTarget,
	relauncher desktopRelaunchRequester,
	quit func(context.Context),
	checker revocation.DecisionChecker,
	licenseID string,
	newTimer revocation.TimerFactory,
	jitter func() time.Duration,
) (*desktopRevocationRuntime, error) {
	if gate == nil || target == nil || relauncher == nil || quit == nil {
		return nil, errors.New("desktop revocation runtime is invalid")
	}
	runtime := &desktopRevocationRuntime{
		gate: gate, target: target, relauncher: relauncher, quit: quit, root: context.Background(),
		rejectTimeout: 15 * time.Second,
	}
	supervisor, err := revocation.NewSupervisor(licenseID, checker, newTimer, jitter, runtime.handleTerminal)
	if err != nil {
		return nil, errors.New("desktop revocation supervisor is invalid")
	}
	runtime.supervisor = supervisor
	runtime.wake = platformwake.New(supervisor.CheckNow)
	return runtime, nil
}

func (runtime *desktopRevocationRuntime) Startup(root context.Context) {
	if runtime == nil || runtime.supervisor == nil {
		return
	}
	if root == nil {
		root = context.Background()
	}
	runtime.rootMu.Lock()
	runtime.root = root
	runtime.rootMu.Unlock()
	runtime.lifecycleMu.Lock()
	if runtime.started || runtime.stopped {
		runtime.lifecycleMu.Unlock()
		return
	}
	worker, cancel := context.WithCancel(context.Background())
	runtime.cancel = cancel
	runtime.started = true
	if runtime.wake != nil {
		runtime.wake.Start()
	}
	runtime.lifecycleMu.Unlock()
	go runtime.supervisor.Run(worker)
}

func (runtime *desktopRevocationRuntime) Stop() {
	if runtime == nil {
		return
	}
	runtime.lifecycleMu.Lock()
	if runtime.stopped {
		runtime.lifecycleMu.Unlock()
		return
	}
	runtime.stopped = true
	cancel := runtime.cancel
	wake := runtime.wake
	runtime.lifecycleMu.Unlock()
	if wake != nil {
		wake.Stop()
	}
	if cancel != nil {
		cancel()
	}
}

func (runtime *desktopRevocationRuntime) handleTerminal(decision revocation.Decision) {
	if runtime == nil || (decision != revocation.Revoked && decision != revocation.CheckRequired) {
		return
	}
	runtime.terminalOnce.Do(func() {
		snapshot := runtime.gate.ApplyRevocationDecision(decision)
		if snapshot.State != license.StateRevoked && snapshot.State != license.StateCheckRequired {
			return
		}
		root := context.WithoutCancel(runtime.rootContext())
		rejectTimeout := runtime.rejectTimeout
		if rejectTimeout <= 0 {
			rejectTimeout = 15 * time.Second
		}
		rejectContext, cancelReject := context.WithTimeout(root, rejectTimeout)
		rejectErr := runtime.target.RejectNewOperations(rejectContext)
		cancelReject()
		runtime.target.Shutdown(root)
		if rejectErr != nil {
			return
		}
		if err := runtime.relauncher.Request(root); err != nil {
			return
		}
		runtime.quit(root)
	})
}

func (runtime *desktopRevocationRuntime) rootContext() context.Context {
	runtime.rootMu.RLock()
	defer runtime.rootMu.RUnlock()
	if runtime.root == nil {
		return context.Background()
	}
	return runtime.root
}

func randomDesktopRevocationJitter() time.Duration {
	maximum := big.NewInt(int64(revocation.NormalCheckJitter) + 1)
	value, err := cryptorand.Int(cryptorand.Reader, maximum)
	if err != nil {
		return 0
	}
	return time.Duration(value.Int64())
}

type desktopSeedGrantProvider interface {
	SeedGrant() (license.SeedGrant, error)
}

var importFirstLaunchSeed = appbootstrap.ImportFirstLaunchSeed

func importBundledDesktopState(
	ctx context.Context,
	info buildinfo.Info,
	paths desktopPaths,
	grants desktopSeedGrantProvider,
	secrets bootstrapstate.SecretWriter,
) (bool, error) {
	if info.Channel != buildinfo.ChannelPublicMacOSARM64 {
		return false, nil
	}
	inspect := appbootstrap.InspectFirstLaunchSeedTarget
	if info.BootstrapMode == buildinfo.BootstrapModeEmpty {
		inspect = appbootstrap.InspectEmptyPublicProfile
	}
	state, err := inspect(paths.root)
	if err != nil {
		return false, newDesktopStartupError(errDesktopStorageUnavailable, err)
	}
	switch state {
	case appbootstrap.FirstLaunchTargetExistingProfile:
		return false, nil
	case appbootstrap.FirstLaunchTargetBlocked:
		return false, errDesktopProfileBlocked
	case appbootstrap.FirstLaunchTargetRequiresSeed:
		if info.BootstrapMode == buildinfo.BootstrapModeEmpty {
			return false, nil
		}
	default:
		return false, errDesktopProfileBlocked
	}
	if grants == nil || secrets == nil {
		return false, errors.New("desktop bootstrap services are unavailable")
	}
	grant, err := grants.SeedGrant()
	if errors.Is(err, license.ErrNoSeedGrant) {
		return false, errDesktopSeedLicenseRequired
	}
	if err != nil {
		return false, newDesktopStartupError(errDesktopSeedIncompatible, err)
	}
	defer clear(grant.Key)
	bundlePath := filepath.Join(paths.resources, "bootstrap-state", bootstrapstate.BundleFile)
	bundleInfo, err := os.Lstat(bundlePath)
	if errors.Is(err, os.ErrNotExist) {
		return false, errDesktopSeedUnavailable
	}
	if err != nil {
		return false, newDesktopStartupError(errDesktopStorageUnavailable, err)
	}
	if !bundleInfo.Mode().IsRegular() {
		return false, errDesktopSeedIncompatible
	}
	imported, err := importFirstLaunchSeed(ctx, appbootstrap.FirstLaunchSeedConfig{
		BundlePath: bundlePath,
		TargetRoot: paths.root,
		BundleID:   grant.ID,
		AppVersion: info.Version,
		Key:        grant.Key,
		Secrets:    secrets,
	})
	if err == nil && imported {
		return true, nil
	}
	if err == nil {
		// This startup observed a clean profile, so only an import completed by
		// this call may open the workspace. A concurrent winner is recovery, not
		// permission to reinterpret imported=false as success.
		return false, errDesktopProfileBlocked
	}
	var insufficient *appbootstrap.InsufficientSpaceError
	var storageProbe *appbootstrap.StorageProbeError
	switch {
	case errors.As(err, &insufficient), errors.As(err, &storageProbe), errors.Is(err, secretservice.ErrKeyringUnavailable):
		return false, err
	case errors.Is(err, syscall.ENOSPC):
		return false, &appbootstrap.InsufficientSpaceError{}
	case errors.Is(err, bootstrapstate.ErrInvalidBundle),
		errors.Is(err, bootstrapstate.ErrUnsafePath),
		errors.Is(err, bootstrapstate.ErrVersionMismatch),
		errors.Is(err, appbootstrap.ErrInvalidFirstLaunchSeed):
		return false, newDesktopStartupError(errDesktopSeedIncompatible, err)
	case errors.Is(err, appbootstrap.ErrFirstLaunchSeedTargetChanged):
		return false, newDesktopStartupError(errDesktopProfileBlocked, err)
	case errors.Is(err, os.ErrNotExist):
		return false, errDesktopSeedUnavailable
	default:
		return false, newDesktopStartupError(errDesktopStorageUnavailable, err)
	}
}

func newDesktopProgram(
	info buildinfo.Info,
	authorized bool,
	appFactory func() (*DesktopApp, error),
) (desktopProgram, error) {
	if info.RequiresActivation() && !authorized {
		return desktopProgram{mode: desktopModeActivation}, nil
	}
	if appFactory == nil {
		return desktopProgramForRuntimeError(info, errors.New("desktop app factory is required"))
	}
	app, err := appFactory()
	if err != nil {
		return desktopProgramForRuntimeError(info, err)
	}
	if app == nil {
		return desktopProgramForRuntimeError(info, errors.New("desktop app factory returned nil"))
	}
	return desktopProgram{mode: desktopModeWorkspace, app: app}, nil
}

func desktopProgramForRuntimeError(info buildinfo.Info, err error) (desktopProgram, error) {
	if info.Channel == buildinfo.ChannelPublicMacOSARM64 {
		return desktopProgram{mode: desktopModeRecovery, recoveryCode: desktopRecoveryCodeForError(err)}, nil
	}
	return desktopProgram{}, err
}

func newDesktopUpdateService(info buildinfo.Info, program desktopProgram) *updater.Service {
	return newDesktopUpdateServiceWithDriver(info, program, sparkle.NewDriver)
}

func newDesktopUpdateServiceWithDriver(
	info buildinfo.Info,
	program desktopProgram,
	driverFactory func(string) updater.Driver,
) *updater.Service {
	enabled := info.UpdatesEnabled() && program.mode == desktopModeWorkspace
	if !enabled || driverFactory == nil {
		return nil
	}
	return updater.NewService(updater.Config{
		Enabled: true,
		Driver:  driverFactory(info.AppcastURL),
	})
}

func runDesktopProgram(
	program desktopProgram,
	startup *wailsbindings.StartupBindings,
	activation *wailsbindings.ActivationBindings,
	updates *wailsbindings.UpdateBindings,
	run func(*options.App) error,
) error {
	if startup == nil || activation == nil || updates == nil || run == nil {
		return errors.New("desktop startup, activation, updater, and Wails runner are required")
	}
	bindings := []interface{}{startup, activation, updates}
	if program.mode == desktopModeWorkspace {
		if program.app == nil || program.app.Bindings() == nil {
			return errors.New("workspace desktop app is required")
		}
		bindings = append(bindings, program.app.Bindings())
	} else if program.mode != desktopModeActivation && program.mode != desktopModeRecovery {
		return errors.New("desktop program mode is invalid")
	}

	onStartup := func(ctx context.Context) {
		startup.Startup(ctx)
		activation.Startup(ctx)
		updates.Startup(ctx)
		if program.mode == desktopModeWorkspace && program.app != nil {
			program.app.Startup(ctx)
			if program.revocation != nil {
				program.revocation.Startup(ctx)
			}
		}
	}
	onShutdown := func(ctx context.Context) {
		if program.revocation != nil {
			program.revocation.Stop()
		}
		updates.Shutdown()
		if program.mode == desktopModeWorkspace && program.app != nil {
			program.app.Shutdown(ctx)
		}
	}

	return run(&options.App{
		Title: "Telegram Companion", Width: 980, Height: 700, MinWidth: 860, MinHeight: 620, WindowStartState: options.Maximised,
		AssetServer: &assetserver.Options{Assets: assets},
		Linux:       &linux.Options{Icon: appIcon, ProgramName: "telegram-companion"},
		Mac: &mac.Options{
			DisableZoom:                  false,
			DisableEscapeExitsFullscreen: false,
		},
		OnStartup: onStartup, OnShutdown: onShutdown,
		Bind: bindings,
	})
}
