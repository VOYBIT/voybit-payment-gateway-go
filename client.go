package voybit

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	DefaultBaseURL = "https://api.voybit.com/api/v1"
	apiKeyHeader   = "X-Voybit-Api-Key"
)

var idempotencyPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{7,127}$`)

// Client calls the Voybit payment gateway.
type Client struct {
	BaseURL string
	APIKey  string
	HTTP    *http.Client
}

// CreatePaymentRequest is the body of POST /gateway/payments.
type CreatePaymentRequest struct {
	// GatewayID is the gateway this payment belongs to. A key that is already
	// limited to one gateway may omit it.
	GatewayID string `json:"gateway_id,omitempty"`
	// AssetID is the gateway asset the customer pays with.
	AssetID string `json:"asset_id"`
	// CryptoAmount is the exact on-chain amount as a decimal string.
	CryptoAmount string `json:"crypto_amount"`
	// AmountMinor is the fiat amount in minor units. 2500 with USD is $25.00.
	AmountMinor int64 `json:"amount_minor"`
	// FiatCurrency is the three-letter fiat code, such as USD.
	FiatCurrency string `json:"fiat_currency"`
	// ExpiresInSeconds is optional. Voybit uses 900 when it is omitted.
	// The accepted range is 300 to 86400.
	ExpiresInSeconds int64 `json:"expires_in_seconds,omitempty"`
	// Description is optional text shown on hosted checkout. Maximum 500 characters.
	Description string `json:"description,omitempty"`
	// Metadata is optional merchant data, up to 16 KiB. It is not shown to the payer.
	Metadata map[string]any `json:"metadata,omitempty"`
}

// Payment is the invoice Voybit returns.
type Payment struct {
	ID             string          `json:"id"`
	PublicID       string          `json:"public_id"`
	MerchantUserID string          `json:"merchant_user_id,omitempty"`
	GatewayID      string          `json:"gateway_id,omitempty"`
	BusinessID     string          `json:"business_id,omitempty"`
	AssetID        string          `json:"asset_id,omitempty"`
	AmountMinor    int64           `json:"amount_minor"`
	FiatCurrency   string          `json:"fiat_currency"`
	CryptoAsset    string          `json:"crypto_asset"`
	CryptoNetwork  string          `json:"crypto_network"`
	ExpectedAmount string          `json:"expected_amount,omitempty"`
	ReceivedAmount string          `json:"received_amount"`
	Status         string          `json:"status"`
	Description    string          `json:"description,omitempty"`
	Metadata       json.RawMessage `json:"metadata,omitempty"`
	CheckoutURL    string          `json:"checkout_url,omitempty"`
	ExpiresAt      time.Time       `json:"expires_at"`
	CreatedAt      time.Time       `json:"created_at"`
	UpdatedAt      time.Time       `json:"updated_at"`
	PaidAt         *time.Time      `json:"paid_at,omitempty"`
	Deposit        Deposit         `json:"deposit_instructions"`
}

// Deposit tells the payer where to send the exact crypto amount.
type Deposit struct {
	Status  string `json:"status"`
	Address string `json:"address,omitempty"`
	Amount  string `json:"amount,omitempty"`
	Asset   string `json:"asset,omitempty"`
	Network string `json:"network,omitempty"`
	Message string `json:"message"`
}

// CreatedPayment is one create-payment result.
type CreatedPayment struct {
	Payment   Payment
	Replayed  bool
	RequestID string
}

// APIError is a non-success response from the payment gateway.
type APIError struct {
	Status    int
	Code      string
	Message   string
	RequestID string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("voybit payment gateway %s: %s (request %s)", e.Code, e.Message, e.RequestID)
}

// CreatePayment creates a hosted crypto payment.
//
// idempotencyKey must be 8 to 128 characters, start with a letter or digit,
// and otherwise contain only letters, digits, and . _ : -. Keep the same key
// and the same request when retrying one order. A new order needs a new key.
func (c *Client) CreatePayment(ctx context.Context, request CreatePaymentRequest, idempotencyKey string) (*CreatedPayment, error) {
	if strings.TrimSpace(c.APIKey) == "" {
		return nil, errors.New("voybit payment gateway API key is required")
	}
	if !idempotencyPattern.MatchString(idempotencyKey) {
		return nil, errors.New("idempotency key must contain 8 to 128 URL-safe characters")
	}
	body, err := json.Marshal(request)
	if err != nil {
		return nil, err
	}
	base := strings.TrimRight(c.BaseURL, "/")
	if base == "" {
		base = DefaultBaseURL
	}
	endpoint := base + "/gateway/payments"
	client := c.HTTP
	if client == nil {
		client = http.DefaultClient
	}

	var lastErr error
	for attempt := 0; attempt < 4; attempt++ {
		attemptCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
		httpRequest, err := http.NewRequestWithContext(attemptCtx, http.MethodPost, endpoint, bytes.NewReader(body))
		if err != nil {
			cancel()
			return nil, err
		}
		httpRequest.Header.Set(apiKeyHeader, c.APIKey)
		httpRequest.Header.Set("Idempotency-Key", idempotencyKey)
		httpRequest.Header.Set("Content-Type", "application/json")
		httpRequest.Header.Set("Accept", "application/json")

		response, err := client.Do(httpRequest)
		if err != nil {
			cancel()
			lastErr = err
			if attempt == 3 || ctx.Err() != nil {
				return nil, err
			}
			if sleepErr := sleep(ctx, backoff(attempt, "")); sleepErr != nil {
				return nil, sleepErr
			}
			continue
		}
		raw, readErr := io.ReadAll(io.LimitReader(response.Body, 1<<20))
		response.Body.Close()
		cancel()
		if readErr != nil {
			return nil, readErr
		}
		requestID := response.Header.Get("X-Request-ID")
		if response.StatusCode >= 200 && response.StatusCode < 300 {
			var payment Payment
			if len(raw) > 0 {
				if err := json.Unmarshal(raw, &payment); err != nil {
					return nil, fmt.Errorf("voybit payment gateway returned invalid JSON: %w", err)
				}
			}
			return &CreatedPayment{
				Payment:   payment,
				Replayed:  response.Header.Get("Idempotency-Replayed") == "true",
				RequestID: requestID,
			}, nil
		}
		apiErr := decodeAPIError(response.StatusCode, requestID, raw)
		lastErr = apiErr
		if !retryable(response.StatusCode) || attempt == 3 {
			return nil, apiErr
		}
		if err := sleep(ctx, backoff(attempt, response.Header.Get("Retry-After"))); err != nil {
			return nil, err
		}
	}
	if lastErr == nil {
		lastErr = errors.New("voybit payment gateway request failed")
	}
	return nil, lastErr
}

func decodeAPIError(status int, requestID string, raw []byte) *APIError {
	apiErr := &APIError{
		Status:    status,
		Code:      "unknown_error",
		Message:   http.StatusText(status),
		RequestID: requestID,
	}
	var envelope struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(raw, &envelope) == nil {
		if envelope.Error.Code != "" {
			apiErr.Code = envelope.Error.Code
		}
		if envelope.Error.Message != "" {
			apiErr.Message = envelope.Error.Message
		}
	}
	return apiErr
}

func retryable(status int) bool {
	switch status {
	case http.StatusRequestTimeout, http.StatusTooManyRequests, http.StatusInternalServerError,
		http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	default:
		return false
	}
}

func backoff(attempt int, retryAfter string) time.Duration {
	if seconds, err := strconv.Atoi(strings.TrimSpace(retryAfter)); err == nil && seconds > 0 {
		return time.Duration(seconds) * time.Second
	}
	delay := 500 * time.Millisecond * time.Duration(1<<attempt)
	if delay > 8*time.Second {
		return 8 * time.Second
	}
	return delay
}

func sleep(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
