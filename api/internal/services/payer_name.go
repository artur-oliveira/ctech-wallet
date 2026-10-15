package services

import (
	"strings"
	"unicode"
)

// nameFold strips the accents of Portuguese names so "JOSÉ" and "Jose" compare.
var nameFold = strings.NewReplacer(
	"á", "a", "à", "a", "â", "a", "ã", "a", "ä", "a", "é", "e", "è", "e", "ê", "e", "ë", "e",
	"í", "i", "ì", "i", "î", "i", "ï", "i", "ó", "o", "ò", "o", "ô", "o", "õ", "o", "ö", "o",
	"ú", "u", "ù", "u", "û", "u", "ü", "u", "ç", "c", "ñ", "n")

// nameConnectors are the particles Portuguese names put between words and that
// banks drop or keep inconsistently.
var nameConnectors = map[string]bool{"de": true, "da": true, "do": true, "das": true, "dos": true, "e": true}

func nameTokens(s string) []string {
	s = nameFold.Replace(strings.ToLower(s))
	words := strings.FieldsFunc(s, func(r rune) bool { return !unicode.IsLetter(r) })
	out := words[:0]
	for _, w := range words {
		if !nameConnectors[w] {
			out = append(out, w)
		}
	}
	return out
}

// payerNameMatches is the second half of the check on a payer whose CPF is only
// partly known: the name the bank reports for the payer has to be the KYC name.
// Case, accents, punctuation and connectors are ignored, and a middle name one
// side leaves out is tolerated: the first and the last name must agree. A
// one-word name must be equal. Either side empty answers false.
func payerNameMatches(payer, kyc string) bool {
	a, b := nameTokens(payer), nameTokens(kyc)
	if len(a) == 0 || len(b) == 0 {
		return false
	}
	if len(a) == 1 || len(b) == 1 {
		return len(a) == len(b) && a[0] == b[0]
	}
	return a[0] == b[0] && a[len(a)-1] == b[len(b)-1]
}
