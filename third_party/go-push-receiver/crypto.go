/*
 * Copyright (c) 2019 Zenichi Amano
 *
 * This file is part of go-push-receiver, which is MIT licensed.
 * See http://opensource.org/licenses/MIT
 */

package pushreceiver

import (
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"

	ece "github.com/crow-misia/http-ece"
	"github.com/portpowered/go-ring/internal/protocol"
	pb "github.com/portpowered/go-ring/third_party/go-push-receiver/pb/mcs"
)

const authSecretLength = 16

func cryptoCurve() ecdh.Curve {
	return ecdh.P256()
}

// appendCryptoInfo appends key for crypto to Credentials.
func (c *FCMCredentials) appendCryptoInfo() error {
	privateKey, publicKey, err := generateKey(cryptoCurve())
	if err != nil {
		return wrapError(err, "generate random key for FCM")
	}

	authSecret, err := generateAuthSecret()
	if err != nil {
		return wrapError(err, "generate random auth secret for FCM")
	}

	c.PrivateKey = privateKey
	c.PublicKey = publicKey
	c.AuthSecret = authSecret

	return nil
}

func decryptData(data *pb.DataMessageStanza, creds *FCMCredentials) (*MessageEvent, error) {
	var bytes []byte

	var err error

	contentEncoding, lookupErr := findByKey(data.GetAppData(), protocol.MCSAppDataContentEncodingKey)
	if lookupErr == nil && contentEncoding.GetValue() == protocol.MCSAppDataContentEncodingAES128GCM {
		bytes, err = decryptDataV1(data, creds)
	} else {
		bytes, err = decryptDataLegacy(data, creds)
	}

	if err != nil {
		return nil, wrapError(err, "decrypt HTTP-ECE data")
	}

	return newMessageEvent(data, bytes), nil
}

func decryptDataLegacy(data *pb.DataMessageStanza, creds *FCMCredentials) ([]byte, error) {
	rawData := data.GetRawData()

	cryptoKeyData, err := findByKey(data.GetAppData(), protocol.MCSAppDataCryptoKeyKey)
	if err != nil {
		return nil, wrapError(err, "dh is not provided")
	}

	cryptoKey, err := base64.URLEncoding.DecodeString(cryptoKeyData.GetValue()[len(protocol.MCSAppDataCryptoKeyDHPrefix):])
	if err != nil {
		return nil, wrapError(err, "decode decrypt data")
	}

	saltData, err := findByKey(data.GetAppData(), protocol.MCSAppDataEncryptionKey)
	if err != nil {
		return nil, wrapError(err, "salt is not provided")
	}

	salt, err := base64.URLEncoding.DecodeString(saltData.GetValue()[len(protocol.MCSAppDataEncryptionSaltPrefix):])
	if err != nil {
		return nil, wrapError(err, "decode salt")
	}

	return ece.Decrypt(rawData,
		ece.WithEncoding(ece.AESGCM),
		ece.WithPrivate(creds.PrivateKey),
		ece.WithAuthSecret(creds.AuthSecret),
		ece.WithDh(cryptoKey),
		ece.WithSalt(salt),
	)
}

func decryptDataV1(data *pb.DataMessageStanza, creds *FCMCredentials) ([]byte, error) {
	rawData := data.GetRawData()

	return ece.Decrypt(rawData,
		ece.WithEncoding(ece.AES128GCM),
		ece.WithPrivate(creds.PrivateKey),
		ece.WithAuthSecret(creds.AuthSecret),
	)
}

// generateKey generates for public key crypto.
func generateKey(curve ecdh.Curve) ([]byte, []byte, error) {
	privateKey, err := curve.GenerateKey(rand.Reader)
	if err != nil {
		return nil, nil, wrapError(err, "generate ECDH private key")
	}

	publicKey := privateKey.PublicKey()

	return privateKey.Bytes(), publicKey.Bytes(), nil
}

// generateAuthSecret generates authSecret.
func generateAuthSecret() ([]byte, error) {
	salt := make([]byte, authSecretLength)
	_, err := rand.Read(salt)

	if err != nil {
		return nil, wrapError(err, "read random auth secret")
	}

	return salt, nil
}

func findByKey(data []*pb.AppData, key string) (*pb.AppData, error) {
	for _, data := range data {
		if data.GetKey() == key {
			return data, nil
		}
	}

	return nil, ErrNotFoundInAppData
}
