package main

// A receiver started from the window keeps its certificate and pairing code
// across restarts, so replication jobs and senders that pinned them carry on
// after the receiver (or the computer) restarts. The private key and the
// code are sealed (DPAPI for the current user on Windows) before they are
// written; the certificate itself is public. The operator can ask for a new
// code and certificate at any start. The receiver command on boot media
// keeps nothing and makes new ones each start.

import (
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

type vmReceiverIdentity struct {
	Certificate tls.Certificate
	Thumbprint  string
	Code        string
}

type vmReceiverIdentityFile struct {
	Version     int    `json:"version"`
	Certificate []byte `json:"certificate"`
	// Sealed holds the private key and code, sealed.
	Sealed []byte `json:"sealed"`
}

type vmReceiverIdentitySecret struct {
	Key  []byte `json:"key"`
	Code string `json:"code"`
}

func vmNewReceiverIdentity(host string) (*vmReceiverIdentity, error) {
	code, err := vmPairingCode()
	if err != nil {
		return nil, err
	}
	certificate, thumbprint, err := vmSelfSignedCertificate(host)
	if err != nil {
		return nil, err
	}
	return &vmReceiverIdentity{Certificate: certificate, Thumbprint: thumbprint, Code: code}, nil
}

func vmSaveReceiverIdentity(path string, identity *vmReceiverIdentity, seal func([]byte) ([]byte, error)) error {
	key, err := x509.MarshalPKCS8PrivateKey(identity.Certificate.PrivateKey)
	if err != nil {
		return err
	}
	secret, err := json.Marshal(vmReceiverIdentitySecret{Key: key, Code: identity.Code})
	if err != nil {
		return err
	}
	sealed, err := seal(secret)
	if err != nil {
		return err
	}
	data, err := json.Marshal(vmReceiverIdentityFile{Version: 1, Certificate: identity.Certificate.Certificate[0], Sealed: sealed})
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	temporary := path + ".tmp"
	if err := os.WriteFile(temporary, data, 0600); err != nil {
		return err
	}
	return os.Rename(temporary, path)
}

func vmLoadReceiverIdentity(path string, unseal func([]byte) ([]byte, error)) (*vmReceiverIdentity, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var file vmReceiverIdentityFile
	if err := json.Unmarshal(data, &file); err != nil {
		return nil, err
	}
	if file.Version != 1 || len(file.Certificate) == 0 || len(file.Sealed) == 0 {
		return nil, errors.New("the saved receiver identity is not one this version reads")
	}
	plain, err := unseal(file.Sealed)
	if err != nil {
		return nil, err
	}
	var secret vmReceiverIdentitySecret
	if err := json.Unmarshal(plain, &secret); err != nil {
		return nil, err
	}
	certificate, err := tls.X509KeyPair(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: file.Certificate}), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: secret.Key}))
	if err != nil {
		return nil, fmt.Errorf("the saved receiver key does not match its certificate: %w", err)
	}
	if len(vmNormalizePairingCode(secret.Code)) != 20 {
		return nil, errors.New("the saved pairing code is not valid")
	}
	sum := sha256.Sum256(file.Certificate)
	return &vmReceiverIdentity{Certificate: certificate, Thumbprint: vmNormalizeThumbprint(hex.EncodeToString(sum[:])), Code: secret.Code}, nil
}

// vmReceiverIdentityFor returns the saved identity, or makes and saves a new
// one when renew is set or nothing usable is saved. kept says the saved one
// was used. A save failure still returns the new identity (it then lasts
// only until the receiver stops) with the error for the log.
func vmReceiverIdentityFor(path, host string, renew bool, seal, unseal func([]byte) ([]byte, error)) (identity *vmReceiverIdentity, kept bool, saveErr error, err error) {
	if !renew {
		if saved, loadErr := vmLoadReceiverIdentity(path, unseal); loadErr == nil {
			return saved, true, nil, nil
		}
	}
	identity, err = vmNewReceiverIdentity(host)
	if err != nil {
		return nil, false, nil, err
	}
	return identity, false, vmSaveReceiverIdentity(path, identity, seal), nil
}
