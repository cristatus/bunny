#!/bin/sh
# Install bunny — portable application manager for Linux.
#
# Usage:
#   curl -fsSL https://raw.githubusercontent.com/cristatus/bunny/main/install.sh | sh
#
# Environment:
#   BUNNY_VERSION   Specific version to install (e.g. v0.5.0). Defaults to latest.
#   BUNNY_HOME      Collapse bunny under a single root instead of the XDG
#                   directories, as an absolute path. The binary lands at
#                   $BUNNY_HOME/bin/bunny.

set -eu

REPO="cristatus/bunny"
VERSION="${BUNNY_VERSION:-latest}"

die() {
  printf 'error: %s\n' "$*" >&2
  exit 1
}

need() {
  command -v "$1" >/dev/null 2>&1 || die "missing required tool: $1"
}

# Without BUNNY_HOME, bunny uses the XDG layout and its shims live in
# ~/.local/bin, which is already on PATH on most distributions.
if [ -n "${BUNNY_HOME:-}" ]; then
  # bunny requires an absolute root, a relative one moving the whole layout as
  # the user cd's around. Refuse here rather than install into a directory the
  # binary would then refuse to use.
  case "$BUNNY_HOME" in
  /*) ;;
  *) die "BUNNY_HOME must be an absolute path (got: $BUNNY_HOME)" ;;
  esac
  INSTALL_DIR="$BUNNY_HOME/bin"
else
  INSTALL_DIR="$HOME/.local/bin"
fi

need curl
need tar
need sha256sum
need uname

os="$(uname -s | tr '[:upper:]' '[:lower:]')"
[ "$os" = "linux" ] || die "bunny only supports Linux (detected: $os)"

case "$(uname -m)" in
x86_64 | amd64) arch=amd64 ;;
*) die "bunny currently supports only Linux x86_64/amd64 (detected: $(uname -m))" ;;
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

# setup resolves the layout from the environment it runs in, so a BUNNY_HOME
# passed as a prefix to this script (set for this command only) has to be
# repeated. Without it setup would wire up the XDG layout while the binary sits
# under the single root. setup then persists the value itself.
setup="$INSTALL_DIR/bunny setup"
if [ -n "${BUNNY_HOME:-}" ]; then
  setup="BUNNY_HOME=$BUNNY_HOME $setup"
fi

cat <<EOF

Next steps:
  $setup
  exec \$SHELL
  bunny doctor
EOF
