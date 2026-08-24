package cursor

import (
	"bytes"
	"encoding/base64"
	"errors"
	"image"
	"image/color"
	"image/png"
	"testing"
)

func testPNGDataURI(t *testing.T, width, height int) (string, []byte) {
	t.Helper()
	bitmap := image.NewRGBA(image.Rect(0, 0, width, height))
	bitmap.Set(0, 0, color.RGBA{R: 1, G: 2, B: 3, A: 255})
	var buffer bytes.Buffer
	if err := png.Encode(&buffer, bitmap); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	raw := buffer.Bytes()
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(raw), raw
}

func TestParseImageDataURIReadsDimensionsAndBytes(t *testing.T) {
	uri, raw := testPNGDataURI(t, 7, 5)
	image, err := ParseImageDataURI(uri)
	if err != nil {
		t.Fatalf("parse image: %v", err)
	}
	if !bytes.Equal(image.Data, raw) || image.Width != 7 || image.Height != 5 {
		t.Errorf("image = bytes:%d dimensions:%dx%d", len(image.Data), image.Width, image.Height)
	}
}

func TestParseImageDataURIToleratesCaseWhitespaceAndUnpaddedBase64(t *testing.T) {
	_, raw := testPNGDataURI(t, 4, 4)
	encoded := base64.RawStdEncoding.EncodeToString(raw)
	var wrapped bytes.Buffer
	for index, value := range encoded {
		if index > 0 && index%40 == 0 {
			wrapped.WriteByte('\n')
		}
		wrapped.WriteRune(value)
	}
	image, err := ParseImageDataURI("DATA:image/PNG;BASE64," + wrapped.String())
	if err != nil || !bytes.Equal(image.Data, raw) {
		t.Fatalf("parse wrapped image = bytes:%d err:%v", len(image.Data), err)
	}
}

func TestParseImageDataURIRejectsUnsupportedInputs(t *testing.T) {
	for _, uri := range []string{"", "https://example.com/cat.png", "data:image/png,raw", "data:image/png;base64", "data:image/png;base64,"} {
		_, err := ParseImageDataURI(uri)
		if !errors.Is(err, ErrNotImageDataURI) {
			t.Errorf("ParseImageDataURI(%q) error = %v, want ErrNotImageDataURI", uri, err)
		}
	}
	_, err := ParseImageDataURI("data:application/pdf;base64," + base64.StdEncoding.EncodeToString([]byte("%PDF")))
	if !errors.Is(err, ErrNotImageDataURI) {
		t.Errorf("non-image media type error = %v", err)
	}
	if _, err := ParseImageDataURI("data:image/png;base64,!!!"); err == nil || errors.Is(err, ErrNotImageDataURI) {
		t.Errorf("bad base64 error = %v", err)
	}
}

func TestParseImageDataURIEnforcesSixteenMiBBound(t *testing.T) {
	atLimit := make([]byte, 16<<20)
	image, err := ParseImageDataURI("data:image/webp;base64," + base64.StdEncoding.EncodeToString(atLimit))
	if err != nil || len(image.Data) != 16<<20 {
		t.Fatalf("16 MiB image = bytes:%d err:%v", len(image.Data), err)
	}
	overLimit := append(atLimit, 0)
	if _, err := ParseImageDataURI("data:image/webp;base64," + base64.StdEncoding.EncodeToString(overLimit)); err == nil {
		t.Fatal("16 MiB + 1 image must be rejected")
	}
}
