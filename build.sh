#!/bin/bash
set -e

VERSION=${1:-$(git describe --tags --always 2>/dev/null || echo "dev")}
VERSION=$(echo "$VERSION" | sed 's/^v//')
BUILD_TIME=$(date -u '+%Y-%m-%dT%H:%M:%SZ')
LDFLAGS="-X main.Version=${VERSION} -X 'main.BuildTime=${BUILD_TIME}' -s -w"
OUT_DIR="dist"
BINARY="hookrun"
ENTRY="./cmd/hookrun"

echo "==> Building HookRun v${VERSION}"
echo "    Build time: ${BUILD_TIME}"
echo "    Output:     ${OUT_DIR}/"
echo ""

rm -rf "${OUT_DIR}"
mkdir -p "${OUT_DIR}"

# Define targets: OS ARCH EXTENSION
TARGETS=(
    "linux amd64"
    "linux arm64"
    "darwin amd64"
    "darwin arm64"
    "windows amd64 .exe"
)

for target in "${TARGETS[@]}"; do
    read -r GOOS GOARCH EXT <<< "$target"

    PLATFORM="${GOOS}-${GOARCH}"
    OUTPUT="${OUT_DIR}/${BINARY}-${PLATFORM}${EXT}"
    PKG_NAME="${BINARY}-v${VERSION}-${PLATFORM}"

    echo -n "  Building ${PLATFORM}..."
    GOOS=$GOOS GOARCH=$GOARCH go build \
        -ldflags "${LDFLAGS}" \
        -trimpath \
        -o "${OUTPUT}" \
        "${ENTRY}"
    echo " done"

    # Package
    if [ "$GOOS" = "windows" ]; then
        ZIP="${OUT_DIR}/${PKG_NAME}.zip"
        echo -n "  Packaging ${PKG_NAME}.zip..."
        cd "${OUT_DIR}"
        zip -q "${PKG_NAME}.zip" "${BINARY}-${PLATFORM}${EXT}"
        rm "${BINARY}-${PLATFORM}${EXT}"
        cd ..
        echo " done"
    else
        TAR="${OUT_DIR}/${PKG_NAME}.tar.gz"
        echo -n "  Packaging ${PKG_NAME}.tar.gz..."
        tar -czf "${TAR}" -C "${OUT_DIR}" "${BINARY}-${PLATFORM}"
        rm "${OUTPUT}"
        echo " done"
    fi
done

echo ""
echo "==> Build complete!"
echo ""
ls -lh "${OUT_DIR}/"
