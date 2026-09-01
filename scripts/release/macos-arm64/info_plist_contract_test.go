package macosarm64_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMacOSInfoPlistDeclaresLaunchableApplicationBundle(t *testing.T) {
	path := filepath.Join("..", "..", "..", "build", "darwin", "Info.plist")
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	plist := string(content)
	required := map[string]string{
		"CFBundleDisplayName":     "Telegram Companion",
		"CFBundleExecutable":      "telegram-companion",
		"CFBundleIdentifier":      "com.telegramcompanion.desktop",
		"CFBundleName":            "Telegram Companion",
		"CFBundlePackageType":     "APPL",
		"NSHighResolutionCapable": "<true/>",
	}
	for key, value := range required {
		if !strings.Contains(plist, "<key>"+key+"</key>") {
			t.Errorf("Info.plist is missing required key %s", key)
		}
		if !strings.Contains(plist, value) {
			t.Errorf("Info.plist is missing value %q for %s", value, key)
		}
	}
}
