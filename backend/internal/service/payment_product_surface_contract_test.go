//go:build unit

package service

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/payment"
	"github.com/stretchr/testify/require"
)

func TestProviderAdminContractDoesNotExposeUserSelfServiceRefundToggle(t *testing.T) {
	for name, value := range map[string]any{
		"response": ProviderInstanceResponse{},
		"create":   CreateProviderInstanceRequest{},
		"update":   UpdateProviderInstanceRequest{},
	} {
		t.Run(name, func(t *testing.T) {
			encoded, err := json.Marshal(value)
			require.NoError(t, err)
			require.NotContains(t, string(encoded), "allow_user_refund")
		})
	}
}

func TestCheckoutContractsDoNotExposeAlipayMobilePrecreateDeepLink(t *testing.T) {
	for name, value := range map[string]any{
		"create order response":  CreateOrderResponse{},
		"create payment request": reflect.TypeOf((*paymentCreateRequestContract)(nil)).Elem(),
	} {
		t.Run(name, func(t *testing.T) {
			typ := reflect.TypeOf(value)
			if reflected, ok := value.(reflect.Type); ok {
				typ = reflected
			}
			for i := 0; i < typ.NumField(); i++ {
				field := typ.Field(i)
				require.NotEqual(t, "AlipayMobilePrecreateDeepLink", field.Name)
				require.NotEqual(t, "AlipayMobilePrecreate", field.Name)
				require.False(t, strings.HasPrefix(field.Tag.Get("json"), "alipay_mobile_precreate_deep_link"))
			}
		})
	}
}

// paymentCreateRequestContract mirrors only the public boundary being audited.
// It is aliased below by a compile-time assignment so the test inspects the
// real payment request type without duplicating its fields.
type paymentCreateRequestContract = payment.CreatePaymentRequest
