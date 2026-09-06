package tor

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
	"unicode"
)

type Transport string

const (
	TransportObfs4     Transport = "obfs4"
	TransportSnowflake Transport = "snowflake"
)

type ManagedPT string

const ManagedPTLyrebird ManagedPT = "lyrebird"

const (
	reviewedLyrebirdMapping  = "ClientTransportPlugin meek_lite,obfs2,obfs3,obfs4,scramblesuit,webtunnel exec ${pt_path}lyrebird"
	reviewedSnowflakeMapping = "ClientTransportPlugin snowflake exec ${pt_path}lyrebird"
)

type BridgeCandidate struct {
	Transport Transport
	Program   ManagedPT
	Canonical string
	ID        string
}

var errBridgeConfigurationInvalid = errors.New("bridge configuration is invalid")

const maxBridgeCandidates = 9

type bridgeBundle struct {
	RecommendedDefault  string `json:"recommendedDefault"`
	PluggableTransports struct {
		Conjure   string `json:"conjure"`
		Lyrebird  string `json:"lyrebird"`
		Snowflake string `json:"snowflake"`
	} `json:"pluggableTransports"`
	Bridges struct {
		Obfs4     []string `json:"obfs4"`
		Snowflake []string `json:"snowflake"`
		Meek      []string `json:"meek"`
	} `json:"bridges"`
}

func LoadBridgeBundle(path string) ([]BridgeCandidate, error) {
	contents, err := os.ReadFile(path)
	if err != nil {
		return nil, errBridgeConfigurationInvalid
	}

	var bundle bridgeBundle
	decoder := json.NewDecoder(bytes.NewReader(contents))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&bundle); err != nil {
		return nil, errBridgeConfigurationInvalid
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return nil, errBridgeConfigurationInvalid
	}
	programs, err := reviewedTransportPrograms(bundle.PluggableTransports)
	if err != nil {
		return nil, errBridgeConfigurationInvalid
	}

	candidates := make([]BridgeCandidate, 0, maxBridgeCandidates)
	seen := map[string]struct{}{}
	appendLine := func(line string) error {
		candidate, err := validateBridgeLineWithPrograms(line, programs)
		if err != nil {
			return err
		}
		if _, ok := seen[candidate.Canonical]; ok {
			return nil
		}
		if len(candidates) == maxBridgeCandidates {
			return nil
		}
		seen[candidate.Canonical] = struct{}{}
		candidates = append(candidates, candidate)
		return nil
	}

	// Retain at most nine candidates only after validating every supplied entry.
	for _, line := range bundle.Bridges.Obfs4 {
		if err := appendLine(line); err != nil {
			return nil, errBridgeConfigurationInvalid
		}
	}
	for _, line := range bundle.Bridges.Snowflake {
		if err := appendLine(line); err != nil {
			return nil, errBridgeConfigurationInvalid
		}
	}
	if len(candidates) == 0 {
		return nil, errBridgeConfigurationInvalid
	}
	return candidates, nil
}

func reviewedTransportPrograms(transports struct {
	Conjure   string `json:"conjure"`
	Lyrebird  string `json:"lyrebird"`
	Snowflake string `json:"snowflake"`
}) (map[Transport]ManagedPT, error) {
	if transports.Lyrebird != reviewedLyrebirdMapping || transports.Snowflake != reviewedSnowflakeMapping {
		return nil, errBridgeConfigurationInvalid
	}
	return map[Transport]ManagedPT{
		TransportObfs4:     ManagedPTLyrebird,
		TransportSnowflake: ManagedPTLyrebird,
	}, nil
}

func validateBridgeLine(line string) (BridgeCandidate, error) {
	return validateBridgeLineWithPrograms(line, map[Transport]ManagedPT{
		TransportObfs4:     ManagedPTLyrebird,
		TransportSnowflake: ManagedPTLyrebird,
	})
}

func validateBridgeLineWithPrograms(line string, programs map[Transport]ManagedPT) (BridgeCandidate, error) {
	if strings.ContainsAny(line, "\x00\r\n") || strings.IndexFunc(line, unicode.IsControl) >= 0 {
		return BridgeCandidate{}, errBridgeConfigurationInvalid
	}
	canonical := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "Bridge "))
	fields := strings.Fields(canonical)
	if len(fields) == 0 {
		return BridgeCandidate{}, errBridgeConfigurationInvalid
	}

	transport := Transport(fields[0])
	program, ok := programs[transport]
	if !ok {
		return BridgeCandidate{}, errBridgeConfigurationInvalid
	}

	switch transport {
	case TransportObfs4:
		return validateObfs4Bridge(fields, canonical, program)
	case TransportSnowflake:
		return validateSnowflakeBridge(fields, canonical, program)
	default:
		return BridgeCandidate{}, errBridgeConfigurationInvalid
	}
}

func validateObfs4Bridge(fields []string, canonical string, program ManagedPT) (BridgeCandidate, error) {
	if len(fields) != 5 {
		return BridgeCandidate{}, errBridgeConfigurationInvalid
	}
	if !validFingerprint(fields[2]) || !strings.HasPrefix(fields[3], "cert=") || !validIATMode(fields[4]) {
		return BridgeCandidate{}, errBridgeConfigurationInvalid
	}
	if !validBase64Value(strings.TrimPrefix(fields[3], "cert=")) {
		return BridgeCandidate{}, errBridgeConfigurationInvalid
	}
	if err := validatePublicEndpoint(fields[1]); err != nil {
		return BridgeCandidate{}, errBridgeConfigurationInvalid
	}
	return BridgeCandidate{
		Transport: TransportObfs4,
		Program:   program,
		Canonical: canonical,
		ID:        bridgeID(canonical),
	}, nil
}

func validateSnowflakeBridge(fields []string, canonical string, program ManagedPT) (BridgeCandidate, error) {
	if len(fields) < 7 || len(fields) > 8 {
		return BridgeCandidate{}, errBridgeConfigurationInvalid
	}
	if !validFingerprint(fields[2]) {
		return BridgeCandidate{}, errBridgeConfigurationInvalid
	}
	if err := validateSnowflakeEndpoint(fields[1]); err != nil {
		return BridgeCandidate{}, errBridgeConfigurationInvalid
	}

	seen := map[string]bool{}
	frontSeen := false
	for _, field := range fields[3:] {
		switch {
		case strings.HasPrefix(field, "fingerprint="):
			fingerprint := strings.TrimPrefix(field, "fingerprint=")
			if seen["fingerprint"] || !validFingerprint(fingerprint) || !strings.EqualFold(fingerprint, fields[2]) {
				return BridgeCandidate{}, errBridgeConfigurationInvalid
			}
			seen["fingerprint"] = true
		case strings.HasPrefix(field, "url="):
			if seen["url"] || strings.TrimPrefix(field, "url=") == "" {
				return BridgeCandidate{}, errBridgeConfigurationInvalid
			}
			seen["url"] = true
		case strings.HasPrefix(field, "front=") || strings.HasPrefix(field, "fronts="):
			front := field
			if strings.HasPrefix(field, "front=") {
				front = strings.TrimPrefix(field, "front=")
			} else {
				front = strings.TrimPrefix(field, "fronts=")
			}
			if frontSeen || front == "" {
				return BridgeCandidate{}, errBridgeConfigurationInvalid
			}
			frontSeen = true
		case strings.HasPrefix(field, "ice="):
			if seen["ice"] || strings.TrimPrefix(field, "ice=") == "" {
				return BridgeCandidate{}, errBridgeConfigurationInvalid
			}
			seen["ice"] = true
		case strings.HasPrefix(field, "utls-imitate="):
			if seen["utls-imitate"] || strings.TrimPrefix(field, "utls-imitate=") == "" {
				return BridgeCandidate{}, errBridgeConfigurationInvalid
			}
			seen["utls-imitate"] = true
		default:
			return BridgeCandidate{}, errBridgeConfigurationInvalid
		}
	}
	if !seen["fingerprint"] || !seen["url"] || !frontSeen || !seen["ice"] {
		return BridgeCandidate{}, errBridgeConfigurationInvalid
	}
	return BridgeCandidate{
		Transport: TransportSnowflake,
		Program:   program,
		Canonical: canonical,
		ID:        bridgeID(canonical),
	}, nil
}

func validatePublicEndpoint(value string) error {
	host, port, err := net.SplitHostPort(value)
	if err != nil {
		return errBridgeConfigurationInvalid
	}
	if err := validateEndpointPort(port); err != nil {
		return errBridgeConfigurationInvalid
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return errBridgeConfigurationInvalid
	}
	if ip.IsLoopback() || ip.IsUnspecified() || ip.IsPrivate() || ip.IsMulticast() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
		return errBridgeConfigurationInvalid
	}
	return nil
}

func validateSnowflakeEndpoint(value string) error {
	host, port, err := net.SplitHostPort(value)
	if err != nil {
		return errBridgeConfigurationInvalid
	}
	if err := validateEndpointPort(port); err != nil {
		return errBridgeConfigurationInvalid
	}
	ip := net.ParseIP(host)
	ip4 := ip.To4()
	if ip4 == nil || ip4[0] != 192 || ip4[1] != 0 || ip4[2] != 2 {
		return errBridgeConfigurationInvalid
	}
	return nil
}

func validateEndpointPort(port string) error {
	if port == "" {
		return errBridgeConfigurationInvalid
	}
	for _, r := range port {
		if r < '0' || r > '9' {
			return errBridgeConfigurationInvalid
		}
	}
	parsed, err := strconv.Atoi(port)
	if err != nil || parsed < 1 || parsed > 65535 {
		return errBridgeConfigurationInvalid
	}
	return nil
}

func validFingerprint(value string) bool {
	if len(value) != 40 {
		return false
	}
	for _, r := range value {
		if !strings.ContainsRune("0123456789abcdefABCDEF", r) {
			return false
		}
	}
	return true
}

func validIATMode(value string) bool {
	switch value {
	case "iat-mode=0", "iat-mode=1", "iat-mode=2":
		return true
	default:
		return false
	}
}

func validBase64Value(value string) bool {
	if value == "" || len(value) > 4096 {
		return false
	}
	if strings.IndexFunc(value, unicode.IsSpace) >= 0 {
		return false
	}
	if strings.Contains(value, "=") {
		decoded, err := base64.StdEncoding.DecodeString(value)
		return err == nil && len(decoded) > 0 && base64.StdEncoding.EncodeToString(decoded) == value
	}
	decoded, err := base64.RawStdEncoding.DecodeString(value)
	return err == nil && len(decoded) > 0 && base64.RawStdEncoding.EncodeToString(decoded) == value
}

func bridgeID(canonical string) string {
	sum := sha256.Sum256([]byte(canonical))
	return hex.EncodeToString(sum[:])
}
