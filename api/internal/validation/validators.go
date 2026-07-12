package validation

import (
	"regexp"
	"strings"

	"github.com/go-playground/validator/v10"
)

// UFSet holds the 27 Brazilian federation units (26 states + DF). Defined once
// here and reused by the "uf" validator — never redeclared per file.
var UFSet = map[string]struct{}{
	"AC": {}, "AL": {}, "AM": {}, "AP": {}, "BA": {}, "CE": {}, "DF": {},
	"ES": {}, "GO": {}, "MA": {}, "MG": {}, "MS": {}, "MT": {}, "PA": {},
	"PB": {}, "PE": {}, "PI": {}, "PR": {}, "RJ": {}, "RN": {}, "RO": {},
	"RR": {}, "RS": {}, "SC": {}, "SE": {}, "SP": {}, "TO": {},
}

// TimezoneSet holds the IANA timezones the fiscal configs accept (mirrors the
// frontend BRAZIL_TIMEZONES list).
var TimezoneSet = map[string]struct{}{
	"America/Sao_Paulo": {}, "America/Belem": {}, "America/Fortaleza": {},
	"America/Recife": {}, "America/Maceio": {}, "America/Bahia": {},
	"America/Manaus": {}, "America/Cuiaba": {}, "America/Porto_Velho": {},
	"America/Boa_Vista": {}, "America/Rio_Branco": {}, "America/Noronha": {},
}

// ufValidator reports whether the field is a valid Brazilian UF code.
func ufValidator(fl validator.FieldLevel) bool {
	_, ok := UFSet[fl.Field().String()]
	return ok
}

// timezoneValidator reports whether the field is an accepted Brazilian timezone.
func timezoneValidator(fl validator.FieldLevel) bool {
	_, ok := TimezoneSet[fl.Field().String()]
	return ok
}

// cpfValidator validates a Brazilian CPF (11 digits + 2 check digits).
// Punctuation is stripped before validation. Ported from the frontend
// validateCPF so both layers agree.
func cpfValidator(fl validator.FieldLevel) bool {
	return ValidCPF(fl.Field().String())
}

// cnpjValidator validates a Brazilian CNPJ, including the alphanumeric format
// (IN RFB 2229/2024). Ported from the frontend validateCNPJ.
func cnpjValidator(fl validator.FieldLevel) bool {
	return ValidCNPJ(fl.Field().String())
}

// cpfCnpjValidator accepts a value that is a valid CPF OR a valid CNPJ.
func cpfCnpjValidator(fl validator.FieldLevel) bool {
	v := fl.Field().String()
	return ValidCPF(v) || ValidCNPJ(v)
}

var (
	nonDigit = regexp.MustCompile(`\D`)
	nonAlnum = regexp.MustCompile(`[^A-Z0-9]`)
)

// ValidCPF reports whether s is a structurally valid CPF (check digits included).
func ValidCPF(s string) bool {
	clean := nonDigit.ReplaceAllString(s, "")
	if len(clean) != 11 {
		return false
	}
	if allSameByte(clean) {
		return false
	}
	sum := 0
	for i := 1; i <= 9; i++ {
		sum += int(clean[i-1]-'0') * (11 - i)
	}
	rem := (sum * 10) % 11
	if rem == 10 || rem == 11 {
		rem = 0
	}
	if rem != int(clean[9]-'0') {
		return false
	}
	sum = 0
	for i := 1; i <= 10; i++ {
		sum += int(clean[i-1]-'0') * (12 - i)
	}
	rem = (sum * 10) % 11
	if rem == 10 || rem == 11 {
		rem = 0
	}
	return rem == int(clean[10]-'0')
}

// ValidCNPJ reports whether s is a structurally valid CNPJ. Supports the
// alphanumeric format: A–Z map to 10–35, digits to their face value; the two
// check digits (positions 13–14) must be numeric.
func ValidCNPJ(s string) bool {
	clean := nonAlnum.ReplaceAllString(strings.ToUpper(s), "")
	if len(clean) != 14 {
		return false
	}
	if allSameByte(clean) {
		return false
	}
	if clean[12] < '0' || clean[12] > '9' || clean[13] < '0' || clean[13] > '9' {
		return false
	}
	val := func(c byte) int {
		if c >= '0' && c <= '9' {
			return int(c - '0')
		}
		return int(c - 'A' + 10)
	}
	w1 := []int{5, 4, 3, 2, 9, 8, 7, 6, 5, 4, 3, 2}
	s1 := 0
	for i := 0; i < 12; i++ {
		s1 += val(clean[i]) * w1[i]
	}
	r1 := s1 % 11
	d1 := 0
	if r1 >= 2 {
		d1 = 11 - r1
	}
	if val(clean[12]) != d1 {
		return false
	}
	w2 := []int{6, 5, 4, 3, 2, 9, 8, 7, 6, 5, 4, 3, 2}
	s2 := 0
	for i := 0; i < 13; i++ {
		s2 += val(clean[i]) * w2[i]
	}
	r2 := s2 % 11
	d2 := 0
	if r2 >= 2 {
		d2 = 11 - r2
	}
	return val(clean[13]) == d2
}

// allSameByte reports whether every byte in s is identical (e.g. "00000000000").
func allSameByte(s string) bool {
	for i := 1; i < len(s); i++ {
		if s[i] != s[0] {
			return false
		}
	}
	return len(s) > 0
}
