package dns

import (
	"bytes"
	"testing"
)

func TestCodecRoundTrip(t *testing.T) {
	name := "abc123.t.undertow.invalid."
	for _, response := range []bool{false, true} {
		want := Message{ID: 53322, Name: name, Response: response, Payload: bytes.Repeat([]byte{0, 1, 2, 255}, 200)}
		b, err := Encode(want)
		if err != nil {
			t.Fatal(err)
		}
		got, err := Decode(b)
		if err != nil {
			t.Fatal(err)
		}
		if got.ID != want.ID || got.Name != want.Name || got.Response != response || !bytes.Equal(got.Payload, want.Payload) {
			t.Fatalf("mismatch: %+v", got)
		}
	}
}

func TestCodecRejectsMalformed(t *testing.T) {
	b, err := Encode(Message{Name: "n.t.undertow.invalid.", Payload: []byte("hello")})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < len(b); i++ {
		if _, err := Decode(b[:i]); err == nil {
			t.Fatalf("accepted truncation at %d", i)
		}
	}
	b[12] = 0xc0
	if _, err := Decode(b); err == nil {
		t.Fatal("accepted compression pointer")
	}
}

func FuzzDecode(f *testing.F) {
	b, _ := Encode(Message{Name: "n.t.undertow.invalid.", Payload: []byte("seed")})
	f.Add(b)
	f.Fuzz(func(t *testing.T, b []byte) { _, _ = Decode(b) })
}
