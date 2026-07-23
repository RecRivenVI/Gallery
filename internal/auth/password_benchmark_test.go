package auth_test

import (
	"bytes"
	"testing"

	"github.com/RecRivenVI/gallery/internal/auth"
)

const benchmarkPassword = "gallery-synthetic-benchmark-password"

func BenchmarkArgon2idHash(b *testing.B) {
	salt := make([]byte, auth.CurrentPasswordParameters().SaltBytes)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if _, err := auth.HashPassword(benchmarkPassword, bytes.NewReader(salt)); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkArgon2idVerify(b *testing.B) {
	salt := make([]byte, auth.CurrentPasswordParameters().SaltBytes)
	encoded, err := auth.HashPassword(benchmarkPassword, bytes.NewReader(salt))
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		valid, _, verifyErr := auth.VerifyPassword(encoded, benchmarkPassword)
		if verifyErr != nil || !valid {
			b.Fatalf("verify valid=%v err=%v", valid, verifyErr)
		}
	}
}

func BenchmarkArgon2idVerifyParallel(b *testing.B) {
	salt := make([]byte, auth.CurrentPasswordParameters().SaltBytes)
	encoded, err := auth.HashPassword(benchmarkPassword, bytes.NewReader(salt))
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			valid, _, verifyErr := auth.VerifyPassword(encoded, benchmarkPassword)
			if verifyErr != nil || !valid {
				b.Errorf("verify valid=%v err=%v", valid, verifyErr)
				return
			}
		}
	})
}
