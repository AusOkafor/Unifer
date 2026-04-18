package identity

// Tests for computeConfidence() and related helpers.
// This file uses package identity (not identity_test) so it can access
// unexported functions: computeConfidence, computeBaseRules, computeFallback.

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// ─── Tier table ──────────────────────────────────────────────────────────────

func TestComputeConfidence_HardIdentity(t *testing.T) {
	t.Run("EmailExact returns 0.98 profile", func(t *testing.T) {
		c, src := computeConfidence(Signals{EmailExact: true, SameCountry: true}, 0, 0, 0, 0, false)
		assert.InDelta(t, 0.98, c, 0.001)
		assert.Equal(t, "profile", src)
	})

	t.Run("PhoneExact returns 0.98 profile", func(t *testing.T) {
		c, src := computeConfidence(Signals{PhoneExact: true, SameCountry: true}, 0, 0, 0, 0, false)
		assert.InDelta(t, 0.98, c, 0.001)
		assert.Equal(t, "profile", src)
	})
}

// ─── SameCountry gate ────────────────────────────────────────────────────────

func TestComputeConfidence_SameCountryFalseAlwaysReturnsZero(t *testing.T) {
	cases := []Signals{
		{EmailExact: true},
		{PhoneExact: true},
		{NameHigh: true, AddressExact: true},
		{OrderAddressExact: true, NameHigh: true},
	}
	for _, sig := range cases {
		sig.SameCountry = false
		c, src := computeConfidence(sig, 0, 0, 0, 0, true)
		assert.Equal(t, 0.0, c, "SameCountry=false must always return 0")
		assert.Equal(t, "", src)
	}
}

// ─── Tier 1 bypasses penalty signals ─────────────────────────────────────────

func TestComputeConfidence_Tier1BypassesPenalties(t *testing.T) {
	// EmailExact is Tier 1 (returns 0.98 before reaching penalty block).
	// All penalty signals active — score must still be 0.98.
	sig := Signals{
		EmailExact:           true,
		SameCountry:          true,
		DifferentLastName:    true,
		DifferentEmailDomain: true,
		PhoneAsymmetry:       true,
		OrderNameConflict:    true,
		OrderAddressConflict: true,
	}
	c, _ := computeConfidence(sig, 0, 0, 0, 0, false)
	assert.InDelta(t, 0.98, c, 0.001, "Tier-1 EmailExact must not be penalised")
}

// ─── Behavioral rules ────────────────────────────────────────────────────────

func TestComputeConfidence_BehavioralFlagOff(t *testing.T) {
	// With behavioralEnabled=false, OrderAddressExact+NameHigh falls through
	// to computeBaseRules → AddressExact+NameHigh is NOT set here, but the
	// profile fallback should still produce a score (via NameHigh+AddressPartial
	// or similar). The important thing is it must NOT return 0.96.
	sig := Signals{
		OrderAddressExact: true,
		NameHigh:          true,
		SameCountry:       true,
	}
	c, src := computeConfidence(sig, 0, 0, 0, 0, false)
	// No profile rule fires for OrderAddressExact alone; score may be 0 or fallback.
	assert.Less(t, c, 0.96, "behavioral rule must not fire when flag is off")
	if c > 0 {
		assert.Equal(t, "profile", src)
	}
}

func TestComputeConfidence_BehavioralOrderAddressExactNameHigh(t *testing.T) {
	sig := Signals{
		OrderAddressExact: true,
		NameHigh:          true,
		SameCountry:       true,
	}
	c, src := computeConfidence(sig, 0, 0, 0, 0, true)
	assert.InDelta(t, 0.96, c, 0.001)
	assert.Equal(t, "behavioral", src)
}

func TestComputeConfidence_BehavioralDifferentLastNameBlocks096(t *testing.T) {
	// DifferentLastName must prevent the 0.96 early return.
	sig := Signals{
		OrderAddressExact: true,
		NameHigh:          true,
		SameCountry:       true,
		DifferentLastName: true,
	}
	c, _ := computeConfidence(sig, 0, 0, 0, 0, true)
	assert.Less(t, c, 0.96, "DifferentLastName must block the 0.96 behavioral rule")
}

func TestComputeConfidence_BehavioralOrderNameHighPhoneExact(t *testing.T) {
	sig := Signals{
		OrderNameHigh: true,
		PhoneExact:    true,
		SameCountry:   true,
	}
	c, src := computeConfidence(sig, 0, 0, 0, 0, true)
	assert.InDelta(t, 0.95, c, 0.001)
	assert.Equal(t, "mixed", src)
}

func TestComputeConfidence_BehavioralOrderAddressExactEmailLocalExact(t *testing.T) {
	sig := Signals{
		OrderAddressExact: true,
		EmailLocalExact:   true,
		SameCountry:       true,
	}
	c, src := computeConfidence(sig, 0, 0, 0, 0, true)
	assert.InDelta(t, 0.94, c, 0.001)
	assert.Equal(t, "mixed", src)
}

func TestComputeConfidence_BehavioralRecentOverlapPartialAddressNameHigh(t *testing.T) {
	sig := Signals{
		RecentOrderOverlap:  true,
		OrderAddressPartial: true,
		NameHigh:            true,
		SameCountry:         true,
	}
	c, src := computeConfidence(sig, 0, 0, 0, 0, true)
	assert.InDelta(t, 0.91, c, 0.001)
	assert.Equal(t, "behavioral", src)
}

func TestComputeConfidence_BehavioralOrderAddressPartialNameHigh(t *testing.T) {
	// Without RecentOrderOverlap, falls to the 0.90 rule.
	sig := Signals{
		OrderAddressPartial: true,
		NameHigh:            true,
		SameCountry:         true,
	}
	c, src := computeConfidence(sig, 0, 0, 0, 0, true)
	assert.InDelta(t, 0.90, c, 0.001)
	assert.Equal(t, "mixed", src)
}

// ─── Profile tier 2 rules ────────────────────────────────────────────────────

func TestComputeConfidence_ProfileAddressExactNameHigh(t *testing.T) {
	sig := Signals{AddressExact: true, NameHigh: true, SameCountry: true}
	c, src := computeConfidence(sig, 0, 0, 0, 0, false)
	assert.InDelta(t, 0.92, c, 0.001)
	assert.Equal(t, "profile", src)
}

func TestComputeConfidence_ProfileNameHighAddressPartial(t *testing.T) {
	sig := Signals{NameHigh: true, AddressPartial: true, SameCountry: true}
	c, src := computeConfidence(sig, 0, 0, 0, 0, false)
	assert.InDelta(t, 0.76, c, 0.001)
	assert.Equal(t, "profile", src)
}

// ─── Penalty signals ─────────────────────────────────────────────────────────

func TestComputeConfidence_PenaltyDifferentLastName(t *testing.T) {
	// Base: NameHigh + AddressPartial → 0.76. Penalty: DifferentLastName → -0.15.
	sig := Signals{
		NameHigh: true, AddressPartial: true,
		SameCountry: true, DifferentLastName: true,
	}
	c, _ := computeConfidence(sig, 0, 0, 0, 0, false)
	assert.InDelta(t, 0.61, c, 0.001)
}

func TestComputeConfidence_PenaltyOrderAddressConflict(t *testing.T) {
	// Base: 0.76. Penalty: OrderAddressConflict → -0.12.
	sig := Signals{
		NameHigh: true, AddressPartial: true,
		SameCountry: true, OrderAddressConflict: true,
	}
	c, _ := computeConfidence(sig, 0, 0, 0, 0, false)
	assert.InDelta(t, 0.64, c, 0.001)
}

func TestComputeConfidence_PenaltyOrderNameConflict(t *testing.T) {
	// Base: 0.76. Penalty: OrderNameConflict → -0.10.
	sig := Signals{
		NameHigh: true, AddressPartial: true,
		SameCountry: true, OrderNameConflict: true,
	}
	c, _ := computeConfidence(sig, 0, 0, 0, 0, false)
	assert.InDelta(t, 0.66, c, 0.001)
}

func TestComputeConfidence_PenaltyCombinedReducesToZero(t *testing.T) {
	// All penalties combined: 0.15 + 0.05 + 0.05 + 0.10 + 0.12 = 0.47
	// Base 0.76 − 0.47 = 0.29 > 0 → should return 0.29 (not negative).
	sig := Signals{
		NameHigh: true, AddressPartial: true,
		SameCountry:          true,
		DifferentLastName:    true,
		DifferentEmailDomain: true,
		PhoneAsymmetry:       true,
		OrderNameConflict:    true,
		OrderAddressConflict: true,
	}
	c, _ := computeConfidence(sig, 0, 0, 0, 0, false)
	// 0.76 - 0.47 = 0.29
	assert.InDelta(t, 0.29, c, 0.001)
}

func TestComputeConfidence_PenaltyNeverProducesNegative(t *testing.T) {
	// A very weak base combined with heavy penalties must return 0, not negative.
	sig := Signals{
		NameHigh: true, EmailDomainMatch: true,
		SameCountry:          true,
		DifferentLastName:    true,
		DifferentEmailDomain: true,
		PhoneAsymmetry:       true,
		OrderNameConflict:    true,
		OrderAddressConflict: true,
	}
	c, _ := computeConfidence(sig, 0, 0, 0, 0, false)
	assert.GreaterOrEqual(t, c, 0.0, "confidence must never be negative")
}

// ─── Zero cases ──────────────────────────────────────────────────────────────

func TestComputeConfidence_NoSignalsReturnsZero(t *testing.T) {
	c, src := computeConfidence(Signals{SameCountry: true}, 0, 0, 0, 0, false)
	assert.Equal(t, 0.0, c)
	assert.Equal(t, "", src)
}
