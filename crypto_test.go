package main

import (
	"testing"
)

func TestEncRoundTrip(t *testing.T) {
	enc, err := buildEnc("ทดสอบ", "1234567890123", "0812345678")
	if err != nil {
		t.Fatal(err)
	}
	plain, err := cryptoJSDecrypt(enc, encPassphrase)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"first_name":"ทดสอบ","id_card_number":"1234567890123","phone":"0812345678","key":"wlsfca16"}`
	if string(plain) != want {
		t.Fatalf("got %s want %s", plain, want)
	}
}

func TestJSDateString(t *testing.T) {
	got, err := jsDateString("1997-04-03")
	if err != nil {
		t.Fatal(err)
	}
	want := "Thu Apr 03 1997 00:00:00 GMT+0700 (Indochina Time)"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}
