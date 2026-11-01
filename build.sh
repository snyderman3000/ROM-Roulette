#!/bin/sh
# Builds dist/ROMRoulette-v<version>-OnionOS.zip (unzip onto the SD card root).
set -e
cd "$(dirname "$0")"
VERSION=$(grep -o 'const version = "[^"]*"' main.go | cut -d'"' -f2)
# go.sum isn't kept in the repo; tidy writes it (skipped inside a go.work workspace)
[ -n "$(go env GOWORK)" ] || go mod tidy
go test ./...
CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=7 go build -trimpath -ldflags="-s -w" -o package/ROMRoulette/romroulette .
# Sound: the effects are generated (tools/sfxgen) and mixed by a small C helper
# built with the Miyoo Mini toolchain (TOOLCHAIN, default /opt/mini).
go run ./tools/sfxgen -out package/ROMRoulette/sfx
TC="${TOOLCHAIN:-/opt/mini}"
if [ -x "$TC/bin/arm-linux-gnueabihf-gcc" ]; then
  "$TC/bin/arm-linux-gnueabihf-gcc" -O2 -mcpu=cortex-a7 -mfpu=neon-vfpv4 -mfloat-abi=hard -Wall \
      sfx/rrsfx.c -o package/ROMRoulette/rrsfx
  "$TC/bin/arm-linux-gnueabihf-strip" package/ROMRoulette/rrsfx
elif [ ! -f package/ROMRoulette/rrsfx ]; then
  echo "no Miyoo toolchain at $TC and no prebuilt package/ROMRoulette/rrsfx" >&2; exit 1
fi
rm -rf dist && mkdir -p dist/stage/App
cp -r package/ROMRoulette dist/stage/App/
rm -rf dist/stage/App/ROMRoulette/data
(cd dist/stage && zip -qr "../ROMRoulette-v$VERSION-OnionOS.zip" App)
rm -rf dist/stage
echo "Built dist/ROMRoulette-v$VERSION-OnionOS.zip"
