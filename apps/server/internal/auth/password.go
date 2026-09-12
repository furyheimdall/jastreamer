package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
)

const (
	passwordMinimumCharacters = 10
	passwordMaximumBytes      = 1024
	argonMemoryKiB            = 19 * 1024
	argonIterations           = 2
	argonParallelism          = 1
	argonSaltBytes            = 16
	argonKeyBytes             = 32
)

type passwordParameters struct {
	memory      uint32
	iterations  uint32
	parallelism uint8
}

func validateNewPassword(password string) error {
	if !utf8.ValidString(password) || len(password) > passwordMaximumBytes {
		return invalidRequest("password must contain at least 10 characters and at most 1024 bytes")
	}
	characters := utf8.RuneCountInString(password)
	if characters < passwordMinimumCharacters {
		return invalidRequest("password must contain at least 10 characters and at most 1024 bytes")
	}
	return nil
}

func hashPassword(password string) (string, error) {
	salt := make([]byte, argonSaltBytes)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generate password salt: %w", err)
	}
	key := argon2.IDKey([]byte(password), salt, argonIterations, argonMemoryKiB, argonParallelism, argonKeyBytes)
	return encodePasswordHash(passwordParameters{argonMemoryKiB, argonIterations, argonParallelism}, salt, key), nil
}

func passwordMatches(encoded, password string) (bool, error) {
	parameters, salt, expected, err := parsePasswordHash(encoded)
	if err != nil {
		return false, err
	}
	actual := argon2.IDKey([]byte(password), salt, parameters.iterations, parameters.memory, parameters.parallelism, uint32(len(expected)))
	return subtle.ConstantTimeCompare(actual, expected) == 1, nil
}

func burnInvalidPassword(password string) {
	salt := [argonSaltBytes]byte{0x6a, 0x61, 0x73, 0x74, 0x72, 0x65, 0x61, 0x6d, 0x65, 0x72, 0x2d, 0x61, 0x75, 0x74, 0x68, 0x21}
	_ = argon2.IDKey([]byte(password), salt[:], argonIterations, argonMemoryKiB, argonParallelism, argonKeyBytes)
}

func encodePasswordHash(parameters passwordParameters, salt, key []byte) string {
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version,
		parameters.memory,
		parameters.iterations,
		parameters.parallelism,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key),
	)
}

func parsePasswordHash(encoded string) (passwordParameters, []byte, []byte, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" || parts[2] != "v="+strconv.Itoa(argon2.Version) {
		return passwordParameters{}, nil, nil, fmt.Errorf("stored password hash has an unsupported format")
	}
	values := strings.Split(parts[3], ",")
	if len(values) != 3 {
		return passwordParameters{}, nil, nil, fmt.Errorf("stored password hash has invalid parameters")
	}
	memory, memoryErr := parseHashParameter(values[0], "m=", 32)
	iterations, iterationsErr := parseHashParameter(values[1], "t=", 32)
	parallelism, parallelismErr := parseHashParameter(values[2], "p=", 8)
	if memoryErr != nil || iterationsErr != nil || parallelismErr != nil {
		return passwordParameters{}, nil, nil, fmt.Errorf("stored password hash has invalid parameters")
	}
	parameters := passwordParameters{memory: uint32(memory), iterations: uint32(iterations), parallelism: uint8(parallelism)}
	if parameters.memory < 8*1024 || parameters.memory > 256*1024 || parameters.iterations < 1 || parameters.iterations > 10 || parameters.parallelism < 1 || parameters.parallelism > 16 {
		return passwordParameters{}, nil, nil, fmt.Errorf("stored password hash parameters are outside safe bounds")
	}
	salt, err := base64.RawStdEncoding.Strict().DecodeString(parts[4])
	if err != nil || len(salt) < 16 || len(salt) > 64 {
		return passwordParameters{}, nil, nil, fmt.Errorf("stored password hash has an invalid salt")
	}
	key, err := base64.RawStdEncoding.Strict().DecodeString(parts[5])
	if err != nil || len(key) < 16 || len(key) > 64 {
		return passwordParameters{}, nil, nil, fmt.Errorf("stored password hash has an invalid digest")
	}
	return parameters, salt, key, nil
}

func parseHashParameter(value, prefix string, bits int) (uint64, error) {
	if !strings.HasPrefix(value, prefix) || len(value) == len(prefix) {
		return 0, fmt.Errorf("missing password hash parameter")
	}
	return strconv.ParseUint(value[len(prefix):], 10, bits)
}
