#!/usr/bin/env bash
# Build only from the checked-in template at HEAD.
set -euo pipefail
root="$(cd "$(dirname "$0")/.." && pwd)"
cd "$root"
: "${FNPACK:?Set FNPACK to the verified fnpack executable}"
: "${KOPIA:?Set KOPIA to the verified Kopia executable for the target architecture}"
arch="${CANGZHOU_ARCH:-x86}"
case "$arch" in
  x86) goarch=amd64; machine=62 ;;
  arm) goarch=arm64; machine=183 ;;
  *) echo 'CANGZHOU_ARCH must be x86 or arm' >&2; exit 1 ;;
esac
[ -x "$FNPACK" ] && [ -x "$KOPIA" ]
version="$(sed -n 's/^version=//p' packaging/fnos/manifest)"
[[ "$version" =~ ^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ ]] || { echo 'Invalid package version' >&2; exit 1; }
if [ -n "${RELEASE_TAG:-}" ] && [ "$RELEASE_TAG" != "v$version" ]; then
  echo 'Release tag must match packaging/fnos/manifest version' >&2; exit 1
fi
commit="$(git rev-parse --verify HEAD^{commit})"
if [ -n "$(git status --porcelain --untracked-files=normal)" ]; then
  echo 'Working tree is not clean; commit changes before building release assets' >&2
  exit 1
fi
out="${CANGZHOU_ARTIFACTS_DIR:-$root/artifacts}"
mkdir -p "$out"
out="$(cd "$out" && pwd)"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
mkdir -p "$tmp/package" "$tmp/source"
git archive --format=tar "$commit" | tar -xf - -C "$tmp/source"
cp -R "$tmp/source/packaging/fnos/." "$tmp/package/"
python3 - "$tmp/package/manifest" "$arch" <<'PYCODE'
import pathlib, re, sys
path = pathlib.Path(sys.argv[1])
content, count = re.subn(r'^platform=.*$', 'platform=' + sys.argv[2], path.read_text(), flags=re.M)
if count != 1: raise SystemExit('Expected exactly one manifest platform')
path.write_text(content)
PYCODE
mkdir -p "$tmp/package/app" "$tmp/package/wizard"
cp "$KOPIA" "$tmp/package/app/kopia"
chmod 755 "$tmp/package/app/kopia"
(cd "$tmp/source/core" && GOOS=linux GOARCH="$goarch" CGO_ENABLED=0 go build -mod=vendor -trimpath -ldflags='-s -w' -o "$tmp/package/app/cangzhou-server" ./cmd/cangzhou)
(cd "$tmp" && "$FNPACK" build -d "$tmp/package")
package="cangzhou-$version-$arch.fpk"
mkdir -p "$tmp/verify" "$tmp/payload"
tar -xzf "$tmp/cangzhou.fpk" -C "$tmp/verify"
tar -xzf "$tmp/verify/app.tgz" -C "$tmp/payload"
python3 - "$tmp/verify/manifest" "$tmp/payload" "$version" "$arch" "$machine" <<'PY'
import pathlib, re, sys
manifest, payload, version, arch, machine = pathlib.Path(sys.argv[1]), pathlib.Path(sys.argv[2]), sys.argv[3], sys.argv[4], int(sys.argv[5])
s = manifest.read_text(encoding='utf-8')
for key, expected in [('appname','cangzhou'), ('version',version), ('platform',arch)]:
    match = re.search(r'^\s*' + key + r'\s*=\s*(.*?)\s*$', s, re.M)
    if not match or match.group(1) != expected: raise SystemExit(f'Unexpected manifest {key}')
for name in ('kopia','cangzhou-server'):
    p=payload/name
    if not p.is_file(): raise SystemExit(f'Missing payload {name}')
    header=p.read_bytes()[:20]
    if header[:5]!=b'\x7fELF\x02' or header[5]!=1 or int.from_bytes(header[18:20], 'little') != machine:
        raise SystemExit(f'{name} is not a 64-bit ELF for {arch}')
if not (payload/'ui/config').is_file(): raise SystemExit('Missing fnOS UI configuration')
PY
install -m 644 "$tmp/cangzhou.fpk" "$out/$package"
(cd "$out" && sha256sum "$package" > SHA256SUMS && sha256sum -c SHA256SUMS)
printf 'Package: %s\nCommit: %s\n' "$out/$package" "$commit"
