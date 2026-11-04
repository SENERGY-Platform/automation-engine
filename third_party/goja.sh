#!/bin/sh
# Copyright (c) 2026 InfAI (CC SES)
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#    http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

# Builds third_party/goja, which go.mod puts in place of github.com/dop251/goja: goja in the
# version go.mod requires, with the patches in third_party/goja-patches applied in the order of
# their names. The import path stays github.com/dop251/goja.
#
#   third_party/goja.sh            rebuild the copy, e.g. after the version in go.mod changed
#   third_party/goja.sh check      fail if the copy differs from a rebuild (CI)
#   third_party/goja.sh save NAME  turn the edits made in the copy into the next patch, NAME.patch
set -eu

root=$(cd "$(dirname "$0")/.." && pwd)
module=github.com/dop251/goja
copy="$root/third_party/goja"
patches="$root/third_party/goja-patches"
work=$(mktemp -d)
trap 'chmod -R u+w "$work"; rm -rf "$work"' EXIT

# the version go.mod requires; the replacement does not change it
version=$(cd "$root" && go list -m -f '{{.Version}}' "$module")
pristine=$(cd "$work" && GOFLAGS=-mod=mod go mod download -json "$module@$version" | sed -n 's/^[[:space:]]*"Dir": "\(.*\)",$/\1/p')
if [ -z "$pristine" ]; then
	echo "$module@$version could not be downloaded" >&2
	exit 1
fi

# rebuild writes goja with the patches into $1, which must not exist
rebuild() {
	cp -R "$pristine" "$1"
	chmod -R u+w "$1"
	for patch in "$patches"/*.patch; do
		[ -e "$patch" ] || continue
		# outside a repository, git apply works like patch, relative to the current directory
		if ! (cd "$1" && git apply --whitespace=nowarn "$patch"); then
			echo "$(basename "$patch") does not apply to $module@$version; adapt it in the copy and save it again" >&2
			exit 1
		fi
	done
}

case "${1:-}" in
"")
	rebuild "$work/goja"
	rm -rf "$copy"
	mv "$work/goja" "$copy"
	echo "third_party/goja: $module@$version with $(ls "$patches"/*.patch 2>/dev/null | wc -l) patches"
	;;
check)
	rebuild "$work/goja"
	if ! diff -r "$work/goja" "$copy"; then
		echo "third_party/goja differs from $module@$version with the patches in third_party/goja-patches; run third_party/goja.sh, or save the edits as a patch" >&2
		exit 1
	fi
	;;
save)
	name=${2:?the name of the patch, e.g. depth-limit}
	rebuild "$work/a"
	cp -R "$copy" "$work/b"
	count=$(ls "$patches"/*.patch 2>/dev/null | wc -l)
	file="$patches/$(printf '%04d' $((count + 1)))-$name.patch"
	# exit code 1 means there are differences
	(cd "$work" && git diff --no-index --no-prefix --binary a b) >"$file" || [ $? -eq 1 ]
	if [ ! -s "$file" ]; then
		rm "$file"
		echo "the copy has no edits beyond the patches" >&2
		exit 1
	fi
	echo "saved $(basename "$file")"
	;;
*)
	echo "usage: $0 [check | save NAME]" >&2
	exit 2
	;;
esac
