package app

import (
	"strings"
	"testing"
)

func TestUnifiedDiffShowsOnlyChangedLineWithContext(t *testing.T) {
	old := []byte("This is the first line.\nThis is the second line.\nThis is the third line.\n")
	new := []byte("This is the first line.\nThis is the second line!!\nThis is the third line.\n")

	got := unifiedDiff("test.txt", old, new, true, true)

	for _, want := range []string{
		"--- a/test.txt\n+++ b/test.txt\n",
		" This is the first line.\n",
		"-This is the second line.\n",
		"+This is the second line!!\n",
		" This is the third line.\n",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("diff missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "-This is the first line.") || strings.Contains(got, "+This is the first line.") {
		t.Fatalf("unchanged context was rendered as a change:\n%s", got)
	}
}
