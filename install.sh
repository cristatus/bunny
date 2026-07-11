#!/bin/sh
# Install bunny — portable application manager for Linux.
#
# Usage:
#   curl -fsSL https://raw.githubusercontent.com/cristatus/bunny/main/install.sh | sh
#
# Environment:
#   BUNNY_VERSION   Specific version to install (e.g. v0.2.0). Defaults to latest.
#   BUNNY_HOME      Install root. Defaults to ~/.bunny. The binary lands at $BUNNY_HOME/bin/bunny.

set -eu

REPO="cristatus/bunny"
VERSION="${BUNNY_VERSION:-latest}"
INSTALL_ROOT="${BUNNY_HOME:-$HOME/.bunny}"
INSTALL_DIR="$INSTALL_ROOT/bin"

die() {
  printf 'error: %s\n' "$*" >&2
  exit 1
}

need() {
  command -v "$1" >/dev/null 2>&1 || die "missing required tool: $1"
}

need curl
need tar
need sha256sum
need uname

os="$(uname -s | tr '[:upper:]' '[:lower:]')"
[ "$os" = "linux" ] || die "bunny only supports Linux (detected: $os)"

case "$(uname -m)" in
x86_64 | amd64) arch=amd64 ;;
aarch64 | arm64) arch=arm64 ;;
*) die "unsupported architecture: $(uname -m)" ;;
esac

# Resolve "latest" via the GitHub release-redirect — works without auth.
if [ "$VERSION" = "latest" ]; then
  VERSION=$(curl -fsSLI -o /dev/null -w '%{url_effective}' \
    "https://github.com/$REPO/releases/latest" | sed 's|.*/||')
  [ -n "$VERSION" ] || die "could not resolve latest release"
fi

# Normalise: tag is v<X.Y.Z>, archive uses bare <X.Y.Z>.
case "$VERSION" in
v*)
  tag="$VERSION"
  num="${VERSION#v}"
  ;;
*)
  tag="v$VERSION"
  num="$VERSION"
  ;;
esac

tarball="bunny_${num}_linux_${arch}.tar.gz"
base="https://github.com/$REPO/releases/download/$tag"

tmp=$(mktemp -d)
stage="$INSTALL_DIR/.bunny-install.$$"
trap 'rm -rf "$tmp"; rm -f "$stage"' EXIT

printf 'Downloading %s ...\n' "$tarball"
curl -fsSL "$base/$tarball" -o "$tmp/$tarball"
curl -fsSL "$base/checksums.txt" -o "$tmp/checksums.txt"

expected=$(awk -v f="$tarball" '$2 == f { print $1 }' "$tmp/checksums.txt")
[ -n "$expected" ] || die "checksum not found for $tarball in checksums.txt"
got=$(sha256sum "$tmp/$tarball" | awk '{ print $1 }')
[ "$expected" = "$got" ] || die "checksum mismatch for $tarball"

mkdir -p "$INSTALL_DIR"
tar -C "$tmp" -xzf "$tmp/$tarball" bunny
cp "$tmp/bunny" "$stage"
chmod +x "$stage"
# stage and final live in the same directory, so this replacement is atomic.
mv -f "$stage" "$INSTALL_DIR/bunny"

printf '\nInstalled bunny %s to %s\n' "$tag" "$INSTALL_DIR/bunny"
cat <<EOF

Next steps:
  $INSTALL_DIR/bunny setup
  exec \$SHELL
  bunny doctor
EOF
