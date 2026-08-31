#!/bin/bash
# SPDX-License-Identifier: BSD-3-Clause
# SPDX-FileCopyrightText: Copyright (c) 2026 Spiral Pool Contributors
# =============================================================================
# Spiral Pool — Block Celebration LED Tests
# =============================================================================
# Covers the two parts of block-celebrate.sh that decide what a miner's LED does
# when nothing is celebrating, and which devices are eligible to celebrate at
# all. Both failed silently in the field: a wrong answer here does not raise an
# error, it leaves an LED lit for hours or discovers zero miners.
#
# Regressions pinned here:
#   1. is_cgminer probed with the plain-text "version" command and required the
#      reply to contain "CGMiner". Avalon MM firmware (Nano3s / MM319, cgminer
#      4.11.1) answers that with Code=14 "Invalid command" while answering the
#      JSON dialect normally — and it accepts plain-text ascset, so the device is
#      fully drivable. A subnet scan therefore discovered none of them.
#   2. The end of a celebration always restored the pre-celebration LED state,
#      with no way to ask for the LED to simply go dark between blocks.
#
# The script guards its main() behind a BASH_SOURCE check, so it can be sourced
# here and its functions called directly with the CGMiner API stubbed out.
# =============================================================================
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=/dev/null
source "$SCRIPT_DIR/../scripts/linux/block-celebrate.sh"
trap - EXIT  # drop the script's own cleanup trap; nothing here owns LEDs or locks

CALLS=()
set_led() { CALLS+=("$*"); return 0; }
log() { :; }
log_error() { :; }
log_success() { :; }

FAIL=0
assert() {
    if [[ "$2" == "$3" ]]; then
        echo "  PASS  $1"
    else
        echo "  FAIL  $1"
        echo "        want: [$3]"
        echo "        got : [$2]"
        FAIL=1
    fi
}

echo "restore_idle_led — what the LED shows between blocks"

# led_idle_state=off must win over a known saved state. This is the whole point
# of the setting: the operator wants dark miners, not the colour they had before.
LED_IDLE_STATE=off; CALLS=()
restore_idle_led 10.0.0.1 "3-100-90-0-255-255"
assert "off: LED off even when a state was saved" "${CALLS[*]:-}" "10.0.0.1 0 0 0 0 0 0"

# With "off" there is nothing to know, so an uncaptured state is not a reason to
# skip — unlike the restore path below.
LED_IDLE_STATE=off; CALLS=()
restore_idle_led 10.0.0.1 ""
assert "off: LED off when no state was saved" "${CALLS[*]:-}" "10.0.0.1 0 0 0 0 0 0"

LED_IDLE_STATE=restore; CALLS=()
restore_idle_led 10.0.0.1 "3-100-90-0-255-255"
assert "restore: puts the saved state back" "${CALLS[*]:-}" "10.0.0.1 3 100 90 0 255 255"

# Guessing here is what used to switch LEDs off by accident: the old fallback
# was "0-100-50-0-253-255", whose leading 0 is mode OFF, not a colour.
LED_IDLE_STATE=restore; CALLS=()
restore_idle_led 10.0.0.1 ""
assert "restore: unknown state left untouched" "${#CALLS[@]}" "0"

echo "is_cgminer — which devices are eligible to celebrate"

cgminer_cmd() { [[ "$2" == "version" ]] && echo 'STATUS=S|VERSION,CGMiner=4.9.0|' || echo ''; }
if is_cgminer 10.0.0.1; then r=yes; else r=no; fi
assert "plain-text version reply is accepted" "$r" "yes"

# The field case. Fails against the pre-fix probe, which saw only the Code=14.
cgminer_cmd() {
    if [[ "$2" == "version" ]]; then
        echo 'STATUS=E,When=1,Code=14,Msg=Invalid command|'
    elif [[ "$2" == *'"command"'*'version'* ]]; then
        echo '{"VERSION":[{"CGMiner":"4.11.1","PROD":"Avalon Nano3s"}]}'
    else
        echo ''
    fi
}
if is_cgminer 10.0.0.1; then r=yes; else r=no; fi
assert "Avalon Nano3s: plain rejected, JSON accepted" "$r" "yes"

cgminer_cmd() { echo ''; }
if is_cgminer 10.0.0.1; then r=yes; else r=no; fi
assert "a host that answers nothing is not a miner" "$r" "no"

echo
if [[ $FAIL -eq 0 ]]; then
    echo "ALL CELEBRATION LED TESTS PASSED"
else
    echo "CELEBRATION LED TESTS FAILED"
fi
exit $FAIL
