package driveaccounts

import (
	"fmt"
	"strings"
	"testing"
)

func TestParseLinksAcceptsFilesFoldersAndDeduplicates(t *testing.T) {
	refs, err := ParseLinks("https://drive.google.com/uc?id=test_123&export=download\n\nhttps://drive.google.com/file/d/test_123/view?usp=sharing\nhttps://drive.google.com/drive/folders/folder_123")
	if err != nil || len(refs) != 2 || refs[0].ID != "test_123" || !refs[1].Folder {
		t.Fatalf("unexpected parsed links: %v", err)
	}
}
func TestParseLinksLimitsAndRejectsAmbiguousHosts(t *testing.T) {
	var lines []string
	for i := 0; i < 100; i++ {
		lines = append(lines, fmt.Sprintf("https://drive.google.com/uc?id=file_%d&export=download", i))
	}
	if _, err := ParseLinks(strings.Join(lines, "\n")); err != nil {
		t.Fatal(err)
	}
	if _, err := ParseLinks(strings.Join(append(lines, lines[0]), "\n")); err == nil {
		t.Fatal("accepted 101 nonempty lines")
	}
	for _, raw := range []string{"", "https://evil.example/uc?id=abc", "https://drive.google.com.evil.example/uc?id=abc", "https://drive.google.com@evil.example/uc?id=abc", "http://drive.google.com/uc?id=abc", "https://drive.google.com:443/uc?id=abc", "https://drive.google.com/uc?id=abc&id=def", "https://drive.google.com/uc?id=abc&redirect=https://evil.example", "https://drive.google.com/uc?id=../secret", "https://drive.google.com/uc?id=abc#fragment"} {
		if _, err := ParseLinks(raw); err == nil {
			t.Errorf("accepted unsafe input %q", raw)
		}
	}
}
