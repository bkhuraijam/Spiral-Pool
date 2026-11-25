// SPDX-License-Identifier: BSD-3-Clause
// SPDX-FileCopyrightText: Copyright (c) 2026 Spiral Pool Contributors

// Package coin - DeVault (DVT) implementation.
//
// DeVault uses SHA256d for proof of work (LWMA difficulty, 120s blocks).
// It is a Bitcoin Cash family fork: addresses are CashAddr-only with the
// "devault:" prefix (base58 is retained only for WIF, not addresses).
// The CashAddr checksum algorithm is identical to BCH's, so this file
// reuses the package-level helpers defined in bitcoincash.go.
package coin

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"math/big"
	"strings"
)

// DeVault address constants
const (
	DVTP2PKHVersion byte = 0x00 // CashAddr 'q...' addresses
	DVTP2SHVersion  byte = 0x05 // CashAddr 'p...' addresses

	// CashAddr human-readable prefixes (from chainparams.cpp)
	DVTCashAddrPrefix        = "devault" // mainnet
	DVTCashAddrRegtestPrefix = "dvreg"   // regtest
)

// DeVault network parameters
const (
	DVTDefaultP2PPort = 33039
	DVTDefaultRPCPort = 3339
)

// DVTGenesisBlockHash is the genesis block hash for chain verification.
const DVTGenesisBlockHash = "0000000038e62464371566f6a8d35c01aa54a7da351b2dbf85d92f30357f3a90"

// DVT implements the Coin interface for DeVault.
type DVT struct{}

// NewDVT creates a new DeVault coin instance.
func NewDVT() *DVT {
	return &DVT{}
}

// Symbol returns the ticker symbol.
func (c *DVT) Symbol() string {
	return "DVT"
}

// Name returns the full coin name.
func (c *DVT) Name() string {
	return "DeVault"
}

// ValidateAddress validates a DeVault address.
func (c *DVT) ValidateAddress(address string) error {
	_, _, err := c.DecodeAddress(address)
	return err
}

// DecodeAddress decodes a DeVault CashAddr address to its hash and type.
// DeVault is CashAddr-only; legacy base58 addresses are not used.
func (c *DVT) DecodeAddress(address string) ([]byte, AddressType, error) {
	if address == "" {
		return nil, AddressTypeUnknown, fmt.Errorf("empty address")
	}

	addrLower := strings.ToLower(address)
	// CashAddr: "devault:" mainnet, "dvreg:" regtest, or bare q.../p...
	if strings.HasPrefix(addrLower, DVTCashAddrPrefix+":") ||
		strings.HasPrefix(addrLower, DVTCashAddrRegtestPrefix+":") ||
		strings.HasPrefix(addrLower, "q") ||
		strings.HasPrefix(addrLower, "p") {
		return c.decodeCashAddr(address)
	}

	return nil, AddressTypeUnknown, fmt.Errorf(
		"DeVault uses CashAddr format (devault:...) — unsupported address: %s", address)
}

// decodeCashAddr decodes a CashAddr format address, reusing the BCH-family
// polymod checksum helpers defined in bitcoincash.go.
func (c *DVT) decodeCashAddr(address string) ([]byte, AddressType, error) {
	addrLower := strings.ToLower(address)

	prefix := DVTCashAddrPrefix
	if strings.HasPrefix(addrLower, DVTCashAddrRegtestPrefix+":") {
		prefix = DVTCashAddrRegtestPrefix
	}

	// Add prefix if a bare q.../p... address was supplied
	if !strings.Contains(addrLower, ":") {
		addrLower = prefix + ":" + addrLower
	}

	parts := strings.Split(addrLower, ":")
	if len(parts) != 2 {
		return nil, AddressTypeUnknown, fmt.Errorf("invalid CashAddr format")
	}

	// Reuse the shared, checksum-verifying decoder from bitcoincash.go
	data, err := decodeCashAddrDataWithPrefix(parts[0], parts[1])
	if err != nil {
		return nil, AddressTypeUnknown, err
	}

	if len(data) < 21 {
		return nil, AddressTypeUnknown, fmt.Errorf("invalid CashAddr data length")
	}

	// First byte encodes the address type; next 20 bytes are the hash
	versionByte := data[0]
	hash := data[1:21]

	switch versionByte & 0x78 {
	case 0x00: // P2PKH (q...)
		return hash, AddressTypeP2PKH, nil
	case 0x08: // P2SH (p...)
		return hash, AddressTypeP2SH, nil
	default:
		return nil, AddressTypeUnknown, fmt.Errorf("unknown CashAddr type: 0x%02x", versionByte)
	}
}

// BuildCoinbaseScript builds the output script for the coinbase transaction.
func (c *DVT) BuildCoinbaseScript(params CoinbaseParams) ([]byte, error) {
	hash, addrType, err := c.DecodeAddress(params.PoolAddress)
	if err != nil {
		return nil, fmt.Errorf("invalid pool address: %w", err)
	}

	switch addrType {
	case AddressTypeP2PKH:
		// OP_DUP OP_HASH160 <20 bytes> OP_EQUALVERIFY OP_CHECKSIG
		script := make([]byte, 25)
		script[0] = 0x76 // OP_DUP
		script[1] = 0xa9 // OP_HASH160
		script[2] = 0x14 // PUSH 20 bytes
		copy(script[3:23], hash)
		script[23] = 0x88 // OP_EQUALVERIFY
		script[24] = 0xac // OP_CHECKSIG
		return script, nil

	case AddressTypeP2SH:
		// OP_HASH160 <20 bytes> OP_EQUAL
		script := make([]byte, 23)
		script[0] = 0xa9 // OP_HASH160
		script[1] = 0x14 // PUSH 20 bytes
		copy(script[2:22], hash)
		script[22] = 0x87 // OP_EQUAL
		return script, nil

	default:
		return nil, fmt.Errorf("unsupported address type: %v", addrType)
	}
}

// SerializeBlockHeader serializes an 80-byte block header.
func (c *DVT) SerializeBlockHeader(header *BlockHeader) []byte {
	buf := make([]byte, 80)
	binary.LittleEndian.PutUint32(buf[0:4], header.Version)
	copy(buf[4:36], header.PreviousBlockHash)
	copy(buf[36:68], header.MerkleRoot)
	binary.LittleEndian.PutUint32(buf[68:72], header.Timestamp)
	binary.LittleEndian.PutUint32(buf[72:76], header.Bits)
	binary.LittleEndian.PutUint32(buf[76:80], header.Nonce)
	return buf
}

// HashBlockHeader hashes a serialized block header using SHA256d.
func (c *DVT) HashBlockHeader(serialized []byte) []byte {
	first := sha256.Sum256(serialized)
	second := sha256.Sum256(first[:])
	return second[:]
}

// TargetFromBits converts compact bits representation to target.
func (c *DVT) TargetFromBits(bits uint32) *big.Int {
	exponent := bits >> 24
	mantissa := bits & 0x007fffff

	// Negative targets are invalid; treat as zero per Bitcoin Core behavior.
	if bits&0x00800000 != 0 {
		return new(big.Int)
	}

	target := new(big.Int).SetUint64(uint64(mantissa))
	if exponent <= 3 {
		target.Rsh(target, uint(8*(3-exponent)))
	} else {
		target.Lsh(target, uint(8*(exponent-3)))
	}
	return target
}

// DifficultyFromTarget calculates difficulty from target (display only).
func (c *DVT) DifficultyFromTarget(target *big.Int) float64 {
	if target.Sign() == 0 {
		return 0
	}

	diff1Target := new(big.Int)
	diff1Target.SetString("00000000ffff0000000000000000000000000000000000000000000000000000", 16)

	diff1Float := new(big.Float).SetInt(diff1Target)
	targetFloat := new(big.Float).SetInt(target)

	result := new(big.Float).Quo(diff1Float, targetFloat)
	difficulty, accuracy := result.Float64()
	if accuracy == big.Below {
		return difficulty
	}
	return difficulty
}

// ShareDifficultyMultiplier returns the multiplier for share difficulty.
// SHA256d coins use 1.0 (unlike Scrypt's 65536).
func (c *DVT) ShareDifficultyMultiplier() float64 {
	return 1.0
}

// GBTRules returns the rules for getblocktemplate.
// DeVault (BCH family) has no SegWit and requires no rules.
func (c *DVT) GBTRules() []string {
	return []string{}
}

// DefaultRPCPort returns the default RPC port.
func (c *DVT) DefaultRPCPort() int {
	return DVTDefaultRPCPort
}

// DefaultP2PPort returns the default P2P port.
func (c *DVT) DefaultP2PPort() int {
	return DVTDefaultP2PPort
}

// P2PKHVersionByte returns the P2PKH version byte.
func (c *DVT) P2PKHVersionByte() byte {
	return DVTP2PKHVersion
}

// P2SHVersionByte returns the P2SH version byte.
func (c *DVT) P2SHVersionByte() byte {
	return DVTP2SHVersion
}

// Bech32HRP returns the bech32 HRP (empty — DeVault uses CashAddr).
func (c *DVT) Bech32HRP() string {
	return ""
}

// Algorithm returns the mining algorithm.
func (c *DVT) Algorithm() string {
	return "sha256d"
}

// SupportsSegWit returns whether the coin supports SegWit.
func (c *DVT) SupportsSegWit() bool {
	return false
}

// BlockTime returns the target block time in seconds.
func (c *DVT) BlockTime() int {
	return 120 // DeVault: 2-minute blocks
}

// MinCoinbaseScriptLen returns the minimum coinbase script length.
func (c *DVT) MinCoinbaseScriptLen() int {
	return 2
}

// CoinbaseMaturity returns confirmations before coinbase is spendable.
func (c *DVT) CoinbaseMaturity() int {
	return 100
}

// GenesisBlockHash returns the expected genesis block hash.
func (c *DVT) GenesisBlockHash() string {
	return DVTGenesisBlockHash
}

// VerifyGenesisBlock checks the node's genesis hash matches DeVault's.
func (c *DVT) VerifyGenesisBlock(nodeGenesisHash string) error {
	if strings.ToLower(nodeGenesisHash) != strings.ToLower(DVTGenesisBlockHash) {
		return fmt.Errorf("DVT genesis block mismatch: got %s, expected %s - "+
			"verify your node is running DeVault (devaultd)",
			nodeGenesisHash, DVTGenesisBlockHash)
	}
	return nil
}

// init registers DeVault in the coin registry.
func init() {
	Register("DVT", func() Coin { return NewDVT() })
	Register("DEVAULT", func() Coin { return NewDVT() })
}
