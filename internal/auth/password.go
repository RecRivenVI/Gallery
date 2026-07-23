package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"golang.org/x/crypto/argon2"
)

const (
	PasswordParametersVersion = 1
	PasswordMaxBytes          = 1024
	PasswordMinBytes          = 10
	argonMemoryKiB            = 19 * 1024
	argonIterations           = 2
	argonParallelism          = 1
	argonSaltBytes            = 16
	argonKeyBytes             = 32
)

var ErrPasswordInvalid = errors.New("密码无效")

type PasswordParameters struct {
	Version     int
	MemoryKiB   uint32
	Iterations  uint32
	Parallelism uint8
	SaltBytes   uint32
	KeyBytes    uint32
}

func CurrentPasswordParameters() PasswordParameters {
	return PasswordParameters{
		Version: PasswordParametersVersion, MemoryKiB: argonMemoryKiB,
		Iterations: argonIterations, Parallelism: argonParallelism,
		SaltBytes: argonSaltBytes, KeyBytes: argonKeyBytes,
	}
}

func HashPassword(password string, random io.Reader) (string, error) {
	if len(password) < PasswordMinBytes || len(password) > PasswordMaxBytes {
		return "", ErrPasswordInvalid
	}
	if random == nil {
		random = rand.Reader
	}
	p := CurrentPasswordParameters()
	salt := make([]byte, p.SaltBytes)
	if _, err := io.ReadFull(random, salt); err != nil {
		return "", fmt.Errorf("生成密码盐: %w", err)
	}
	key := argon2.IDKey([]byte(password), salt, p.Iterations, p.MemoryKiB, p.Parallelism, p.KeyBytes)
	return fmt.Sprintf("$argon2id$v=19$m=%d,t=%d,p=%d$%s$%s",
		p.MemoryKiB, p.Iterations, p.Parallelism,
		base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key)), nil
}

func VerifyPassword(encoded, password string) (valid, needsRehash bool, err error) {
	if len(password) > PasswordMaxBytes {
		return false, false, ErrPasswordInvalid
	}
	p, salt, expected, err := parsePasswordHash(encoded)
	if err != nil {
		return false, false, err
	}
	actual := argon2.IDKey([]byte(password), salt, p.Iterations, p.MemoryKiB, p.Parallelism, uint32(len(expected)))
	valid = subtle.ConstantTimeCompare(actual, expected) == 1
	current := CurrentPasswordParameters()
	needsRehash = valid && (p.MemoryKiB != current.MemoryKiB || p.Iterations != current.Iterations ||
		p.Parallelism != current.Parallelism || uint32(len(salt)) != current.SaltBytes || uint32(len(expected)) != current.KeyBytes)
	return valid, needsRehash, nil
}

func parsePasswordHash(encoded string) (PasswordParameters, []byte, []byte, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" || parts[2] != "v=19" {
		return PasswordParameters{}, nil, nil, ErrPasswordInvalid
	}
	var p PasswordParameters
	p.Version = PasswordParametersVersion
	seen := make(map[string]struct{}, 3)
	for _, item := range strings.Split(parts[3], ",") {
		key, value, ok := strings.Cut(item, "=")
		if !ok {
			return PasswordParameters{}, nil, nil, ErrPasswordInvalid
		}
		n, err := strconv.ParseUint(value, 10, 32)
		if err != nil {
			return PasswordParameters{}, nil, nil, ErrPasswordInvalid
		}
		if _, duplicate := seen[key]; duplicate {
			return PasswordParameters{}, nil, nil, ErrPasswordInvalid
		}
		seen[key] = struct{}{}
		switch key {
		case "m":
			if n > 256*1024 {
				return PasswordParameters{}, nil, nil, ErrPasswordInvalid
			}
			p.MemoryKiB = uint32(n)
		case "t":
			if n > 20 {
				return PasswordParameters{}, nil, nil, ErrPasswordInvalid
			}
			p.Iterations = uint32(n)
		case "p":
			if n > 16 {
				return PasswordParameters{}, nil, nil, ErrPasswordInvalid
			}
			p.Parallelism = uint8(n)
		default:
			return PasswordParameters{}, nil, nil, ErrPasswordInvalid
		}
	}
	if len(seen) != 3 || p.MemoryKiB < 8*1024 || p.Iterations == 0 || p.Parallelism == 0 {
		return PasswordParameters{}, nil, nil, ErrPasswordInvalid
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil || len(salt) < 16 || len(salt) > 64 {
		return PasswordParameters{}, nil, nil, ErrPasswordInvalid
	}
	key, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil || len(key) < 16 || len(key) > 64 {
		return PasswordParameters{}, nil, nil, ErrPasswordInvalid
	}
	return p, salt, key, nil
}
