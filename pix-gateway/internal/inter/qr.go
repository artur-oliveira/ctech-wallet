package inter

import (
	"encoding/base64"

	"github.com/skip2/go-qrcode"
)

// qrPNG renders the EMV copy-paste payload (pixCopiaECola) as a PNG QR code and
// returns it base64-encoded, ready to embed as data:image/png;base64,<...>.
// Inter's PIX API returns only the string, never the image — the frontend's
// <img> needs the image, so we generate it here at the bank boundary.
//
// A render failure returns an error rather than a broken image; callers log and
// leave QRCodeB64 empty. The EMV string still reaches the frontend as text, so a
// QR miss is a degraded experience, never a broken charge.
func qrPNG(text string) (string, error) {
	png, err := qrcode.Encode(text, qrcode.Medium, 256)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(png), nil
}
