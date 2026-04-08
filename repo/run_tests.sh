#!/usr/bin/env bash
# run_tests.sh — Execute all test suites and print a pass/fail summary.
#
# Usage:
#   ./run_tests.sh            # run all suites
#   ./run_tests.sh -v         # verbose (stream test output)
#   ./run_tests.sh -race      # enable Go race detector
#   ./run_tests.sh -v -race   # both

set -euo pipefail

# ---------------------------------------------------------------------------
# Colour helpers
# ---------------------------------------------------------------------------
GREEN="\033[0;32m"
RED="\033[0;31m"
YELLOW="\033[0;33m"
RESET="\033[0m"

pass() { echo -e "${GREEN}PASS${RESET}  $1"; }
fail() { echo -e "${RED}FAIL${RESET}  $1"; }
info() { echo -e "${YELLOW}----${RESET}  $1"; }

# ---------------------------------------------------------------------------
# Parse flags
# ---------------------------------------------------------------------------
VERBOSE=""
RACE=""
for arg in "$@"; do
  case "$arg" in
    -v)     VERBOSE="-v" ;;
    -race)  RACE="-race" ;;
    *)      echo "Unknown flag: $arg"; exit 1 ;;
  esac
done

# ---------------------------------------------------------------------------
# Locate the project root (the directory containing this script).
# ---------------------------------------------------------------------------
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SCRIPT_DIR"

# Export the migration path so test DBs can find the schema regardless of the
# working directory from which tests run.
export MIGRATION_PATH="$SCRIPT_DIR/migrations/001_schema.sql"

# ---------------------------------------------------------------------------
# Suite definitions: (label, package_path)
# ---------------------------------------------------------------------------
declare -a SUITE_LABELS=(
  "Unit tests (state machine, inventory, auth, validation, rate-limiter)"
  "API functional tests (normal inputs, bad params, permission errors)"
  "Integration tests — auth & session"
  "Integration tests — materials, comments, favorites"
  "Integration tests — orders & fulfillment"
  "Integration tests — distribution & ledger"
  "Integration tests — messaging & inbox"
  "Integration tests — moderation queue"
  "Integration tests — admin panel"
)
declare -a SUITE_PKGS=(
  "./unit_tests/..."
  "./API_tests/..."
  "./internal/integration/"
  "./internal/integration/"
  "./internal/integration/"
  "./internal/integration/"
  "./internal/integration/"
  "./internal/integration/"
  "./internal/integration/"
)
# Optional test-name filter per suite (empty = no filter, runs all).
declare -a SUITE_RUN=(
  ""
  ""
  "TestAuth|TestHealth|TestLogin|TestLogout|TestRegister"
  "TestMaterial|TestAddComment|TestReport|TestFavorite|TestShareLink|TestRate"
  "TestPlaceOrder|TestOrderDetail|TestOrderCancel|TestConfirmPayment|TestReturn"
  "TestDistribution|TestIssue|TestReturn|TestExchange|TestReissue|TestLedger|TestCustody"
  "TestMessag|TestInbox|TestDND|TestSubscri|TestBadge"
  "TestModeration|TestApprove|TestRemove"
  "TestAdmin"
)

# ---------------------------------------------------------------------------
# Run each suite and collect results.
# ---------------------------------------------------------------------------
TOTAL=0
PASSED=0
FAILED=0
FAILED_SUITES=()

echo ""
echo "========================================================"
echo " w2t86 — Test Runner"
echo " $(date '+%Y-%m-%d %H:%M:%S')"
echo "========================================================"
echo ""

for i in "${!SUITE_LABELS[@]}"; do
  label="${SUITE_LABELS[$i]}"
  pkg="${SUITE_PKGS[$i]}"
  run_filter="${SUITE_RUN[$i]}"

  TOTAL=$((TOTAL + 1))
  info "Running: $label"

  # Build the go test command.
  cmd=(go test $RACE $VERBOSE -timeout 120s)
  if [[ -n "$run_filter" ]]; then
    cmd+=(-run "$run_filter")
  fi
  cmd+=("$pkg")

  # Capture output; on failure print it.
  tmp=$(mktemp)
  if "${cmd[@]}" >"$tmp" 2>&1; then
    pass "$label"
    PASSED=$((PASSED + 1))
    # In verbose mode the output was already streamed; otherwise stay quiet.
    if [[ -n "$VERBOSE" ]]; then
      cat "$tmp"
    fi
  else
    fail "$label"
    FAILED=$((FAILED + 1))
    FAILED_SUITES+=("$label")
    # Always show output on failure.
    cat "$tmp"
  fi
  rm -f "$tmp"
  echo ""
done

# ---------------------------------------------------------------------------
# Summary
# ---------------------------------------------------------------------------
echo "========================================================"
echo " Results: ${PASSED}/${TOTAL} suites passed"
echo "========================================================"

if [[ $FAILED -gt 0 ]]; then
  echo ""
  echo -e "${RED}Failed suites:${RESET}"
  for s in "${FAILED_SUITES[@]}"; do
    echo -e "  ${RED}✘${RESET} $s"
  done
  echo ""
  exit 1
else
  echo ""
  echo -e "${GREEN}All suites passed.${RESET}"
  echo ""
  exit 0
fi
