#!/usr/bin/env bash
set -euo pipefail
: "${GITEA_TOKEN:?Gitea Actions token is unavailable}"
: "${GITHUB_API_URL:?Gitea API URL is unavailable}"
: "${GITHUB_REPOSITORY:?Gitea repository is unavailable}"
: "${RELEASE_TAG:?Release tag is unavailable}"
[[ "$RELEASE_TAG" =~ ^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ ]]
version="${RELEASE_TAG#v}"
notes="releases/$version/README.md"
[ -s "$notes" ] || { echo 'Missing release notes' >&2; exit 1; }
[ "$(sed -n 's/^version=//p' package/manifest)" = "$version" ]
[[ "$GITEA_TOKEN" =~ ^[A-Za-z0-9._-]+$ ]] || { echo 'Unsupported token format' >&2; exit 1; }
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
umask 077
printf 'header = "Authorization: token %s"\n' "$GITEA_TOKEN" > "$tmp/curl.conf"
api="${GITHUB_API_URL%/}/repos/${GITHUB_REPOSITORY}"
status="$(curl --config "$tmp/curl.conf" --silent --show-error --output "$tmp/release.json" --write-out '%{http_code}' "$api/releases/tags/$RELEASE_TAG")"
if [ "$status" = 404 ]; then
  jq -n --arg tag "$RELEASE_TAG" --rawfile body "$notes" '{tag_name:$tag,name:("藏舟 " + $tag),body:$body,draft:false,prerelease:false}' > "$tmp/create.json"
  curl --config "$tmp/curl.conf" --fail --silent --show-error -H 'Content-Type: application/json' -X POST --data-binary "@$tmp/create.json" "$api/releases" > "$tmp/release.json"
elif [ "$status" != 200 ]; then
  echo "Unable to inspect existing release (HTTP $status)" >&2; exit 1
fi
jq -e --arg tag "$RELEASE_TAG" '.tag_name == $tag and .draft == false and .prerelease == false' "$tmp/release.json" >/dev/null
release_id="$(jq -er '.id | numbers' "$tmp/release.json")"
for name in "cangzhou-$version-x86.fpk" "cangzhou-$version-arm.fpk" SHA256SUMS; do
  asset="artifacts/$name"
  [ -s "$asset" ] || { echo "Missing asset: $name" >&2; exit 1; }
  count="$(jq --arg name "$name" '[.assets[]? | select(.name == $name)] | length' "$tmp/release.json")"
  if [ "$count" -gt 1 ]; then echo "Duplicate existing asset: $name" >&2; exit 1; fi
  if [ "$count" -eq 1 ]; then
    existing="$(jq -er --arg name "$name" '.assets[] | select(.name == $name) | .browser_download_url' "$tmp/release.json")"
    # Public repository: do not forward the Gitea token to a download URL.
    curl --fail --silent --show-error --location "$existing" -o "$tmp/existing"
    if [ "$(sha256sum "$asset" | cut -d' ' -f1)" != "$(sha256sum "$tmp/existing" | cut -d' ' -f1)" ]; then
      echo "Existing asset differs: $name; refusing to overwrite" >&2; exit 1
    fi
    continue
  fi
  curl --config "$tmp/curl.conf" --fail --silent --show-error -F "attachment=@$asset" "$api/releases/$release_id/assets?name=$name" > /dev/null
done
