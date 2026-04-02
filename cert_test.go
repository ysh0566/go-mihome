package miot

import (
	"context"
	"crypto/sha1"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"os"
	"testing"
)

func TestCertManagerGenerateCSR(t *testing.T) {
	store, err := NewStorage(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	mgr, err := NewCertManager(store, "100000001", "cn")
	if err != nil {
		t.Fatal(err)
	}

	keyPEM, err := mgr.GenerateUserKey()
	if err != nil {
		t.Fatal(err)
	}
	keyBlock, _ := pem.Decode(keyPEM)
	if keyBlock == nil || keyBlock.Type != "PRIVATE KEY" {
		t.Fatalf("private key PEM block = %#v", keyBlock)
	}
	if _, err := x509.ParsePKCS8PrivateKey(keyBlock.Bytes); err != nil {
		t.Fatalf("ParsePKCS8PrivateKey() error = %v", err)
	}

	did := "demo-csr-device-1"
	csrPEM, err := mgr.GenerateUserCSR(keyPEM, did)
	if err != nil {
		t.Fatal(err)
	}
	csrBlock, _ := pem.Decode(csrPEM)
	if csrBlock == nil || csrBlock.Type != "CERTIFICATE REQUEST" {
		t.Fatalf("CSR PEM block = %#v", csrBlock)
	}
	csr, err := x509.ParseCertificateRequest(csrBlock.Bytes)
	if err != nil {
		t.Fatalf("ParseCertificateRequest() error = %v", err)
	}

	sum := sha1.Sum([]byte(did))
	wantCN := "mips.100000001." + hex.EncodeToString(sum[:]) + ".2"
	if csr.Subject.CommonName != wantCN {
		t.Fatalf("CSR common name = %q, want %q", csr.Subject.CommonName, wantCN)
	}
	if len(csr.Subject.Country) != 1 || csr.Subject.Country[0] != "CN" {
		t.Fatalf("CSR country = %#v", csr.Subject.Country)
	}
	if len(csr.Subject.Organization) != 1 || csr.Subject.Organization[0] != "Mijia Device" {
		t.Fatalf("CSR organization = %#v", csr.Subject.Organization)
	}
}

func TestCertManagerVerifyCACertCreatesFile(t *testing.T) {
	ctx := context.Background()
	store, err := NewStorage(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	mgr, err := NewCertManager(store, "100000001", "cn")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(mgr.CAPath()); !os.IsNotExist(err) {
		t.Fatalf("Stat(CAPath()) error = %v, want not exist", err)
	}

	if err := mgr.VerifyCACert(ctx); err != nil {
		t.Fatal(err)
	}
	if err := mgr.VerifyCACert(ctx); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(mgr.CAPath())
	if err != nil {
		t.Fatal(err)
	}
	if len(data) == 0 {
		t.Fatal("CA certificate file is empty")
	}
}
