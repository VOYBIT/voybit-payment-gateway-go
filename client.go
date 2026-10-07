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
	userAgent      = "voybit-payment-gateway-go/0.1.0"
)

var (
	idempotencyPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{7,127}$`)
	errInvalidResponse = errors.New("payment gateway returned invalid JSON")
)

// Client calls POST /gateway/payments.
type Client struct {
	APIKey  string
	BaseURL string
	HTTP    *http.Client
}

// New returns a client. apiKey stays on the server.
func New(apiKey string) *Client {
	return &Client{APIKey: apiKey}
}

// CreatePaymentRequest is the JSON body of POST /gateway/payments.
type CreatePaymentRequest struct {
	GatewayID        string         `json:"gateway_id,omitempty"`
	AssetID          string         `json:"asset_id"`
	CryptoAmount     string         `json:"crypto_amount"`
	AmountMinor      int64          `json:"amount_minor"`
	FiatCurrency     string         `json:"fiat_currency"`
	ExpiresInSeconds int64          `json:"expires_in_seconds,omitempty"`
	Description      string         `json:"description,omitempty"`
	Metadata         map[string]any `json:"metadata,omitempty"`
}

// Payment is a payment gateway response.
type Payment struct {
	ID             string          `json:"id"`
	PublicID       string          `json:"public_id"`
	GatewayID      string          `json:"gateway_id,omitempty"`
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

// Deposit is the address and amount for this payment.
type Deposit struct {
	Status  string `json:"status"`
	Address string `json:"address,omitempty"`
	URI     string `json:"payment_uri,omitempty"`
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

// APIError is a non-success response.
type APIError struct {
	Status    int
	Code      string
	Message   string
	RequestID string
}

func (e *APIError) Error() string {
	message := e.Message
	if message == "" {
		message = e.Code
	}
	if e.RequestID == "" {
		return "payment gateway: " + message
	}
	return fmt.Sprintf("payment gateway: %s (%s)", message, e.RequestID)
}

// CreatePayment creates a payment.
// Reuse idempotencyKey only when the request body is unchanged.
func (c *Client) CreatePayment(ctx context.Context, request CreatePaymentRequest, idempotencyKey string) (*CreatedPayment, error) {
	if strings.TrimSpace(c.APIKey) == "" {
		return nil, errors.New("API key is required")
	}
	if !idempotencyPattern.MatchString(idempotencyKey) {
		return nil, errors.New("Idempotency-Key must be 8 to 128 URL-safe characters")
	}
	body, err := json.Marshal(request)
	if err != nil {
		return nil, err
	}
	base := strings.TrimRight(c.BaseURL, "/")
	if base == "" {
		base = DefaultBaseURL
	}

	var last error
	for attempt := 0; attempt < 4; attempt++ {
		created, retryAfter, err := c.post(ctx, base+"/gateway/payments", body, idempotencyKey)
		if err == nil {
			return created, nil
		}
		last = err
		var apiErr *APIError
		stop := errors.Is(err, errInvalidResponse) || ctx.Err() != nil || attempt == 3
		if errors.As(err, &apiErr) {
			stop = stop || !retryable(apiErr.Status)
		} else if !isTransportError(err) {
			stop = true
		}
		if stop {
			return nil, err
		}
		if err := sleep(ctx, backoff(attempt, retryAfter)); err != nil {
			return nil, err
		}
	}
	if last == nil {
		last = errors.New("payment gateway request failed")
	}
	return nil, last
}

func (c *Client) post(ctx context.Context, endpoint string, body []byte, idempotencyKey string) (*CreatedPayment, string, error) {
	attemptCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	httpRequest, err := http.NewRequestWithContext(attemptCtx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, "", err
	}
	httpRequest.Header.Set("X-Voybit-Api-Key", c.APIKey)
	httpRequest.Header.Set("Idempotency-Key", idempotencyKey)
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("Accept", "application/json")
	httpRequest.Header.Set("User-Agent", userAgent)

	response, err := c.httpClient().Do(httpRequest)
	if err != nil {
		return nil, "", err
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return nil, "", err
	}
	requestID := response.Header.Get("X-Request-ID")
	if response.StatusCode >= 200 && response.StatusCode < 300 {
		var payment Payment
		if len(bytes.TrimSpace(raw)) > 0 {
			if err := json.Unmarshal(raw, &payment); err != nil {
				return nil, "", fmt.Errorf("%w: %v", errInvalidResponse, err)
			}
		}
		return &CreatedPayment{
			Payment:   payment,
			Replayed:  response.Header.Get("Idempotency-Replayed") == "true",
			RequestID: requestID,
		}, "", nil
	}
	return nil, response.Header.Get("Retry-After"), decodeAPIError(response.StatusCode, requestID, raw)
}

func (c *Client) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{CheckRedirect: refuseRedirect}
}

func refuseRedirect(*http.Request, []*http.Request) error {
	return http.ErrUseLastResponse
}

func decodeAPIError(status int, requestID string, raw []byte) *APIError {
	apiErr := &APIError{Status: status, Code: "unknown_error", Message: http.StatusText(status), RequestID: requestID}
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

func isTransportError(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return true
	}
	var netErr interface{ Timeout() bool }
	return errors.As(err, &netErr)
}

func retryable(status int) bool {
	switch status {
	case http.StatusRequestTimeout, http.StatusTooManyRequests,
		http.StatusInternalServerError, http.StatusBadGateway,
		http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	default:
		return false
	}
}

func backoff(attempt int, retryAfter string) time.Duration {
	if seconds, err := strconv.Atoi(strings.TrimSpace(retryAfter)); err == nil && seconds > 0 {
		if seconds > 30 {
			seconds = 30
		}
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
