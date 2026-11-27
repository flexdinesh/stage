package auth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"strings"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
)

const (
	passwordMin = 15
	passwordMax = 128
	argonSlots  = 2
	argonMemory = 64 * 1024 // KiB per derivation
	argonTime   = 3
	argonLanes  = 1
	verifierV1  = "$argon2id$v=19$m=65536,t=3,p=1$"
)

// The local list covers common passwords that already meet the length floor.
// Compare the whole passphrase, without filtering words or substrings.
var commonPasswords = map[string]struct{}{
	"123456789012345": {}, "1234567890123456": {}, "12345678901234567": {},
	"123456789012345678": {}, "1234567890123456789": {}, "12345678901234567890": {},
	"123456789123456": {}, "111111111111111": {}, "000000000000000": {},
	"qwertyuiopasdfgh": {}, "qwertyuiopasdfghjkl": {}, "qwertyuiopasdfghjklzxcvbnm": {},
	"abcdefghijklmnopqrstuvwxyz": {}, "abcdefghijklmnopqrstuvwxyz1234567890": {},
	"passwordpassword": {}, "passwordpassword1": {}, "passwordpassword123": {},
	"passwordpasswordpassword": {}, "stagepassword123": {}, "changemechangeme": {},
	"password1234567": {}, "password12345678": {}, "12345678password": {},
	"password123456789": {}, "password1234567890": {}, "mypassword123456": {},
	"thisismypassword": {}, "this is my password": {}, "this is a password": {},
	"thisisnotapassword": {}, "letmeinletmeinletmein": {}, "adminadminadmin": {},
	"let me in let me in": {}, "passphrasepassphrase": {},
	"iloveyouiloveyou": {}, "correcthorsebatterystaple": {}, "correct horse battery staple": {},
}

func passwordInput(password string) bool {
	return len(password) <= passwordMax*utf8.UTFMax && utf8.ValidString(password) &&
		utf8.RuneCountInString(password) >= passwordMin && utf8.RuneCountInString(password) <= passwordMax
}

func validatePassword(password string) error {
	if !passwordInput(password) {
		return ErrPasswordLength
	}
	if _, common := commonPasswords[cases.Fold().String(norm.NFC.String(password))]; common {
		return ErrCommonPassword
	}
	return nil
}

var verifierEncoding = base64.RawStdEncoding.Strict()

func (s *Service) hashPassword(ctx context.Context, password string) (string, error) {
	if err := validatePassword(password); err != nil {
		return "", err
	}
	var salt [16]byte
	rand.Read(salt[:])
	key, err := s.derive(ctx, norm.NFC.String(password), salt)
	if err != nil {
		return "", err
	}
	return verifierV1 + verifierEncoding.EncodeToString(salt[:]) + "$" + verifierEncoding.EncodeToString(key), nil
}

func (s *Service) verifyPassword(ctx context.Context, verifier, password string) (bool, error) {
	// Unavailable accounts and malformed verifiers take the same bounded work
	// as an incorrect password. Accept only named, fixed parameter sets.
	salt, expected, valid := parseVerifier(verifier)
	inputValid := passwordInput(password)
	if !inputValid {
		password = "stage invalid passphrase"
	}
	actual, err := s.derive(ctx, norm.NFC.String(password), salt)
	if err != nil {
		return false, err
	}
	return subtle.ConstantTimeCompare(actual, expected[:]) == 1 && valid && inputValid, nil
}

func parseVerifier(verifier string) (salt [16]byte, key [32]byte, ok bool) {
	value, ok := strings.CutPrefix(verifier, verifierV1)
	left, right, split := strings.Cut(value, "$")
	if !ok || !split || len(left) != verifierEncoding.EncodedLen(len(salt)) || len(right) != verifierEncoding.EncodedLen(len(key)) {
		return salt, key, false
	}
	_, saltErr := verifierEncoding.Decode(salt[:], []byte(left))
	_, keyErr := verifierEncoding.Decode(key[:], []byte(right))
	return salt, key, saltErr == nil && keyErr == nil
}

func (s *Service) derive(ctx context.Context, password string, salt [16]byte) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	select {
	case s.hashSlots <- struct{}{}:
		defer func() { <-s.hashSlots }()
	default:
		return nil, ErrBusy
	}
	key := argon2.IDKey([]byte(password), salt[:], argonTime, argonMemory, argonLanes, 32)
	return key, ctx.Err()
}
