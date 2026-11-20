#!/usr/bin/env bash
# Analyze all host CI shell scripts; the image runtime itself is entirely Go.
set -euo pipefail
cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.."
scripts=(ci/*.sh ci/tests/*.sh)
for script in "${scripts[@]}"; do
    case "$(head -n 1 "$script")" in
        '#!/bin/sh') sh -n "$script" ;;
        *) bash -n "$script" ;;
    esac
done
shellcheck --external-sources --severity=style "${scripts[@]}"
