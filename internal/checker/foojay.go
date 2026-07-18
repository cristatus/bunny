package checker

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"path"
	"strings"

	"github.com/cristatus/bunny/internal/manifest"
	"github.com/cristatus/bunny/internal/verparse"
)

func init() { Register(&Foojay{}) }

// foojayBaseURL is the Foojay Disco API root. Overridable in tests.
var foojayBaseURL = "https://api.foojay.io/disco/v3.0"

// Foojay discovers JDK builds via the vendor-neutral Foojay Disco API. The
// distribution (vendor) comes from the update config; the major version is
// taken from the package's current version. It is a two-step lookup: /packages
// finds the latest GA build, /ids/{id} resolves the canonical download URL and
// checksum (the package id is ephemeral, so it is queried fresh every time).
type Foojay struct{}

func (f *Foojay) Type() string { return "foojay" }

func (f *Foojay) Check(ctx context.Context, cfg *manifest.UpdateConfig, currentVersion, sourceURL string) (*Result, error) {
	if cfg.Distribution == "" {
		return nil, fmt.Errorf("foojay checker requires distribution")
	}
	major := verparse.Major(currentVersion)
	if major == "" {
		return nil, fmt.Errorf("foojay checker: cannot derive major version from %q", currentVersion)
	}

	q := url.Values{}
	q.Set("distribution", cfg.Distribution)
	q.Set("version", major)
	q.Set("architecture", "x64")
	q.Set("operating_system", "linux")
	q.Set("lib_c_type", "glibc")
	q.Set("archive_type", "tar.gz")
	q.Set("package_type", "jdk")
	q.Set("javafx_bundled", "false")
	q.Set("latest", "available")
	q.Set("release_status", "ga")
	q.Set("directly_downloadable", "true")

	body, err := httpReadAll(ctx, foojayBaseURL+"/packages?"+q.Encode())
	if err != nil {
		return nil, err
	}
	var pkgs struct {
		Result []struct {
			ID          string `json:"id"`
			JavaVersion string `json:"java_version"`
			Size        int64  `json:"size"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(body), &pkgs); err != nil {
		return nil, err
	}
	if len(pkgs.Result) == 0 {
		return nil, fmt.Errorf("no foojay package for %s major %s", cfg.Distribution, major)
	}
	pkg := pkgs.Result[0]

	r := &Result{
		LatestVersion: pkg.JavaVersion,
		Size:          pkg.Size,
		HasUpdate:     pkg.JavaVersion != currentVersion,
	}

	info, err := httpReadAll(ctx, foojayBaseURL+"/ids/"+pkg.ID)
	if err != nil {
		return nil, err
	}
	var ids struct {
		Result []struct {
			DirectDownloadURI string `json:"direct_download_uri"`
			Checksum          string `json:"checksum"`
			ChecksumType      string `json:"checksum_type"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(info), &ids); err != nil {
		return nil, err
	}
	if len(ids.Result) == 0 {
		return nil, fmt.Errorf("foojay: no package info for id %s", pkg.ID)
	}
	d := ids.Result[0]
	r.DownloadURL = d.DirectDownloadURI
	switch d.ChecksumType {
	case "sha256":
		if IsValidSHA256(d.Checksum) {
			r.Hash, r.HashAlgorithm = d.Checksum, "sha256"
		}
	case "sha512":
		if IsValidSHA512(d.Checksum) {
			r.Hash, r.HashAlgorithm = d.Checksum, "sha512"
		}
	}
	if r.Hash == "" && cfg.HashURL != "" {
		hashURL := ExpandTemplate(cfg.HashURL, pkg.JavaVersion)
		target := path.Base(d.DirectDownloadURI)
		if hash, algorithm, err := FetchChecksumFromURL(ctx, hashURL, target, cfg.HashPattern); err == nil {
			r.Hash, r.HashAlgorithm = hash, algorithm
		}
	}
	if r.Hash == "" {
		if hash, ok := foojayGitHubDigest(ctx, d.DirectDownloadURI); ok {
			r.Hash, r.HashAlgorithm = hash, "sha256"
		}
	}
	return r, nil
}

// foojayGitHubDigest resolves the SHA-256 GitHub displays beside a release
// asset. Some JDK vendors publish only SHA-1 through Disco even though GitHub
// has calculated a stronger digest for the exact same artifact.
func foojayGitHubDigest(ctx context.Context, downloadURL string) (string, bool) {
	u, err := url.Parse(downloadURL)
	if err != nil || u.Hostname() != "github.com" {
		return "", false
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) != 6 || parts[2] != "releases" || parts[3] != "download" {
		return "", false
	}
	tag, err := url.PathUnescape(parts[4])
	if err != nil {
		return "", false
	}
	filename, err := url.PathUnescape(parts[5])
	if err != nil {
		return "", false
	}
	filename = path.Base(filename)
	assets, err := (&GitHub{}).fetchAssets(ctx, parts[0]+"/"+parts[1], tag)
	if err != nil {
		return "", false
	}
	for _, asset := range assets {
		if asset.Filename == filename && IsValidSHA256(asset.Digest) {
			return asset.Digest, true
		}
	}
	return "", false
}
