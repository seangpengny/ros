package security

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"golang.org/x/crypto/argon2"
)

const (
	argonMemory  uint32 = 64 * 1024
	argonTime    uint32 = 3
	argonThreads uint8  = 4
	argonKeyLen  uint32 = 32
	argonSaltLen        = 16
)

func HashPassword(password string) (string, error) {
	if err := ValidatePassword(password); err != nil {
		return "", err
	}
	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generate password salt: %w", err)
	}
	hash := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	encode := base64.RawStdEncoding
	return fmt.Sprintf("$argon2id$v=19$m=%d,t=%d,p=%d$%s$%s", argonMemory, argonTime, argonThreads, encode.EncodeToString(salt), encode.EncodeToString(hash)), nil
}

func VerifyPassword(encoded, password string) (bool, error) {
	if err := ValidatePassword(password); err != nil {
		return false, err
	}
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" || parts[2] != "v=19" {
		return false, errors.New("unsupported password hash format")
	}
	params := map[string]uint64{}
	for _, item := range strings.Split(parts[3], ",") {
		pair := strings.SplitN(item, "=", 2)
		if len(pair) != 2 {
			return false, errors.New("invalid password hash parameters")
		}
		value, err := strconv.ParseUint(pair[1], 10, 32)
		if err != nil {
			return false, errors.New("invalid password hash parameter value")
		}
		params[pair[0]] = value
	}
	memory, okMemory := params["m"]
	iterations, okIterations := params["t"]
	threads, okThreads := params["p"]
	if !okMemory || !okIterations || !okThreads || memory == 0 || iterations == 0 || threads == 0 || threads > 255 {
		return false, errors.New("incomplete password hash parameters")
	}
	decode := base64.RawStdEncoding
	salt, err := decode.DecodeString(parts[4])
	if err != nil || len(salt) < 16 {
		return false, errors.New("invalid password hash salt")
	}
	expected, err := decode.DecodeString(parts[5])
	if err != nil || len(expected) == 0 {
		return false, errors.New("invalid password hash")
	}
	actual := argon2.IDKey([]byte(password), salt, uint32(iterations), uint32(memory), uint8(threads), uint32(len(expected)))
	return subtle.ConstantTimeCompare(actual, expected) == 1, nil
}

func ValidatePassword(password string) error {
	if len(password) < 12 || len(password) > 1024 {
		return errors.New("password must be between 12 and 1024 bytes")
	}
	return nil
}
