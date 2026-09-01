package buildinfo

import (
	"crypto/ed25519"
	"encoding/base64"
	"go/build/constraint"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAppleSiliconBuildConstraintsExcludeIOS(t *testing.T) {
	macOSTags := map[string]bool{
		"public_macos_arm64": true,
		"darwin":             true,
		"arm64":              true,
	}
	iOSTags := map[string]bool{
		"public_macos_arm64": true,
		"darwin":             true,
		"arm64":              true,
		"ios":                true,
	}

	tests := []struct {
		name string
		path string
		tags map[string]bool
		want bool
	}{
		{name: "public channel on macOS", path: "channel_public_macos_arm64.go", tags: macOSTags, want: true},
		{name: "public channel on iOS", path: "channel_public_macos_arm64.go", tags: iOSTags, want: false},
		{name: "unsupported guard on macOS", path: "channel_public_macos_arm64_unsupported.go", tags: macOSTags, want: false},
		{name: "unsupported guard on iOS", path: "channel_public_macos_arm64_unsupported.go", tags: iOSTags, want: true},
		{name: "machine ID implementation on macOS", path: filepath.Join("..", "license", "machineid_darwin.go"), tags: macOSTags, want: true},
		{name: "machine ID implementation on iOS", path: filepath.Join("..", "license", "machineid_darwin.go"), tags: iOSTags, want: false},
		{name: "machine ID stub on macOS", path: filepath.Join("..", "license", "machineid_stub.go"), tags: macOSTags, want: false},
		{name: "machine ID stub on iOS", path: filepath.Join("..", "license", "machineid_stub.go"), tags: iOSTags, want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := buildConstraintMatches(t, tt.path, tt.tags); got != tt.want {
				t.Fatalf("build constraint for %s matches = %t, want %t", tt.path, got, tt.want)
			}
		})
	}
}

func TestValidatePublicRequiresReleaseMetadata(t *testing.T) {
	_, err := validate(Info{Channel: ChannelPublicMacOSARM64, ProductID: ProductID})
	if err == nil {
		t.Fatal("expected public metadata validation error")
	}
}

func TestValidatePublicRequiresVersion(t *testing.T) {
	info := validPublicInfo()
	info.Version = ""

	if _, err := validate(info); err == nil {
		t.Fatal("expected missing version error")
	}
}

func TestValidatePublicRequiresValidEd25519PublicKey(t *testing.T) {
	info := validPublicInfo()
	info.LicensePublicKey = "not-a-public-key"

	if _, err := validate(info); err == nil {
		t.Fatal("expected invalid public key error")
	}
}

func TestValidatePublicRejectsAllZeroEd25519PublicKeys(t *testing.T) {
	zero := base64.StdEncoding.EncodeToString(make([]byte, ed25519.PublicKeySize))
	tests := []struct {
		name   string
		mutate func(*Info)
		want   string
	}{
		{
			name:   "license",
			mutate: func(info *Info) { info.LicensePublicKey = zero },
			want:   "invalid license public key: key must not be all-zero",
		},
		{
			name:   "revocation",
			mutate: func(info *Info) { info.RevocationPublicKey = zero },
			want:   "invalid revocation public key: key must not be all-zero",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info := validPublicInfo()
			tt.mutate(&info)
			if _, err := validate(info); err == nil || err.Error() != tt.want {
				t.Fatalf("all-zero Ed25519 error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestValidatePublicRequiresHTTPSAppcast(t *testing.T) {
	info := validPublicInfo()
	info.AppcastURL = "http://updates.example.test/appcast.xml"

	if _, err := validate(info); err == nil {
		t.Fatal("expected HTTPS appcast error")
	}
}

func TestValidatePublicRejectsAppcastCredentials(t *testing.T) {
	info := validPublicInfo()
	info.AppcastURL = "https://release:secret@updates.example.test/appcast.xml"

	if _, err := validate(info); err == nil {
		t.Fatal("expected appcast credentials to be rejected")
	}
}

func TestValidatePublicRejectsNonCanonicalPublicKey(t *testing.T) {
	info := validPublicInfo()
	decoded, err := base64.StdEncoding.DecodeString(info.LicensePublicKey)
	if err != nil {
		t.Fatalf("decode test public key: %v", err)
	}

	for _, value := range []string{
		" " + info.LicensePublicKey,
		base64.RawStdEncoding.EncodeToString(decoded),
	} {
		info.LicensePublicKey = value
		if _, err := validate(info); err == nil {
			t.Fatalf("expected noncanonical public key %q to be rejected", value)
		}
	}
}

func TestValidatePublicCanonicalizesMetadata(t *testing.T) {
	info := validPublicInfo()
	info.Version = "\t0.7.0 \n"
	info.AppcastURL = " https://updates.example.test/appcast.xml\t"

	got, err := validate(info)
	if err != nil {
		t.Fatalf("validate public metadata: %v", err)
	}
	if got.Version != "0.7.0" {
		t.Fatalf("Version = %q, want %q", got.Version, "0.7.0")
	}
	if got.LicensePublicKey != validPublicInfo().LicensePublicKey {
		t.Fatalf("LicensePublicKey = %q, want canonical base64", got.LicensePublicKey)
	}
	if got.AppcastURL != "https://updates.example.test/appcast.xml" {
		t.Fatalf("AppcastURL = %q, want trimmed URL", got.AppcastURL)
	}
}

func TestValidateAcceptsCompletePublicMetadata(t *testing.T) {
	got, err := validate(validPublicInfo())
	if err != nil {
		t.Fatalf("validate complete public metadata: %v", err)
	}
	if !got.RequiresActivation() || !got.UpdatesEnabled() {
		t.Fatalf("public metadata did not enable public capabilities: %#v", got)
	}
}

func TestValidateInternalDoesNotEnablePublicCapabilities(t *testing.T) {
	got, err := validate(Info{Channel: ChannelInternal, ProductID: ProductID, Version: "0.7.0"})
	if err != nil || got.RequiresActivation() || got.UpdatesEnabled() {
		t.Fatalf("unexpected metadata: %#v %v", got, err)
	}
}

func TestChannelConstantsAreDistinct(t *testing.T) {
	if ChannelInternal == ChannelPublicMacOSARM64 {
		t.Fatal("channel constants must be distinct")
	}
}

func validPublicInfo() Info {
	publicKey, _ := ed25519.NewKeyFromSeed(make([]byte, ed25519.SeedSize)).Public().(ed25519.PublicKey)
	return Info{
		Channel:               ChannelPublicMacOSARM64,
		ProductID:             ProductID,
		Version:               "0.7.0",
		LicensePublicKey:      base64.StdEncoding.EncodeToString(publicKey),
		AppcastURL:            "https://updates.example.test/appcast.xml",
		RevocationManifestURL: ExpectedRevocationManifestURL,
		RevocationKeyID:       ExpectedRevocationKeyID,
		RevocationPublicKey:   base64.StdEncoding.EncodeToString(publicKey),
	}
}

func buildConstraintMatches(t *testing.T, path string, tags map[string]bool) bool {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	line, _, ok := strings.Cut(string(contents), "\n")
	if !ok {
		t.Fatalf("%s has no build constraint line", path)
	}
	expression, err := constraint.Parse(strings.TrimSuffix(line, "\r"))
	if err != nil {
		t.Fatalf("parse build constraint in %s: %v", path, err)
	}
	return expression.Eval(func(tag string) bool { return tags[tag] })
}
