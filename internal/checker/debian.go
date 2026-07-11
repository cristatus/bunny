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
		HasUpdate:     pkg.version != currentVersion,
	}
	if pkg.filename != "" {
		r.DownloadURL = root + "/" + pkg.filename
	}
	_ = sourceURL
	return r, nil
}

type debPkg struct {
	version, filename, sha256 string
	size                      int64
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
			if cur != nil && cur.version != "" {
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
		}
	}
	if cur != nil && cur.version != "" {
		if best == nil || compareDebVersions(cur.version, best.version) > 0 {
			best = cur
		}
	}
	if best == nil {
		return nil, fmt.Errorf("package %q not found", pkgName)
	}
	log.Debug("Debian package", "version", best.version)
	return best, nil
}

func compareDebVersions(v1, v2 string) int {
	split := func(v string) []string {
		v = strings.ReplaceAll(v, "-", ".")
		v = strings.ReplaceAll(v, "+", ".")
		return strings.Split(v, ".")
	}
	p1, p2 := split(v1), split(v2)
	max := len(p1)
	if len(p2) > max {
		max = len(p2)
	}
	for i := 0; i < max; i++ {
		var a, b string
		if i < len(p1) {
			a = p1[i]
		}
		if i < len(p2) {
			b = p2[i]
		}
		na, ea := strconv.Atoi(a)
		nb, eb := strconv.Atoi(b)
		if ea == nil && eb == nil {
			if na != nb {
				return na - nb
			}
			continue
		}
		if a != b {
			if a > b {
				return 1
			}
			return -1
		}
	}
	return 0
}
