package exampleutil

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"

	miot "github.com/ysh0566/go-mihome"
)

// CloudConfig stores example-level Xiaomi cloud credentials and cache settings.
type CloudConfig struct {
	ClientID     string
	CloudServer  string
	AccessToken  string
	RefreshToken string
	StorageDir   string
}

// PropertySelection identifies one MIoT property target for read-only examples.
type PropertySelection struct {
	DeviceDID string
	SIID      int
	PIID      int
}

// SpecSelection identifies one MIoT spec instance to parse.
type SpecSelection struct {
	URN string
}

// LANConfig stores the LAN transport credentials and property target used by the LAN example.
type LANConfig struct {
	Device   miot.LANDeviceConfig
	Property PropertySelection
}

type staticTokenProvider struct {
	token string
}

func (p staticTokenProvider) AccessToken(context.Context) (string, error) {
	token := strings.TrimSpace(p.token)
	if token == "" {
		return "", fmt.Errorf("missing MIOT_ACCESS_TOKEN")
	}
	return token, nil
}

// LookupString returns the environment variable when set, otherwise the fallback value.
func LookupString(key, fallback string) string {
	if value, ok := os.LookupEnv(key); ok {
		return strings.TrimSpace(value)
	}
	return fallback
}

// LookupInt returns the parsed environment variable when set, otherwise the fallback value.
func LookupInt(key string, fallback int) (int, error) {
	value, ok := os.LookupEnv(key)
	if !ok {
		return fallback, nil
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer: %w", key, err)
	}
	return parsed, nil
}

// LoadCloudConfig reads example cloud configuration from environment variables with fallback defaults.
func LoadCloudConfig(defaults CloudConfig) (CloudConfig, error) {
	cfg := defaults
	cfg.ClientID = LookupString("MIOT_CLIENT_ID", cfg.ClientID)
	cfg.CloudServer = LookupString("MIOT_CLOUD_SERVER", cfg.CloudServer)
	cfg.AccessToken = LookupString("MIOT_ACCESS_TOKEN", cfg.AccessToken)
	cfg.RefreshToken = LookupString("MIOT_REFRESH_TOKEN", cfg.RefreshToken)
	cfg.StorageDir = LookupString("MIOT_STORAGE_DIR", cfg.StorageDir)
	if cfg.CloudServer == "" {
		cfg.CloudServer = "cn"
	}
	return cfg, nil
}

// Validate checks whether the cloud config is ready for use.
func (c CloudConfig) Validate(requireToken bool) error {
	if strings.TrimSpace(c.ClientID) == "" {
		return fmt.Errorf("missing MIOT_CLIENT_ID or default client id")
	}
	if strings.TrimSpace(c.CloudServer) == "" {
		return fmt.Errorf("missing MIOT_CLOUD_SERVER or default cloud server")
	}
	if requireToken && strings.TrimSpace(c.AccessToken) == "" {
		return fmt.Errorf("missing MIOT_ACCESS_TOKEN or default access token")
	}
	return nil
}

// NewCloudClient constructs a Xiaomi cloud client using a static token provider.
func (c CloudConfig) NewCloudClient() (*miot.CloudClient, error) {
	if err := c.Validate(true); err != nil {
		return nil, err
	}
	return miot.NewCloudClient(
		miot.CloudConfig{
			ClientID:    c.ClientID,
			CloudServer: c.CloudServer,
		},
		miot.WithCloudTokenProvider(staticTokenProvider{token: c.AccessToken}),
	)
}

// NewStorage constructs a typed MIoT storage cache rooted at the configured directory.
func (c CloudConfig) NewStorage() (*miot.Storage, error) {
	if strings.TrimSpace(c.StorageDir) == "" {
		return nil, fmt.Errorf("missing MIOT_STORAGE_DIR or default storage dir")
	}
	return miot.NewStorage(c.StorageDir)
}

// TokenProvider returns a static token provider for example clients that need the interface directly.
func (c CloudConfig) TokenProvider() miot.TokenProvider {
	return staticTokenProvider{token: c.AccessToken}
}

// LoadPropertySelection reads the read-only property target from environment variables with fallback defaults.
func LoadPropertySelection(defaults PropertySelection) (PropertySelection, error) {
	selection := defaults
	selection.DeviceDID = LookupString("MIOT_DEVICE_DID", selection.DeviceDID)

	siid, err := LookupInt("MIOT_PROPERTY_SIID", selection.SIID)
	if err != nil {
		return PropertySelection{}, err
	}
	selection.SIID = siid

	piid, err := LookupInt("MIOT_PROPERTY_PIID", selection.PIID)
	if err != nil {
		return PropertySelection{}, err
	}
	selection.PIID = piid
	return selection, nil
}

// Validate checks whether the property target is complete.
func (s PropertySelection) Validate() error {
	if strings.TrimSpace(s.DeviceDID) == "" {
		return fmt.Errorf("missing MIOT_DEVICE_DID or default device did")
	}
	if s.SIID <= 0 {
		return fmt.Errorf("missing MIOT_PROPERTY_SIID or default property siid")
	}
	if s.PIID <= 0 {
		return fmt.Errorf("missing MIOT_PROPERTY_PIID or default property piid")
	}
	return nil
}

// Query converts the selection into the typed MIoT property query.
func (s PropertySelection) Query() miot.PropertyQuery {
	return miot.PropertyQuery{
		DID:  s.DeviceDID,
		SIID: s.SIID,
		PIID: s.PIID,
	}
}

// LoadSpecSelection reads the spec URN used by parsing examples.
func LoadSpecSelection(defaultURN string) (SpecSelection, error) {
	return SpecSelection{
		URN: LookupString("MIOT_SPEC_URN", defaultURN),
	}, nil
}

// Validate checks whether the spec selection contains a URN.
func (s SpecSelection) Validate() error {
	if strings.TrimSpace(s.URN) == "" {
		return fmt.Errorf("missing MIOT_SPEC_URN or default spec urn")
	}
	return nil
}

// LoadLANConfig reads LAN device credentials and route details from environment variables with fallback defaults.
func LoadLANConfig(defaults LANConfig) (LANConfig, error) {
	cfg := defaults
	cfg.Device.DID = LookupString("MIOT_LAN_DID", cfg.Device.DID)
	cfg.Device.Token = LookupString("MIOT_LAN_TOKEN", cfg.Device.Token)
	cfg.Device.IP = LookupString("MIOT_LAN_IP", cfg.Device.IP)
	cfg.Device.Interface = LookupString("MIOT_LAN_IFACE", cfg.Device.Interface)

	propDefaults := cfg.Property
	if strings.TrimSpace(propDefaults.DeviceDID) == "" {
		propDefaults.DeviceDID = cfg.Device.DID
	}
	property, err := LoadPropertySelection(propDefaults)
	if err != nil {
		return LANConfig{}, err
	}
	cfg.Property = property
	return cfg, nil
}

// Validate checks whether the LAN example has enough configuration to connect and read one property.
func (c LANConfig) Validate() error {
	if strings.TrimSpace(c.Device.DID) == "" {
		return fmt.Errorf("missing MIOT_LAN_DID or default LAN did")
	}
	if strings.TrimSpace(c.Device.Token) == "" {
		return fmt.Errorf("missing MIOT_LAN_TOKEN or default LAN token")
	}
	if strings.TrimSpace(c.Device.IP) == "" {
		return fmt.Errorf("missing MIOT_LAN_IP or default LAN ip")
	}
	if c.Property.DeviceDID == "" {
		c.Property.DeviceDID = c.Device.DID
	}
	return c.Property.Validate()
}
