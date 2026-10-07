package voybit

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"
)

const webhookTolerance = 5 * time.Minute

// Event is a payment webhook body.
type Event struct {
	ID             string    `json:"id"`
	Type           string    `json:"type"`
	PaymentID      string    `json:"payment_id"`
	PublicID       string    `json:"public_id"`
	Status         string    `json:"status"`
	Description    string    `json:"description,omitempty"`
	Asset          string    `json:"asset,omitempty"`
	Network        string    `json:"network,omitempty"`
	ExpectedAmount string    `json:"expected_amount,omitempty"`
	ReceivedAmount string    `json:"received_amount,omitempty"`
	CheckoutURL    string    `json:"checkout_url,omitempty"`
	ExpiresAt      time.Time `json:"expires_at,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
}

// VerifyWebhook checks id, timestamp, and signature against the raw body.
// secret is the gateway webhook secret. now may be zero to use the current time.
func VerifyWebhook(secret, id, timestamp, signature string, rawBody []byte, now time.Time) error {
	if secret == "" || id == "" || !strings.HasPrefix(signature, "v1=") {
		return errors.New("webhook signature is invalid")
	}
	supplied, err := hex.DecodeString(strings.TrimPrefix(signature, "v1="))
	seconds, stampErr := strconv.ParseInt(timestamp, 10, 64)
	if err != nil || stampErr != nil || len(supplied) != sha256.Size || len(signature) != 3+sha256.Size*2 {
		return errors.New("webhook signature is invalid")
	}
	if now.IsZero() {
		now = time.Now()
	}
	age := now.Unix() - seconds
	if age < 0 {
		age = -age
	}
	if age > int64(webhookTolerance/time.Second) {
		return errors.New("webhook timestamp is outside the 5 minute window")
	}
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(id + "." + timestamp + "."))
	_, _ = mac.Write(rawBody)
	if !hmac.Equal(mac.Sum(nil), supplied) {
		return errors.New("webhook signature does not match")
	}
	return nil
}

// ParseEvent decodes a body that VerifyWebhook has already accepted.
func ParseEvent(rawBody []byte) (Event, error) {
	var event Event
	if err := json.Unmarshal(rawBody, &event); err != nil {
		return Event{}, err
	}
	return event, nil
}
