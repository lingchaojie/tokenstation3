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

func TestDeriveSeatLimitFromVirtualRange(t *testing.T) {
	start := 4900
	total := 5000

	limit, err := deriveSeatLimitFromVirtualRange(&start, &total)

	require.NoError(t, err)
	require.NotNil(t, limit)
	require.Equal(t, 100, *limit)
}

func TestDeriveSeatLimitFromVirtualRangeAllowsUnlimited(t *testing.T) {
	limit, err := deriveSeatLimitFromVirtualRange(nil, nil)

	require.NoError(t, err)
	require.Nil(t, limit)
}

func TestDeriveSeatLimitFromVirtualRangeRejectsPartialRange(t *testing.T) {
	start := 4900

	_, err := deriveSeatLimitFromVirtualRange(&start, nil)

	require.Error(t, err)
	require.Contains(t, err.Error(), "virtual seat")
}

func TestDeriveSeatLimitFromVirtualRangeRejectsNegativeValues(t *testing.T) {
	start := -1
	total := 5000

	_, err := deriveSeatLimitFromVirtualRange(&start, &total)

	require.Error(t, err)
	require.Contains(t, err.Error(), ">= 0")
}

func TestDeriveSeatLimitFromVirtualRangeRejectsTotalBelowStart(t *testing.T) {
	start := 5001
	total := 5000

	_, err := deriveSeatLimitFromVirtualRange(&start, &total)

	require.Error(t, err)
	require.Contains(t, err.Error(), ">= start")
}

func TestDeriveSeatLimitFromOptionalVirtualRangeTracksAbsentClearAndValue(t *testing.T) {
	limit, set, err := deriveSeatLimitFromOptionalVirtualRange(OptionalInt{}, OptionalInt{})
	require.NoError(t, err)
	require.False(t, set)
	require.Nil(t, limit)

	var clearStart OptionalInt
	require.NoError(t, clearStart.UnmarshalJSON([]byte("null")))
	var clearTotal OptionalInt
	require.NoError(t, clearTotal.UnmarshalJSON([]byte("null")))
	limit, set, err = deriveSeatLimitFromOptionalVirtualRange(clearStart, clearTotal)
	require.NoError(t, err)
	require.True(t, set)
	require.Nil(t, limit)

	var valueStart OptionalInt
	require.NoError(t, valueStart.UnmarshalJSON([]byte("4900")))
	var valueTotal OptionalInt
	require.NoError(t, valueTotal.UnmarshalJSON([]byte("5000")))
	limit, set, err = deriveSeatLimitFromOptionalVirtualRange(valueStart, valueTotal)
	require.NoError(t, err)
	require.True(t, set)
	require.NotNil(t, limit)
	require.Equal(t, 100, *limit)
}

func TestEnsureSeatLimitMatchesVirtualRangeRejectsConflict(t *testing.T) {
	seatLimit := 99
	derivedLimit := 100

	err := ensureSeatLimitMatchesVirtualRange(&seatLimit, &derivedLimit)

	require.Error(t, err)
	require.Contains(t, err.Error(), "seat limit")
}

func TestNormalizeCreatePlanSeatRangeRejectsConflictingExplicitSeatLimit(t *testing.T) {
	seatLimit := 99
	start := 4900
	total := 5000

	_, _, _, err := normalizeCreatePlanSeatRange(&seatLimit, &start, &total)

	require.Error(t, err)
	require.Contains(t, err.Error(), "seat limit")
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

// --- normalizePlanCurrency tests ---
// Empty must stay empty (not coerced to the default payment currency),
// so existing plans keep rendering without any currency label.

func TestNormalizePlanCurrency_EmptyKeepsEmpty(t *testing.T) {
	currency, err := normalizePlanCurrency("")
	require.NoError(t, err)
	require.Equal(t, "", currency)
}

func TestNormalizePlanCurrency_WhitespaceKeepsEmpty(t *testing.T) {
	currency, err := normalizePlanCurrency("   ")
	require.NoError(t, err)
	require.Equal(t, "", currency)
}

func TestNormalizePlanCurrency_LowercaseNormalized(t *testing.T) {
	currency, err := normalizePlanCurrency("nzd")
	require.NoError(t, err)
	require.Equal(t, "NZD", currency)
}

func TestNormalizePlanCurrency_ValidUppercase(t *testing.T) {
	currency, err := normalizePlanCurrency("USD")
	require.NoError(t, err)
	require.Equal(t, "USD", currency)
}

func TestNormalizePlanCurrency_TooShort(t *testing.T) {
	_, err := normalizePlanCurrency("NZ")
	require.Error(t, err)
	require.Contains(t, err.Error(), "currency")
}

func TestNormalizePlanCurrency_TooLong(t *testing.T) {
	_, err := normalizePlanCurrency("NZDD")
	require.Error(t, err)
	require.Contains(t, err.Error(), "currency")
}

func TestNormalizePlanCurrency_NonLetter(t *testing.T) {
	_, err := normalizePlanCurrency("N2D")
	require.Error(t, err)
	require.Contains(t, err.Error(), "currency")
}
