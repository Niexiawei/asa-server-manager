#!/bin/bash
BASE=/opt/asa-server/basedir
PREFIX=$BASE/umu-prefix
PROTON=$BASE/proton/GE-Proton10-34
UMU=$BASE/umu-launcher/umu-run
RUSER=asa-umu-runtime

RHOME=$(getent passwd "$RUSER" | cut -d: -f6)
[ -z "$RHOME" ] && RHOME=$BASE/runtime-home

echo "== identity =="
id "$RUSER"; getent passwd "$RUSER"; echo "runtime HOME = $RHOME"

echo "== interpreter selection (same logic as the new code) =="
PY=""
for m in $(seq 20 -1 10); do
  command -v "python3.$m" >/dev/null 2>&1 && { PY="python3.$m"; break; }
done
[ -z "$PY" ] && PY=python3
PYBIN=$(command -v "$PY")
echo "picked: $PYBIN -> $("$PYBIN" --version 2>&1)"

echo "== artifacts present? =="
ls -la "$UMU" "$PROTON/proton" 2>&1

echo "== move the half-baked prefix aside, recreate empty =="
[ -e "$PREFIX" ] && mv "$PREFIX" "$PREFIX.broken.$(date +%s)"
mkdir -p "$PREFIX"
chown "$RUSER:$RUSER" "$PREFIX"

echo "== run wineboot --init as $RUSER (verbose) =="
runuser -u "$RUSER" -- env -i \
  HOME="$RHOME" USER="$RUSER" LOGNAME="$RUSER" \
  PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin \
  TERM="${TERM:-xterm}" LANG="${LANG:-C.UTF-8}" \
  WINEPREFIX="$PREFIX" \
  GAMEID=umu-default \
  PROTONPATH="$PROTON" \
  UMU_LOG=debug \
  PROTON_LOG=1 \
  "$PYBIN" "$UMU" wineboot --init 2>&1 | tee /tmp/umu-wineboot.log
echo "exit=${PIPESTATUS[0]}"

echo "== result =="
ls -la "$PREFIX"
ls -la "$PREFIX/system.reg" "$PREFIX/user.reg" 2>&1
echo "steam runtime cache:"
ls -la "$RHOME/.local/share/umu/" 2>&1
echo "proton logs:"
ls -la "$RHOME"/steam-*.log 2>&1
