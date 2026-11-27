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
# 完整安装包：自带快速模式转文字组件（约 240MB），装好就能转文字、不用联网下载。
# 只给第一次安装用；自动更新仍下上面的小包（updater 认的是固定文件名），已装好的组件留在数据目录里不受影响。
M=$(ls -d asr-build/sv24/*/ 2>/dev/null | head -1); P=asr-build/pack
[ -f "$M/model.int8.onnx" ] && [ -f $P/asr-runtime-windows-amd64.zip ] || { echo "缺 asr-build（见 CLAUDE.md 转文字一节）"; exit 1; }
rm -rf dist/mac-full dist/win-full
mkdir -p dist/mac-full && cp -R "dist/mac/视频号下载器.app" dist/mac-full/
B="dist/mac-full/视频号下载器.app/Contents/Resources/asr-bundle"; mkdir -p "$B"
cp $P/asr-runtime-mac-arm64.zip $P/asr-runtime-mac-x86_64.zip "$M/model.int8.onnx" "$M/tokens.txt" "$B/"
codesign --force --deep -s - "dist/mac-full/视频号下载器.app" 2>/dev/null || true
rm -f dist/SphDownloader-mac-full.zip
ditto -c -k --keepParent "dist/mac-full/视频号下载器.app" dist/SphDownloader-mac-full.zip
mkdir -p dist/win-full && cp -R dist/win/视频号下载器 dist/win-full/
B=dist/win-full/视频号下载器/asr-bundle; mkdir -p $B
cp $P/asr-runtime-windows-amd64.zip "$M/model.int8.onnx" "$M/tokens.txt" $B/
rm -f dist/SphDownloader-windows-full.zip
(cd dist/win-full && zip -qr ../SphDownloader-windows-full.zip 视频号下载器)
ls -lh dist/SphDownloader-*

if [ "$1" = "--publish" ]; then
  NOTES="${2:-v$VER}"
  # 转文字组件（只传一次，固定在 asr-v1，不能成为 latest，否则自动更新会找错）
  if ! gh release view asr-v1 >/dev/null 2>&1; then
    P=asr-build/pack; M=$(ls -d asr-build/sv24/*/ 2>/dev/null | head -1)
    [ -f $P/asr-runtime-mac-arm64.zip ] && [ -f "$M/model.int8.onnx" ] || { echo "缺 asr-build（见 CLAUDE.md 转文字一节）"; exit 1; }
    gh release create asr-v1 $P/asr-runtime-mac-arm64.zip $P/asr-runtime-mac-x86_64.zip $P/asr-runtime-windows-amd64.zip \
      "$M/model.int8.onnx" "$M/tokens.txt" --latest=false --title "转文字组件 v1" \
      --notes "视频号下载器的转文字组件（sherpa-onnx v2.0.3 + SenseVoice int8 2024-07-17 + silero VAD），软件第一次转文字时自动下载，不用手动装。"
  fi
  # 精准模式（Qwen3-ASR）的模型也传一份到 asr-v1 当备用下载源（国内镜像下不了时用）；已经传过就跳过
  Q=asr-build/cmp/sherpa-onnx-qwen3-asr-0.6B-int8-2026-03-25
  if ! gh release view asr-v1 --json assets -q '.assets[].name' | grep -q '^decoder.int8.onnx$'; then
    [ -f $Q/decoder.int8.onnx ] || { echo "缺 $Q（见 CLAUDE.md 识别模式一节）"; exit 1; }
    T=$(mktemp -d); cp $Q/conv_frontend.onnx $Q/encoder.int8.onnx $Q/decoder.int8.onnx $T/
    for f in vocab.json merges.txt tokenizer_config.json; do cp $Q/tokenizer/$f $T/tokenizer_$f; done
    gh release upload asr-v1 $T/* && rm -rf $T
  fi
  gh release create "v$VER" dist/SphDownloader-windows-full.zip dist/SphDownloader-mac-full.zip \
    dist/SphDownloader-windows.zip dist/SphDownloader-windows.exe dist/SphDownloader-mac.zip \
    --title "v$VER" --notes "$NOTES" --latest
  echo "已发布 v$VER"
fi
