package main

import (
	"context"
	"fmt"
	"os"

	voybit "github.com/VOYBIT/voybit-payment-gateway-go"
)

func main() {
	client := voybit.New(os.Getenv("VOYBIT_API_KEY"))
	created, err := client.CreateCheckoutSession(context.Background(), voybit.CreateCheckoutSessionRequest{
		FiatAmount:           "25.00",
		FiatCurrency:         "USD",
		PaymentWindowSeconds: 1800,
		Description:          "Order 1001",
		Metadata:             map[string]any{"order_id": "1001"},
	}, "order:1001:attempt:1")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println(
		created.CheckoutSession.ID,
		created.CheckoutSession.Status,
		created.CheckoutSession.CheckoutURL,
	)
}
