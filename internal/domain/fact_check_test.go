package domain

import "testing"

func TestValidateFactCheck(t *testing.T) {
	if err := ValidateFactCheck(FactCheck{Text: "verdict"}); err != nil {
		t.Fatalf("ValidateFactCheck(text) = %v, want nil", err)
	}
	if err := ValidateFactCheck(FactCheck{Text: "   "}); err == nil {
		t.Fatal("ValidateFactCheck(blank) = nil, want error")
	}
	if err := ValidateFactCheck(FactCheck{Text: "verdict", Country: "de"}); err == nil {
		t.Fatal("ValidateFactCheck(lowercase country) = nil, want error")
	}
	if err := ValidateFactCheck(FactCheck{Text: "verdict", Country: "DE"}); err != nil {
		t.Fatalf("ValidateFactCheck(DE) = %v, want nil", err)
	}
	long := make([]rune, MaxFactCheckTextLength+1)
	for i := range long {
		long[i] = 'a'
	}
	if err := ValidateFactCheck(FactCheck{Text: string(long)}); err == nil {
		t.Fatal("ValidateFactCheck(too long) = nil, want error")
	}
}

func TestFactCheckHashIsStablePerMessage(t *testing.T) {
	first := FactCheckHash(42, 7, "verdict")
	if first != FactCheckHash(42, 7, "verdict") {
		t.Fatal("FactCheckHash is not stable for the same message and text")
	}
	if first == FactCheckHash(42, 8, "verdict") {
		t.Fatal("FactCheckHash collides across message ids")
	}
	if first == FactCheckHash(43, 7, "verdict") {
		t.Fatal("FactCheckHash collides across channels")
	}
	if first == FactCheckHash(42, 7, "other verdict") {
		t.Fatal("FactCheckHash collides across texts")
	}
	if first == 0 {
		t.Fatal("FactCheckHash must never be zero")
	}
}
