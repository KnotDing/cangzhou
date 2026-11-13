#!/usr/bin/env bash
# Build only from the checked-in template. The source archive comes from HEAD.
set -euo pipefail
root="$(cd "$(dirname "$0")/.." && pwd)"
cd "$root"
: "${FNPACK:?Set FNPACK to the verified fnpack executable}"
: "${KOPIA:?Set KOPIA to the verified Linux x86-64 Kopia executable}"
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
mkdir -p "$tmp/package/app" "$tmp/package/wizard"
cp "$KOPIA" "$tmp/package/app/kopia"
chmod 755 "$tmp/package/app/kopia"
(cd "$tmp/source/core" && GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -mod=vendor -trimpath -ldflags='-s -w' -o "$tmp/package/app/cangzhou-server" ./cmd/cangzhou)
(cd "$tmp" && "$FNPACK" build -d "$tmp/package")
package="cangzhou-$version-x86.fpk"
source="cangzhou-$version-source.tar.gz"
install -m 644 "$tmp/cangzhou.fpk" "$out/$package"
git archive --format=tar --prefix="cangzhou-$version/" "$commit" | gzip -n > "$out/$source"
mkdir -p "$tmp/verify" "$tmp/payload"
tar -xzf "$out/$package" -C "$tmp/verify"
tar -xzf "$tmp/verify/app.tgz" -C "$tmp/payload"
python3 - "$tmp/verify/manifest" "$tmp/payload" "$version" <<'PY'
import pathlib, re, sys
manifest, payload, version = pathlib.Path(sys.argv[1]), pathlib.Path(sys.argv[2]), sys.argv[3]
s = manifest.read_text(encoding='utf-8')
for key, expected in [('appname','cangzhou'), ('version',version), ('platform','x86')]:
    match = re.search(r'^\s*' + key + r'\s*=\s*(.*?)\s*$', s, re.M)
    if not match or match.group(1) != expected: raise SystemExit(f'Unexpected manifest {key}')
for name in ('kopia','cangzhou-server'):
    p=payload/name
    if not p.is_file(): raise SystemExit(f'Missing payload {name}')
    header=p.read_bytes()[:20]
    if header[:5]!=b'\x7fELF\x02' or header[5]!=1 or header[18:20]!=b'\x3e\x00':
        raise SystemExit(f'{name} is not Linux x86-64 ELF')
if not (payload/'ui/config').is_file(): raise SystemExit('Missing fnOS UI configuration')
PY
(cd "$out" && sha256sum "$package" "$source" > SHA256SUMS && sha256sum -c SHA256SUMS)
printf 'Package: %s\nSource: %s\nCommit: %s\n' "$out/$package" "$out/$source" "$commit"
