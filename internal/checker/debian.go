package checker

import (
	"bufio"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/charmbracelet/log"

	"github.com/cristatus/bunny/internal/manifest"
)

func init() { Register(&Debian{}) }

// maxPackagesIndex caps the decompressed Debian Packages index we read. These
// indexes are large (unlike the small metadata bodies bounded by
// maxMetadataBody), but still need a ceiling to bound memory.
const maxPackagesIndex = 128 << 20

// Debian queries an APT repository's Packages index for the latest version.
type Debian struct{}

func (d *Debian) Type() string { return "debian" }

func (d *Debian) Check(ctx context.Context, cfg *manifest.UpdateConfig, currentVersion, sourceURL string) (*Result, error) {
	if cfg.Root == "" || cfg.PackageName == "" {
		return nil, fmt.Errorf("debian checker requires root and package-name")
	}

	root := strings.TrimSuffix(cfg.Root, "/")
	var pkgURL string
	switch {
	case cfg.Dist == "" && cfg.Component == "":
		pkgURL = root + "/Packages.gz"
	case cfg.Dist != "" && cfg.Component == "":
		pkgURL = root + "/" + cfg.Dist + "/Packages.gz"
	default:
		pkgURL = fmt.Sprintf("%s/dists/%s/%s/binary-amd64/Packages.gz", root, cfg.Dist, cfg.Component)
	}

	pkg, err := d.fetchPackage(ctx, pkgURL, cfg.PackageName, true)
	if err != nil {
		alt := strings.TrimSuffix(pkgURL, ".gz")
		pkg, err = d.fetchPackage(ctx, alt, cfg.PackageName, false)
		if err != nil {
			return nil, err
		}
	}

	r := &Result{
		LatestVersion: pkg.version,
		Hash:          pkg.sha256,
		HashAlgorithm: "sha256",
		Size:          pkg.size,
		HasUpdate:     compareDebVersions(pkg.version, currentVersion) > 0,
	}
	if pkg.filename != "" {
		r.DownloadURL = root + "/" + pkg.filename
	}
	_ = sourceURL
	return r, nil
}

type debPkg struct {
	version, filename, sha256, arch string
	size                            int64
}

func (d *Debian) fetchPackage(ctx context.Context, url, pkgName string, gz bool) (*debPkg, error) {
	resp, err := doRequest(ctx, http.MethodGet, url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	var r io.Reader = resp.Body
	if gz {
		gr, err := gzip.NewReader(resp.Body)
		if err != nil {
			return nil, err
		}
		defer gr.Close()
		r = io.LimitReader(gr, maxPackagesIndex)
	} else {
		r = io.LimitReader(resp.Body, maxPackagesIndex)
	}

	var best, cur *debPkg
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024) // some Debian Packages files have long lines
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			if cur != nil && cur.version != "" && isSupportedArch(cur.arch) {
				if best == nil || compareDebVersions(cur.version, best.version) > 0 {
					best = cur
				}
			}
			cur = nil
			continue
		}
		if name, ok := strings.CutPrefix(line, "Package: "); ok {
			if name == pkgName {
				cur = &debPkg{}
			}
			continue
		}
		if cur == nil {
			continue
		}
		switch {
		case strings.HasPrefix(line, "Version: "):
			cur.version = strings.TrimPrefix(line, "Version: ")
		case strings.HasPrefix(line, "Filename: "):
			cur.filename = strings.TrimPrefix(line, "Filename: ")
		case strings.HasPrefix(line, "SHA256: "):
			cur.sha256 = strings.TrimPrefix(line, "SHA256: ")
		case strings.HasPrefix(line, "Size: "):
			cur.size, _ = strconv.ParseInt(strings.TrimPrefix(line, "Size: "), 10, 64)
		case strings.HasPrefix(line, "Architecture: "):
			cur.arch = strings.TrimPrefix(line, "Architecture: ")
		}
	}
	if cur != nil && cur.version != "" && isSupportedArch(cur.arch) {
		if best == nil || compareDebVersions(cur.version, best.version) > 0 {
			best = cur
		}
	}
	// A line past the buffer stops the scan early, and "latest" picked from
	// part of the index is a wrong answer, not a partial one.
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read Packages index: %w", err)
	}
	if best == nil {
		return nil, fmt.Errorf("package %q not found", pkgName)
	}
	log.Debug("Debian package", "version", best.version)
	return best, nil
}

// isSupportedArch reports whether a Packages entry targets bunny's only
// supported platform. Repos split by dists/{dist}/{component}/binary-{arch}
// already scope each Packages.gz to one architecture, but a flat repo (root+
// dist, no component) serves every architecture from a single index, and
// entries for other architectures can sort ahead of amd64 for the same
// version, silently winning the "latest" pick.
func isSupportedArch(arch string) bool {
	return arch == "" || arch == "amd64" || arch == "all"
}

// compareDebVersions orders two Debian versions the way dpkg does:
// [epoch:]upstream[-revision], the epoch compared numerically, then upstream
// and revision by verrevcmp. That is not a dotted-number compare: "~" sorts
// before everything, even the end of the string, so 1.2~rc1 < 1.2, and runs
// of digits compare as numbers wherever they fall, so 1.10~rc1 > 1.9.
func compareDebVersions(v1, v2 string) int {
	e1, u1, r1 := splitDebVersion(v1)
	e2, u2, r2 := splitDebVersion(v2)
	if e1 != e2 {
		if e1 < e2 {
			return -1
		}
		return 1
	}
	if c := verrevcmp(u1, u2); c != 0 {
		return c
	}
	return verrevcmp(r1, r2)
}

func splitDebVersion(v string) (epoch int, upstream, revision string) {
	if i := strings.IndexByte(v, ':'); i >= 0 {
		epoch, _ = strconv.Atoi(v[:i])
		v = v[i+1:]
	}
	if i := strings.LastIndexByte(v, '-'); i >= 0 {
		return epoch, v[:i], v[i+1:]
	}
	return epoch, v, ""
}

// verrevcmp is dpkg's comparison of an upstream version or revision:
// alternating non-digit runs, compared character by character with debOrder,
// and digit runs, compared numerically.
func verrevcmp(a, b string) int {
	isDigit := func(c byte) bool { return c >= '0' && c <= '9' }
	for a != "" || b != "" {
		for (a != "" && !isDigit(a[0])) || (b != "" && !isDigit(b[0])) {
			ac, bc := debOrder(a), debOrder(b)
			if ac != bc {
				return cmpInt(ac, bc)
			}
			a, b = a[1:], b[1:]
		}
		for a != "" && a[0] == '0' {
			a = a[1:]
		}
		for b != "" && b[0] == '0' {
			b = b[1:]
		}
		first := 0
		for a != "" && isDigit(a[0]) && b != "" && isDigit(b[0]) {
			if first == 0 {
				first = int(a[0]) - int(b[0])
			}
			a, b = a[1:], b[1:]
		}
		if a != "" && isDigit(a[0]) {
			return 1
		}
		if b != "" && isDigit(b[0]) {
			return -1
		}
		if first != 0 {
			return cmpInt(first, 0)
		}
	}
	return 0
}

// debOrder weighs the next non-digit character: "~" below the end of the
// string, letters next, then everything else.
func debOrder(s string) int {
	switch {
	case s == "":
		return 0
	case s[0] == '~':
		return -1
	case s[0] >= '0' && s[0] <= '9':
		return 0
	case (s[0] >= 'a' && s[0] <= 'z') || (s[0] >= 'A' && s[0] <= 'Z'):
		return int(s[0])
	default:
		return int(s[0]) + 256
	}
}

func cmpInt(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}
