// Copyright 2019 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package google

import (
	"fmt"
	"time"

	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"sync"

	"github.com/google/go-tpm/tpm2"
	"github.com/google/go-tpm/tpmutil"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/jws"
)

// TpmTokenConfig parameters to start Credential based off of TPM RSA Private Key.
type TpmTokenConfig struct {
	Tpm, Email, Audience string
	TpmHandle            uint32
	KeyId                string
}

type tpmTokenSource struct {
	refreshMutex         *sync.Mutex
	tpm, email, audience string

	tpmHandle tpmutil.Handle
	keyId     string
}

// TpmTokenSource returns a TokenSource for a ServiceAccount where
// the privateKey is sealed within a Trusted Platform Module (TPM)
// The TokenSource uses the TPM to sign a JWT representing an AccessTokenCredential.
//
// This TpmTokenSource will only work on platforms where the PrivateKey for the Service
// Account is already loaded on the TPM previously and available via Persistent Handle.
//
// https://developers.google.com/identity/protocols/OAuth2ServiceAccount#jwt-auth
// https://medium.com/google-cloud/faster-serviceaccount-authentication-for-google-cloud-platform-apis-f1355abc14b2
// https://godoc.org/golang.org/x/oauth2/google#JWTAccessTokenSourceFromJSON
// https://github.com/tpm2-software/tpm2-tools/wiki/Duplicating-Objects
//
//  Tpm (string): The device Handle for the TPM (eg. "/dev/tpm0")
//  Email (string): The service account to get the token for.
//  Audience (string): The audience representing the service the token is valid for.
//      The audience must match the name of the Service the token is intended for.  See
//      documentation links above.
//      (eg. https://pubsub.googleapis.com/google.pubsub.v1.Publisher)
//  TpmHandle (uint32): The persistent Handle representing the sealed keypair.
//      This must be set prior to using this library.
//  KeyId (string): (optional) The private KeyID for the service account key saved to the TPM.
//      Find the keyId associated with the service account by running:
//      `gcloud iam service-accounts keys list --iam-account=<email>``
//
func TpmTokenSource(tokenConfig TpmTokenConfig) (oauth2.TokenSource, error) {

	if tokenConfig.Tpm == "" || tokenConfig.TpmHandle == 0 || tokenConfig.Email == "" || tokenConfig.Audience == "" {
		return nil, fmt.Errorf("salrashid123/x/oauth2/google: TPMTokenConfig.Tpm, TPMTokenConfig.TpmHandle, TPMTokenConfig.Email and Audience and cannot be nil")
	}

	return &tpmTokenSource{
		refreshMutex: &sync.Mutex{},
		email:        tokenConfig.Email,
		audience:     tokenConfig.Audience,
		tpm:          tokenConfig.Tpm,
		tpmHandle:    tpmutil.Handle(tokenConfig.TpmHandle),
		keyId:        tokenConfig.KeyId,
	}, nil

}

func (ts *tpmTokenSource) Token() (*oauth2.Token, error) {
	ts.refreshMutex.Lock()
	defer ts.refreshMutex.Unlock()

	rwc, err := tpm2.OpenTPM(ts.tpm)
	if err != nil {
		return nil, fmt.Errorf("google: Unable to Open TPM: %v", err)
	}
	defer func() {
		if err := rwc.Close(); err != nil {
			fmt.Errorf("google: Unable to close TPM: %v", err)
		}
	}()

	iat := time.Now()
	exp := iat.Add(time.Hour)

	hdr, err := json.Marshal(&jws.Header{
		Algorithm: "RS256",
		Typ:       "JWT",
		KeyID:     string(ts.keyId),
	})
	if err != nil {
		return nil, fmt.Errorf("google: Unable to marshal TPM JWT Header: %v", err)
	}
	cs, err := json.Marshal(&jws.ClaimSet{
		Iss: ts.email,
		Sub: ts.email,
		Aud: ts.audience,
		Iat: iat.Unix(),
		Exp: exp.Unix(),
	})
	if err != nil {
		return nil, fmt.Errorf("google: Unable to marshal TPM JWT ClaimSet: %v", err)
	}

	j := base64.URLEncoding.EncodeToString([]byte(hdr)) + "." + base64.URLEncoding.EncodeToString([]byte(cs))

	digest := sha256.Sum256([]byte(j))
	sig, err := tpm2.Sign(rwc, ts.tpmHandle, "", digest[:], &tpm2.SigScheme{
		Alg:  tpm2.AlgRSASSA,
		Hash: tpm2.AlgSHA256,
	})
	if err != nil {
		return nil, fmt.Errorf("google: Unable to Sign wit TPM: %v", err)
	}

	msg := j + "." + base64.RawStdEncoding.EncodeToString([]byte(sig.RSA.Signature))

	return &oauth2.Token{AccessToken: msg, TokenType: "Bearer", Expiry: exp}, nil
}