package macos

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMacOSIconPipelineContract(t *testing.T) {
	root := filepath.Join("..", "..")
	source := filepath.Join(root, "frontend", "src", "assets", "telegram-turquoise.svg")
	if _, err := os.Stat(source); err != nil {
		t.Fatalf("existing Telegram Companion logo asset is required: %v", err)
	}

	pipeline := filepath.Join(root, "build", "macos", "generate_icon.sh")
	pipelineBytes, err := os.ReadFile(pipeline)
	if err != nil {
		t.Fatalf("icon pipeline is required: %v", err)
	}
	pipelineText := string(pipelineBytes)
	for _, required := range []string{
		"telegram-turquoise.svg",
		"iconutil",
		"for size in 16 32 128 256 512; do",
		"icon_${size}x${size}@2x.png",
		"icon_512x512@2x.png",
		"icon.icns",
	} {
		if !strings.Contains(pipelineText, required) {
			t.Errorf("icon pipeline does not contain %q", required)
		}
	}
	if strings.Contains(pipelineText, "icon_1024x1024.png") {
		t.Fatal("standard iconset must not contain icon_1024x1024.png")
	}

	plist, err := os.ReadFile(filepath.Join(root, "build", "darwin", "Info.plist"))
	if err != nil {
		t.Fatalf("macOS Info.plist is required: %v", err)
	}
	plistText := string(plist)
	if !strings.Contains(plistText, "CFBundleIconFile") || !strings.Contains(plistText, "<string>iconfile</string>") {
		t.Fatal("macOS Info.plist must reference the Wails-packaged iconfile resource")
	}

	buildStage, err := os.ReadFile(filepath.Join(root, "scripts", "release", "macos-arm64", "build-stage.sh"))
	if err != nil {
		t.Fatalf("macOS build stage is required: %v", err)
	}
	if !strings.Contains(string(buildStage), "build/macos/generate_icon.sh") {
		t.Fatal("macOS Wails build must run the icon pipeline before wails build")
	}
}
