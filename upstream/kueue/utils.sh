#!/usr/bin/env bash
# shellcheck disable=SC2164

apply_patches() {
	for patch in patch/*.patch; do
		pushd src >/dev/null
		# Check if patch can be applied (not already applied)
		if git apply --check ../"$patch" 2>/dev/null; then
			echo "Applying $patch"
			git apply ../"$patch" || {
				echo "Error: Failed to apply $patch"
				popd >/dev/null
				return 1
			}
		else
			echo "Skipping $patch (already applied or conflicts)"
		fi
		popd >/dev/null
	done
}
