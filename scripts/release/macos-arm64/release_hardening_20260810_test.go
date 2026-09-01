package macosarm64_test

import (
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestReleaseHardening20260810PreflightsRequireFileClassifier(t *testing.T) {
	for _, name := range []string{"preflight-private.sh", "preflight-public.sh"} {
		source, err := os.ReadFile(scriptPath(name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if !strings.Contains(string(source), "require_command file") {
			t.Errorf("%s does not explicitly require the file command", name)
		}
	}
}

func TestReleaseHardening20260810CleanPublicBundleFailsClosedOnStateArtifacts(t *testing.T) {
	releaseHardening20260810RequireDarwin(t)

	cases := []struct {
		name      string
		relative  string
		directory bool
	}{
		{name: "state_tcs", relative: "state.tcs"},
		{name: "bootstrap", relative: "bootstrap-state", directory: true},
		{name: "license", relative: "license.tcomplicense"},
		{name: "app_db", relative: "app.db"},
		{name: "session", relative: "sessions", directory: true},
		{name: "tdata", relative: "tdata", directory: true},
	}

	cleanApp := releaseHardening20260810AppBundle(t)
	if output, err := releaseHardening20260810RunVerifierFunction(t, cleanApp, "verify_clean_public_bundle"); err != nil {
		t.Fatalf("clean public bundle was rejected: %v\n%s", err, output)
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			app := releaseHardening20260810AppBundle(t)
			path := filepath.Join(app, "Contents", "Resources", testCase.relative)
			if testCase.directory {
				if err := os.MkdirAll(path, 0o755); err != nil {
					t.Fatal(err)
				}
			} else if err := os.WriteFile(path, []byte("state"), 0o644); err != nil {
				t.Fatal(err)
			}

			output, err := releaseHardening20260810RunVerifierFunction(t, app, "verify_clean_public_bundle")
			if err == nil {
				t.Fatalf("state-bearing path %q was accepted", testCase.relative)
			}
			if !strings.Contains(output, "clean public bundle contains forbidden state") {
				t.Fatalf("unexpected rejection for %q: %v\n%s", testCase.relative, err, output)
			}
		})
	}
}

func TestReleaseHardening20260826CleanUpdateArchiveRejectsSeedMaterial(t *testing.T) {
	releaseHardening20260810RequireDarwin(t)

	newArchive := func(t *testing.T, relative string, content []byte) string {
		t.Helper()
		app := releaseHardening20260810AppBundle(t)
		if relative != "" {
			path := filepath.Join(app, "Contents", "Resources", relative)
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, content, 0o644); err != nil {
				t.Fatal(err)
			}
		}
		archive := filepath.Join(t.TempDir(), "update.zip")
		command := exec.Command("ditto", "-c", "-k", "--sequesterRsrc", "--keepParent", app, archive)
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("create update archive: %v\n%s", err, output)
		}
		return archive
	}

	clean := newArchive(t, "readme.txt", []byte("clean"))
	seededGlobal := releaseHardening20260810AppBundle(t)
	seedPath := filepath.Join(seededGlobal, "Contents", "Resources", "bootstrap-state", "state.tcs")
	if err := os.MkdirAll(filepath.Dir(seedPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(seedPath, []byte("TCSEED2\nprivate"), 0o644); err != nil {
		t.Fatal(err)
	}
	if output, err := releaseHardening20260826RunArchiveVerifier(t, clean, seededGlobal); err != nil {
		t.Fatalf("clean update archive was rejected: %v\n%s", err, output)
	}

	for _, testCase := range []struct {
		name     string
		relative string
		content  []byte
	}{
		{name: "canonical_seed", relative: "bootstrap-state/state.tcs", content: []byte("TCSEED2\nseed")},
		{name: "renamed_envelope", relative: "payload.bin", content: []byte("TCSEED2\nseed")},
		{name: "database", relative: "app.db", content: []byte("state")},
		{name: "session", relative: "sessions/value", content: []byte("state")},
		{name: "tdata", relative: "tdata/value", content: []byte("state")},
		{name: "license", relative: "license.tcomplicense", content: []byte("state")},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			archive := newArchive(t, testCase.relative, testCase.content)
			if output, err := releaseHardening20260826RunArchiveVerifier(t, archive, seededGlobal); err == nil {
				t.Fatalf("unsafe update archive was accepted\n%s", output)
			}
		})
	}
}

func TestReleaseHardening20260814SeededPublicBundleAcceptsOnlyCanonicalSeed(t *testing.T) {
	releaseHardening20260810RequireDarwin(t)

	seed := []byte("TCSEED2\ncontract-test-envelope")
	digest := sha256.Sum256(seed)
	t.Setenv("PUBLIC_BOOTSTRAP_BUNDLE_SHA256", fmt.Sprintf("%x", digest))

	newSeededApp := func(t *testing.T) string {
		t.Helper()
		app := releaseHardening20260810AppBundle(t)
		seedPath := filepath.Join(app, "Contents", "Resources", "bootstrap-state", "state.tcs")
		if err := os.MkdirAll(filepath.Dir(seedPath), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(seedPath, seed, 0o644); err != nil {
			t.Fatal(err)
		}
		return app
	}

	validApp := newSeededApp(t)
	if output, err := releaseHardening20260810RunVerifierFunction(t, validApp, "verify_seeded_public_bundle"); err != nil {
		t.Fatalf("canonical seeded public bundle was rejected: %v\n%s", err, output)
	}

	t.Run("wrong_hash", func(t *testing.T) {
		app := newSeededApp(t)
		t.Setenv("PUBLIC_BOOTSTRAP_BUNDLE_SHA256", strings.Repeat("0", 64))
		if output, err := releaseHardening20260810RunVerifierFunction(t, app, "verify_seeded_public_bundle"); err == nil {
			t.Fatalf("seeded public bundle with the wrong digest was accepted\n%s", output)
		}
	})

	t.Run("seed_symlink", func(t *testing.T) {
		app := releaseHardening20260810AppBundle(t)
		seedDirectory := filepath.Join(app, "Contents", "Resources", "bootstrap-state")
		if err := os.MkdirAll(seedDirectory, 0o755); err != nil {
			t.Fatal(err)
		}
		target := filepath.Join(t.TempDir(), "state.tcs")
		if err := os.WriteFile(target, seed, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, filepath.Join(seedDirectory, "state.tcs")); err != nil {
			t.Fatal(err)
		}
		if output, err := releaseHardening20260810RunVerifierFunction(t, app, "verify_seeded_public_bundle"); err == nil {
			t.Fatalf("symlinked public bootstrap seed was accepted\n%s", output)
		}
	})

	for _, testCase := range []struct {
		name     string
		relative string
	}{
		{name: "bootstrap_sibling", relative: "Resources/bootstrap-state/extra.bin"},
		{name: "other_bootstrap", relative: "Resources/legacy-bootstrap/state.tcs"},
		{name: "out_of_place_state", relative: "Resources/state.tcs"},
		{name: "license", relative: "Resources/license.tcomplicense"},
		{name: "database", relative: "Resources/app.db"},
		{name: "session", relative: "Resources/session-data/value"},
		{name: "tdata", relative: "Resources/tdata/value"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			app := newSeededApp(t)
			path := filepath.Join(app, "Contents", testCase.relative)
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte("forbidden"), 0o644); err != nil {
				t.Fatal(err)
			}

			output, err := releaseHardening20260810RunVerifierFunction(t, app, "verify_seeded_public_bundle")
			if err == nil {
				t.Fatalf("forbidden seeded bundle path %q was accepted", testCase.relative)
			}
			if !strings.Contains(output, "seeded public bundle contains forbidden state") {
				t.Fatalf("unexpected rejection for %q: %v\n%s", testCase.relative, err, output)
			}
		})
	}
}

func TestReleaseHardening20260810PortableBundleModesFailClosed(t *testing.T) {
	releaseHardening20260810RequireDarwin(t)

	for _, testCase := range []struct {
		name   string
		target string
		mode   os.FileMode
	}{
		{name: "private_regular_file", target: "readme.txt", mode: 0o600},
		{name: "private_directory", target: "Resources", mode: 0o700},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			app := releaseHardening20260810AppBundle(t)
			readme := filepath.Join(app, "Contents", "Resources", "readme.txt")
			if err := os.WriteFile(readme, []byte("portable"), 0o644); err != nil {
				t.Fatal(err)
			}
			if output, err := releaseHardening20260810RunVerifierFunction(t, app, "verify_portable_bundle_modes"); err != nil {
				t.Fatalf("portable baseline was rejected: %v\n%s", err, output)
			}

			target := filepath.Join(app, "Contents", testCase.target)
			if testCase.target == "readme.txt" {
				target = readme
			}
			if err := os.Chmod(target, testCase.mode); err != nil {
				t.Fatal(err)
			}
			output, err := releaseHardening20260810RunVerifierFunction(t, app, "verify_portable_bundle_modes")
			if err == nil {
				t.Fatalf("non-portable mode %o was accepted for %s", testCase.mode, target)
			}
			if !strings.Contains(output, "non-portable bundle mode") {
				t.Fatalf("unexpected mode rejection: %v\n%s", err, output)
			}
		})
	}
}

func TestReleaseHardening20260810EmbeddedTransportVersionCommandsExecute(t *testing.T) {
	releaseHardening20260810RequireDarwin(t)
	app := releaseHardening20260810AppBundle(t)

	tor := filepath.Join(app, "Contents", "Resources", "tor", "tor")
	snowflake := filepath.Join(app, "Contents", "Resources", "bin", "snowflake-client")
	releaseHardening20260810WriteExecutable(t, tor, "#!/bin/sh\n[ \"$1\" = \"--version\" ] || exit 9\nprintf 'Tor version 0.4.9.11 test\\n'\n")
	releaseHardening20260810WriteExecutable(t, snowflake, "#!/bin/sh\n[ \"$1\" = \"-version\" ] || exit 9\nprintf 'snowflake-client 2.14.1\\n'\n")

	if output, err := releaseHardening20260810RunVerifierFunction(t, app, "verify_embedded_transport_versions"); err != nil {
		t.Fatalf("embedded transport version checks failed: %v\n%s", err, output)
	}
}

func releaseHardening20260810RequireDarwin(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "darwin" {
		t.Skip("macOS release shell behavior is verified on Darwin")
	}
}

func releaseHardening20260810AppBundle(t *testing.T) string {
	t.Helper()
	app := filepath.Join(t.TempDir(), "Telegram Companion.app")
	for _, path := range []string{
		app,
		filepath.Join(app, "Contents"),
		filepath.Join(app, "Contents", "Resources"),
	} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return app
}

func releaseHardening20260810WriteExecutable(t *testing.T, path, source string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(source), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o755); err != nil {
		t.Fatal(err)
	}
}

func releaseHardening20260810RunVerifierFunction(t *testing.T, app, function string) (string, error) {
	t.Helper()
	command := exec.Command("bash", "-c", "source \"$VERIFY_PRIVATE_SCRIPT\"; \"$VERIFY_FUNCTION\"")
	command.Env = append(os.Environ(),
		"APP_PATH="+app,
		"VERIFY_PRIVATE_SCRIPT="+scriptPath("verify-private.sh"),
		"VERIFY_FUNCTION="+function,
	)
	output, err := command.CombinedOutput()
	return string(output), err
}

func releaseHardening20260826RunArchiveVerifier(t *testing.T, archive, privateApp string) (string, error) {
	t.Helper()
	command := exec.Command("bash", "-c", "source \"$VERIFY_PRIVATE_SCRIPT\"; verify_clean_update_archive \"$UPDATE_ZIP\"")
	command.Env = append(os.Environ(),
		"APP_PATH="+privateApp,
		"UPDATE_ZIP="+archive,
		"VERIFY_PRIVATE_SCRIPT="+scriptPath("verify-private.sh"),
	)
	output, err := command.CombinedOutput()
	return string(output), err
}
