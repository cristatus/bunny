package runtime

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// requireBwrap skips when the sandbox is unavailable, as it is in minimal CI
// images and inside containers without user namespaces.
func requireBwrap(t *testing.T) {
	t.Helper()
	if _, err := FindBwrap(); err != nil {
		t.Skip("bwrap unavailable:", err)
	}
}

// stagingRoot builds the layout Install creates: work/{src,pkg}. It lives under
// the real home directory, because that is where staging normally sits and
// where --tmpfs /home used to swallow writes.
func stagingRoot(t *testing.T) (workDir, srcDir, pkgDir string) {
	t.Helper()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory:", err)
	}
	workDir, err = os.MkdirTemp(home, ".bunny-prepare-test-")
	if err != nil {
		t.Skip("cannot stage under home:", err)
	}
	t.Cleanup(func() { os.RemoveAll(workDir) })
	srcDir, pkgDir = filepath.Join(workDir, "src"), filepath.Join(workDir, "pkg")
	for _, d := range []string{srcDir, pkgDir} {
		if err := os.MkdirAll(d, 0755); err != nil {
			t.Fatal(err)
		}
	}
	return workDir, srcDir, pkgDir
}

// Everything a prepare step writes inside the staging root has to still be
// there when the sandbox exits. Binding src and pkg individually left the root
// itself masked by --tmpfs /home, so a write to {work} appeared to succeed and
// then vanished.
func TestPrepareWritesToTheStagingRootSurvive(t *testing.T) {
	requireBwrap(t)
	workDir, srcDir, pkgDir := stagingRoot(t)

	vars := map[string]string{"work": workDir, "src": srcDir, "pkg": pkgDir}
	err := PrepareStepsContext(context.Background(), workDir, srcDir, nil, []string{
		"echo staged > {work}/at-root.txt",
		"mkdir -p {work}/data && echo seeded > {work}/data/server.xml",
		"echo built > {pkg}/binary",
	}, vars)
	if err != nil {
		t.Fatal(err)
	}

	for path, want := range map[string]string{
		filepath.Join(workDir, "at-root.txt"):        "staged\n",
		filepath.Join(workDir, "data", "server.xml"): "seeded\n",
		filepath.Join(pkgDir, "binary"):              "built\n",
	} {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Errorf("%s did not survive the sandbox: %v", path, err)
			continue
		}
		if string(data) != want {
			t.Errorf("%s = %q, want %q", path, data, want)
		}
	}
}

// The staging root is the only writable area: the rest of the filesystem is a
// read-only bind, and $HOME is a tmpfs that goes away with the sandbox.
func TestPrepareCannotWriteOutsideTheStagingRoot(t *testing.T) {
	requireBwrap(t)
	workDir, srcDir, _ := stagingRoot(t)

	outside := filepath.Join(filepath.Dir(workDir), "escaped.txt")
	t.Cleanup(func() { os.Remove(outside) })

	// Failure is fine and so is silent success into the tmpfs; what must not
	// happen is the file appearing on the real filesystem.
	_ = PrepareStepsContext(context.Background(), workDir, srcDir, nil,
		[]string{"echo escaped > " + outside}, nil)

	if _, err := os.Stat(outside); !os.IsNotExist(err) {
		t.Errorf("prepare wrote outside the staging root: %s", outside)
	}
}

// The shadow is the whole point of the design: a prepare step writes to the
// path the package will really see at run time, so a manifest can bake {data}
// into a config file and seed that same directory with one placeholder, and
// the bytes land in staging for the caller to merge out.
func TestPrepareShadowsARealPathAtAStagingDir(t *testing.T) {
	requireBwrap(t)
	workDir, srcDir, _ := stagingRoot(t)

	// A real data dir that must not be written to directly during prepare.
	dataDir := filepath.Join(t.TempDir(), "data", "tomcat")
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		t.Fatal(err)
	}
	seedDir := filepath.Join(workDir, "seed")
	if err := os.MkdirAll(seedDir, 0755); err != nil {
		t.Fatal(err)
	}

	vars := map[string]string{"data": dataDir, "pkg": filepath.Join(workDir, "pkg")}
	err := PrepareStepsContext(context.Background(), workDir, srcDir,
		map[string]string{dataDir: seedDir},
		[]string{
			"mkdir -p {data}/conf && echo '<Server/>' > {data}/conf/server.xml",
			// The same real path, baked into a config file, has to resolve at
			// run time — which is only true because {data} is not a staging path.
			"echo CATALINA_BASE={data} > {pkg}/setenv.sh",
		}, vars)
	if err != nil {
		t.Fatal(err)
	}

	staged := filepath.Join(seedDir, "conf", "server.xml")
	if data, err := os.ReadFile(staged); err != nil || string(data) != "<Server/>\n" {
		t.Errorf("write to {data} did not land in the shadow: %q, %v", data, err)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "conf", "server.xml")); !os.IsNotExist(err) {
		t.Error("prepare reached the real data dir; it should only reach the shadow")
	}
	baked, err := os.ReadFile(filepath.Join(workDir, "pkg", "setenv.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(baked), "CATALINA_BASE="+dataDir+"\n"; got != want {
		t.Errorf("{data} baked into config as %q, want the real path %q", got, want)
	}
}
