//go:build unit

package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestValidatePlanRequired_AllValid(t *testing.T) {
	err := validatePlanRequired("Pro", 9.99, 30, "days", nil, nil)
	require.NoError(t, err)
}

func TestValidatePlanRequired_EmptyName(t *testing.T) {
	err := validatePlanRequired("", 9.99, 30, "days", nil, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "plan name")
}

func TestValidatePlanRequired_WhitespaceName(t *testing.T) {
	err := validatePlanRequired("   ", 9.99, 30, "days", nil, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "plan name")
}

func TestValidatePlanRequired_ZeroPrice(t *testing.T) {
	err := validatePlanRequired("Pro", 0, 30, "days", nil, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "price")
}

func TestValidatePlanRequired_NegativePrice(t *testing.T) {
	err := validatePlanRequired("Pro", -5, 30, "days", nil, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "price")
}

func TestValidatePlanRequired_ZeroValidityDays(t *testing.T) {
	err := validatePlanRequired("Pro", 9.99, 0, "days", nil, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "validity days")
}

func TestValidatePlanRequired_NegativeValidityDays(t *testing.T) {
	err := validatePlanRequired("Pro", 9.99, -7, "days", nil, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "validity days")
}

func TestValidatePlanRequired_EmptyValidityUnit(t *testing.T) {
	err := validatePlanRequired("Pro", 9.99, 30, "", nil, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "validity unit")
}

func TestValidatePlanRequired_WhitespaceValidityUnit(t *testing.T) {
	err := validatePlanRequired("Pro", 9.99, 30, "   ", nil, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "validity unit")
}

func TestValidatePlanRequired_NameValidatedFirst(t *testing.T) {
	err := validatePlanRequired("", 0, 0, "", nil, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "plan name")
}

func TestValidatePlanRequired_TrimmedValidName(t *testing.T) {
	err := validatePlanRequired("  Pro  ", 9.99, 30, "days", nil, nil)
	require.NoError(t, err)
}

func TestValidatePlanRequired_NegativeOriginalPrice(t *testing.T) {
	neg := -10.0
	err := validatePlanRequired("Pro", 9.99, 30, "days", &neg, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "original price")
}

func TestValidatePlanRequired_ZeroOriginalPrice(t *testing.T) {
	zero := 0.0
	err := validatePlanRequired("Pro", 9.99, 30, "days", &zero, nil)
	require.NoError(t, err)
}

func TestValidatePlanRequired_ValidOriginalPrice(t *testing.T) {
	op := 19.99
	err := validatePlanRequired("Pro", 9.99, 30, "days", &op, nil)
	require.NoError(t, err)
}

func TestValidatePlanRequired_SeatLimitAllowsNilZeroPositive(t *testing.T) {
	zero := 0
	one := 1

	require.NoError(t, validatePlanRequired("Pro", 9.99, 30, "days", nil, nil))
	require.NoError(t, validatePlanRequired("Pro", 9.99, 30, "days", nil, &zero))
	require.NoError(t, validatePlanRequired("Pro", 9.99, 30, "days", nil, &one))
}

func TestValidatePlanRequired_SeatLimitRejectsNegative(t *testing.T) {
	negative := -1

	err := validatePlanRequired("Pro", 9.99, 30, "days", nil, &negative)

	require.Error(t, err)
	require.Contains(t, err.Error(), "seat limit")
}

// --- validatePlanPatch tests ---

func TestValidatePlanPatch_NegativeOriginalPrice(t *testing.T) {
	neg := -5.0
	err := validatePlanPatch(UpdatePlanRequest{OriginalPrice: &neg})
	require.Error(t, err)
	require.Contains(t, err.Error(), "original price")
}

func TestValidatePlanPatch_ZeroOriginalPrice(t *testing.T) {
	zero := 0.0
	err := validatePlanPatch(UpdatePlanRequest{OriginalPrice: &zero})
	require.NoError(t, err)
}

func TestValidatePlanPatch_ValidOriginalPrice(t *testing.T) {
	op := 29.99
	err := validatePlanPatch(UpdatePlanRequest{OriginalPrice: &op})
	require.NoError(t, err)
}

func TestValidatePlanPatch_NilOriginalPrice(t *testing.T) {
	err := validatePlanPatch(UpdatePlanRequest{OriginalPrice: nil})
	require.NoError(t, err)
}

func TestValidatePlanPatch_SeatLimitAllowsClearZeroPositive(t *testing.T) {
	var clear OptionalInt
	require.NoError(t, clear.UnmarshalJSON([]byte("null")))

	var zero OptionalInt
	require.NoError(t, zero.UnmarshalJSON([]byte("0")))

	var positive OptionalInt
	require.NoError(t, positive.UnmarshalJSON([]byte("42")))

	require.NoError(t, validatePlanPatch(UpdatePlanRequest{SeatLimit: clear}))
	require.NoError(t, validatePlanPatch(UpdatePlanRequest{SeatLimit: zero}))
	require.NoError(t, validatePlanPatch(UpdatePlanRequest{SeatLimit: positive}))
}

func TestValidatePlanPatch_SeatLimitRejectsNegative(t *testing.T) {
	var seatLimit OptionalInt
	require.NoError(t, seatLimit.UnmarshalJSON([]byte("-1")))

	err := validatePlanPatch(UpdatePlanRequest{SeatLimit: seatLimit})

	require.Error(t, err)
	require.Contains(t, err.Error(), "seat limit")
}

func TestOptionalIntTracksAbsentNullAndValue(t *testing.T) {
	var absent OptionalInt
	require.False(t, absent.Set)
	require.Nil(t, absent.Value)

	var nullValue OptionalInt
	require.NoError(t, nullValue.UnmarshalJSON([]byte("null")))
	require.True(t, nullValue.Set)
	require.Nil(t, nullValue.Value)

	var numberValue OptionalInt
	require.NoError(t, numberValue.UnmarshalJSON([]byte("7")))
	require.True(t, numberValue.Set)
	require.NotNil(t, numberValue.Value)
	require.Equal(t, 7, *numberValue.Value)
}

// --- validatePlanPatch: other fields ---

func ptrStr(s string) *string     { return &s }
func ptrInt(i int) *int           { return &i }
func ptrFloat(f float64) *float64 { return &f }

func TestValidatePlanPatch_EmptyName(t *testing.T) {
	err := validatePlanPatch(UpdatePlanRequest{Name: ptrStr("")})
	require.Error(t, err)
	require.Contains(t, err.Error(), "plan name")
}

func TestValidatePlanPatch_ValidName(t *testing.T) {
	err := validatePlanPatch(UpdatePlanRequest{Name: ptrStr("Basic")})
	require.NoError(t, err)
}

func TestValidatePlanPatch_NegativePrice(t *testing.T) {
	err := validatePlanPatch(UpdatePlanRequest{Price: ptrFloat(-1)})
	require.Error(t, err)
	require.Contains(t, err.Error(), "price")
}

func TestValidatePlanPatch_ZeroPrice(t *testing.T) {
	err := validatePlanPatch(UpdatePlanRequest{Price: ptrFloat(0)})
	require.Error(t, err)
	require.Contains(t, err.Error(), "price")
}

func TestValidatePlanPatch_ValidPrice(t *testing.T) {
	err := validatePlanPatch(UpdatePlanRequest{Price: ptrFloat(9.99)})
	require.NoError(t, err)
}

func TestValidatePlanPatch_ZeroValidityDays(t *testing.T) {
	err := validatePlanPatch(UpdatePlanRequest{ValidityDays: ptrInt(0)})
	require.Error(t, err)
	require.Contains(t, err.Error(), "validity days")
}

func TestValidatePlanPatch_EmptyValidityUnit(t *testing.T) {
	err := validatePlanPatch(UpdatePlanRequest{ValidityUnit: ptrStr("")})
	require.Error(t, err)
	require.Contains(t, err.Error(), "validity unit")
}

func TestValidatePlanPatch_ValidValidityUnit(t *testing.T) {
	err := validatePlanPatch(UpdatePlanRequest{ValidityUnit: ptrStr("days")})
	require.NoError(t, err)
}

func TestValidatePlanPatch_AllNil(t *testing.T) {
	err := validatePlanPatch(UpdatePlanRequest{})
	require.NoError(t, err)
}
