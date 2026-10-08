package voybit

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestCreatePayment(t *testing.T) {
	var seen []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/gateway/payments" || r.Header.Get("X-Voybit-Api-Key") != "vb_test_example_secret" {
			t.Errorf("request = %s %s", r.URL.Path, r.Header.Get("X-Voybit-Api-Key"))
		}
		if r.Header.Get("Idempotency-Key") != "order:1001:attempt:1" {
			t.Errorf("idempotency = %s", r.Header.Get("Idempotency-Key"))
		}
		body, _ := io.ReadAll(r.Body)
		seen = body
		w.Header().Set("X-Request-ID", "req_1")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{
			"id":"7155d76a-9f81-40eb-9233-878aac50eb20",
			"public_id":"nYVvXxsYGr5LZk8Dn7hU0Q",
			"amount_minor":2500,
			"fiat_currency":"USD",
			"crypto_asset":"USDT",
			"crypto_network":"tron",
			"expected_amount":"25.0000",
			"received_amount":"0",
			"status":"pending",
			"checkout_url":"https://voybit.com/pay/nYVvXxsYGr5LZk8Dn7hU0Q",
			"expires_at":"2026-10-07T16:30:00Z",
			"created_at":"2026-10-07T16:00:00Z",
			"updated_at":"2026-10-07T16:00:00Z",
			"deposit_instructions":{"status":"ready","address":"TExample","payment_uri":"tron:TExample","amount":"25.0000","asset":"USDT","network":"tron","message":"Send the exact amount."}
		}`))
	}))
	defer server.Close()

	client := &Client{BaseURL: server.URL, APIKey: "vb_test_example_secret", HTTP: server.Client()}
	created, err := client.CreatePayment(context.Background(), CreatePaymentRequest{
		AssetID:      "6f0b1a10-7c31-4a2e-9b11-0a1000000003",
		CryptoAmount: "25.0000",
		AmountMinor:  2500,
		FiatCurrency: "USD",
		Description:  "Order 1001",
		Metadata:     map[string]any{"order_id": "1001"},
	}, "order:1001:attempt:1")
	if err != nil {
		t.Fatal(err)
	}
	if created.Payment.CheckoutURL == "" || created.Payment.Deposit.Address != "TExample" || created.Payment.Deposit.URI == "" || created.Replayed {
		t.Fatalf("payment = %#v", created)
	}
	var sent CreatePaymentRequest
	if err := json.Unmarshal(seen, &sent); err != nil {
		t.Fatal(err)
	}
	if sent.AmountMinor != 2500 || sent.CryptoAmount != "25.0000" {
		t.Fatalf("sent = %#v", sent)
	}
}

func TestCreatePaymentDoesNotRetryValidation(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{"error":{"code":"asset_unavailable","message":"That asset is not enabled for this gateway."}}`))
	}))
	defer server.Close()

	client := &Client{BaseURL: server.URL, APIKey: "vb_test_example_secret", HTTP: server.Client()}
	_, err := client.CreatePayment(context.Background(), CreatePaymentRequest{
		AssetID: "asset", CryptoAmount: "1", AmountMinor: 100, FiatCurrency: "USD",
	}, "order:1001:attempt:1")
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Code != "asset_unavailable" || apiErr.RequestID != "" || calls != 1 {
		t.Fatalf("err=%v calls=%d", err, calls)
	}
}

func TestCreateCheckoutSession(t *testing.T) {
	var seen []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/gateway/checkout-sessions" {
			t.Errorf("path = %s", r.URL.Path)
		}
		if r.Header.Get("X-Voybit-Api-Key") != "vb_test_example_secret" {
			t.Errorf("api key = %s", r.Header.Get("X-Voybit-Api-Key"))
		}
		seen, _ = io.ReadAll(r.Body)
		w.Header().Set("X-Request-ID", "req_session_1")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"session_id":"7155d76a-9f81-40eb-9233-878aac50eb20","public_id":"nYVvXxsYGr5LZk8Dn7hU0Q","status":"open","checkout_url":"https://voybit.com/pay/nYVvXxsYGr5LZk8Dn7hU0Q","fiat_amount":"25.00","fiat_currency":"USD"}`))
	}))
	defer server.Close()

	client := &Client{BaseURL: server.URL, APIKey: "vb_test_example_secret", HTTP: server.Client()}
	created, err := client.CreateCheckoutSession(context.Background(), CreateCheckoutSessionRequest{
		FiatAmount:           "25.00",
		FiatCurrency:         "USD",
		Description:          "Order 1001",
		Metadata:             map[string]any{"order_id": "1001"},
		PaymentWindowSeconds: 1800,
	}, "order:1001:attempt:1")
	if err != nil {
		t.Fatal(err)
	}
	if created.CheckoutSession.Status != "open" ||
		created.CheckoutSession.SessionID != "7155d76a-9f81-40eb-9233-878aac50eb20" ||
		created.RequestID != "req_session_1" {
		t.Fatalf("session = %#v", created)
	}
	var sent map[string]any
	if err := json.Unmarshal(seen, &sent); err != nil {
		t.Fatal(err)
	}
	if sent["fiat_amount"] != "25.00" || sent["fiat_currency"] != "USD" {
		t.Fatalf("sent = %#v", sent)
	}
	if _, ok := sent["asset_id"]; ok {
		t.Fatal("hosted checkout included asset_id")
	}
	if _, ok := sent["crypto_amount"]; ok {
		t.Fatal("hosted checkout included crypto_amount")
	}
}

func TestCreateCheckoutSessionValidatesInputs(t *testing.T) {
	client := New("vb_test_example_secret")
	_, err := client.CreateCheckoutSession(context.Background(), CreateCheckoutSessionRequest{
		FiatAmount: "0", FiatCurrency: "USD",
	}, "order:1001:attempt:1")
	if err == nil || !strings.Contains(err.Error(), "positive decimal") {
		t.Fatalf("amount error = %v", err)
	}
	_, err = client.CreateCheckoutSession(context.Background(), CreateCheckoutSessionRequest{
		FiatAmount: "25.00", FiatCurrency: "usd",
	}, "order:1001:attempt:1")
	if err == nil || !strings.Contains(err.Error(), "three-letter") {
		t.Fatalf("currency error = %v", err)
	}
}

func TestVerifyWebhook(t *testing.T) {
	secret := "whsec_example"
	raw := []byte(`{"id":"evt_1","type":"payment.paid","payment_id":"pay_1","public_id":"pub_1","status":"paid","created_at":"2026-10-07T16:00:00Z"}`)
	now := time.Unix(1_700_000_000, 0)
	stamp := strconv.FormatInt(now.Unix(), 10)
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte("delivery-1." + stamp + "."))
	_, _ = mac.Write(raw)
	signature := "v1=" + hex.EncodeToString(mac.Sum(nil))

	if err := VerifyWebhook(secret, "delivery-1", stamp, signature, raw, now); err != nil {
		t.Fatal(err)
	}
	event, err := ParseEvent(raw)
	if err != nil || event.Type != "payment.paid" || event.Status != "paid" {
		t.Fatalf("event=%#v err=%v", event, err)
	}
	if err := VerifyWebhook(secret, "delivery-1", stamp, signature, append(raw, ' '), now); err == nil {
		t.Fatal("tampered body was accepted")
	}
	old := strconv.FormatInt(now.Add(-6*time.Minute).Unix(), 10)
	mac = hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte("delivery-1." + old + "."))
	_, _ = mac.Write(raw)
	oldSignature := "v1=" + hex.EncodeToString(mac.Sum(nil))
	if err := VerifyWebhook(secret, "delivery-1", old, oldSignature, raw, now); err == nil {
		t.Fatal("old timestamp was accepted")
	}
}
