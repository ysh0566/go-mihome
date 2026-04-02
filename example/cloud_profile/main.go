package main

import (
	"context"
	"log"

	miot "github.com/ysh0566/go-mihome"
	"github.com/ysh0566/go-mihome/example/internal/exampleutil"
)

const (
	defaultClientID    = "2882303761520431603"
	defaultCloudServer = "cn"
)

func main() {
	log.SetFlags(0)

	cfg, err := exampleutil.LoadCloudConfig(exampleutil.CloudConfig{
		ClientID:    defaultClientID,
		CloudServer: defaultCloudServer,
	})
	if err != nil {
		log.Fatal(err)
	}

	client, err := cfg.NewCloudClient()
	if err != nil {
		log.Fatal(err)
	}

	profile, err := client.GetUserInfo(context.Background())
	if err != nil {
		log.Fatal(err)
	}

	if err := exampleutil.PrintJSONStdout(struct {
		ClientID    string              `json:"client_id"`
		CloudServer string              `json:"cloud_server"`
		Profile     miot.AccountProfile `json:"profile"`
	}{
		ClientID:    cfg.ClientID,
		CloudServer: cfg.CloudServer,
		Profile:     profile,
	}); err != nil {
		log.Fatal(err)
	}
}
