package relay

import (
	"bufio"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"time"
)

// Control handshake (install → relay, inside TLS to the relay's own name),
// one JSON object per line:
//
//	install: {"v":1,"spki":"<DER, base64>"}
//	relay:   {"nonce":"<32 random bytes, base64>"}
//	install: {"sig":"<ECDSA P-256 ASN.1 signature, base64>"}   over SHA-256(signedMessage)
//	relay:   {"ok":true,"id":"…","url":"https://<id>.<suffix>"} or {"error":"…","permanent":true}
//
// Then the connection carries a yamux session: the relay opens one stream
// per visitor, writes a header line {"addr":"<visitor ip:port>"} and then
// the visitor's raw bytes (starting with its TLS ClientHello).

// ProtocolVersion is the control protocol version.
const ProtocolVersion = 1

const (
	maxLineBytes     = 4096
	handshakeTimeout = 10 * time.Second
)

type helloMsg struct {
	V    int    `json:"v"`
	SPKI []byte `json:"spki"`
}

type challengeMsg struct {
	Nonce []byte `json:"nonce"`
}

type proofMsg struct {
	Sig []byte `json:"sig"`
}

type resultMsg struct {
	OK        bool   `json:"ok,omitempty"`
	ID        string `json:"id,omitempty"`
	URL       string `json:"url,omitempty"`
	Error     string `json:"error,omitempty"`
	Permanent bool   `json:"permanent,omitempty"`
	// Code names the refusal (Code* constants); Permanent and Error stay
	// what older apps read.
	Code string `json:"code,omitempty"`
}

type streamHeader struct {
	Addr string `json:"addr"`
}

// signedMessage binds a proof to the relay's name and its nonce, so a
// signature can't be replayed to another relay or later.
func signedMessage(relayName string, nonce []byte) []byte {
	h := sha256.New()
	h.Write([]byte("eggify-relay-v1\n"))
	h.Write([]byte(relayName))
	h.Write([]byte{'\n'})
	h.Write(nonce)
	return h.Sum(nil)
}

func signProof(id *Identity, relayName string, nonce []byte) ([]byte, error) {
	key, ok := id.Certificate.PrivateKey.(*ecdsa.PrivateKey)
	if !ok {
		return nil, errors.New("relay identity: not an ECDSA key")
	}
	return ecdsa.SignASN1(rand.Reader, key, signedMessage(relayName, nonce))
}

func verifyProof(spki []byte, relayName string, nonce, sig []byte) error {
	pub, err := x509.ParsePKIXPublicKey(spki)
	if err != nil {
		return fmt.Errorf("bad key: %w", err)
	}
	ec, ok := pub.(*ecdsa.PublicKey)
	if !ok || ec.Curve != elliptic.P256() {
		return errors.New("key must be ECDSA P-256")
	}
	if !ecdsa.VerifyASN1(ec, signedMessage(relayName, nonce), sig) {
		return errors.New("bad signature")
	}
	return nil
}

func writeLine(w io.Writer, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = w.Write(append(b, '\n'))
	return err
}

// readLine reads one JSON line of at most maxLineBytes into v.
func readLine(r *bufio.Reader, v any) error {
	var line []byte
	for {
		part, isPrefix, err := r.ReadLine()
		if err != nil {
			return err
		}
		line = append(line, part...)
		if len(line) > maxLineBytes {
			return errors.New("line too long")
		}
		if !isPrefix {
			break
		}
	}
	return json.Unmarshal(line, v)
}

// bufferedConn reads through a bufio.Reader that may already hold bytes
// read past the last handshake line.
type bufferedConn struct {
	net.Conn
	r *bufio.Reader
}

func (c *bufferedConn) Read(p []byte) (int, error) { return c.r.Read(p) }
