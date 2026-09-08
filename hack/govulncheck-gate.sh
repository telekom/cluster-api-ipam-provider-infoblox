#!/usr/bin/env bash
#
# Gate on the reachable vulnerabilities that govulncheck reports.
#
# A finding in a module fails this script. A finding in the Go standard library
# only warns. Only a bump of the Go toolchain clears a standard library finding,
# never a change in this repository, so such a finding must not block a pull
# request. The scan still runs in full and this script reports every reachable
# finding, as an error annotation or as a warning annotation.
#
# Usage: hack/govulncheck-gate.sh <govulncheck-json-file>
#
# The file holds the output of `govulncheck -format json`. That command exits 0
# even when it reports a vulnerability, so this script provides the exit code.

set -euo pipefail

readonly REPORT="${1:?usage: $0 <govulncheck-json-file>}"

if [[ ! -s "${REPORT}" ]]; then
	echo "govulncheck produced no output: ${REPORT}" >&2
	exit 1
fi

if ! command -v jq >/dev/null 2>&1; then
	echo "jq is required" >&2
	exit 1
fi

# govulncheck writes a stream of JSON objects. An "osv" object describes a
# vulnerability. A "finding" object reports one call path to it.
#
# A finding is reachable when its first trace frame names a function. That frame
# also names the module that holds the vulnerable symbol, which is "stdlib" for
# the standard library.
#
# One vulnerability can produce both a module finding and a standard library
# finding. GO-2026-5026 is such a case. Such a vulnerability blocks, because a
# module update clears the module part of it.
#
# Each output line is: BLOCK|WARN <id> <modules> <fixed versions> <summary>
classified="$(jq -r -n '
  [inputs] as $objects
  | ($objects
      | map(select(.osv) | {key: .osv.id, value: (.osv.summary // "no summary")})
      | from_entries) as $summaries
  | $objects
  | map(select(.finding) | .finding | select(.trace[0].function != null))
  | map({id: .osv, module: .trace[0].module, fixed: (.fixed_version // "unknown")})
  | group_by(.id)
  | map({
      id: .[0].id,
      modules: (map(select(.module != "stdlib") | .module) | unique),
      fixed: (map(.fixed) | unique | sort | join(", ")),
      summary: ($summaries[.[0].id] // "no summary"),
    })
  | sort_by(.id)
  | map(
      if (.modules | length) > 0
      then "BLOCK\t\(.id)\t\(.modules | join(", "))\t\(.fixed)\t\(.summary)"
      else "WARN\t\(.id)\t-\t\(.fixed)\t\(.summary)"
      end)
  | .[]
' <"${REPORT}")"

blocking_count=0
warning_count=0

while IFS=$'\t' read -r kind id modules fixed summary; do
	[[ -n "${kind}" ]] || continue
	case "${kind}" in
	WARN)
		warning_count=$((warning_count + 1))
		echo "::warning::${id}: ${summary}. Reachable in the Go standard library, fixed in Go ${fixed}. This does not fail the check, because only a bump of the Go toolchain clears it."
		;;
	BLOCK)
		blocking_count=$((blocking_count + 1))
		echo "::error::${id}: ${summary}. Reachable in ${modules}, fixed in ${fixed}."
		;;
	*)
		echo "unexpected classifier output: ${kind}" >&2
		exit 1
		;;
	esac
done <<<"${classified}"

if ((warning_count > 0)); then
	echo "govulncheck: ${warning_count} reachable standard library vulnerabilities, reported as warnings"
fi

if ((blocking_count > 0)); then
	echo "govulncheck: ${blocking_count} reachable module vulnerabilities"
	echo "Update the affected modules to the fixed versions above."
	exit 1
fi

echo "govulncheck: no reachable module vulnerabilities"
