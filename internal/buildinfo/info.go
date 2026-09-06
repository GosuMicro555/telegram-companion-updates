package buildinfo

import (
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"strings"
)

// ProductID is the stable product scope used by licenses and release metadata.
const ProductID = "telegram-companion"

const (
	ExpectedRevocationManifestURL = "https://raw.githubusercontent.com/GosuMicro555/telegram-companion-updates/main/revocations.tcrev"
	ExpectedRevocationKeyID       = "revocation-2026-01"
)

type Channel string

const (
	ChannelInternal         Channel = "internal"
	ChannelPublicMacOSARM64 Channel = "public-macos-arm64"
)

var bootstrapMode = "seeded"

type BootstrapMode string

const (
	BootstrapModeSeeded BootstrapMode = "seeded"
	BootstrapModeEmpty  BootstrapMode = "empty"
)

type Info struct {
	Channel               Channel
	ProductID             string
	Version               string
	LicensePublicKey      string
	AppcastURL            string
	RevocationManifestURL string
	RevocationKeyID       string
	RevocationPublicKey   string
	BootstrapMode         BootstrapMode
}

func (i Info) RequiresActivation() bool {
	return i.Channel == ChannelPublicMacOSARM64
}

func (i Info) UpdatesEnabled() bool {
	return i.Channel == ChannelPublicMacOSARM64
}

// Current returns the metadata selected at link time for this build.
func Current() (Info, error) {
	info, err := validate(Info{
		Channel:               compileTimeChannel,
		ProductID:             ProductID,
		Version:               version,
		LicensePublicKey:      licensePublicKey,
		AppcastURL:            appcastURL,
		RevocationManifestURL: revocationManifestURL,
		RevocationKeyID:       revocationKeyID,
		RevocationPublicKey:   revocationPublicKey,
		BootstrapMode:         BootstrapMode(bootstrapMode),
	})
	if err != nil {
		return Info{}, err
	}
	if err := validateLinkedRevocationMetadata(info, revocationBuildMetadata); err != nil {
		return Info{}, err
	}
	return info, nil
}

func validateLinkedRevocationMetadata(info Info, linked string) error {
	switch info.Channel {
	case ChannelInternal:
		if linked != "" {
			return errors.New("internal build must not link revocation metadata fingerprint")
		}
		return nil
	case ChannelPublicMacOSARM64:
		if linked == "" || linked != revocationMetadataFingerprint(info) {
			return errors.New("public build revocation metadata fingerprint is missing or invalid")
		}
		return nil
	default:
		return errors.New("unsupported build channel for revocation metadata fingerprint")
	}
}

func revocationMetadataFingerprint(info Info) string {
	return "TCREVBUILD1|" + info.RevocationManifestURL + "|" + info.RevocationKeyID + "|" + info.RevocationPublicKey
}

func validate(info Info) (Info, error) {
	if info.ProductID != ProductID {
		return Info{}, fmt.Errorf("unsupported product ID %q", info.ProductID)
	}
	if info.BootstrapMode == "" {
		info.BootstrapMode = BootstrapModeSeeded
	}

	switch info.Channel {
	case ChannelInternal:
		if info.BootstrapMode != BootstrapModeSeeded {
			return Info{}, fmt.Errorf("unsupported internal bootstrap mode %q", info.BootstrapMode)
		}
		if info.RevocationManifestURL != "" || info.RevocationKeyID != "" || info.RevocationPublicKey != "" {
			return Info{}, errors.New("internal build must not configure revocation metadata")
		}
		return info, nil
	case ChannelPublicMacOSARM64:
		info.Version = strings.TrimSpace(info.Version)
		if info.Version == "" {
			return Info{}, errors.New("public build version is required")
		}

		publicKey, err := canonicalPublicKey(info.LicensePublicKey)
		if err != nil {
			return Info{}, fmt.Errorf("invalid license public key: %w", err)
		}
		info.LicensePublicKey = publicKey

		appcast, err := canonicalAppcastURL(info.AppcastURL)
		if err != nil {
			return Info{}, fmt.Errorf("invalid appcast URL: %w", err)
		}
		info.AppcastURL = appcast

		if info.RevocationManifestURL != ExpectedRevocationManifestURL {
			return Info{}, errors.New("invalid revocation manifest URL")
		}
		if info.RevocationKeyID != ExpectedRevocationKeyID {
			return Info{}, errors.New("invalid revocation key ID")
		}
		revocationKey, err := canonicalPublicKey(info.RevocationPublicKey)
		if err != nil {
			return Info{}, fmt.Errorf("invalid revocation public key: %w", err)
		}
		info.RevocationPublicKey = revocationKey
		if info.BootstrapMode != BootstrapModeSeeded && info.BootstrapMode != BootstrapModeEmpty {
			return Info{}, fmt.Errorf("unsupported public bootstrap mode %q", info.BootstrapMode)
		}

		return info, nil
	default:
		return Info{}, fmt.Errorf("unsupported build channel %q", info.Channel)
	}
}

func canonicalPublicKey(value string) (string, error) {
	return canonicalBase64Key(value, ed25519.PublicKeySize, "Ed25519")
}

func canonicalBase64Key(value string, size int, algorithm string) (string, error) {
	if value == "" {
		return "", errors.New("key is required")
	}
	if value != strings.TrimSpace(value) {
		return "", errors.New("key must not contain leading or trailing whitespace")
	}

	decoded, err := base64.StdEncoding.DecodeString(value)
	if err != nil || len(decoded) != size {
		return "", fmt.Errorf("expected canonical base64-encoded %s public key (%d bytes)", algorithm, size)
	}
	canonical := base64.StdEncoding.EncodeToString(decoded)
	if value != canonical {
		return "", errors.New("key must use canonical standard base64 encoding")
	}
	allZero := true
	for _, value := range decoded {
		if value != 0 {
			allZero = false
			break
		}
	}
	if allZero {
		return "", errors.New("key must not be all-zero")
	}

	return canonical, nil
}

func canonicalAppcastURL(value string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil {
		return "", errors.New("URL is malformed")
	}
	if !strings.EqualFold(parsed.Scheme, "https") || parsed.Host == "" {
		return "", errors.New("URL must use HTTPS")
	}
	if parsed.User != nil {
		return "", errors.New("URL must not contain user info")
	}

	parsed.Scheme = strings.ToLower(parsed.Scheme)
	parsed.Host = strings.ToLower(parsed.Host)
	return parsed.String(), nil
}
