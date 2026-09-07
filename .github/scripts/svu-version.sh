#!/bin/sh

set -eu

: "${GITHUB_OUTPUT:?GITHUB_OUTPUT is required}"

current="$(svu current 2>/dev/null || true)"
next="$(svu next)"

if ! printf '%s\n' "${next}" |
	grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+([+-][0-9A-Za-z.-]+)?$'; then
	echo "::error::svu returned an invalid semantic version: ${next}"
	exit 1
fi

release=false
if [ "${next}" != "${current}" ]; then
	release=true
fi

{
	echo "current=${current}"
	echo "next=${next}"
	echo "release=${release}"
	echo "version=${next#v}"
} >>"${GITHUB_OUTPUT}"

echo "Current version: ${current:-none}"
echo "Next version: ${next}"
echo "Release required: ${release}"
