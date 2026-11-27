package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/binary"
	"math/big"
	"strings"
	"testing"
	"time"
)

// testPE builds a small PE32+ file with one .rsrc section holding an
// RT_VERSION resource for 8.0.3.
func testPE() []byte {
	data := make([]byte, 0x400)
	copy(data, "MZ")
	binary.LittleEndian.PutUint32(data[0x3c:], 0x40)
	copy(data[0x40:], "PE\x00\x00")
	binary.LittleEndian.PutUint16(data[0x44:], 0x8664)
	binary.LittleEndian.PutUint16(data[0x46:], 1)   // sections
	binary.LittleEndian.PutUint16(data[0x54:], 240) // optional header size
	opt := 0x58
	binary.LittleEndian.PutUint16(data[opt:], 0x20b)
	binary.LittleEndian.PutUint32(data[opt+108:], 16)
	dirs := opt + 112
	binary.LittleEndian.PutUint32(data[dirs+16:], 0x1000) // resources
	binary.LittleEndian.PutUint32(data[dirs+20:], 0x200)
	section := opt + 240
	copy(data[section:], ".rsrc")
	binary.LittleEndian.PutUint32(data[section+8:], 0x200)
	binary.LittleEndian.PutUint32(data[section+12:], 0x1000)
	binary.LittleEndian.PutUint32(data[section+16:], 0x200)
	binary.LittleEndian.PutUint32(data[section+20:], 0x200)
	r := data[0x200:]
	directory := func(at int, id, target uint32) {
		binary.LittleEndian.PutUint16(r[at+14:], 1)
		binary.LittleEndian.PutUint32(r[at+16:], id)
		binary.LittleEndian.PutUint32(r[at+20:], target)
	}
	directory(0x00, 16, 0x80000018) // RT_VERSION -> name directory
	directory(0x18, 1, 0x80000030)  // name 1 -> language directory
	directory(0x30, 0x409, 0x48)    // language -> data entry
	binary.LittleEndian.PutUint32(r[0x48:], 0x1060)
	binary.LittleEndian.PutUint32(r[0x4c:], 0x60)
	block := r[0x60:]
	copy(block[40:], []byte{0xBD, 0x04, 0xEF, 0xFE})
	binary.LittleEndian.PutUint32(block[48:], 8<<16|0)
	binary.LittleEndian.PutUint32(block[52:], 3<<16|1234)
	return data
}

type testSigner struct {
	root, leaf *x509.Certificate
	key        *ecdsa.PrivateKey
}

func newTestSigner(t *testing.T, organisation string) testSigner {
	rootKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	root := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Test Root"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	rootDER, _ := x509.CreateCertificate(rand.Reader, root, root, &rootKey.PublicKey, rootKey)
	root, _ = x509.ParseCertificate(rootDER)
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	leaf := &x509.Certificate{SerialNumber: big.NewInt(77), Subject: pkix.Name{CommonName: organisation, Organization: []string{organisation}}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageCodeSigning}}
	leafDER, err := x509.CreateCertificate(rand.Reader, leaf, root, &key.PublicKey, rootKey)
	if err != nil {
		t.Fatal(err)
	}
	leaf, _ = x509.ParseCertificate(leafDER)
	return testSigner{root: root, leaf: leaf, key: key}
}

func asnTagged(tag int, compound bool, content []byte) []byte {
	out, _ := asn1.Marshal(asn1.RawValue{Class: asn1.ClassContextSpecific, Tag: tag, IsCompound: compound, Bytes: content})
	return out
}

func asnSequence(parts ...[]byte) []byte {
	var content []byte
	for _, part := range parts {
		content = append(content, part...)
	}
	out, _ := asn1.Marshal(asn1.RawValue{Class: asn1.ClassUniversal, Tag: asn1.TagSequence, IsCompound: true, Bytes: content})
	return out
}

func asnSet(parts ...[]byte) []byte {
	var content []byte
	for _, part := range parts {
		content = append(content, part...)
	}
	out, _ := asn1.Marshal(asn1.RawValue{Class: asn1.ClassUniversal, Tag: asn1.TagSet, IsCompound: true, Bytes: content})
	return out
}

func asnOID(oid asn1.ObjectIdentifier) []byte { out, _ := asn1.Marshal(oid); return out }
func asnBytes(b []byte) []byte                { out, _ := asn1.Marshal(b); return out }

// sign appends an Authenticode signature by s over data. badAttributes
// signs a messageDigest that does not match the content.
func (s testSigner) sign(t *testing.T, data []byte, badAttributes bool) []byte {
	data = append([]byte(nil), data...)
	layout, err := parsePELayout(data)
	if err != nil {
		t.Fatal(err)
	}
	layout.certOffset = len(data)
	fileHash := peFileHash(data, layout, sha256.New)
	sha256ID := asnSequence(asnOID(oidDigestSHA256), []byte{0x05, 0x00})
	indirect := asnSequence(
		asnSequence(asnOID(asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 311, 2, 1, 15}), asnSequence()),
		asnSequence(sha256ID, asnBytes(fileHash)))
	var inner asn1.RawValue
	asn1.Unmarshal(indirect, &inner)
	contentDigest := sha256.Sum256(inner.Bytes)
	if badAttributes {
		contentDigest[0] ^= 1
	}
	attributes := [][]byte{
		asnSequence(asnOID(oidAttrContentType), asnSet(asnOID(oidSpcIndirectData))),
		asnSequence(asnOID(oidAttrMessageDigest), asnSet(asnBytes(contentDigest[:]))),
	}
	attributeSet := asnSet(attributes...)
	sum := sha256.Sum256(attributeSet)
	signature, _ := ecdsa.SignASN1(rand.Reader, s.key, sum[:])
	serial, _ := asn1.Marshal(s.leaf.SerialNumber)
	signerInfo := asnSequence([]byte{0x02, 0x01, 0x01},
		asnSequence(s.leaf.RawIssuer, serial),
		sha256ID,
		append([]byte{0xA0}, attributeSet[1:]...),
		asnSequence(asnOID(asn1.ObjectIdentifier{1, 2, 840, 10045, 4, 3, 2})),
		asnBytes(signature))
	signedData := asnSequence([]byte{0x02, 0x01, 0x01},
		asnSet(sha256ID),
		asnSequence(asnOID(oidSpcIndirectData), asnTagged(0, true, indirect)),
		asnTagged(0, true, s.leaf.Raw),
		asnSet(signerInfo))
	pkcs7 := asnSequence(asnOID(oidPKCS7SignedData), asnTagged(0, true, signedData))
	entry := make([]byte, 8, 8+len(pkcs7)+8)
	entry = append(entry, pkcs7...)
	for len(entry)%8 != 0 {
		entry = append(entry, 0)
	}
	binary.LittleEndian.PutUint32(entry, uint32(len(entry)))
	binary.LittleEndian.PutUint16(entry[4:], 0x200)
	binary.LittleEndian.PutUint16(entry[6:], 2)
	binary.LittleEndian.PutUint32(data[layout.certEntry:], uint32(len(data)))
	binary.LittleEndian.PutUint32(data[layout.certEntry+4:], uint32(len(entry)))
	return append(data, entry...)
}

// A genuine signature passes; a changed byte, a signer this computer does
// not trust, attributes that do not match, no signature and a file that is
// not a PE are each refused with the reason.
func TestAuthenticodeVerifyChecksHashSignatureAndChain(t *testing.T) {
	broadcom := newTestSigner(t, "Broadcom Inc")
	roots := x509.NewCertPool()
	roots.AddCert(broadcom.root)
	plain := testPE()
	signed := broadcom.sign(t, plain, false)

	certificate, err := authenticodeVerify(signed, roots, time.Now())
	if err != nil || authenticodeOrganisation(certificate) != "Broadcom Inc" {
		t.Fatalf("a genuine signature was refused: %v", err)
	}
	if major, minor, patch, build, err := peVersion(signed); err != nil || major != 8 || minor != 0 || patch != 3 || build != 1234 {
		t.Fatalf("version %d.%d.%d.%d %v", major, minor, patch, build, err)
	}

	tampered := append([]byte(nil), signed...)
	tampered[0x300] ^= 1
	if _, err := authenticodeVerify(tampered, roots, time.Now()); err == nil || !strings.Contains(err.Error(), "changed after it was signed") {
		t.Fatalf("a changed file gave %v", err)
	}
	other := newTestSigner(t, "Broadcom Inc")
	if _, err := authenticodeVerify(other.sign(t, plain, false), roots, time.Now()); err == nil || !strings.Contains(err.Error(), "not trusted") {
		t.Fatalf("a signer from an untrusted root gave %v", err)
	}
	if _, err := authenticodeVerify(broadcom.sign(t, plain, true), roots, time.Now()); err == nil || !strings.Contains(err.Error(), "do not match") {
		t.Fatalf("mismatched attributes gave %v", err)
	}
	if _, err := authenticodeVerify(signed, roots, time.Now().Add(48*time.Hour)); err == nil {
		t.Fatal("an expired signer was accepted")
	}
	if _, err := authenticodeVerify(plain, roots, time.Now()); err != errAuthenticodeNoSigned {
		t.Fatalf("an unsigned file gave %v", err)
	}
	if _, err := authenticodeVerify(make([]byte, 100), roots, time.Now()); err == nil || !strings.Contains(err.Error(), "not a Windows program file (100 bytes") {
		t.Fatalf("a non-PE file gave %v", err)
	}
}
