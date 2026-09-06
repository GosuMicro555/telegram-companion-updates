package macosarm64_test

import (
	"encoding/base64"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestPrepareLocalEnvUsesOnlyPersistedPublicKeys(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("release environment permissions are verified on macOS")
	}

	script := filepath.Join(filepath.Dir(scriptPath("lib.sh")), "prepare-local-env.sh")
	keyRoot := t.TempDir()
	keys := map[string]byte{
		"license-public-key.b64":        1,
		"sparkle-ed25519-public.b64":    2,
		"revocation-ed25519-public.b64": 3,
	}
	for name, fill := range keys {
		material := make([]byte, 32)
		for index := range material {
			material[index] = fill
		}
		encoded := base64.StdEncoding.EncodeToString(material)
		if err := os.WriteFile(filepath.Join(keyRoot, name), []byte(encoded+"\n"), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	output := filepath.Join(t.TempDir(), "release.env")
	cmd := exec.Command("bash", script, output)
	cmd.Env = append(os.Environ(), "KEY_ROOT="+keyRoot)
	combined, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("prepare release environment: %v\n%s", err, combined)
	}

	contents, err := os.ReadFile(output)
	if err != nil {
		t.Fatalf("read release environment: %v", err)
	}
	text := string(contents)
	for _, required := range []string{
		"LICENSE_PUBLIC_KEY=",
		"SPARKLE_PUBLIC_ED_KEY=",
		"APPCAST_URL=",
		"REVOCATION_MANIFEST_URL=",
		"REVOCATION_KEY_ID=",
		"REVOCATION_PUBLIC_KEY=",
	} {
		if strings.Count(text, required) != 1 {
			t.Errorf("release environment does not contain %q", required)
		}
	}
	if lines := strings.Split(strings.TrimSpace(text), "\n"); len(lines) != 6 {
		t.Fatalf("release environment has %d variables, want exactly 6", len(lines))
	}
	revocationMaterial := make([]byte, 32)
	for index := range revocationMaterial {
		revocationMaterial[index] = 3
	}
	for _, exact := range []string{
		"REVOCATION_MANIFEST_URL=https://raw.githubusercontent.com/GosuMicro555/telegram-companion-updates/main/revocations.tcrev\n",
		"REVOCATION_KEY_ID=revocation-2026-01\n",
		"REVOCATION_PUBLIC_KEY=" + base64.StdEncoding.EncodeToString(revocationMaterial) + "\n",
	} {
		if !strings.Contains(text, exact) {
			t.Errorf("release environment does not contain exact public value %q", exact)
		}
	}
	info, err := os.Stat(output)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("release environment mode = %o, want 600", got)
	}

	source, err := os.ReadFile(script)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"security ", "openssl rand"} {
		if strings.Contains(string(source), forbidden) {
			t.Errorf("prepare-local-env.sh contains forbidden private-key operation %q", forbidden)
		}
	}
}

func TestPrepareLocalEnvRejectsUnsafeRevocationPublicKey(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("release environment permissions are verified on macOS")
	}

	script := filepath.Join(filepath.Dir(scriptPath("lib.sh")), "prepare-local-env.sh")
	canonical := func(fill byte) string {
		material := make([]byte, 32)
		for index := range material {
			material[index] = fill
		}
		return base64.StdEncoding.EncodeToString(material)
	}
	run := func(t *testing.T, keys map[string]string) error {
		t.Helper()
		keyRoot := t.TempDir()
		for name, encoded := range keys {
			if err := os.WriteFile(filepath.Join(keyRoot, name), []byte(encoded+"\n"), 0o600); err != nil {
				t.Fatalf("write %s: %v", name, err)
			}
		}
		cmd := exec.Command("bash", script, filepath.Join(t.TempDir(), "release.env"))
		cmd.Env = append(os.Environ(), "KEY_ROOT="+keyRoot)
		return cmd.Run()
	}

	license := canonical(1)
	sparkle := canonical(2)
	revocation := canonical(3)
	for name, keys := range map[string]map[string]string{
		"missing": {
			"license-public-key.b64":     license,
			"sparkle-ed25519-public.b64": sparkle,
		},
		"noncanonical": {
			"license-public-key.b64":        license,
			"sparkle-ed25519-public.b64":    sparkle,
			"revocation-ed25519-public.b64": strings.TrimRight(revocation, "="),
		},
		"malformed": {
			"license-public-key.b64":        license,
			"sparkle-ed25519-public.b64":    sparkle,
			"revocation-ed25519-public.b64": "not-base64",
		},
		"revocation duplicates license key": {
			"license-public-key.b64":        license,
			"sparkle-ed25519-public.b64":    sparkle,
			"revocation-ed25519-public.b64": license,
		},
		"revocation duplicates Sparkle key": {
			"license-public-key.b64":        license,
			"sparkle-ed25519-public.b64":    sparkle,
			"revocation-ed25519-public.b64": sparkle,
		},
		"license duplicates Sparkle key": {
			"license-public-key.b64":        license,
			"sparkle-ed25519-public.b64":    license,
			"revocation-ed25519-public.b64": revocation,
		},
	} {
		t.Run(name, func(t *testing.T) {
			if err := run(t, keys); err == nil {
				t.Fatal("prepare-local-env.sh accepted an unsafe revocation public key")
			}
		})
	}
}
