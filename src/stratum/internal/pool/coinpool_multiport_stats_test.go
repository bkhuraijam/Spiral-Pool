// SPDX-License-Identifier: BSD-3-Clause
// SPDX-FileCopyrightText: Copyright (c) 2026 Spiral Pool Contributors

// Package pool - Regression tests for per-coin stats reported to /api/pools.
//
// Both cases here were reported together against 2.7.0: a coin serving real
// smart-port hashrate reported connectedMiners=0 and poolEffort=0. They have
// separate causes and separate fixes.
package pool

import (
	"testing"
	"time"

	"github.com/spiralpool/stratum/internal/api"
	"github.com/spiralpool/stratum/internal/coin"
	"github.com/spiralpool/stratum/internal/config"
	"github.com/spiralpool/stratum/internal/shares"
	"github.com/spiralpool/stratum/internal/stratum"
	"github.com/spiralpool/stratum/pkg/protocol"
	"go.uber.org/zap"
)

// fakeMultiPortSessions reports a fixed smart-port session count for one coin.
type fakeMultiPortSessions struct {
	counts map[string]int64
}

func (f *fakeMultiPortSessions) GetMultiPortConnectionsForCoin(symbol string) []api.WorkerConnection {
	return nil
}

func (f *fakeMultiPortSessions) GetMultiPortConnectionCountForCoin(symbol string) int64 {
	return f.counts[symbol]
}

var _ api.MultiPortSessionProvider = (*fakeMultiPortSessions)(nil)

// newStatsTestCoinPool builds a CoinPool with a real (unstarted) stratum server,
// so Stats().ActiveConnections is a truthful 0 — the situation of a coin that is
// only ever reached through the smart port.
func newStatsTestCoinPool(t *testing.T, symbol string) *CoinPool {
	t.Helper()
	return &CoinPool{
		logger:        zap.NewNop().Sugar(),
		poolID:        "test-pool",
		coinSymbol:    symbol,
		stratumServer: stratum.NewServer(&config.StratumConfig{}, zap.NewNop()),
	}
}

// TestGetConnections_SmartPortOnlyCoin is the reported defect: a coin with no
// dedicated-port traffic reported 0 connected miners while genuinely serving
// smart-port sessions, because its multi-port session provider was never wired.
func TestGetConnections_SmartPortOnlyCoin(t *testing.T) {
	t.Parallel()

	cp := newStatsTestCoinPool(t, "BCH")

	// Unwired: this is what a late-started coin pool looked like in 2.7.0.
	if got := cp.GetConnections(); got != 0 {
		t.Fatalf("precondition: unwired pool should report 0, got %d", got)
	}

	// Wired, as startMultiPort() does for pools present at startup and as the
	// retry loop now does for pools that come online later.
	cp.SetMultiPortSessionProvider(&fakeMultiPortSessions{
		counts: map[string]int64{"BCH": 5},
	})

	if got := cp.GetConnections(); got != 5 {
		t.Errorf("wired pool should include 5 smart-port sessions, got %d", got)
	}
}

// TestGetConnections_OnlyCountsOwnCoin guards the symbol match: a pool must not
// absorb sessions currently assigned to a different coin in the rotation.
func TestGetConnections_OnlyCountsOwnCoin(t *testing.T) {
	t.Parallel()

	provider := &fakeMultiPortSessions{counts: map[string]int64{"DGB": 5}}

	bch := newStatsTestCoinPool(t, "BCH")
	bch.SetMultiPortSessionProvider(provider)
	if got := bch.GetConnections(); got != 0 {
		t.Errorf("BCH should not count DGB's sessions, got %d", got)
	}

	dgb := newStatsTestCoinPool(t, "DGB")
	dgb.SetMultiPortSessionProvider(provider)
	if got := dgb.GetConnections(); got != 5 {
		t.Errorf("DGB should count its own 5 sessions, got %d", got)
	}
}

// newEffortTestCoinPool builds a CoinPool with the two inputs GetPoolEffort
// needs before it reaches the last-block branch: a network difficulty and a
// non-zero pool hashrate.
func newEffortTestCoinPool(t *testing.T, hashrate float64) *CoinPool {
	t.Helper()
	coinImpl, err := coin.Create("DGB")
	if err != nil {
		t.Fatalf("coin.Create: %v", err)
	}
	db := newCriticalMockDB()
	db.poolHashrate = hashrate

	validator := shares.NewValidatorWithCoin(
		func(string) (*protocol.Job, bool) { return nil, false },
		coinImpl,
	)
	validator.SetNetworkDifficulty(1000)

	cp := newCriticalTestCoinPool(&criticalMockNodeMgr{}, db)
	cp.coin = coinImpl
	cp.shareValidator = validator
	return cp
}

// TestGetPoolEffort_NoBlockEverFound is the second reported defect: effort read
// 0 on a coin submitting shares continuously, because a pool that has never
// found a block has a zero lastBlockTime and the function bailed out on it.
func TestGetPoolEffort_NoBlockEverFound(t *testing.T) {
	t.Parallel()

	cp := newEffortTestCoinPool(t, 17.25e12) // ~17.25 TH/s, as reported
	cp.startTime = time.Now().Add(-8 * time.Hour)

	// lastBlockTime deliberately left zero: no block found, none in the database.
	if got := cp.GetPoolEffort(); got <= 0 {
		t.Errorf("effort should be measured from round start when no block was "+
			"ever found, got %v", got)
	}
}

// TestGetPoolEffort_UsesLastBlockWhenKnown verifies the fallback did not
// displace the normal path: a known last block still anchors the round.
func TestGetPoolEffort_UsesLastBlockWhenKnown(t *testing.T) {
	t.Parallel()

	cp := newEffortTestCoinPool(t, 17.25e12)
	cp.startTime = time.Now().Add(-8 * time.Hour)

	cp.lastBlockTimeMu.Lock()
	cp.lastBlockTime = time.Now().Add(-1 * time.Hour)
	cp.lastBlockTimeMu.Unlock()

	fromBlock := cp.GetPoolEffort()

	cp.lastBlockTimeMu.Lock()
	cp.lastBlockTime = time.Time{}
	cp.lastBlockTimeMu.Unlock()

	fromStart := cp.GetPoolEffort()

	// The 8h round start must yield materially more effort than the 1h block.
	if fromStart <= fromBlock {
		t.Errorf("8h round start (%v) should exceed 1h since last block (%v)",
			fromStart, fromBlock)
	}
}

// TestGetPoolEffort_ZeroBeforeStart confirms the fallback does not invent
// effort for a pool whose Start() has not run.
func TestGetPoolEffort_ZeroBeforeStart(t *testing.T) {
	t.Parallel()

	cp := newEffortTestCoinPool(t, 17.25e12)
	// startTime and lastBlockTime both zero.
	if got := cp.GetPoolEffort(); got != 0 {
		t.Errorf("effort should be 0 before Start(), got %v", got)
	}
}
