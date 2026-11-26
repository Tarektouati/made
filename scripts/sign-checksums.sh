#!/bin/sh
# Called by GoReleaser after archives, SBOMs and checksums are complete.
set -eu

if [ "$#" -ne 2 ]; then
    echo "usage: sign-checksums.sh <checksums> <bundle>" >&2
    exit 2
fi

artifact=$1
bundle=$2
# A previous file must not make a signer that writes nothing look successful.
rm -f "$bundle"
cosign sign-blob "--bundle=$bundle" "$artifact" --yes
if [ ! -s "$bundle" ]; then
    printf 'cosign did not write a nonempty signature bundle: %s\n' "$bundle" >&2
    exit 1
fi
