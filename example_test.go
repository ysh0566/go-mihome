package miot

import (
	"context"
	"fmt"
)

func ExampleNewCloudClient() {
	client, err := NewCloudClient(
		CloudConfig{
			ClientID:    "2882303761520431603",
			CloudServer: "cn",
		},
		WithCloudTokenProvider(exampleTokenProvider{token: "access-token"}),
	)
	if err != nil {
		fmt.Println("error")
		return
	}
	fmt.Println(client != nil)
	// Output: true
}

type exampleTokenProvider struct {
	token string
}

func (p exampleTokenProvider) AccessToken(context.Context) (string, error) {
	return p.token, nil
}
