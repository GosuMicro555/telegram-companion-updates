package tor

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"math/rand"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const (
	DefaultSOCKSAddress = "127.0.0.1:19050"
	defaultProbeAddress = "149.154.167.50:443"
	defaultBridgeLine   = "snowflake 192.0.2.3:80 2B280B23E1107BB62ABFC40DDCC8824814F80A72 fingerprint=2B280B23E1107BB62ABFC40DDCC8824814F80A72 url=https://1098762253.rsc.cdn77.org front=www.phpmyadmin.net,cdn.zk.mk ice=stun:stun.antisip.com:3478,stun:stun.epygi.com:3478,stun:stun.uls.co.za:3478,stun:stun.voipgate.com:3478,stun:stun.mixvoip.com:3478,stun:stun.nextcloud.com:3478,stun:stun.bethesda.net:3478,stun:stun.nextcloud.com:443 utls-imitate=hellorandomizedalpn"
)

type Config struct {
	StateDir          string
	ResourcesDir      string
	TorPath           string
	LyrebirdPath      string
	SnowflakePath     string
	SnowflakeBridge   string
	BridgeConfigPath  string
	MaxBridgeAttempts int
	SOCKSAddress      string
	ProbeAddress      string
	BootstrapTimeout  time.Duration
	StopTimeout       time.Duration

	startProcess      processStarter
	waitReady         readinessWaiter
	waitBackoff       backoffWaiter
	now               func() time.Time
	shuffleCandidates func([]BridgeCandidate)

	transportAliasDir string
}

type resolvedConfig struct {
	stateDir          string
	dataDir           string
	torrcPath         string
	outputPath        string
	bootstrapLogPath  string
	torPath           string
	lyrebirdPath      string
	snowflakePath     string
	bridge            string
	bridgeCandidates  []BridgeCandidate
	snowflakeFallback BridgeCandidate
	maxBridgeAttempts int
	socksAddress      string
	probeAddress      string
	bootstrapTimeout  time.Duration
	stopTimeout       time.Duration
	startProcess      processStarter
	waitReady         readinessWaiter
	waitBackoff       backoffWaiter
	now               func() time.Time
	shuffleCandidates func([]BridgeCandidate)

	transportAliasDir string
}

func resolveConfig(input Config) (resolvedConfig, error) {
	stateDir := filepath.Clean(strings.TrimSpace(input.StateDir))
	if stateDir == "." || !filepath.IsAbs(stateDir) {
		return resolvedConfig{}, errors.New("tor state directory must be absolute")
	}
	socksAddress := strings.TrimSpace(input.SOCKSAddress)
	if socksAddress == "" {
		socksAddress = DefaultSOCKSAddress
	}
	if socksAddress != DefaultSOCKSAddress {
		return resolvedConfig{}, errors.New("managed Tor SOCKS address must be 127.0.0.1:19050")
	}
	host, _, err := net.SplitHostPort(socksAddress)
	if err != nil || host != "127.0.0.1" {
		return resolvedConfig{}, errors.New("managed Tor SOCKS address must use loopback")
	}

	torPath, err := discoverExecutable("tor", input.TorPath, "TELEGRAM_COMPANION_TOR_PATH", input.ResourcesDir)
	if err != nil {
		return resolvedConfig{}, errors.New("tor executable unavailable")
	}
	bridgeConfigPath := strings.TrimSpace(input.BridgeConfigPath)
	if bridgeConfigPath == "" {
		bridgeConfigPath = strings.TrimSpace(os.Getenv("TELEGRAM_COMPANION_BRIDGE_CONFIG"))
	}
	if bridgeConfigPath == "" && strings.TrimSpace(input.ResourcesDir) != "" {
		candidatePath := filepath.Join(strings.TrimSpace(input.ResourcesDir), "tor", "pluggable_transports", "pt_config.json")
		if _, statErr := os.Stat(candidatePath); statErr == nil {
			bridgeConfigPath = candidatePath
		}
	}
	lyrebirdRequested := strings.TrimSpace(input.LyrebirdPath) != "" || strings.TrimSpace(os.Getenv("TELEGRAM_COMPANION_LYREBIRD_PATH")) != ""
	if !lyrebirdRequested && strings.TrimSpace(input.ResourcesDir) != "" {
		resourcesDir := strings.TrimSpace(input.ResourcesDir)
		for _, path := range []string{
			filepath.Join(resourcesDir, "tor", "pluggable_transports", "lyrebird"),
			filepath.Join(resourcesDir, "bin", "lyrebird"),
		} {
			if _, statErr := os.Stat(path); statErr == nil {
				lyrebirdRequested = true
				break
			}
		}
	}
	if bridgeConfigPath == "" && lyrebirdRequested {
		return resolvedConfig{}, errors.New("bridge_config_invalid")
	}
	bridgeCandidates := []BridgeCandidate{}
	if bridgeConfigPath != "" {
		loaded, loadErr := LoadBridgeBundle(bridgeConfigPath)
		if loadErr != nil {
			return resolvedConfig{}, errors.New("bridge_config_invalid")
		}
		bridgeCandidates = loaded
	}

	lyrebirdPath := ""
	if len(bridgeCandidates) > 0 {
		for _, candidate := range bridgeCandidates {
			if candidate.Program == ManagedPTLyrebird {
				lyrebirdPath, err = discoverLyrebird(input.LyrebirdPath, input.ResourcesDir)
				if err != nil {
					return resolvedConfig{}, errors.New("pt_binary_unavailable")
				}
				break
			}
		}
	} else if strings.TrimSpace(input.LyrebirdPath) != "" || strings.TrimSpace(os.Getenv("TELEGRAM_COMPANION_LYREBIRD_PATH")) != "" {
		lyrebirdPath, err = discoverLyrebird(input.LyrebirdPath, input.ResourcesDir)
		if err != nil {
			return resolvedConfig{}, errors.New("pt_binary_unavailable")
		}
	}

	snowflakePath := ""
	bridge := ""
	snowflakeFallback := BridgeCandidate{}
	if len(bridgeCandidates) == 0 {
		snowflakePath, err = discoverExecutable("snowflake-client", input.SnowflakePath, "TELEGRAM_COMPANION_SNOWFLAKE_PATH", input.ResourcesDir)
		if err != nil {
			return resolvedConfig{}, errors.New("snowflake-client executable unavailable")
		}
		bridge = strings.TrimSpace(input.SnowflakeBridge)
		if bridge == "" {
			bridge = strings.TrimSpace(os.Getenv("TELEGRAM_COMPANION_SNOWFLAKE_BRIDGE"))
		}
		if bridge == "" {
			bridge = defaultBridgeLine
		}
		candidate, bridgeErr := validateBridgeLine(bridge)
		if bridgeErr != nil || candidate.Transport != TransportSnowflake {
			return resolvedConfig{}, errors.New("snowflake bridge configuration is invalid")
		}
		bridge = candidate.Canonical
		snowflakeFallback = candidate
	}
	maxBridgeAttempts := input.MaxBridgeAttempts
	if maxBridgeAttempts <= 0 {
		maxBridgeAttempts = 8
	}

	bootstrapTimeout := input.BootstrapTimeout
	if bootstrapTimeout <= 0 {
		bootstrapTimeout = 3 * time.Minute
	}
	stopTimeout := input.StopTimeout
	if stopTimeout <= 0 {
		stopTimeout = 5 * time.Second
	}
	probeAddress := strings.TrimSpace(input.ProbeAddress)
	if probeAddress == "" {
		probeAddress = defaultProbeAddress
	}
	config := resolvedConfig{
		stateDir: stateDir, dataDir: filepath.Join(stateDir, "data"),
		torrcPath: filepath.Join(stateDir, "torrc"), outputPath: filepath.Join(stateDir, "tor-output.log"),
		bootstrapLogPath: filepath.Join(stateDir, "bootstrap.log"),
		torPath:          torPath, lyrebirdPath: lyrebirdPath, snowflakePath: snowflakePath, bridge: bridge,
		bridgeCandidates: bridgeCandidates, snowflakeFallback: snowflakeFallback, maxBridgeAttempts: maxBridgeAttempts,
		socksAddress: socksAddress, probeAddress: probeAddress,
		bootstrapTimeout: bootstrapTimeout, stopTimeout: stopTimeout,
		startProcess: input.startProcess, waitReady: input.waitReady,
		waitBackoff: input.waitBackoff, now: input.now, shuffleCandidates: input.shuffleCandidates,
	}
	if config.startProcess == nil {
		config.startProcess = startOSProcess
	}
	if config.waitReady == nil {
		config.waitReady = waitForReadiness
	}
	if config.waitBackoff == nil {
		config.waitBackoff = waitForBackoff
	}
	if config.now == nil {
		config.now = time.Now
	}
	if config.shuffleCandidates == nil {
		config.shuffleCandidates = func(candidates []BridgeCandidate) {
			rand.Shuffle(len(candidates), func(left, right int) {
				candidates[left], candidates[right] = candidates[right], candidates[left]
			})
		}
	}
	config.transportAliasDir = strings.TrimSpace(input.transportAliasDir)
	if (strings.ContainsFunc(snowflakePath, torTokenUnsafeRune) || strings.ContainsFunc(lyrebirdPath, torTokenUnsafeRune)) && config.transportAliasDir == "" {
		cacheDir, cacheErr := os.UserCacheDir()
		if cacheErr != nil {
			return resolvedConfig{}, errors.New("resolve private Tor transport cache")
		}
		config.transportAliasDir = filepath.Join(cacheDir, "telegram-companion", "pt")
	}
	if config.transportAliasDir != "" {
		config.transportAliasDir = filepath.Clean(config.transportAliasDir)
		if config.transportAliasDir == "." || !filepath.IsAbs(config.transportAliasDir) {
			return resolvedConfig{}, errors.New("tor transport alias directory must be absolute")
		}
	}
	return config, nil
}

func discoverExecutable(name, explicit, environment, resourcesDir string) (string, error) {
	override := strings.TrimSpace(explicit)
	if override == "" {
		override = strings.TrimSpace(os.Getenv(environment))
	}
	if override != "" {
		return validateExecutable(override)
	}
	resourcesDir = strings.TrimSpace(resourcesDir)
	if resourcesDir != "" {
		candidates := []string{
			filepath.Join(resourcesDir, "bin", name),
			filepath.Join(resourcesDir, name),
			filepath.Join(resourcesDir, "tor", name),
		}
		for _, candidate := range candidates {
			if path, err := validateExecutable(candidate); err == nil {
				return path, nil
			}
		}
	}
	path, err := exec.LookPath(name)
	if err != nil && name == "tor" && runtime.GOOS != "windows" {
		path, err = exec.LookPath("/usr/sbin/tor")
	}
	if err != nil {
		return "", err
	}
	return validateExecutable(path)
}

func discoverLyrebird(explicit, resourcesDir string) (string, error) {
	override := strings.TrimSpace(explicit)
	if override == "" {
		override = strings.TrimSpace(os.Getenv("TELEGRAM_COMPANION_LYREBIRD_PATH"))
	}
	if override != "" {
		return validateExecutable(override)
	}
	resourcesDir = strings.TrimSpace(resourcesDir)
	if resourcesDir == "" {
		return "", errors.New("lyrebird unavailable")
	}
	for _, candidate := range []string{
		filepath.Join(resourcesDir, "tor", "pluggable_transports", "lyrebird"),
		filepath.Join(resourcesDir, "bin", "lyrebird"),
	} {
		if path, err := validateExecutable(candidate); err == nil {
			return path, nil
		}
	}
	return "", errors.New("lyrebird unavailable")
}

func validateExecutable(path string) (string, error) {
	if !strings.ContainsRune(path, filepath.Separator) {
		resolved, err := exec.LookPath(path)
		if err != nil {
			return "", err
		}
		path = resolved
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(absolute)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() || (runtime.GOOS != "windows" && info.Mode().Perm()&0o111 == 0) {
		return "", errors.New("path is not executable")
	}
	return absolute, nil
}

func prepareState(config resolvedConfig) error {
	for _, directory := range []string{config.stateDir, config.dataDir} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			return errors.New("create private Tor state")
		}
		if err := os.Chmod(directory, 0o700); err != nil {
			return errors.New("secure private Tor state")
		}
	}
	active := config.snowflakeFallback
	if len(config.bridgeCandidates) > 0 {
		active = config.bridgeCandidates[0]
	}
	contents, err := renderTorrc(config, active)
	if err != nil {
		if active.Transport == TransportObfs4 {
			return errors.New("lyrebird executable path is invalid for Tor configuration")
		}
		return errors.New("snowflake executable path is invalid for Tor configuration")
	}
	if err := writePrivateAtomic(config.torrcPath, []byte(contents), 0o600); err != nil {
		return errors.New("write private Tor configuration")
	}
	return resetProcessLogs(config)
}

func renderTorrc(config resolvedConfig, candidate BridgeCandidate) (string, error) {
	validated, err := validateBridgeLine(candidate.Canonical)
	if err != nil || validated.Transport != candidate.Transport || validated.Canonical != candidate.Canonical {
		return "", errors.New("bridge configuration is invalid")
	}
	quotedDataDir, err := quoteTorValue(config.dataDir)
	if err != nil {
		return "", errors.New("tor state path is invalid for configuration")
	}
	transportPath, err := transportPathForCandidate(config, candidate)
	if err != nil {
		return "", err
	}
	transportPath, err = prepareManagedTransportExecutable(transportPath, config.transportAliasDir)
	if err != nil {
		return "", errors.New("managed transport path is invalid for Tor configuration")
	}
	return strings.Join([]string{
		"SocksPort " + config.socksAddress + " IsolateSOCKSAuth",
		"DataDirectory " + quotedDataDir,
		"Log notice stdout",
		"UseBridges 1",
		"ClientTransportPlugin " + string(candidate.Transport) + " exec " + transportPath,
		"Bridge " + candidate.Canonical,
		"AvoidDiskWrites 1",
		"",
	}, "\n"), nil
}

func transportPathForCandidate(config resolvedConfig, candidate BridgeCandidate) (string, error) {
	if len(config.bridgeCandidates) == 0 && candidate.Transport == TransportSnowflake {
		if config.snowflakePath == "" {
			return "", errors.New("managed transport unavailable")
		}
		return config.snowflakePath, nil
	}
	switch candidate.Program {
	case ManagedPTLyrebird:
		if config.lyrebirdPath == "" {
			return "", errors.New("managed transport unavailable")
		}
		return config.lyrebirdPath, nil
	default:
		return "", errors.New("managed transport unavailable")
	}
}

func writePrivateAtomic(path string, contents []byte, mode os.FileMode) error {
	temporary, err := os.CreateTemp(filepath.Dir(path), ".torrc-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(mode); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(contents); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return err
	}
	return os.Chmod(path, mode)
}

func resetProcessLogs(config resolvedConfig) error {
	for _, path := range []string{config.bootstrapLogPath, config.outputPath} {
		if err := os.WriteFile(path, nil, 0o600); err != nil {
			return errors.New("reset private Tor log")
		}
		if err := os.Chmod(path, 0o600); err != nil {
			return errors.New("secure private Tor log")
		}
	}
	return nil
}

func prepareManagedTransportExecutable(path, aliasDir string) (string, error) {
	if strings.ContainsAny(path, "\x00\r\n") {
		return "", errors.New("managed transport path contains forbidden control characters")
	}
	if !strings.ContainsFunc(path, torTokenUnsafeRune) {
		return path, nil
	}

	if aliasDir == "" || strings.ContainsFunc(aliasDir, torTokenUnsafeRune) {
		return "", errors.New("managed transport alias directory is not Tor-safe")
	}
	if err := os.MkdirAll(filepath.Dir(aliasDir), 0o700); err != nil {
		return "", err
	}
	if err := os.Mkdir(aliasDir, 0o700); err != nil && !os.IsExist(err) {
		return "", err
	}
	info, err := os.Lstat(aliasDir)
	if err != nil {
		return "", err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return "", errors.New("managed transport alias path is not a private directory")
	}
	if err := os.Chmod(aliasDir, 0o700); err != nil {
		return "", err
	}

	sum := sha256.Sum256([]byte(path))
	aliasPath := filepath.Join(aliasDir, fmt.Sprintf("snowflake-client-%x", sum[:8]))
	if err := os.Symlink(path, aliasPath); err != nil {
		if !os.IsExist(err) {
			return "", err
		}
	}
	target, err := os.Readlink(aliasPath)
	if err != nil || target != path {
		return "", errors.New("managed transport alias conflicts with an existing file")
	}
	return aliasPath, nil
}

func torTokenUnsafeRune(value rune) bool {
	if value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z' || value >= '0' && value <= '9' {
		return false
	}
	switch value {
	case '/', '.', '_', '-', '+':
		return false
	case ':', '\\':
		return runtime.GOOS != "windows"
	default:
		return true
	}
}

func quoteTorValue(value string) (string, error) {
	if strings.ContainsAny(value, "\x00\r\n") {
		return "", errors.New("tor configuration value contains forbidden control characters")
	}
	replacer := strings.NewReplacer("\\", "\\\\", "\"", "\\\"")
	return fmt.Sprintf("\"%s\"", replacer.Replace(value)), nil
}
