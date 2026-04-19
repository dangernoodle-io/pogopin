#!/usr/bin/env bash
set -euo pipefail

stat_mt_size() {
  if [ "$(uname -s)" = "Darwin" ]; then
    stat -f '%m %z' "$1" 2>/dev/null
  else
    stat -c '%Y %s' "$1" 2>/dev/null
  fi
}

BINARY_DIR="${CLAUDE_PLUGIN_DATA}/bin"
BINARY="${BINARY_DIR}/pogo"
VERSION_FILE="${CLAUDE_PLUGIN_DATA}/.version"
REPO="dangernoodle-io/pogopin"

# Dev mode: use a local binary instead of fetching from GitHub.
if [ -n "${POGOPIN_DEV_BINARY:-}" ]; then
  if [ ! -x "$POGOPIN_DEV_BINARY" ]; then
    echo "pogopin: dev binary not found: $POGOPIN_DEV_BINARY" >&2
    exit 1
  fi
  mkdir -p "$BINARY_DIR"
  if [ -x "$BINARY" ] && [ "$(stat_mt_size "$POGOPIN_DEV_BINARY")" = "$(stat_mt_size "$BINARY")" ]; then
    exit 0
  fi
  cp "$POGOPIN_DEV_BINARY" "$BINARY"
  chmod 755 "$BINARY"
  [ "$(uname -s)" = "Darwin" ] && codesign -s - "$BINARY" 2>/dev/null
  printf 'dev' > "$VERSION_FILE"
  echo "pogopin: installed dev binary from $POGOPIN_DEV_BINARY"
  exit 0
fi

# Local binary: check well-known paths before hitting GitHub.
LOCAL_BIN=""
for candidate in /usr/local/bin/pogo /opt/homebrew/bin/pogo; do
  if [ -x "$candidate" ]; then
    LOCAL_BIN="$candidate"
    break
  fi
done

if [ -n "$LOCAL_BIN" ]; then
  mkdir -p "$BINARY_DIR"
  # Resolve symlinks so we copy the actual binary
  if [ "$(uname -s)" = "Darwin" ]; then
    REAL_BIN="$(readlink "$LOCAL_BIN" 2>/dev/null || echo "$LOCAL_BIN")"
    case "$REAL_BIN" in
      /*) ;;
      *)  REAL_BIN="$(cd "$(dirname "$LOCAL_BIN")" && cd "$(dirname "$REAL_BIN")" && echo "$(pwd)/$(basename "$REAL_BIN")")" ;;
    esac
  else
    REAL_BIN="$(readlink -f "$LOCAL_BIN")"
  fi
  if [ -x "$BINARY" ] && [ "$(stat_mt_size "$REAL_BIN")" = "$(stat_mt_size "$BINARY")" ]; then
    exit 0
  fi
  cp "$REAL_BIN" "$BINARY"
  chmod 755 "$BINARY"
  [ "$(uname -s)" = "Darwin" ] && codesign -s - "$BINARY" 2>/dev/null
  LOCAL_VERSION="$("$BINARY" --version 2>/dev/null || echo "local")"
  printf '%s' "$LOCAL_VERSION" > "$VERSION_FILE"
  echo "pogopin: installed ${LOCAL_VERSION} from ${LOCAL_BIN}"
  exit 0
fi

# Fallback: fetch from GitHub releases.

# Detect OS.
case "$(uname -s)" in
  Darwin) OS="darwin" ;;
  Linux)  OS="linux" ;;
  *)
    echo "pogopin: unsupported OS: $(uname -s)" >&2
    exit 1
    ;;
esac

# Detect arch.
case "$(uname -m)" in
  x86_64)        ARCH="amd64" ;;
  arm64|aarch64) ARCH="arm64" ;;
  *)
    echo "pogopin: unsupported arch: $(uname -m)" >&2
    exit 1
    ;;
esac

# Archive extension per OS.
if [ "$OS" = "darwin" ]; then
  EXT="zip"
else
  EXT="tar.gz"
fi

# Fetch latest release tag.
LATEST_TAG="$(curl -sL "https://api.github.com/repos/${REPO}/releases/latest" \
  | grep '"tag_name"' \
  | head -1 \
  | sed 's/.*"tag_name": *"\(.*\)".*/\1/')"

if [ -z "$LATEST_TAG" ]; then
  echo "pogopin: failed to fetch latest release tag" >&2
  [ -x "$BINARY" ] && exit 0
  exit 1
fi

# Strip leading v for archive naming.
LATEST_VERSION="${LATEST_TAG#v}"

# Check installed version.
INSTALLED_VERSION=""
if [ -f "$VERSION_FILE" ]; then
  INSTALLED_VERSION="$(cat "$VERSION_FILE")"
fi

# Skip if up to date.
if [ "$INSTALLED_VERSION" = "$LATEST_VERSION" ] && [ -x "$BINARY" ]; then
  exit 0
fi

echo "pogopin: installing ${LATEST_VERSION} (${OS}/${ARCH})..."

mkdir -p "$BINARY_DIR"
WORK_DIR="$(mktemp -d)"
trap 'rm -rf "$WORK_DIR"' EXIT

BASE_URL="https://github.com/${REPO}/releases/download/${LATEST_TAG}"
ARCHIVE_NAME="pogo_${LATEST_VERSION}_${OS}_${ARCH}.${EXT}"
CHECKSUM_NAME="pogo_${LATEST_VERSION}_SHA256SUMS"

# Download archive and checksums.
curl -sL --fail -o "${WORK_DIR}/${ARCHIVE_NAME}" "${BASE_URL}/${ARCHIVE_NAME}"
curl -sL --fail -o "${WORK_DIR}/${CHECKSUM_NAME}" "${BASE_URL}/${CHECKSUM_NAME}"

# Verify checksum.
(cd "$WORK_DIR" && grep "${ARCHIVE_NAME}" "${CHECKSUM_NAME}" | shasum -a 256 -c -)

# Extract.
if [ "$EXT" = "zip" ]; then
  unzip -qo "${WORK_DIR}/${ARCHIVE_NAME}" -d "${WORK_DIR}/extracted"
else
  mkdir -p "${WORK_DIR}/extracted"
  tar -xzf "${WORK_DIR}/${ARCHIVE_NAME}" -C "${WORK_DIR}/extracted"
fi

# Install binary.
install -m 755 "${WORK_DIR}/extracted/pogo" "$BINARY"
[ "$OS" = "darwin" ] && codesign -s - "$BINARY" 2>/dev/null
printf '%s' "$LATEST_VERSION" > "$VERSION_FILE"

echo "pogopin: installed ${LATEST_VERSION}"
