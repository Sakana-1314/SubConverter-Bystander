#!/bin/sh
# 用便携包自带的 glibc loader 与库启动 SubConverter-Extended，避免依赖发行版 glibc。
set -eu

ROOT=/opt/subconverter
case "$(uname -m)" in
  x86_64 | amd64)
    LOADER="$ROOT/lib64/ld-linux-x86-64.so.2"
    LIBS="$ROOT/lib/x86_64-linux-gnu:$ROOT/usr/lib/x86_64-linux-gnu:$ROOT/lib64:$ROOT/lib:$ROOT/usr/lib"
    ;;
  aarch64 | arm64)
    LOADER="$ROOT/lib/ld-linux-aarch64.so.1"
    LIBS="$ROOT/lib/aarch64-linux-gnu:$ROOT/usr/lib/aarch64-linux-gnu:$ROOT/lib:$ROOT/usr/lib"
    ;;
  *)
    echo "不支持的架构：$(uname -m)" >&2
    exit 1
    ;;
esac

exec "$LOADER" --library-path "$LIBS" \
  "$ROOT/subconverter" -f "${PREF_PATH:-$ROOT/base/pref.toml}"
