#!/bin/sh
# SPDX-License-Identifier: AGPL-3.0-or-later
#
# Run every Fuzz target in the module for a while each.
# Usage: tools/fuzz.sh [FUZZTIME]   (default 10s; any go test -fuzztime value)
#
# Each command checks its own status; a failure stops the run at once.

fuzztime=${1:-10s}
packages=$(go list ./...) || exit 1
found=0
for package in $packages; do
	targets=$(go test -list '^Fuzz' "$package") || exit 1
	for target in $targets; do
		case $target in
			Fuzz*) ;;
			*) continue ;;
		esac
		found=$((found + 1))
		echo "fuzzing $package $target for $fuzztime"
		go test -run '^$' -fuzz "^$target\$" -fuzztime "$fuzztime" "$package" || exit 1
	done
done
if [ "$found" -eq 0 ]; then
	echo 'no fuzz targets found' >&2
	exit 1
fi
echo "$found fuzz targets passed"
