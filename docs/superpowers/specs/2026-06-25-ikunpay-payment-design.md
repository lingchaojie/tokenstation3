# IkunPay Payment Integration Design

Date: 2026-06-25

## Goal

Integrate IkunPay as a payment provider for the existing user-facing Alipay and WeChat payment methods. Users should continue to see `alipay` and `wxpay`; admins can choose IkunPay as the source provider for either method.

## Scope

In scope:

- Add a new backend provider key, `ikunpay`, using IkunPay V2 RSA APIs.
- Support IkunPay-backed `alipay` and `wxpay` order creation, query, webhook notification, refund, and order close.
- Add admin provider-instance configuration for IkunPay.
- Extend visible payment method source routing so Alipay and WeChat can route through official providers, EasyPay, or IkunPay.
- Add focused backend and frontend tests.

Out of scope:

- Storing real merchant secrets in code, docs, tests, or commits.
- Changing the user-facing payment method names.
- Adding payout/transfer APIs.
- Replacing the existing EasyPay V1/MD5 provider.

## Existing System

The platform already has a payment domain:

- Provider abstraction: `backend/internal/payment/types.go`
- Provider factory: `backend/internal/payment/provider/factory.go`
- Existing providers: official Alipay, official WeChat Pay, Stripe, Airwallex, and EasyPay.
- Provider instance CRUD/config encryption: `backend/internal/service/payment_config_providers.go`
- Visible payment method routing: `backend/internal/service/payment_resume_service.go` and `backend/internal/service/payment_visible_method_instances.go`
- Webhook routes: `backend/internal/server/routes/payment.go`
- Admin provider UI: `frontend/src/components/payment/providerConfig.ts` and `PaymentProviderDialog.vue`

EasyPay currently covers a V1-style MD5 aggregation API. IkunPay V2 uses RSA signatures and different endpoints, so it should be implemented as a separate provider instead of overloading EasyPay.

## IkunPay API Summary

IkunPay V2 uses:

- API base: configurable, default `https://ikunpay.com`
- Submit format: `application/x-www-form-urlencoded`
- Response format: JSON
- Signature type: RSA with SHA-256
- Merchant signing key: merchant private key
- Response and callback verification key: platform public key

Relevant endpoints:

- Create order: `POST /api/pay/create`
- Query order: `POST /api/pay/query`
- Refund order: `POST /api/pay/refund`
- Query refund: `POST /api/pay/refundquery`
- Close order: `POST /api/pay/close`
- Notification callback: GET request to our configured `notify_url`

IkunPay supports `type=alipay` and `type=wxpay`, plus other methods that are not part of this integration.

## Recommended Approach

Add a dedicated `IkunPay` provider implementation.

Reasons:

- Keeps EasyPay V1/MD5 behavior stable.
- Keeps RSA signing, response verification, and V2 endpoint behavior isolated and testable.
- Lets admins clearly configure and route official, EasyPay, and IkunPay sources independently.

Rejected alternatives:

- Add a V2 mode to EasyPay: less code initially, but mixes two protocols and config models in one provider.
- Use IkunPay V1: faster but depends on legacy MD5 APIs and loses the cleaner V2 refund/query model.

## Backend Design

### Provider Constants

Add:

- `payment.TypeIkunPay = "ikunpay"`

Update helpers:

- `GetBasePaymentType` should return `ikunpay` only for provider identity, not for `alipay`/`wxpay` visible methods.
- Provider factory should create `provider.NewIkunPay`.

### Provider Config

IkunPay provider config keys:

- `pid`: merchant ID.
- `merchantPrivateKey`: merchant RSA private key, sensitive.
- `platformPublicKey`: IkunPay platform RSA public key, sensitive because it is long cryptographic config and should not be echoed unnecessarily.
- `apiBase`: base URL, default `https://ikunpay.com`.
- `notifyUrl`: backend webhook URL.
- `returnUrl`: browser return URL.
- `paymentMode`: inherited provider instance mode. `qrcode` should prefer QR/API rendering; `popup` should prefer hosted jump.

No real values are committed. Operators enter them through the admin provider dialog.

### Signature Helper

Add small, local helpers for IkunPay:

- Remove empty values.
- Exclude `sign` and `sign_type`.
- Sort keys by ASCII order.
- Join as `key=value` with `&`.
- Sign with RSA PKCS#1 v1.5 SHA-256 using the merchant private key.
- Verify with RSA PKCS#1 v1.5 SHA-256 using the platform public key.

The key parser should accept bare base64 key bodies and PEM-wrapped keys. Tests should prove both accepted formats work.

### Create Payment

`CreatePayment` posts to `/api/pay/create`.

Request fields:

- `pid`
- `method`
- `device`
- `type`
- `out_trade_no`
- `notify_url`
- `return_url`
- `name`
- `money`
- `clientip`
- `timestamp`
- `sign`
- `sign_type=RSA`

Mode behavior:

- Default and `qrcode`: use `method=web`, `device=pc` or `mobile`.
- `popup` or `redirect`: use `method=jump` so IkunPay returns a hosted URL.

Response mapping:

- Success requires `code=0`.
- `trade_no` becomes upstream trade number.
- `pay_type=qrcode`: `pay_info` becomes `QRCode`.
- `pay_type=jump` or `urlscheme`: `pay_info` becomes `PayURL`.
- `pay_type=html`: return `PayURL` only if the response includes a usable URL; otherwise fail with a clear unsupported-response error.
- Unknown `pay_type` should fail with a clear error rather than silently creating an unusable order.

### Query Order

`QueryOrder` posts to `/api/pay/query`, using `out_trade_no` by default.

Status mapping:

- `status=1`: provider paid.
- `status=0`: provider pending.
- `status=2`: provider refunded.
- `status=3` or other non-success terminal values: provider failed unless future docs require a distinct mapping.

The returned amount is parsed from `money`.

### Notification

Add webhook route:

- `GET /api/v1/payment/webhook/ikunpay`
- `POST /api/v1/payment/webhook/ikunpay` for tolerance, even though docs specify GET.

Handler can follow the EasyPay pattern:

- Parse query/form values.
- Verify RSA signature using platform public key.
- Require `trade_status=TRADE_SUCCESS` for success.
- Return `success` after accepted notification processing.
- Populate metadata with `pid` for provider snapshot validation.

The notification verifier must tolerate additional future IkunPay fields by including all non-empty, non-sign fields in verification.

### Refund and Close

Refund:

- Call `POST /api/pay/refund`.
- Use `trade_no` if present; otherwise use `out_trade_no`.
- Include `out_refund_no` derived deterministically from our refund/order identifier when available.
- Success requires `code=0`.
- Return `RefundID` from `refund_no` or fallback to the request order identifier.

Close:

- Implement `payment.CancelableProvider`.
- Call `POST /api/pay/close`.
- Treat `code=0` as success.
- If IkunPay reports unsupported close behavior, surface the provider message.

### Provider Snapshot Validation

Extend provider snapshot metadata validation:

- Snapshot IkunPay `pid` as merchant identity.
- Webhook/refund verification checks notification metadata `pid` matches the order snapshot.

This prevents a webhook for one IkunPay merchant instance from fulfilling an order created by another instance.

## Visible Method Routing

Add sources:

- `ikunpay_alipay`
- `ikunpay_wxpay`

Update:

- `NormalizeVisibleMethodSource`
- `VisibleMethodProviderKeyForSource`
- `buildVisibleMethodSourceAvailability`
- `enabledVisibleMethodsForProvider`
- source settings tests
- payment limits routing tests
- resume/order creation routing tests

When an admin selects `ikunpay_alipay`, user-facing `alipay` order creation selects an enabled IkunPay provider instance that supports `alipay`. Same for `wxpay`.

## Frontend Design

Update provider config:

- Add `ikunpay: ['alipay', 'wxpay']` to `PROVIDER_SUPPORTED_TYPES`.
- Add webhook path `/api/v1/payment/webhook/ikunpay`.
- Add callback path config with `notifyUrl` and `returnUrl`.
- Add config fields `pid`, `merchantPrivateKey`, `platformPublicKey`, and `apiBase`.
- Add default `apiBase=https://ikunpay.com`.
- Expose payment mode controls like EasyPay: QR code and popup.

Update admin visible method sources:

- Add IkunPay source options for Alipay and WeChat.
- Add aliases for `ikunpay` to map to the method-specific source.
- Update i18n labels in Chinese and English.

No user checkout UI changes are required. Existing `PaymentView`, QR dialog, redirect/popup behavior, and order polling should continue to work through the existing create-order response fields.

## Error Handling

Provider errors should include:

- Missing config key.
- Invalid RSA key format.
- Signature verification failure.
- IkunPay non-zero `code` with `msg`.
- Unsupported `pay_type`.
- Invalid amount or timestamp response.

Secrets must not be logged. Error summaries must not include request parameters that contain key material.

## Operational Setup

Admin configuration flow:

1. Create IkunPay provider instance.
2. Enter `pid`, `apiBase`, merchant private key, and platform public key.
3. Confirm callback URLs:
   - `notifyUrl`: `/api/v1/payment/webhook/ikunpay`
   - `returnUrl`: `/payment/result`
4. Enable supported types `alipay` and/or `wxpay`.
5. In payment settings, route visible Alipay and/or WeChat methods to IkunPay.

IkunPay backend settings:

- V2 RSA mode should be enabled for production use.
- The merchant private key generated by IkunPay must be copied immediately and stored only in the platform provider config.

## Testing

Backend tests:

- IkunPay RSA sign string sorting and exclusion rules.
- RSA sign/verify round trip with generated test keys.
- Create payment request sends expected form fields and maps qrcode/jump responses.
- Query maps IkunPay statuses to provider statuses.
- Notification verifies signature and maps `TRADE_SUCCESS`.
- Notification rejects tampered amount/order/signature.
- Refund maps success and provider error messages.
- Cancel payment calls close endpoint.
- Provider config validation, sensitive field masking, protected config fields, and valid provider key.
- Visible method source routing for IkunPay.

Frontend tests:

- `providerConfig` exposes IkunPay fields, callbacks, and supported types.
- Admin visible method source options include IkunPay.
- Source normalization maps `ikunpay` aliases correctly.
- Provider dialog validates required IkunPay fields and preserves sensitive fields on edit.

Verification commands:

- `cd backend && go test -tags=unit ./internal/payment/provider ./internal/service ./internal/handler ./internal/server/routes`
- `cd backend && go test ./...`
- `PATH="$(go env GOPATH)/bin:$PATH" make test-backend`
- `pnpm --dir frontend run lint:check`
- `pnpm --dir frontend run typecheck`
- `pnpm --dir frontend exec vitest run src/api/__tests__/settings.paymentVisibleMethods.spec.ts src/components/payment/__tests__/providerConfig.spec.ts src/components/payment/__tests__/PaymentProviderDialog.spec.ts`
- `make test-frontend-critical`

## Done Criteria

- Admin can create an enabled IkunPay provider instance without exposing secrets back through the API.
- Alipay and WeChat visible method sources can route to IkunPay.
- Creating an IkunPay-backed order returns either a QR payload or hosted payment URL usable by existing frontend flows.
- IkunPay webhook notifications verify RSA signatures and fulfill the correct local order.
- Query, refund, and cancel paths work through the provider abstraction.
- Focused backend and frontend tests pass, plus the repo's existing critical payment checks.
