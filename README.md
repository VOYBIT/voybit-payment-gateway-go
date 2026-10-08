# Voybit payment gateway for Go

## Get an API key

1. Create an account at [dashboard.voybit.com](https://dashboard.voybit.com).
2. Open **Gateways**, create a payment gateway, enable the assets customers may choose, and store its webhook secret as `VOYBIT_WEBHOOK_SECRET`.
3. Open **API keys**, choose **Create secret key**, and bind it to that gateway. Copy the full `vb_live_…` value once and store it as `VOYBIT_API_KEY` on your server.

Create a payment and verify its webhook. Keep the API key and webhook secret on your server.

```bash
go get github.com/VOYBIT/voybit-payment-gateway-go
```

## Create a buyer-choice checkout

`POST https://api.voybit.com/api/v1/gateway/checkout-sessions`

| Header | |
| --- | --- |
| `X-Voybit-Api-Key` | Gateway API key. |
| `Idempotency-Key` | 8–128 characters: letters, digits, `.` `_` `:` `-`. Reuse it only with the same body. |

| Field | |
| --- | --- |
| `fiat_amount` | Required positive decimal string, such as `25.00`. |
| `fiat_currency` | Required: `USD`, `EUR`, or `GBP`. |
| `payment_window_seconds` | Optional. 300–86400. Default 900. |
| `description` | Optional. Maximum 500 characters. |
| `metadata` | Optional object. Maximum 16 KiB. |

A new payment returns `201`. The same key and body return `200`. A different body returns `409`.

Send the payer to `checkout_url`. The payer chooses from the gateway’s enabled assets and confirms a live quote before the address and QR are created. Fulfil an order only when `status` is `paid` or `overpaid`.

```go
client := voybit.New(os.Getenv("VOYBIT_API_KEY"))
created, err := client.CreateCheckoutSession(ctx, voybit.CreateCheckoutSessionRequest{
    FiatAmount:   "25.00",
    FiatCurrency: "USD",
    Description:  "Order 1001",
    Metadata:     map[string]any{"order_id": "1001"},
}, "order:1001:attempt:1")
```

## Webhook

Read the raw body and verify it before parsing. The signature is `v1=` plus HMAC-SHA256 of `<id>.<timestamp>.<raw body>`, using the gateway webhook secret.

| Header | |
| --- | --- |
| `Voybit-Webhook-Id` | Delivery id. Ignore a repeat. |
| `Voybit-Webhook-Timestamp` | Unix seconds. Reject values outside 5 minutes. |
| `Voybit-Webhook-Signature` | `v1=` and the hex signature. |

`type` is `payment.` plus the status, for example `payment.paid`.

```go
body, err := io.ReadAll(r.Body)
if err != nil {
    http.Error(w, "invalid body", http.StatusBadRequest)
    return
}
err = voybit.VerifyWebhook(
    os.Getenv("VOYBIT_WEBHOOK_SECRET"),
    r.Header.Get("Voybit-Webhook-Id"),
    r.Header.Get("Voybit-Webhook-Timestamp"),
    r.Header.Get("Voybit-Webhook-Signature"),
    body,
    time.Now(),
)
```
