package miot

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"time"
)

const (
	certDomain     = "cert"
	caCertFileName = "mihome_ca.cert"
)

const miHomeCACertPEM = `-----BEGIN CERTIFICATE-----
MIIBazCCAQ+gAwIBAgIEA/UKYDAMBggqhkjOPQQDAgUAMCIxEzARBgNVBAoTCk1p
amlhIFJvb3QxCzAJBgNVBAYTAkNOMCAXDTE2MTEyMzAxMzk0NVoYDzIwNjYxMTEx
MDEzOTQ1WjAiMRMwEQYDVQQKEwpNaWppYSBSb290MQswCQYDVQQGEwJDTjBZMBMG
ByqGSM49AgEGCCqGSM49AwEHA0IABL71iwLa4//4VBqgRI+6xE23xpovqPCxtv96
2VHbZij61/Ag6jmi7oZ/3Xg/3C+whglcwoUEE6KALGJ9vccV9PmjLzAtMAwGA1Ud
EwQFMAMBAf8wHQYDVR0OBBYEFJa3onw5sblmM6n40QmyAGDI5sURMAwGCCqGSM49
BAMCBQADSAAwRQIgchciK9h6tZmfrP8Ka6KziQ4Lv3hKfrHtAZXMHPda4IYCIQCG
az93ggFcbrG9u2wixjx1HKW4DUA5NXZG0wWQTpJTbQ==
-----END CERTIFICATE-----
-----BEGIN CERTIFICATE-----
MIIBjzCCATWgAwIBAgIBATAKBggqhkjOPQQDAjAiMRMwEQYDVQQKEwpNaWppYSBS
b290MQswCQYDVQQGEwJDTjAgFw0yMjA2MDkxNDE0MThaGA8yMDcyMDUyNzE0MTQx
OFowLDELMAkGA1UEBhMCQ04xHTAbBgNVBAoMFE1JT1QgQ0VOVFJBTCBHQVRFV0FZ
MFkwEwYHKoZIzj0CAQYIKoZIzj0DAQcDQgAEdYrzbnp/0x/cZLZnuEDXTFf8mhj4
CVpZPwgj9e9Ve5r3K7zvu8Jjj7JF1JjQYvEC6yhp1SzBgglnK4L8xQzdiqNQME4w
HQYDVR0OBBYEFCf9+YBU7pXDs6K6CAQPRhlGJ+cuMB8GA1UdIwQYMBaAFJa3onw5
sblmM6n40QmyAGDI5sURMAwGA1UdEwQFMAMBAf8wCgYIKoZIzj0EAwIDSAAwRQIh
AKUv+c8v98vypkGMTzMwckGjjVqTef8xodsy6PhcSCq+AiA/n9mDs62hAo5zXyJy
Bs1s7mqXPf1XgieoxIvs1MqyiA==
-----END CERTIFICATE-----
`

const miHomeCACertSHA256 = "8b7bf306be3632e08b0ead308249e5f2b2520dc921ad143872d5fcc7c68d6759"

// CertManagerOption configures a CertManager instance.
type CertManagerOption func(*CertManager)

// CertManager manages MIoT CA, user key, and user certificate files.
type CertManager struct {
	store       FileStore
	root        string
	uid         string
	cloudServer string
	clock       Clock
}

// WithCertClock injects a test clock into CertManager.
func WithCertClock(clock Clock) CertManagerOption {
	return func(m *CertManager) {
		if clock != nil {
			m.clock = clock
		}
	}
}

// NewCertManager creates a certificate manager for one user and region.
func NewCertManager(store RawFileBackend, uid, cloudServer string, opts ...CertManagerOption) (*CertManager, error) {
	if store == nil {
		return nil, &Error{Code: ErrInvalidArgument, Op: "new cert manager", Msg: "store is nil"}
	}
	if uid == "" {
		return nil, &Error{Code: ErrInvalidArgument, Op: "new cert manager", Msg: "uid is empty"}
	}
	if cloudServer == "" {
		return nil, &Error{Code: ErrInvalidArgument, Op: "new cert manager", Msg: "cloud server is empty"}
	}
	manager := &CertManager{
		store:       store.RawFiles(),
		root:        store.RootPath(),
		uid:         uid,
		cloudServer: cloudServer,
		clock:       realClock{},
	}
	for _, opt := range opts {
		if opt != nil {
			opt(manager)
		}
	}
	return manager, nil
}

// CAPath returns the raw filesystem path of the MIoT CA bundle.
func (m *CertManager) CAPath() string {
	return filepath.Join(m.root, certDomain, caCertFileName)
}

// KeyPath returns the raw filesystem path of the stored user key.
func (m *CertManager) KeyPath() string {
	return filepath.Join(m.root, certDomain, fmt.Sprintf("%s_%s.key", m.uid, m.cloudServer))
}

// CertPath returns the raw filesystem path of the stored user certificate.
func (m *CertManager) CertPath() string {
	return filepath.Join(m.root, certDomain, fmt.Sprintf("%s_%s.cert", m.uid, m.cloudServer))
}

// VerifyCACert ensures the bundled CA file exists and matches the expected hash.
func (m *CertManager) VerifyCACert(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	data, err := m.store.ReadFile(m.CAPath())
	if errors.Is(err, fs.ErrNotExist) {
		if err := m.writeRawFile(m.CAPath(), []byte(miHomeCACertPEM)); err != nil {
			return err
		}
		data, err = m.store.ReadFile(m.CAPath())
	}
	if err != nil {
		return err
	}
	sum := sha256.Sum256(data)
	if hex.EncodeToString(sum[:]) != miHomeCACertSHA256 {
		return &Error{Code: ErrInvalidResponse, Op: "verify ca cert", Msg: "ca certificate hash mismatch"}
	}
	return nil
}

// UserCertRemaining validates a user certificate and reports the remaining lifetime.
func (m *CertManager) UserCertRemaining(ctx context.Context, certPEM []byte, did string) (time.Duration, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if len(certPEM) == 0 {
		var err error
		certPEM, err = m.LoadUserCert(ctx)
		if err != nil {
			return 0, err
		}
	}
	block, _ := pem.Decode(certPEM)
	if block == nil {
		return 0, &Error{Code: ErrInvalidResponse, Op: "parse user cert", Msg: "invalid certificate PEM"}
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return 0, Wrap(ErrInvalidResponse, "parse user cert", err)
	}
	if len(cert.Subject.Country) != 1 || cert.Subject.Country[0] != "CN" {
		return 0, &Error{Code: ErrInvalidResponse, Op: "validate user cert", Msg: "invalid certificate country"}
	}
	if len(cert.Subject.Organization) != 1 || cert.Subject.Organization[0] != "Mijia Device" {
		return 0, &Error{Code: ErrInvalidResponse, Op: "validate user cert", Msg: "invalid certificate organization"}
	}
	if did != "" && cert.Subject.CommonName != m.commonNameForDID(did) {
		return 0, &Error{Code: ErrInvalidResponse, Op: "validate user cert", Msg: "invalid certificate common name"}
	}
	now := m.clock.Now()
	if now.Before(cert.NotBefore) || now.After(cert.NotAfter) {
		return 0, &Error{Code: ErrInvalidResponse, Op: "validate user cert", Msg: "certificate is not valid at the current time"}
	}
	return cert.NotAfter.Sub(now), nil
}

// GenerateUserKey creates a PEM-encoded PKCS8 Ed25519 private key.
func (m *CertManager) GenerateUserKey() ([]byte, error) {
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		return nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}), nil
}

// GenerateUserCSR creates a PEM-encoded CSR for the provided device ID.
func (m *CertManager) GenerateUserCSR(keyPEM []byte, did string) ([]byte, error) {
	if did == "" {
		return nil, &Error{Code: ErrInvalidArgument, Op: "generate user csr", Msg: "did is empty"}
	}
	block, _ := pem.Decode(keyPEM)
	if block == nil {
		return nil, &Error{Code: ErrInvalidArgument, Op: "generate user csr", Msg: "invalid private key PEM"}
	}
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, Wrap(ErrInvalidArgument, "parse private key", err)
	}
	privateKey, ok := key.(ed25519.PrivateKey)
	if !ok {
		return nil, &Error{Code: ErrInvalidArgument, Op: "generate user csr", Msg: "private key is not ed25519"}
	}
	template := &x509.CertificateRequest{
		Subject: pkix.Name{
			Country:      []string{"CN"},
			Organization: []string{"Mijia Device"},
			CommonName:   m.commonNameForDID(did),
		},
	}
	csrDER, err := x509.CreateCertificateRequest(rand.Reader, template, privateKey)
	if err != nil {
		return nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csrDER}), nil
}

// LoadUserKey loads the stored user private key PEM.
func (m *CertManager) LoadUserKey(ctx context.Context) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return m.store.ReadFile(m.KeyPath())
}

// UpdateUserKey stores a PEM-encoded user private key.
func (m *CertManager) UpdateUserKey(ctx context.Context, keyPEM []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return m.writeRawFile(m.KeyPath(), keyPEM)
}

// LoadUserCert loads the stored user certificate PEM.
func (m *CertManager) LoadUserCert(ctx context.Context) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return m.store.ReadFile(m.CertPath())
}

// UpdateUserCert stores a PEM-encoded user certificate.
func (m *CertManager) UpdateUserCert(ctx context.Context, certPEM []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return m.writeRawFile(m.CertPath(), certPEM)
}

// RemoveCACert removes the stored CA bundle if it exists.
func (m *CertManager) RemoveCACert(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return removeRawFile(m.store, m.CAPath())
}

// RemoveUserKey removes the stored user key if it exists.
func (m *CertManager) RemoveUserKey(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return removeRawFile(m.store, m.KeyPath())
}

// RemoveUserCert removes the stored user certificate if it exists.
func (m *CertManager) RemoveUserCert(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return removeRawFile(m.store, m.CertPath())
}

func (m *CertManager) commonNameForDID(did string) string {
	sum := sha1.Sum([]byte(did))
	return "mips." + m.uid + "." + hex.EncodeToString(sum[:]) + ".2"
}

func (m *CertManager) writeRawFile(fullPath string, data []byte) error {
	if err := m.store.MkdirAll(filepath.Dir(fullPath), storageDirectoryPerm); err != nil {
		return err
	}
	return m.store.WriteFile(fullPath, data, storageFilePerm)
}

func removeRawFile(store FileStore, fullPath string) error {
	err := store.Remove(fullPath)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}

type realClock struct{}

func (realClock) Now() time.Time {
	return time.Now()
}

func (realClock) After(d time.Duration) <-chan time.Time {
	return time.After(d)
}

func (realClock) NewTicker(d time.Duration) Ticker {
	return realTicker{ticker: time.NewTicker(d)}
}

func (realClock) NewTimer(d time.Duration) Timer {
	return realTimer{timer: time.NewTimer(d)}
}

type realTicker struct {
	ticker *time.Ticker
}

func (t realTicker) C() <-chan time.Time {
	return t.ticker.C
}

func (t realTicker) Stop() {
	t.ticker.Stop()
}

type realTimer struct {
	timer *time.Timer
}

func (t realTimer) C() <-chan time.Time {
	return t.timer.C
}

func (t realTimer) Stop() bool {
	return t.timer.Stop()
}

func (t realTimer) Reset(d time.Duration) bool {
	return t.timer.Reset(d)
}
