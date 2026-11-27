package main

// authenticodeVerify checks a Windows PE file's embedded Authenticode
// signature from its bytes, without Windows' trust providers: the file hash
// the signature names must match the file, the signed attributes must name
// that hash and carry the signer's valid signature, and the signer's
// certificate must chain to a trusted root for code signing. The VDDK folder
// check (vddk_windows.go) uses it when Windows' own WinVerifyTrust cannot
// give a verdict (3.2.38 field report: every genuine Broadcom-signed VDDK
// DLL came back "The form specified for the subject is not one supported or
// known by the specified trust provider", and GetFileVersionInfo said
// vixDiskLib.dll had no resource section, although both are present).
//
// peVersion reads the file version from the PE's own version resource for
// the same reason.

import (
	"bytes"
	"crypto"
	"crypto/ecdsa"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/binary"
	"errors"
	"fmt"
	"hash"
	"math/big"
	"time"
)

var (
	oidPKCS7SignedData      = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 7, 2}
	oidSpcIndirectData      = asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 311, 2, 1, 4}
	oidAttrContentType      = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 9, 3}
	oidAttrMessageDigest    = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 9, 4}
	oidDigestSHA1           = asn1.ObjectIdentifier{1, 3, 14, 3, 2, 26}
	oidDigestSHA256         = asn1.ObjectIdentifier{2, 16, 840, 1, 101, 3, 4, 2, 1}
	oidDigestSHA384         = asn1.ObjectIdentifier{2, 16, 840, 1, 101, 3, 4, 2, 2}
	oidDigestSHA512         = asn1.ObjectIdentifier{2, 16, 840, 1, 101, 3, 4, 2, 3}
	errAuthenticodeNoSigned = errors.New("it has no embedded signature")
)

type pkcs7ContentInfo struct {
	ContentType asn1.ObjectIdentifier
	Content     asn1.RawValue `asn1:"explicit,tag:0"`
}

type pkcs7SignedData struct {
	Version          int
	DigestAlgorithms asn1.RawValue
	ContentInfo      pkcs7ContentInfo
	Certificates     asn1.RawValue     `asn1:"optional,tag:0"`
	CRLs             asn1.RawValue     `asn1:"optional,tag:1"`
	SignerInfos      []pkcs7SignerInfo `asn1:"set"`
}

type pkcs7IssuerSerial struct {
	Issuer asn1.RawValue
	Serial *big.Int
}

type pkcs7SignerInfo struct {
	Version                   int
	IssuerAndSerial           pkcs7IssuerSerial
	DigestAlgorithm           pkix.AlgorithmIdentifier
	AuthenticatedAttributes   asn1.RawValue `asn1:"optional,tag:0"`
	DigestEncryptionAlgorithm pkix.AlgorithmIdentifier
	EncryptedDigest           []byte
	UnauthenticatedAttributes asn1.RawValue `asn1:"optional,tag:1"`
}

type pkcs7Attribute struct {
	Type   asn1.ObjectIdentifier
	Values asn1.RawValue `asn1:"set"`
}

type spcDigestInfo struct {
	Algorithm pkix.AlgorithmIdentifier
	Digest    []byte
}

type spcIndirectData struct {
	Data   asn1.RawValue
	Digest spcDigestInfo
}

func authenticodeHash(oid asn1.ObjectIdentifier) (crypto.Hash, func() hash.Hash, error) {
	switch {
	case oid.Equal(oidDigestSHA1):
		return crypto.SHA1, sha1.New, nil
	case oid.Equal(oidDigestSHA256):
		return crypto.SHA256, sha256.New, nil
	case oid.Equal(oidDigestSHA384):
		return crypto.SHA384, sha512.New384, nil
	case oid.Equal(oidDigestSHA512):
		return crypto.SHA512, sha512.New, nil
	}
	return 0, nil, fmt.Errorf("its signature uses an unknown hash (%v)", oid)
}

// peLayout is what the Authenticode hash and the resource reader need from
// the PE headers.
type peLayout struct {
	checksum   int // file offset of the optional header's CheckSum
	certEntry  int // file offset of the security data directory entry
	certOffset int // file offset of the certificate table (0: none)
	certSize   int
	resRVA     uint32
	resSize    uint32
	sections   []peSection
}

type peSection struct {
	va, size, raw, rawSize uint32
}

func parsePELayout(data []byte) (peLayout, error) {
	var layout peLayout
	if len(data) < 0x40 || data[0] != 'M' || data[1] != 'Z' {
		head := data
		if len(head) > 8 {
			head = head[:8]
		}
		return layout, fmt.Errorf("it is not a Windows program file (%d bytes, starting % x); it may be blocked, online only or not fully extracted", len(data), head)
	}
	pe := int(binary.LittleEndian.Uint32(data[0x3c:]))
	if pe < 0 || pe+24 > len(data) || !bytes.Equal(data[pe:pe+4], []byte("PE\x00\x00")) {
		return layout, errors.New("its PE header is damaged")
	}
	sections := int(binary.LittleEndian.Uint16(data[pe+6:]))
	optSize := int(binary.LittleEndian.Uint16(data[pe+20:]))
	opt := pe + 24
	if opt+optSize > len(data) || optSize < 2 {
		return layout, errors.New("its optional header is damaged")
	}
	var dirs int
	switch binary.LittleEndian.Uint16(data[opt:]) {
	case 0x10b:
		dirs = opt + 96
	case 0x20b:
		dirs = opt + 112
	default:
		return layout, errors.New("its optional header is not PE32 or PE32+")
	}
	if dirs+5*8 > opt+optSize {
		return layout, errors.New("it has no certificate directory")
	}
	layout.checksum = opt + 64
	layout.certEntry = dirs + 4*8
	layout.certOffset = int(binary.LittleEndian.Uint32(data[layout.certEntry:]))
	layout.certSize = int(binary.LittleEndian.Uint32(data[layout.certEntry+4:]))
	layout.resRVA = binary.LittleEndian.Uint32(data[dirs+2*8:])
	layout.resSize = binary.LittleEndian.Uint32(data[dirs+2*8+4:])
	table := opt + optSize
	for i := 0; i < sections; i++ {
		s := table + i*40
		if s+40 > len(data) {
			return layout, errors.New("its section table is damaged")
		}
		layout.sections = append(layout.sections, peSection{
			size:    binary.LittleEndian.Uint32(data[s+8:]),
			va:      binary.LittleEndian.Uint32(data[s+12:]),
			rawSize: binary.LittleEndian.Uint32(data[s+16:]),
			raw:     binary.LittleEndian.Uint32(data[s+20:]),
		})
	}
	return layout, nil
}

// peFileHash is the Authenticode hash: the whole file except the CheckSum,
// the security directory entry and the certificate table.
func peFileHash(data []byte, layout peLayout, newHash func() hash.Hash) []byte {
	h := newHash()
	h.Write(data[:layout.checksum])
	h.Write(data[layout.checksum+4 : layout.certEntry])
	h.Write(data[layout.certEntry+8 : layout.certOffset])
	h.Write(data[layout.certOffset+layout.certSize:])
	return h.Sum(nil)
}

// authenticodeVerify returns the verified signer's certificate. roots nil
// means this computer's trusted roots.
func authenticodeVerify(data []byte, roots *x509.CertPool, now time.Time) (*x509.Certificate, error) {
	layout, err := parsePELayout(data)
	if err != nil {
		return nil, err
	}
	if layout.certOffset == 0 || layout.certSize < 8 {
		return nil, errAuthenticodeNoSigned
	}
	if layout.certOffset < layout.certEntry+8 || layout.certOffset+layout.certSize > len(data) {
		return nil, errors.New("its certificate table is outside the file")
	}
	// WIN_CERTIFICATE: dwLength, wRevision, wCertificateType (2: PKCS#7).
	table := data[layout.certOffset : layout.certOffset+layout.certSize]
	length := int(binary.LittleEndian.Uint32(table))
	if length < 8 || length > len(table) || binary.LittleEndian.Uint16(table[6:]) != 2 {
		return nil, errors.New("its certificate table holds no Authenticode signature")
	}
	var info pkcs7ContentInfo
	if _, err := asn1.Unmarshal(table[8:length], &info); err != nil || !info.ContentType.Equal(oidPKCS7SignedData) {
		return nil, errors.New("its signature is not PKCS#7 signed data")
	}
	var signed pkcs7SignedData
	if _, err := asn1.Unmarshal(info.Content.Bytes, &signed); err != nil {
		return nil, fmt.Errorf("its signature cannot be read: %v", err)
	}
	if !signed.ContentInfo.ContentType.Equal(oidSpcIndirectData) || len(signed.SignerInfos) != 1 {
		return nil, errors.New("its signature is not an Authenticode signature")
	}
	// The signed content: SpcIndirectDataContent, whose digest is the file's.
	// encoding/asn1 keeps the [0] wrapper in an explicit RawValue, so the
	// content itself is its Bytes.
	var content asn1.RawValue
	if _, err := asn1.Unmarshal(signed.ContentInfo.Content.Bytes, &content); err != nil {
		return nil, errors.New("its signed content cannot be read")
	}
	var indirect spcIndirectData
	if _, err := asn1.Unmarshal(content.FullBytes, &indirect); err != nil {
		return nil, errors.New("its signed content cannot be read")
	}
	_, fileHash, err := authenticodeHash(indirect.Digest.Algorithm.Algorithm)
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(peFileHash(data, layout, fileHash), indirect.Digest.Digest) {
		return nil, errors.New("the file was changed after it was signed (its hash does not match the signature)")
	}

	signer := signed.SignerInfos[0]
	hashID, newHash, err := authenticodeHash(signer.DigestAlgorithm.Algorithm)
	if err != nil {
		return nil, err
	}
	if len(signer.AuthenticatedAttributes.FullBytes) == 0 {
		return nil, errors.New("its signature has no signed attributes")
	}
	// The signature covers the attributes encoded as a SET.
	attributesSet := append([]byte{0x31}, signer.AuthenticatedAttributes.FullBytes[1:]...)
	var attributes []pkcs7Attribute
	if _, err := asn1.UnmarshalWithParams(attributesSet, &attributes, "set"); err != nil {
		return nil, errors.New("its signed attributes cannot be read")
	}
	contentHash := newHash()
	contentHash.Write(content.Bytes)
	var digestOK, typeOK bool
	for _, attribute := range attributes {
		switch {
		case attribute.Type.Equal(oidAttrMessageDigest):
			var digest []byte
			if _, err := asn1.Unmarshal(attribute.Values.Bytes, &digest); err == nil && bytes.Equal(digest, contentHash.Sum(nil)) {
				digestOK = true
			}
		case attribute.Type.Equal(oidAttrContentType):
			var kind asn1.ObjectIdentifier
			if _, err := asn1.Unmarshal(attribute.Values.Bytes, &kind); err == nil && kind.Equal(oidSpcIndirectData) {
				typeOK = true
			}
		}
	}
	if !digestOK || !typeOK {
		return nil, errors.New("its signed attributes do not match the signed content")
	}

	certificates, err := x509.ParseCertificates(signed.Certificates.Bytes)
	if err != nil || len(certificates) == 0 {
		return nil, errors.New("its signature carries no readable certificates")
	}
	var leaf *x509.Certificate
	intermediates := x509.NewCertPool()
	for _, certificate := range certificates {
		if leaf == nil && bytes.Equal(certificate.RawIssuer, signer.IssuerAndSerial.Issuer.FullBytes) && certificate.SerialNumber.Cmp(signer.IssuerAndSerial.Serial) == 0 {
			leaf = certificate
			continue
		}
		intermediates.AddCert(certificate)
	}
	if leaf == nil {
		return nil, errors.New("its signature does not carry the signer's certificate")
	}
	attributesHash := newHash()
	attributesHash.Write(attributesSet)
	sum := attributesHash.Sum(nil)
	switch key := leaf.PublicKey.(type) {
	case *rsa.PublicKey:
		err = rsa.VerifyPKCS1v15(key, hashID, sum, signer.EncryptedDigest)
	case *ecdsa.PublicKey:
		if !ecdsa.VerifyASN1(key, sum, signer.EncryptedDigest) {
			err = errors.New("bad signature")
		}
	default:
		err = errors.New("unsupported key type")
	}
	if err != nil {
		return nil, fmt.Errorf("its signature is not valid (%v)", err)
	}
	if _, err := leaf.Verify(x509.VerifyOptions{Intermediates: intermediates, Roots: roots, CurrentTime: now, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageCodeSigning}}); err != nil {
		return nil, fmt.Errorf("its signer's certificate is not trusted here (%v)", err)
	}
	return leaf, nil
}

// authenticodeOrganisation is the signer's organisation (O=).
func authenticodeOrganisation(certificate *x509.Certificate) string {
	if len(certificate.Subject.Organization) == 0 {
		return certificate.Subject.CommonName
	}
	return certificate.Subject.Organization[0]
}

// peVersion reads VS_FIXEDFILEINFO's file version from the PE's RT_VERSION
// resource.
func peVersion(data []byte) (major, minor, patch, build uint16, err error) {
	layout, err := parsePELayout(data)
	if err != nil {
		return 0, 0, 0, 0, err
	}
	toOffset := func(rva uint32) (int, bool) {
		for _, s := range layout.sections {
			size := s.size
			if s.rawSize > size {
				size = s.rawSize
			}
			if rva >= s.va && rva < s.va+size {
				off := int(rva - s.va + s.raw)
				return off, off < len(data)
			}
		}
		return 0, false
	}
	base, ok := toOffset(layout.resRVA)
	if !ok || layout.resRVA == 0 {
		return 0, 0, 0, 0, errors.New("it has no resources")
	}
	// entry finds the directory entry with id (or the first, when id < 0).
	entry := func(dir int, id int) (uint32, bool) {
		if dir+16 > len(data) {
			return 0, false
		}
		named := int(binary.LittleEndian.Uint16(data[dir+12:]))
		ids := int(binary.LittleEndian.Uint16(data[dir+14:]))
		for i := 0; i < named+ids; i++ {
			e := dir + 16 + i*8
			if e+8 > len(data) {
				return 0, false
			}
			name := binary.LittleEndian.Uint32(data[e:])
			if id < 0 || (name&0x80000000 == 0 && int(name) == id) {
				return binary.LittleEndian.Uint32(data[e+4:]), true
			}
		}
		return 0, false
	}
	next := uint32(0)
	dir := base
	for level, id := range []int{16, -1, -1} { // RT_VERSION, then any name, any language
		value, found := entry(dir, id)
		if !found {
			return 0, 0, 0, 0, errors.New("it has no version resource")
		}
		if level < 2 {
			if value&0x80000000 == 0 {
				return 0, 0, 0, 0, errors.New("its version resource is damaged")
			}
			dir = base + int(value&0x7fffffff)
			continue
		}
		next = value
	}
	leaf := base + int(next)
	if leaf+8 > len(data) {
		return 0, 0, 0, 0, errors.New("its version resource is damaged")
	}
	start, ok := toOffset(binary.LittleEndian.Uint32(data[leaf:]))
	size := int(binary.LittleEndian.Uint32(data[leaf+4:]))
	if !ok || start+size > len(data) {
		return 0, 0, 0, 0, errors.New("its version resource is damaged")
	}
	block := data[start : start+size]
	signature := []byte{0xBD, 0x04, 0xEF, 0xFE}
	at := bytes.Index(block, signature)
	if at < 0 || at+16 > len(block) {
		return 0, 0, 0, 0, errors.New("its version resource has no file version")
	}
	ms := binary.LittleEndian.Uint32(block[at+8:])
	ls := binary.LittleEndian.Uint32(block[at+12:])
	return uint16(ms >> 16), uint16(ms), uint16(ls >> 16), uint16(ls), nil
}

// peImports lists the DLL names a PE file imports (its import directory, not
// delay loads). The VDDK folder check uses it to name a dependency this
// computer lacks before VixDiskLib fails to start with a bare error 16053.
func peImports(data []byte) ([]string, error) {
	layout, err := parsePELayout(data)
	if err != nil {
		return nil, err
	}
	pe := int(binary.LittleEndian.Uint32(data[0x3c:]))
	opt := pe + 24
	dirs := opt + 96
	if binary.LittleEndian.Uint16(data[opt:]) == 0x20b {
		dirs = opt + 112
	}
	importRVA := binary.LittleEndian.Uint32(data[dirs+8:])
	if importRVA == 0 {
		return nil, nil
	}
	toOffset := func(rva uint32) (int, bool) {
		for _, s := range layout.sections {
			size := s.size
			if s.rawSize > size {
				size = s.rawSize
			}
			if rva >= s.va && rva < s.va+size {
				off := int(rva - s.va + s.raw)
				return off, off < len(data)
			}
		}
		return 0, false
	}
	at, ok := toOffset(importRVA)
	if !ok {
		return nil, errors.New("its import table is outside the file")
	}
	var names []string
	for i := 0; i < 4096; i++ {
		entry := at + i*20
		if entry+20 > len(data) {
			return nil, errors.New("its import table is damaged")
		}
		nameRVA := binary.LittleEndian.Uint32(data[entry+12:])
		if nameRVA == 0 {
			return names, nil
		}
		start, ok := toOffset(nameRVA)
		if !ok {
			return nil, errors.New("its import table is damaged")
		}
		end := bytes.IndexByte(data[start:], 0)
		if end < 0 || end > 260 {
			return nil, errors.New("its import table is damaged")
		}
		names = append(names, string(data[start:start+end]))
	}
	return nil, errors.New("its import table is damaged")
}
