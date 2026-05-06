// SPDX-License-Identifier: BSD-3-Clause
// SPDX-FileCopyrightText: Copyright (c) 2026 Spiral Pool Contributors

// Package coin - eCash (XEC) implementation.
//
// eCash uses SHA256d for proof of work, same as Bitcoin and Bitcoin Cash.
// eCash forked from Bitcoin Cash and uses CashAddr format (ecash:q...).
// eCash has additional consensus rules: Real Time Target (RTT) and MinerFund.
package coin

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/binary"
	"fmt"
	"math/big"
	"strings"
)

// eCash address constants
const (
	XECP2PKHVersion byte = 0x00 // Same as Bitcoin legacy
	XECP2SHVersion  byte = 0x05 // Same as Bitcoin legacy
	CashAddrPrefixXEC      = "ecash:"
)

// eCash network parameters
const (
	XECDefaultP2PPort = 8333 // Same as Bitcoin
	XECDefaultRPCPort = 8332 // Same as Bitcoin
)

// XECGenesisBlockHash is the genesis block hash for chain verification.
// eCash shares the same genesis block as Bitcoin (forked at block 478558).
const XECGenesisBlockHash = "000000000019d6689c085ae165831e934ff763ae46a2a6c172b3f1b60a8ce26f"

// ECashCoin implements the Coin interface for eCash.
type ECashCoin struct{}

// NewECashCoin creates a new eCash coin instance.
func NewECashCoin() *ECashCoin {
	return &ECashCoin{}
}

// Symbol returns the ticker symbol.
func (c *ECashCoin) Symbol() string {
	return "XEC"
}

// Name returns the full coin name.
func (c *ECashCoin) Name() string {
	return "eCash"
}

// ValidateAddress validates an eCash address.
func (c *ECashCoin) ValidateAddress(address string) error {
	_, _, err := c.DecodeAddress(address)
	return err
}

// DecodeAddress decodes an eCash address to its hash and type.
func (c *ECashCoin) DecodeAddress(address string) ([]byte, AddressType, error) {
	if address == "" {
		return nil, AddressTypeUnknown, fmt.Errorf("empty address")
	}

	// CashAddr format (ecash: for mainnet)
	addrLower := strings.ToLower(address)
	if strings.HasPrefix(addrLower, CashAddrPrefixXEC) ||
		strings.HasPrefix(addrLower, "q") ||
		strings.HasPrefix(addrLower, "p") {
		return c.decodeCashAddr(address)
	}

	// Legacy Base58Check (1... or 3...)
	if len(address) < 25 || len(address) > 35 {
		return nil, AddressTypeUnknown, fmt.Errorf("invalid address length: %d", len(address))
	}

	decoded, err := base58Decode(address)
	if err != nil {
		return nil, AddressTypeUnknown, fmt.Errorf("base58 decode failed: %w", err)
	}

	if len(decoded) != 25 {
		return nil, AddressTypeUnknown, fmt.Errorf("decoded length %d, expected 25", len(decoded))
	}

	// Verify checksum
	payload := decoded[:21]
	checksum := decoded[21:]
	expectedChecksum := doubleSHA256(payload)[:4]

	if subtle.ConstantTimeCompare(checksum, expectedChecksum) != 1 {
		return nil, AddressTypeUnknown, fmt.Errorf("invalid checksum")
	}

	version := decoded[0]
	hash := decoded[1:21]

	switch version {
	case XECP2PKHVersion:
		return hash, AddressTypeP2PKH, nil
	case XECP2SHVersion:
		return hash, AddressTypeP2SH, nil
	default:
		return nil, AddressTypeUnknown, fmt.Errorf("unknown address version: 0x%02x", version)
	}
}

// decodeCashAddr decodes a CashAddr format address for eCash.
func (c *ECashCoin) decodeCashAddr(address string) ([]byte, AddressType, error) {
	addrLower := strings.ToLower(address)
	prefix := "ecash"

	if !strings.Contains(addrLower, ":") {
		addrLower = prefix + ":" + addrLower
	}

	parts := strings.Split(addrLower, ":")
	if len(parts) != 2 {
		return nil, AddressTypeUnknown, fmt.Errorf("invalid CashAddr format")
	}

	data, err := decodeCashAddrDataWithPrefix(parts[0], parts[1])
	if err != nil {
		return nil, AddressTypeUnknown, err
	}

	if len(data) < 21 {
		return nil, AddressTypeUnknown, fmt.Errorf("invalid CashAddr data length")
	}

	versionByte := data[0]
	hash := data[1:21]

	addrType := versionByte & 0x78
	switch addrType {
	case 0x00: // P2PKH (q...)
		return hash, AddressTypeP2PKH, nil
	case 0x08: // P2SH (p...)
		return hash, AddressTypeP2SH, nil
	default:
		return nil, AddressTypeUnknown, fmt.Errorf("unknown CashAddr type: 0x%02x", versionByte)
	}
}

// BuildCoinbaseScript builds the output script for the coinbase transaction.
func (c *ECashCoin) BuildCoinbaseScript(params CoinbaseParams) ([]byte, error) {
	hash, addrType, err := c.DecodeAddress(params.PoolAddress)
	if err != nil {
		return nil, fmt.Errorf("invalid pool address: %w", err)
	}

	switch addrType {
	case AddressTypeP2PKH:
		script := make([]byte, 25)
		script[0] = 0x76 // OP_DUP
		script[1] = 0xa9 // OP_HASH160
		script[2] = 0x14 // PUSH 20 bytes
		copy(script[3:23], hash)
		script[23] = 0x88 // OP_EQUALVERIFY
		script[24] = 0xac // OP_CHECKSIG
		return script, nil

	case AddressTypeP2SH:
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
func (c *ECashCoin) SerializeBlockHeader(header *BlockHeader) []byte {
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
func (c *ECashCoin) HashBlockHeader(serialized []byte) []byte {
	first := sha256.Sum256(serialized)
	second := sha256.Sum256(first[:])
	return second[:]
}

// TargetFromBits converts compact bits representation to target.
func (c *ECashCoin) TargetFromBits(bits uint32) *big.Int {
	exponent := bits >> 24
	mantissa := bits & 0x007fffff

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

// DifficultyFromTarget calculates difficulty from target.
func (c *ECashCoin) DifficultyFromTarget(target *big.Int) float64 {
	if target.Sign() == 0 {
		return 0
	}

	diff1Target := new(big.Int)
	diff1Target.SetString("00000000ffff0000000000000000000000000000000000000000000000000000", 16)

	diff1Float := new(big.Float).SetInt(diff1Target)
	targetFloat := new(big.Float).SetInt(target)

	result := new(big.Float).Quo(diff1Float, targetFloat)
	difficulty, _ := result.Float64()

	return difficulty
}

// ShareDifficultyMultiplier returns the multiplier for share difficulty.
func (c *ECashCoin) ShareDifficultyMultiplier() float64 {
	return 1.0
}

// DefaultRPCPort returns the default RPC port.
func (c *ECashCoin) DefaultRPCPort() int {
	return XECDefaultRPCPort
}

// DefaultP2PPort returns the default P2P port.
func (c *ECashCoin) DefaultP2PPort() int {
	return XECDefaultP2PPort
}

// P2PKHVersionByte returns the P2PKH version byte.
func (c *ECashCoin) P2PKHVersionByte() byte {
	return XECP2PKHVersion
}

// P2SHVersionByte returns the P2SH version byte.
func (c *ECashCoin) P2SHVersionByte() byte {
	return XECP2SHVersion
}

// Bech32HRP returns the bech32 human-readable part (empty for eCash - uses CashAddr).
func (c *ECashCoin) Bech32HRP() string {
	return ""
}

// Algorithm returns the mining algorithm.
func (c *ECashCoin) Algorithm() string {
	return "sha256d"
}

// SupportsSegWit returns whether the coin supports SegWit.
func (c *ECashCoin) SupportsSegWit() bool {
	return false
}

// BlockTime returns the target block time in seconds.
func (c *ECashCoin) BlockTime() int {
	return 600 // 10 minutes
}

// MinCoinbaseScriptLen returns the minimum coinbase script length.
func (c *ECashCoin) MinCoinbaseScriptLen() int {
	return 2
}

// CoinbaseMaturity returns the number of confirmations before coinbase is spendable.
func (c *ECashCoin) CoinbaseMaturity() int {
	return 100
}

// GenesisBlockHash returns the expected genesis block hash for chain verification.
func (c *ECashCoin) GenesisBlockHash() string {
	return XECGenesisBlockHash
}

// VerifyGenesisBlock checks if the provided hash matches the expected genesis block.
func (c *ECashCoin) VerifyGenesisBlock(nodeGenesisHash string) error {
	if strings.ToLower(nodeGenesisHash) != strings.ToLower(XECGenesisBlockHash) {
		return fmt.Errorf("XEC genesis block mismatch: got %s, expected %s",
			nodeGenesisHash, XECGenesisBlockHash)
	}
	return nil
}

// init registers eCash in the coin registry.
func init() {
	Register("XEC", func() Coin { return NewECashCoin() })
	Register("ECASH", func() Coin { return NewECashCoin() })
}
