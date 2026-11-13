#!/bin/bash
# 打包两个平台，并（加 --publish 时）发布到 GitHub Release，软件会自动更新到这个版本。
#   ./release.sh              只打包到 dist/
#   ./release.sh --publish "更新说明"
set -e
cd "$(dirname "$0")"
export PATH="$HOME/.local/bin:$PATH"
VER=$(sed -n 's/^[[:space:]]*version = "\(.*\)"/\1/p' main.go)
echo "== v$VER"
go test ./... >/dev/null
echo "测试通过"

# Windows
python3 assets/make_syso.py assets/icon.ico rsrc_windows_amd64.syso >/dev/null
rm -rf dist/win && mkdir -p dist/win/视频号下载器
GOOS=windows GOARCH=amd64 go build -trimpath -ldflags "-s -w -H windowsgui" -o dist/SphDownloader-windows.exe .
cp dist/SphDownloader-windows.exe dist/win/视频号下载器/视频号下载器.exe
cp 修复网络.bat 使用说明.txt dist/win/视频号下载器/
rm -f dist/SphDownloader-windows.zip
(cd dist/win && zip -qr ../SphDownloader-windows.zip 视频号下载器)

# Mac
./build-mac.sh >/dev/null
rm -f dist/SphDownloader-mac.zip
ditto -c -k --keepParent "dist/mac/视频号下载器.app" dist/SphDownloader-mac.zip
ls -lh dist/SphDownloader-*

if [ "$1" = "--publish" ]; then
  NOTES="${2:-v$VER}"
  gh release create "v$VER" dist/SphDownloader-windows.zip dist/SphDownloader-windows.exe dist/SphDownloader-mac.zip \
    --title "v$VER" --notes "$NOTES"
  echo "已发布 v$VER"
fi
