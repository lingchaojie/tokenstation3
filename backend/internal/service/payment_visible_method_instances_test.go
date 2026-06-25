//go:build unit

package service

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/payment"
	"github.com/stretchr/testify/require"
)

func TestEnabledVisibleMethodsForProviderIncludesIkunPay(t *testing.T) {
	t.Parallel()

	got := enabledVisibleMethodsForProvider(payment.TypeIkunPay, "wxpay,alipay")
	require.Equal(t, []string{payment.TypeAlipay, payment.TypeWxpay}, got)
}

func TestResolveEnabledVisibleMethodInstanceUsesIkunPayConfiguredSource(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	client := newPaymentConfigServiceTestClient(t)

	_, err := client.PaymentProviderInstance.Create().
		SetProviderKey(payment.TypeEasyPay).
		SetName("EasyPay Alipay").
		SetConfig("{}").
		SetSupportedTypes("alipay").
		SetEnabled(true).
		SetSortOrder(1).
		Save(ctx)
	require.NoError(t, err)

	_, err = client.PaymentProviderInstance.Create().
		SetProviderKey(payment.TypeIkunPay).
		SetName("IkunPay Alipay").
		SetConfig("{}").
		SetSupportedTypes("alipay").
		SetEnabled(true).
		SetSortOrder(2).
		Save(ctx)
	require.NoError(t, err)

	svc := &PaymentConfigService{
		entClient: client,
		settingRepo: &paymentConfigSettingRepoStub{
			values: map[string]string{
				SettingPaymentVisibleMethodAlipaySource: VisibleMethodSourceIkunPayAlipay,
			},
		},
	}

	inst, err := svc.resolveEnabledVisibleMethodInstance(ctx, payment.TypeAlipay)
	require.NoError(t, err)
	require.NotNil(t, inst)
	require.Equal(t, payment.TypeIkunPay, inst.ProviderKey)
}
