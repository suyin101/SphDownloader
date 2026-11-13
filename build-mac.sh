#!/bin/bash
# 打包 Mac 版：dist/mac/视频号下载器.app（DEBUG=1 时额外保存页面脚本，排查用）
set -e
cd "$(dirname "$0")"
VER=$(sed -n 's/^[[:space:]]*version = "\(.*\)"/\1/p' main.go)
APP="dist/mac/视频号下载器.app"
rm -rf dist/mac && mkdir -p "$APP/Contents/MacOS" "$APP/Contents/Resources"
LDF="-s -w"
[ "$DEBUG" = "1" ] && LDF="$LDF -X main.debugBuild=1"
export CGO_ENABLED=1 CGO_CFLAGS="-Wno-deprecated" CGO_CXXFLAGS="-Wno-deprecated -Wno-deprecated-literal-operator"
GOOS=darwin GOARCH=arm64 go build -trimpath -ldflags "$LDF" -o /tmp/sphdl-arm64 . 2>/dev/null
GOOS=darwin GOARCH=amd64 CC="clang -arch x86_64" CXX="clang++ -arch x86_64" go build -trimpath -ldflags "$LDF" -o /tmp/sphdl-amd64 . 2>/dev/null
lipo -create -output "$APP/Contents/MacOS/SphDownloader" /tmp/sphdl-arm64 /tmp/sphdl-amd64
cp assets/AppIcon.icns "$APP/Contents/Resources/AppIcon.icns"
cat > "$APP/Contents/Info.plist" <<PL
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>CFBundleName</key><string>视频号下载器</string>
<key>CFBundleDisplayName</key><string>视频号下载器</string>
<key>CFBundleIdentifier</key><string>com.suyin.sphdownloader</string>
<key>CFBundleExecutable</key><string>SphDownloader</string>
<key>CFBundleIconFile</key><string>AppIcon</string>
<key>CFBundlePackageType</key><string>APPL</string>
<key>CFBundleShortVersionString</key><string>$VER</string>
<key>CFBundleVersion</key><string>$VER</string>
<key>NSHighResolutionCapable</key><true/>
<key>NSAppTransportSecurity</key><dict><key>NSAllowsArbitraryLoads</key><true/><key>NSAllowsLocalNetworking</key><true/></dict>
<key>LSMinimumSystemVersion</key><string>11.0</string>
</dict></plist>
PL
codesign --force --deep -s - "$APP" 2>/dev/null || true
echo "$APP (v$VER)"
