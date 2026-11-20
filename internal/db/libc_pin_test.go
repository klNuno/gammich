package db

import (
	"os/exec"
	"strings"
	"testing"
)

// modernc.org/sqlite is transpiled against one exact modernc.org/libc release
// and asks every downstream go.mod to resolve that same version
// (https://gitlab.com/cznic/sqlite/-/issues/177). Another dependency that
// requires a newer libc silently lifts it past that pin under minimal version
// selection, and the build still compiles.
func TestLibcMatchesSqlitePin(t *testing.T) {
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go command not found")
	}
	run := func(args ...string) string {
		t.Helper()
		out, err := exec.Command(goBin, args...).Output()
		if err != nil {
			t.Fatalf("go %s: %v", strings.Join(args, " "), err)
		}
		return strings.TrimSpace(string(out))
	}

	sqlite := run("list", "-m", "-f", "{{.Version}}", "modernc.org/sqlite")
	resolved := run("list", "-m", "-f", "{{.Version}}", "modernc.org/libc")

	from := "modernc.org/sqlite@" + sqlite
	pinned := ""
	for line := range strings.Lines(run("mod", "graph")) {
		mod, dep, ok := strings.Cut(strings.TrimSpace(line), " ")
		if ok && mod == from && strings.HasPrefix(dep, "modernc.org/libc@") {
			pinned = strings.TrimPrefix(dep, "modernc.org/libc@")
			break
		}
	}
	if pinned == "" {
		t.Fatalf("%s requires no modernc.org/libc in the module graph", from)
	}
	if resolved != pinned {
		t.Fatalf("modernc.org/libc resolves to %s but %s pins %s; hold back the dependency that requires the newer libc until a sqlite release pins it", resolved, from, pinned)
	}
}
