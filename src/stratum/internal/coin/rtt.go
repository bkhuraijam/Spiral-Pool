// SPDX-License-Identifier: BSD-3-Clause
// SPDX-FileCopyrightText: Copyright (c) 2026 Spiral Pool Contributors

// Package coin - Real Time Target (RTT) validation for eCash.
//
// eCash uses Avalanche post-consensus with Real Time Targeting (RTT).
// Blocks must meet both the normal PoW target AND the RTT target.
// RTT data comes from the block template in the "rtt" field.
package coin

import (
	"encoding/hex"
	"fmt"
	"math"
	"math/big"

	"github.com/spiralpool/stratum/internal/daemon"
)

// rttFilterCoefficients for each time window.
var rttFilterCoefficients = []float64{
	5.0372626864e-11, // 1-block window (index 0, used after Nov 2025 upgrade)
	4.9192018423e-14, // 2-blocks window (index 1)
	4.8039080491e-17, // 5-blocks window (index 2)
	4.9192018423e-19, // 11-blocks window (index 3)
	4.6913164542e-20, // 17-blocks window (index 4)
}

// IsRTTDataValid checks if the RTT data from the node is usable.
func IsRTTDataValid(template *daemon.BlockTemplate) bool {
	if template == nil || template.RTT == nil {
		return false
	}
	if len(template.RTT.PrevHeaderTime) < 2 {
		return false
	}

	firstTs := template.RTT.PrevHeaderTime[0]
	for i := 1; i < len(template.RTT.PrevHeaderTime); i++ {
		if template.RTT.PrevHeaderTime[i] != firstTs {
			return true
		}
	}
	return false
}

// rttCompactToBig converts a compact target representation to a big.Int.
func rttCompactToBig(compact string) (*big.Int, error) {
	if len(compact) != 8 {
		return nil, fmt.Errorf("compact target must be 8 hex characters, got %d", len(compact))
	}
	data, err := hex.DecodeString(compact)
	if err != nil {
		return nil, fmt.Errorf("invalid hex in compact target: %w", err)
	}
	exponent := int(data[0])
	mantissa := new(big.Int).SetBytes(data[1:4])
	if mantissa.Sign() == 0 {
		return big.NewInt(0), nil
	}
	if exponent >= 3 {
		mantissa.Lsh(mantissa, uint((exponent-3)*8))
	} else {
		mantissa.Rsh(mantissa, uint((3-exponent)*8))
	}
	return mantissa, nil
}

// ComputeRTT computes the Real Time Target using the eCash formula.
func ComputeRTT(template *daemon.BlockTemplate, currentTime int64) (*big.Int, error) {
	if template == nil || template.RTT == nil {
		return nil, fmt.Errorf("no RTT data in template")
	}
	if len(template.RTT.PrevHeaderTime) == 0 {
		return nil, fmt.Errorf("empty prevheadertime array")
	}

	prevTarget, err := rttCompactToBig(template.RTT.PrevBits)
	if err != nil {
		return nil, fmt.Errorf("parsing prevbits '%s': %w", template.RTT.PrevBits, err)
	}

	nextTarget, err := rttCompactToBig(template.Bits)
	if err != nil {
		return nil, fmt.Errorf("parsing bits '%s': %w", template.Bits, err)
	}

	numWindows := len(template.RTT.PrevHeaderTime)

	// Before Nov 2025 upgrade: 4 windows, skip first coefficient
	// After Nov 2025 upgrade: 5 windows, use all coefficients
	filterIndex := 0
	if numWindows <= 4 {
		filterIndex = 1
	}

	prevTargetFloat := new(big.Float).SetInt(prevTarget)
	prevWindowTimestamp := int64(0)

	for i := 0; i < numWindows; i++ {
		if filterIndex >= len(rttFilterCoefficients) {
			break
		}

		timestamp := template.RTT.PrevHeaderTime[i]

		if timestamp == 0 {
			filterIndex++
			continue
		}
		if i > 0 && timestamp == prevWindowTimestamp {
			filterIndex++
			continue
		}
		prevWindowTimestamp = timestamp

		diffTime := currentTime - timestamp
		if diffTime < 1 {
			diffTime = 1
		}

		coeff := rttFilterCoefficients[filterIndex]
		filterIndex++

		diffTimePow5 := math.Pow(float64(diffTime), 5)
		result := new(big.Float).Mul(prevTargetFloat, big.NewFloat(coeff))
		result.Mul(result, big.NewFloat(diffTimePow5))

		target, _ := result.Int(nil)

		if target.Cmp(nextTarget) < 0 {
			nextTarget = target
		}
	}

	return nextTarget, nil
}

// GetRTTTarget returns the RTT target, preferring the node's pre-computed nexttarget.
func GetRTTTarget(template *daemon.BlockTemplate, currentTime int64) (*big.Int, error) {
	if template == nil || template.RTT == nil {
		return nil, fmt.Errorf("no RTT data in template")
	}

	if !IsRTTDataValid(template) {
		return nil, fmt.Errorf("RTT data malformed - all timestamps identical")
	}

	// Prefer the node's pre-computed nexttarget
	if template.RTT.NextTarget != "" {
		target, err := rttCompactToBig(template.RTT.NextTarget)
		if err == nil && target.Sign() > 0 {
			return target, nil
		}
	}

	// Fallback: compute locally
	return ComputeRTT(template, currentTime)
}

// RTTRawData contains raw RTT fields from a Job (avoids daemon import dependency).
type RTTRawData struct {
	PrevHeaderTime []int64
	PrevBits       string
	NextTarget     string
	Bits           string
}

// IsRTTDataValidRaw checks if raw RTT data is usable.
func IsRTTDataValidRaw(rtt *RTTRawData) bool {
	if rtt == nil || len(rtt.PrevHeaderTime) < 2 {
		return false
	}
	firstTs := rtt.PrevHeaderTime[0]
	for i := 1; i < len(rtt.PrevHeaderTime); i++ {
		if rtt.PrevHeaderTime[i] != firstTs {
			return true
		}
	}
	return false
}

// ComputeRTTRaw computes RTT from raw fields.
func ComputeRTTRaw(rtt *RTTRawData, currentTime int64) (*big.Int, error) {
	if rtt == nil || len(rtt.PrevHeaderTime) == 0 {
		return nil, fmt.Errorf("no RTT data")
	}

	prevTarget, err := rttCompactToBig(rtt.PrevBits)
	if err != nil {
		return nil, fmt.Errorf("parsing prevbits '%s': %w", rtt.PrevBits, err)
	}

	nextTarget, err := rttCompactToBig(rtt.Bits)
	if err != nil {
		return nil, fmt.Errorf("parsing bits '%s': %w", rtt.Bits, err)
	}

	numWindows := len(rtt.PrevHeaderTime)
	filterIndex := 0
	if numWindows <= 4 {
		filterIndex = 1
	}

	prevTargetFloat := new(big.Float).SetInt(prevTarget)
	prevWindowTimestamp := int64(0)

	for i := 0; i < numWindows; i++ {
		if filterIndex >= len(rttFilterCoefficients) {
			break
		}
		timestamp := rtt.PrevHeaderTime[i]
		if timestamp == 0 {
			filterIndex++
			continue
		}
		if i > 0 && timestamp == prevWindowTimestamp {
			filterIndex++
			continue
		}
		prevWindowTimestamp = timestamp
		diffTime := currentTime - timestamp
		if diffTime < 1 {
			diffTime = 1
		}
		coeff := rttFilterCoefficients[filterIndex]
		filterIndex++
		diffTimePow5 := math.Pow(float64(diffTime), 5)
		result := new(big.Float).Mul(prevTargetFloat, big.NewFloat(coeff))
		result.Mul(result, big.NewFloat(diffTimePow5))
		target, _ := result.Int(nil)
		if target.Cmp(nextTarget) < 0 {
			nextTarget = target
		}
	}
	return nextTarget, nil
}

// GetRTTTargetRaw returns RTT target from raw fields.
func GetRTTTargetRaw(rtt *RTTRawData, currentTime int64) (*big.Int, error) {
	if !IsRTTDataValidRaw(rtt) {
		return nil, fmt.Errorf("RTT data malformed")
	}
	if rtt.NextTarget != "" {
		target, err := rttCompactToBig(rtt.NextTarget)
		if err == nil && target.Sign() > 0 {
			return target, nil
		}
	}
	return ComputeRTTRaw(rtt, currentTime)
}

// CheckRTTTargetRaw validates a block hash against RTT from raw fields.
func CheckRTTTargetRaw(blockHashBE []byte, rtt *RTTRawData, currentTime int64) (bool, error) {
	rttTarget, err := GetRTTTargetRaw(rtt, currentTime)
	if err != nil {
		return false, err
	}
	hashInt := new(big.Int).SetBytes(blockHashBE)
	return hashInt.Cmp(rttTarget) <= 0, nil
}


// CheckRTTTarget validates if a block hash meets the RTT target.
func CheckRTTTarget(blockHashBE []byte, template *daemon.BlockTemplate, currentTime int64) (bool, error) {
	rttTarget, err := GetRTTTarget(template, currentTime)
	if err != nil {
		return false, err
	}

	hashInt := new(big.Int).SetBytes(blockHashBE)
	return hashInt.Cmp(rttTarget) <= 0, nil
}
