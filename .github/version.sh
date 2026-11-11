#!/usr/bin/env bash
# Prints the version embedded into builds: the tag without "v" on tag builds,
# otherwise dev-<short sha>.
set -euo pipefail
if [[ "${GITHUB_REF:-}" == refs/tags/v* ]]; then
	echo "${GITHUB_REF_NAME#v}"
else
	echo "dev-$(git rev-parse --short HEAD)"
fi
