package main

import (
	"context"
	"fmt"
	"os"

	voybit "github.com/VOYBIT/voybit-payment-gateway-go"
)

func main() {
	client := voybit.New(os.Getenv("VOYBIT_API_KEY"))
	created, err := client.CreatePayment(context.Background(), voybit.CreatePaymentRequest{
		AssetID:          os.Getenv("VOYBIT_ASSET_ID"),
		CryptoAmount:     "25.0000",
		AmountMinor:      2500,
		FiatCurrency:     "USD",
		ExpiresInSeconds: 1800,
		Description:      "Order 1001",
		Metadata:         map[string]any{"order_id": "1001"},
	}, "order:1001:attempt:1")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println(created.Payment.ID, created.Payment.Status, created.Payment.CheckoutURL)
}
