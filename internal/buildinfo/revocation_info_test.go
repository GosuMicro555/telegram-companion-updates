package buildinfo

import (
	"crypto/ed25519"
	"encoding/base64"
	"strings"
	"testing"
)

const expectedRevocationManifestURL = "https://raw.githubusercontent.com/GosuMicro555/telegram-companion-updates/main/revocations.tcrev"

func TestValidatePublicRequiresExactCompleteRevocationMetadata(t *testing.T) {
	valid := validPublicRevocationInfoForTest()
	got, err := validate(valid)
	if err != nil {
		t.Fatalf("validate complete revocation metadata: %v", err)
	}
	if got.RevocationManifestURL != expectedRevocationManifestURL || got.RevocationKeyID != "revocation-2026-01" || got.RevocationPublicKey != valid.RevocationPublicKey {
		t.Fatalf("revocation metadata changed: %#v", got)
	}

	tests := []struct {
		name   string
		mutate func(*Info)
	}{
		{name: "missing URL", mutate: func(info *Info) { info.RevocationManifestURL = "" }},
		{name: "missing key ID", mutate: func(info *Info) { info.RevocationKeyID = "" }},
		{name: "missing public key", mutate: func(info *Info) { info.RevocationPublicKey = "" }},
		{name: "alternate host", mutate: func(info *Info) { info.RevocationManifestURL = "https://example.com/revocations.tcrev" }},
		{name: "alternate repository", mutate: func(info *Info) {
			info.RevocationManifestURL = "https://raw.githubusercontent.com/other/repo/main/revocations.tcrev"
		}},
		{name: "alternate path", mutate: func(info *Info) {
			info.RevocationManifestURL = strings.TrimSuffix(expectedRevocationManifestURL, "revocations.tcrev") + "other.tcrev"
		}},
		{name: "HTTP", mutate: func(info *Info) {
			info.RevocationManifestURL = strings.Replace(expectedRevocationManifestURL, "https://", "http://", 1)
		}},
		{name: "user info", mutate: func(info *Info) {
			info.RevocationManifestURL = strings.Replace(expectedRevocationManifestURL, "https://", "https://user@", 1)
		}},
		{name: "query", mutate: func(info *Info) { info.RevocationManifestURL += "?x=1" }},
		{name: "fragment", mutate: func(info *Info) { info.RevocationManifestURL += "#x" }},
		{name: "wrong key ID", mutate: func(info *Info) { info.RevocationKeyID = "revocation-other" }},
		{name: "noncanonical public key", mutate: func(info *Info) { info.RevocationPublicKey = strings.TrimSuffix(info.RevocationPublicKey, "=") }},
		{name: "short public key", mutate: func(info *Info) {
			info.RevocationPublicKey = base64.StdEncoding.EncodeToString(make([]byte, ed25519.PublicKeySize-1))
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			info := valid
			test.mutate(&info)
			if _, err := validate(info); err == nil {
				t.Fatal("invalid revocation metadata accepted")
			}
		})
	}
}

func TestValidateInternalRejectsRevocationMetadata(t *testing.T) {
	valid := validPublicRevocationInfoForTest()
	for name, mutate := range map[string]func(*Info){
		"URL":        func(info *Info) { info.RevocationManifestURL = valid.RevocationManifestURL },
		"key ID":     func(info *Info) { info.RevocationKeyID = valid.RevocationKeyID },
		"public key": func(info *Info) { info.RevocationPublicKey = valid.RevocationPublicKey },
	} {
		t.Run(name, func(t *testing.T) {
			info := Info{Channel: ChannelInternal, ProductID: ProductID, Version: "dev"}
			mutate(&info)
			if _, err := validate(info); err == nil {
				t.Fatal("internal revocation metadata accepted")
			}
		})
	}
}

func TestValidateLinkedRevocationMetadataRequiresExactComposite(t *testing.T) {
	info := validPublicRevocationInfoForTest()
	want := revocationMetadataFingerprint(info)
	if want == "" {
		t.Fatal("fingerprint is empty")
	}
	for name, linked := range map[string]string{
		"missing": "",
		"partial": info.RevocationPublicKey,
		"wrong":   want + "-changed",
	} {
		t.Run(name, func(t *testing.T) {
			if err := validateLinkedRevocationMetadata(info, linked); err == nil {
				t.Fatal("invalid linker fingerprint accepted")
			}
		})
	}
	if err := validateLinkedRevocationMetadata(info, want); err != nil {
		t.Fatalf("exact linker fingerprint rejected: %v", err)
	}
	internal := Info{Channel: ChannelInternal, ProductID: ProductID, Version: "dev"}
	if err := validateLinkedRevocationMetadata(internal, ""); err != nil {
		t.Fatalf("empty internal fingerprint rejected: %v", err)
	}
	if err := validateLinkedRevocationMetadata(internal, want); err == nil {
		t.Fatal("internal linker fingerprint accepted")
	}
}

func validPublicRevocationInfoForTest() Info {
	info := validPublicInfo()
	key := make([]byte, ed25519.PublicKeySize)
	for index := range key {
		key[index] = byte(index + 1)
	}
	info.RevocationManifestURL = expectedRevocationManifestURL
	info.RevocationKeyID = "revocation-2026-01"
	info.RevocationPublicKey = base64.StdEncoding.EncodeToString(key)
	return info
}
